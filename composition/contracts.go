package composition

import (
	"fmt"
	"slices"
	"sort"

	"github.com/Masterminds/semver/v3"
)

var DefaultSupportedContracts = map[string][]string{
	ContractComposition:    {"2.0"},
	ContractFrontendPlugin: {"1.0"},
	ContractSettings:       {"1.0"},
	ContractPermissions:    {"1.0"},
	ContractFixtures:       {"1.0"},
}

func ValidateLockedContracts(descriptor *Descriptor, manifest *PackageManifest, lock *Lock, toolVersion string, supported map[string][]string) error {
	return validateContracts(descriptor, manifest, lock, toolVersion, supported, true)
}

func ValidateDevelopContracts(descriptor *Descriptor, manifest *PackageManifest, lock *Lock, toolVersion string, supported map[string][]string) error {
	return validateContracts(descriptor, manifest, lock, toolVersion, supported, false)
}

func validateContracts(descriptor *Descriptor, manifest *PackageManifest, lock *Lock, toolVersion string, supported map[string][]string, enforceReleaseVersion bool) error {
	if err := descriptor.Validate(); err != nil {
		return err
	}
	if err := manifest.Validate(); err != nil {
		return err
	}
	if err := lock.Validate(); err != nil {
		return err
	}
	if manifest.ID != lock.Package || (enforceReleaseVersion && manifest.Version != lock.Version) {
		return ErrPackageIdentity
	}
	if descriptor.Base.ID != manifest.ID {
		return fmt.Errorf("%w: package %s@%s does not satisfy %s@%s", ErrPackageIdentity, manifest.ID, manifest.Version, descriptor.Base.ID, descriptor.Base.Version)
	}
	if enforceReleaseVersion {
		baseConstraint, _ := semver.NewConstraint(descriptor.Base.Version)
		packageVersion, _ := semver.StrictNewVersion(manifest.Version)
		if !baseConstraint.Check(packageVersion) {
			return fmt.Errorf("%w: package %s@%s does not satisfy %s@%s", ErrPackageIdentity, manifest.ID, manifest.Version, descriptor.Base.ID, descriptor.Base.Version)
		}
	}
	providedServices := make(map[string]struct{}, len(manifest.Services))
	for _, service := range manifest.Services {
		providedServices[service.Name] = struct{}{}
	}
	for _, service := range descriptor.Services.Include {
		if _, exists := providedServices[service]; !exists {
			return fmt.Errorf("%w: package does not provide selected service %q", ErrContract, service)
		}
	}
	minimumTool, _ := semver.NewConstraint(manifest.MinimumCodeflyVersion)
	actualTool, err := semver.NewVersion(toolVersion)
	if err != nil || !minimumTool.Check(actualTool) {
		return fmt.Errorf("%w: Codefly %s does not satisfy package requirement %s", ErrContract, toolVersion, manifest.MinimumCodeflyVersion)
	}
	for _, contract := range requiredContracts(descriptor) {
		if _, exists := lock.Contracts[contract]; !exists {
			return fmt.Errorf("%w: lock is missing required contract %q", ErrContract, contract)
		}
	}
	for contract, lockedVersion := range lock.Contracts {
		packageRange, exists := manifest.Contracts[contract]
		if !exists {
			return fmt.Errorf("%w: package is missing locked contract %q", ErrContract, contract)
		}
		constraint, _ := semver.NewConstraint(packageRange)
		version, err := semver.NewVersion(lockedVersion)
		if err != nil || !constraint.Check(version) || !slices.Contains(supported[contract], lockedVersion) {
			return fmt.Errorf("%w: locked %s version %s is not supported by the package and Codefly", ErrContract, contract, lockedVersion)
		}
	}
	return nil
}

func NegotiateContracts(descriptor *Descriptor, manifest *PackageManifest, toolVersion string, supported map[string][]string) (map[string]string, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	baseConstraint, _ := semver.NewConstraint(descriptor.Base.Version)
	packageVersion, _ := semver.StrictNewVersion(manifest.Version)
	if descriptor.Base.ID != manifest.ID || !baseConstraint.Check(packageVersion) {
		return nil, fmt.Errorf("%w: package %s@%s does not satisfy %s@%s", ErrPackageIdentity, manifest.ID, manifest.Version, descriptor.Base.ID, descriptor.Base.Version)
	}
	minimumTool, _ := semver.NewConstraint(manifest.MinimumCodeflyVersion)
	actualTool, err := semver.NewVersion(toolVersion)
	if err != nil {
		return nil, fmt.Errorf("Codefly version %q is invalid: %w", toolVersion, err)
	}
	if !minimumTool.Check(actualTool) {
		return nil, fmt.Errorf("%w: Codefly %s does not satisfy package requirement %s", ErrContract, toolVersion, manifest.MinimumCodeflyVersion)
	}
	providedServices := make(map[string]struct{}, len(manifest.Services))
	for _, service := range manifest.Services {
		providedServices[service.Name] = struct{}{}
	}
	for _, service := range descriptor.Services.Include {
		if _, exists := providedServices[service]; !exists {
			return nil, fmt.Errorf("%w: package does not provide selected service %q", ErrContract, service)
		}
	}
	required := requiredContracts(descriptor)
	negotiated := make(map[string]string, len(required))
	for _, contract := range required {
		packageRange, exists := manifest.Contracts[contract]
		if !exists {
			return nil, fmt.Errorf("%w: package does not advertise required contract %q", ErrContract, contract)
		}
		constraint, _ := semver.NewConstraint(packageRange)
		candidates := append([]string(nil), supported[contract]...)
		sort.Slice(candidates, func(i, j int) bool {
			left, leftErr := semver.NewVersion(candidates[i])
			right, rightErr := semver.NewVersion(candidates[j])
			if leftErr != nil || rightErr != nil {
				return candidates[i] < candidates[j]
			}
			return left.LessThan(right)
		})
		for index := len(candidates) - 1; index >= 0; index-- {
			version, parseErr := semver.NewVersion(candidates[index])
			if parseErr != nil {
				return nil, fmt.Errorf("supported %s contract version %q is invalid: %w", contract, candidates[index], parseErr)
			}
			if constraint.Check(version) {
				negotiated[contract] = candidates[index]
				break
			}
		}
		if _, exists := negotiated[contract]; !exists {
			return nil, fmt.Errorf("%w: no supported %s version satisfies %s", ErrContract, contract, packageRange)
		}
	}
	return negotiated, nil
}

func requiredContracts(descriptor *Descriptor) []string {
	required := []string{ContractComposition}
	if len(descriptor.Contributions.Frontend) > 0 {
		required = append(required, ContractFrontendPlugin)
	}
	if len(descriptor.Contributions.Settings) > 0 {
		required = append(required, ContractSettings)
	}
	if len(descriptor.Contributions.Permissions) > 0 {
		required = append(required, ContractPermissions)
	}
	if len(descriptor.Contributions.Fixtures) > 0 {
		required = append(required, ContractFixtures)
	}
	return required
}
