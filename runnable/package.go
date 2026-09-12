// Package runnable validates and digests the immutable installation facts of a
// Runnable: the package descriptor a build produces and the binding an
// installation on one execution facility produces. Both are content-addressed
// so identical registrations are recognizable and changed content under the
// same release identity is a conflict, never a silent replacement.
package runnable

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"buf.build/go/protovalidate"
	"github.com/Masterminds/semver/v3"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

const (
	// PackageSchemaV1 is the only package descriptor schema this package accepts.
	PackageSchemaV1 = "codefly.runnable-package/v1"
	// BindingSchemaV1 is the only binding schema this package accepts.
	BindingSchemaV1 = "codefly.runnable-binding/v1"
)

var (
	// ErrInvalid is returned for a malformed or internally inconsistent
	// descriptor or binding.
	ErrInvalid = errors.New("invalid Codefly runnable")
	// ErrConflict is returned when content differs under the same release
	// identity.
	ErrConflict = errors.New("runnable release conflict")

	validator protovalidate.Validator
)

func init() {
	var err error
	validator, err = protovalidate.New()
	if err != nil {
		panic(err)
	}
}

// artifactFacility is the facility an artifact form executes on. A binding may
// only pair the two this way.
var artifactFacility = map[basev0.RunnableArtifact_Kind]basev0.RunnableFacility_Kind{
	basev0.RunnableArtifact_NATIVE: basev0.RunnableFacility_NATIVE,
	basev0.RunnableArtifact_IMAGE:  basev0.RunnableFacility_KUBERNETES,
}

// PreparePackage clones pkg, validates it, canonicalizes its sets and
// populates the digest. A non-empty caller-supplied digest must already match:
// conflicting immutable bytes are never silently repaired.
func PreparePackage(pkg *basev0.RunnablePackage) (*basev0.RunnablePackage, error) {
	if pkg == nil {
		return nil, fmt.Errorf("%w: package is required", ErrInvalid)
	}
	prepared := &basev0.RunnablePackage{}
	proto.Merge(prepared, pkg)
	supplied := prepared.GetDigest()
	prepared.Digest = ""
	if err := validatePackage(prepared); err != nil {
		return nil, err
	}
	canonicalizePackage(prepared)
	digest, err := digestOf(PackageDigestFormatV1, prepared)
	if err != nil {
		return nil, err
	}
	if supplied != "" && supplied != digest {
		return nil, fmt.Errorf("%w: digest %s does not match descriptor bytes (%s)", ErrInvalid, supplied, digest)
	}
	prepared.Digest = digest
	return prepared, nil
}

// VerifyPackage checks that pkg carries the digest of its own canonical bytes.
func VerifyPackage(pkg *basev0.RunnablePackage) error {
	if pkg.GetDigest() == "" {
		return fmt.Errorf("%w: package carries no digest", ErrInvalid)
	}
	prepared, err := PreparePackage(pkg)
	if err != nil {
		return err
	}
	if !proto.Equal(prepared, pkg) {
		return fmt.Errorf("%w: package is not in canonical form", ErrInvalid)
	}
	return nil
}

// CompareRelease decides what registering incoming means next to existing,
// both verified packages of the same release identity: nil is an identical,
// idempotent registration; ErrConflict is changed content under the same
// immutable identity.
func CompareRelease(existing, incoming *basev0.RunnablePackage) error {
	if err := VerifyPackage(existing); err != nil {
		return fmt.Errorf("existing: %w", err)
	}
	if err := VerifyPackage(incoming); err != nil {
		return fmt.Errorf("incoming: %w", err)
	}
	if !proto.Equal(existing.GetIdentity(), incoming.GetIdentity()) {
		return fmt.Errorf("%w: packages have different release identities", ErrInvalid)
	}
	if existing.GetDigest() != incoming.GetDigest() {
		return fmt.Errorf("%w: release %s/%s@%s is already registered with digest %s; incoming digest is %s",
			ErrConflict, existing.GetIdentity().GetModule(), existing.GetIdentity().GetName(),
			existing.GetIdentity().GetVersion(), existing.GetDigest(), incoming.GetDigest())
	}
	return nil
}

// PrepareBinding clones binding, validates it against the verified package it
// installs, canonicalizes its sets and populates the digest.
func PrepareBinding(binding *basev0.RunnableBinding, pkg *basev0.RunnablePackage) (*basev0.RunnableBinding, error) {
	if binding == nil {
		return nil, fmt.Errorf("%w: binding is required", ErrInvalid)
	}
	if err := VerifyPackage(pkg); err != nil {
		return nil, err
	}
	prepared := &basev0.RunnableBinding{}
	proto.Merge(prepared, binding)
	supplied := prepared.GetDigest()
	prepared.Digest = ""
	if err := validateBinding(prepared, pkg); err != nil {
		return nil, err
	}
	canonicalizeBinding(prepared)
	digest, err := digestOf(BindingDigestFormatV1, prepared)
	if err != nil {
		return nil, err
	}
	if supplied != "" && supplied != digest {
		return nil, fmt.Errorf("%w: digest %s does not match binding bytes (%s)", ErrInvalid, supplied, digest)
	}
	prepared.Digest = digest
	return prepared, nil
}

// VerifyBinding checks that binding installs pkg and carries the digest of its
// own canonical bytes.
func VerifyBinding(binding *basev0.RunnableBinding, pkg *basev0.RunnablePackage) error {
	if binding.GetDigest() == "" {
		return fmt.Errorf("%w: binding carries no digest", ErrInvalid)
	}
	prepared, err := PrepareBinding(binding, pkg)
	if err != nil {
		return err
	}
	if !proto.Equal(prepared, binding) {
		return fmt.Errorf("%w: binding is not in canonical form", ErrInvalid)
	}
	return nil
}

func validatePackage(pkg *basev0.RunnablePackage) error {
	if pkg.GetSchema() != PackageSchemaV1 {
		return fmt.Errorf("%w: package schema %q is not supported; expected %s", ErrInvalid, pkg.GetSchema(), PackageSchemaV1)
	}
	if err := validator.Validate(pkg); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := semver.StrictNewVersion(pkg.GetIdentity().GetVersion()); err != nil {
		return fmt.Errorf("%w: version %q is not a strict semantic version", ErrInvalid, pkg.GetIdentity().GetVersion())
	}
	agent, err := resources.AgentFromProto(pkg.GetAgent())
	if err != nil {
		return fmt.Errorf("%w: agent: %v", ErrInvalid, err)
	}
	if !agent.IsRunnable() {
		return fmt.Errorf("%w: agent kind %s is not %s", ErrInvalid, agent.Kind, resources.RunnableAgent)
	}
	if agent.Version == "latest" {
		return fmt.Errorf("%w: agent version must be pinned, not latest", ErrInvalid)
	}
	if _, err = resources.RunnableContractFromProto(pkg.GetContract()); err != nil {
		return fmt.Errorf("%w: contract: %v", ErrInvalid, err)
	}
	facilities, err := validateExecution(pkg.GetExecution())
	if err != nil {
		return err
	}
	if err := validateBuild(pkg.GetBuild()); err != nil {
		return err
	}
	if err := validateDependencies(pkg.GetServiceDependencies()); err != nil {
		return err
	}
	if err := validateUniqueNames("workspace configuration dependency", pkg.GetWorkspaceConfigurationDependencies()); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(pkg.GetArtifacts()))
	for _, artifact := range pkg.GetArtifacts() {
		if err := validateArtifact(artifact); err != nil {
			return err
		}
		key := artifact.GetKind().String() + " " + artifact.GetPlatform()
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: artifact %s is declared twice", ErrInvalid, key)
		}
		seen[key] = struct{}{}
		if _, allowed := facilities[artifactFacility[artifact.GetKind()]]; !allowed {
			return fmt.Errorf("%w: %s artifact for %s needs facility %s, which the package does not declare",
				ErrInvalid, artifact.GetKind(), artifact.GetPlatform(), artifactFacility[artifact.GetKind()])
		}
	}
	return nil
}

func validateExecution(execution *basev0.RunnableExecution) (map[basev0.RunnableFacility_Kind]struct{}, error) {
	facilities := make(map[basev0.RunnableFacility_Kind]struct{}, len(execution.GetFacilities()))
	for _, facility := range execution.GetFacilities() {
		kind := facility.GetKind()
		if _, known := basev0.RunnableFacility_Kind_name[int32(kind)]; !known || kind == basev0.RunnableFacility_UNKNOWN {
			return nil, fmt.Errorf("%w: execution facility %s is not supported", ErrInvalid, kind)
		}
		if _, exists := facilities[kind]; exists {
			return nil, fmt.Errorf("%w: execution facility %s is declared twice", ErrInvalid, kind)
		}
		facilities[kind] = struct{}{}
	}
	if err := execution.GetTimeout().CheckValid(); err != nil || execution.GetTimeout().AsDuration() <= 0 {
		return nil, fmt.Errorf("%w: execution timeout must be positive", ErrInvalid)
	}
	if execution.GetCancellation() == basev0.RunnableExecution_CANCELLATION_UNKNOWN {
		return nil, fmt.Errorf("%w: execution cancellation is required", ErrInvalid)
	}
	if execution.GetRecovery() == basev0.RunnableExecution_RECOVERY_UNKNOWN {
		return nil, fmt.Errorf("%w: execution recovery is required", ErrInvalid)
	}
	return facilities, nil
}

func validateDependencies(dependencies []*basev0.RunnableDependency) error {
	seen := make(map[string]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		unique := dependency.GetModule() + "/" + dependency.GetName()
		if err := resources.DependencyKind(dependency.GetKind()).Validate(); err != nil {
			return fmt.Errorf("%w: dependency %s: %v", ErrInvalid, unique, err)
		}
		if _, exists := seen[unique]; exists {
			return fmt.Errorf("%w: dependency %s is declared twice", ErrInvalid, unique)
		}
		seen[unique] = struct{}{}
		if err := validateUniqueNames("dependency "+unique+" endpoint", dependency.GetEndpoints()); err != nil {
			return err
		}
	}
	return nil
}

func validateUniqueNames(kind string, names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%w: %s cannot be empty", ErrInvalid, kind)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("%w: %s %q is declared twice", ErrInvalid, kind, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateBuild(build *basev0.RunnableBuild) error {
	if err := validateConfinedPath("handler", build.GetHandler().GetPath()); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(build.GetInputs()))
	for _, input := range build.GetInputs() {
		if err := validateConfinedPath("build input", input.GetPath()); err != nil {
			return err
		}
		if _, exists := seen[input.GetPath()]; exists {
			return fmt.Errorf("%w: build input %q is pinned twice", ErrInvalid, input.GetPath())
		}
		seen[input.GetPath()] = struct{}{}
	}
	return nil
}

// validateConfinedPath applies the declaration's path rule to a path carried
// on the wire, so a descriptor cannot pin content outside the runnable tree.
func validateConfinedPath(kind, p string) error {
	if !filepath.IsLocal(p) || strings.ContainsAny(p, "\x00\\") {
		return fmt.Errorf("%w: %s path %q must stay within the runnable directory", ErrInvalid, kind, p)
	}
	return nil
}

func validateArtifact(artifact *basev0.RunnableArtifact) error {
	os, arch, ok := strings.Cut(artifact.GetPlatform(), "/")
	if !ok || os == "" || arch == "" || strings.Contains(arch, "/") {
		return fmt.Errorf("%w: artifact platform %q must be os/arch", ErrInvalid, artifact.GetPlatform())
	}
	switch artifact.GetKind() {
	case basev0.RunnableArtifact_NATIVE:
		if len(artifact.GetCommand()) == 0 {
			return fmt.Errorf("%w: native artifact for %s declares no launch command", ErrInvalid, artifact.GetPlatform())
		}
	case basev0.RunnableArtifact_IMAGE:
		if len(artifact.GetCommand()) > 0 {
			return fmt.Errorf("%w: image artifact for %s must not declare a launch command; the image carries its entrypoint", ErrInvalid, artifact.GetPlatform())
		}
		if !strings.HasSuffix(artifact.GetReference(), "@"+artifact.GetDigest()) {
			return fmt.Errorf("%w: image reference %q is not pinned to its digest %s", ErrInvalid, artifact.GetReference(), artifact.GetDigest())
		}
	default:
		return fmt.Errorf("%w: artifact kind %s is not supported", ErrInvalid, artifact.GetKind())
	}
	return nil
}

func validateBinding(binding *basev0.RunnableBinding, pkg *basev0.RunnablePackage) error {
	if binding.GetSchema() != BindingSchemaV1 {
		return fmt.Errorf("%w: binding schema %q is not supported; expected %s", ErrInvalid, binding.GetSchema(), BindingSchemaV1)
	}
	if err := validator.Validate(binding); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if binding.GetPackageDigest() != pkg.GetDigest() {
		return fmt.Errorf("%w: binding installs package digest %s but was given package %s", ErrInvalid, binding.GetPackageDigest(), pkg.GetDigest())
	}
	if !proto.Equal(binding.GetIdentity(), pkg.GetIdentity()) {
		return fmt.Errorf("%w: binding identity does not match the package release identity", ErrInvalid)
	}
	facility := binding.GetFacility().GetKind()
	if !slices.ContainsFunc(pkg.GetExecution().GetFacilities(), func(f *basev0.RunnableFacility) bool { return f.GetKind() == facility }) {
		return fmt.Errorf("%w: package does not allow execution facility %s", ErrInvalid, facility)
	}
	if !slices.ContainsFunc(pkg.GetArtifacts(), func(a *basev0.RunnableArtifact) bool { return proto.Equal(a, binding.GetArtifact()) }) {
		return fmt.Errorf("%w: binding artifact is not one of the package artifacts", ErrInvalid)
	}
	if artifactFacility[binding.GetArtifact().GetKind()] != facility {
		return fmt.Errorf("%w: %s artifact cannot execute on facility %s", ErrInvalid, binding.GetArtifact().GetKind(), facility)
	}
	declared := make(map[string]*basev0.RunnableDependency, len(pkg.GetServiceDependencies()))
	for _, dependency := range pkg.GetServiceDependencies() {
		declared[dependency.GetModule()+"/"+dependency.GetName()] = dependency
	}
	for _, mapping := range binding.GetDependencyNetworkMappings() {
		endpoint := mapping.GetEndpoint()
		unique := endpoint.GetModule() + "/" + endpoint.GetService()
		dependency, ok := declared[unique]
		if !ok {
			return fmt.Errorf("%w: binding maps %s, which the package does not declare as a dependency", ErrInvalid, unique)
		}
		if len(dependency.GetEndpoints()) > 0 && !slices.Contains(dependency.GetEndpoints(), endpoint.GetName()) {
			return fmt.Errorf("%w: binding maps endpoint %s/%s, which dependency %s does not consume", ErrInvalid, unique, endpoint.GetName(), unique)
		}
	}
	if err := validateUniqueNames("credential reference", binding.GetCredentialReferences()); err != nil {
		return err
	}
	if err := validateUniqueNames("configuration reference", binding.GetConfigurationReferences()); err != nil {
		return err
	}
	required := slices.Sorted(slices.Values(pkg.GetWorkspaceConfigurationDependencies()))
	bound := slices.Sorted(slices.Values(binding.GetConfigurationReferences()))
	if !slices.Equal(required, bound) {
		return fmt.Errorf("%w: binding resolves configurations %v but the package declares %v", ErrInvalid, bound, required)
	}
	return nil
}

func canonicalizePackage(pkg *basev0.RunnablePackage) {
	slices.SortFunc(pkg.Execution.Facilities, func(a, b *basev0.RunnableFacility) int {
		return int(a.GetKind()) - int(b.GetKind())
	})
	slices.Sort(pkg.WorkspaceConfigurationDependencies)
	slices.SortFunc(pkg.Build.Inputs, func(a, b *basev0.RunnableInputDigest) int {
		return strings.Compare(a.GetPath(), b.GetPath())
	})
	slices.SortFunc(pkg.Artifacts, func(a, b *basev0.RunnableArtifact) int {
		if a.GetKind() != b.GetKind() {
			return int(a.GetKind()) - int(b.GetKind())
		}
		return strings.Compare(a.GetPlatform(), b.GetPlatform())
	})
	slices.SortFunc(pkg.ServiceDependencies, func(a, b *basev0.RunnableDependency) int {
		if a.GetModule() != b.GetModule() {
			return strings.Compare(a.GetModule(), b.GetModule())
		}
		return strings.Compare(a.GetName(), b.GetName())
	})
	for _, dependency := range pkg.ServiceDependencies {
		slices.Sort(dependency.Endpoints)
	}
}

func canonicalizeBinding(binding *basev0.RunnableBinding) {
	slices.Sort(binding.CredentialReferences)
	slices.Sort(binding.ConfigurationReferences)
	slices.SortFunc(binding.DependencyNetworkMappings, func(a, b *basev0.NetworkMapping) int {
		return strings.Compare(networkMappingKey(a), networkMappingKey(b))
	})
	for _, mapping := range binding.DependencyNetworkMappings {
		slices.SortFunc(mapping.Instances, func(a, b *basev0.NetworkInstance) int {
			if a.GetAccess().GetKind() != b.GetAccess().GetKind() {
				return strings.Compare(a.GetAccess().GetKind(), b.GetAccess().GetKind())
			}
			return strings.Compare(a.GetAddress(), b.GetAddress())
		})
	}
}

func networkMappingKey(mapping *basev0.NetworkMapping) string {
	endpoint := mapping.GetEndpoint()
	return strings.Join([]string{endpoint.GetModule(), endpoint.GetService(), endpoint.GetName(), endpoint.GetApi()}, "/")
}
