package composition

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}(?:[0-9a-f]{24})?$`)

func LoadDescriptor(moduleDir string) (*Descriptor, error) {
	data, err := os.ReadFile(filepath.Join(moduleDir, DescriptorFileName))
	if err != nil {
		return nil, fmt.Errorf("read composition descriptor: %w", err)
	}
	var descriptor Descriptor
	if err := decodeStrictYAML(data, &descriptor); err != nil {
		return nil, fmt.Errorf("decode composition descriptor: %w", err)
	}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	return &descriptor, nil
}

func (descriptor *Descriptor) Validate() error {
	if descriptor == nil {
		return errors.New("composition descriptor is required")
	}
	if descriptor.Kind != DescriptorKind {
		return fmt.Errorf("composition descriptor kind must be %q", DescriptorKind)
	}
	if err := validateIdentifier("module name", descriptor.Name); err != nil {
		return err
	}
	if err := validateIdentifier("base package", descriptor.Base.ID); err != nil {
		return err
	}
	if _, err := semver.NewConstraint(descriptor.Base.Version); err != nil {
		return fmt.Errorf("base version constraint %q is invalid: %w", descriptor.Base.Version, err)
	}
	if err := uniqueStrings("included service", descriptor.Services.Include); err != nil {
		return err
	}
	for _, service := range descriptor.Services.Include {
		if err := validateIdentifier("included service", service); err != nil {
			return err
		}
	}
	paths := make(map[string]string)
	identities := make(map[string]string)
	add := func(kind, path, identity string) error {
		if err := validateRelativePath(kind+" contribution", path); err != nil {
			return err
		}
		if previous, exists := paths[path]; exists {
			return fmt.Errorf("contribution path %q is declared by both %s and %s", path, previous, kind)
		}
		paths[path] = kind
		key := kind + "\x00" + identity
		if previous, exists := identities[key]; exists {
			return fmt.Errorf("duplicate %s contribution identity %q at %s and %s", kind, identity, previous, path)
		}
		identities[key] = path
		return nil
	}
	for _, contribution := range descriptor.Contributions.Frontend {
		if strings.TrimSpace(contribution.Export) == "" {
			return errors.New("frontend contribution export is required")
		}
		if err := add("frontend", contribution.Path, contribution.Export); err != nil {
			return err
		}
	}
	for _, contribution := range descriptor.Contributions.Settings {
		if strings.TrimSpace(contribution.Message) == "" {
			return errors.New("settings contribution message is required")
		}
		if err := add("settings", contribution.Path, contribution.Message); err != nil {
			return err
		}
	}
	for _, contribution := range descriptor.Contributions.Permissions {
		if err := add("permissions", contribution.Path, contribution.Path); err != nil {
			return err
		}
	}
	for _, contribution := range descriptor.Contributions.Fixtures {
		if err := add("fixtures", contribution.Path, contribution.Path); err != nil {
			return err
		}
	}
	for _, contribution := range descriptor.Contributions.Tests {
		if len(contribution.Command) == 0 || strings.TrimSpace(contribution.Command[0]) == "" {
			return fmt.Errorf("integration contribution %q command is required", contribution.Path)
		}
		if err := add("tests", contribution.Path, contribution.Path); err != nil {
			return err
		}
	}
	bindings := make(map[string]struct{})
	for _, binding := range descriptor.Bindings {
		for label, value := range map[string]string{
			"binding plugin": binding.Plugin, "binding alias": binding.Alias,
			"binding target module": binding.Target.Module, "binding target service": binding.Target.Service,
		} {
			if err := validateIdentifier(label, value); err != nil {
				return err
			}
		}
		identity := binding.Plugin + "/" + binding.Alias
		if _, exists := bindings[identity]; exists {
			return fmt.Errorf("duplicate service binding %q", identity)
		}
		bindings[identity] = struct{}{}
	}
	return nil
}

func LoadPackageManifest(root string) (*PackageManifest, error) {
	data, err := os.ReadFile(filepath.Join(root, PackageManifestFileName))
	if err != nil {
		return nil, fmt.Errorf("read module package manifest: %w", err)
	}
	var manifest PackageManifest
	if err := decodeStrictYAML(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode module package manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if err := validatePackageLayout(root, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func validatePackageLayout(root string, manifest *PackageManifest) error {
	for _, relative := range manifest.ArtifactRoots {
		current := root
		for _, component := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
			current = filepath.Join(current, component)
			info, err := os.Lstat(current)
			if err != nil {
				return fmt.Errorf("module package artifact root %q: %w", relative, err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("module package artifact root %q contains a symlink", relative)
			}
		}
	}
	for _, command := range append(slices.Clone(manifest.Generators), manifest.Conformance...) {
		if command.WorkingDirectory == "" {
			continue
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(command.WorkingDirectory)))
		if err != nil || !info.IsDir() {
			return fmt.Errorf("module package command %q working directory %q is unavailable", command.Name, command.WorkingDirectory)
		}
	}
	return nil
}

func (manifest *PackageManifest) Validate() error {
	if manifest == nil {
		return errors.New("module package manifest is required")
	}
	if manifest.Kind != PackageKind || manifest.Schema != PackageSchema {
		return fmt.Errorf("module package manifest must have kind %q and schema %q", PackageKind, PackageSchema)
	}
	if err := validateIdentifier("package id", manifest.ID); err != nil {
		return err
	}
	if _, err := semver.StrictNewVersion(manifest.Version); err != nil {
		return fmt.Errorf("package version %q is invalid: %w", manifest.Version, err)
	}
	if _, err := semver.NewConstraint(manifest.MinimumCodeflyVersion); err != nil {
		return fmt.Errorf("minimum Codefly version %q is invalid: %w", manifest.MinimumCodeflyVersion, err)
	}
	if len(manifest.ArtifactRoots) == 0 {
		return errors.New("module package must declare at least one artifact root")
	}
	if err := uniqueStrings("artifact root", manifest.ArtifactRoots); err != nil {
		return err
	}
	for _, root := range manifest.ArtifactRoots {
		if err := validateRelativePath("artifact root", root); err != nil {
			return err
		}
	}
	if _, exists := manifest.Contracts[ContractComposition]; !exists {
		return fmt.Errorf("module package must declare the %q contract", ContractComposition)
	}
	for contract, constraint := range manifest.Contracts {
		if err := validateContractName(contract); err != nil {
			return err
		}
		if _, err := semver.NewConstraint(constraint); err != nil {
			return fmt.Errorf("contract %s constraint %q is invalid: %w", contract, constraint, err)
		}
	}
	serviceNames := make([]string, 0, len(manifest.Services))
	for _, service := range manifest.Services {
		serviceNames = append(serviceNames, service.Name)
		if err := validateIdentifier("provided service", service.Name); err != nil {
			return err
		}
		if err := uniqueStrings("endpoint for service "+service.Name, service.Endpoints); err != nil {
			return err
		}
	}
	if err := uniqueStrings("provided service", serviceNames); err != nil {
		return err
	}
	commands := append(slices.Clone(manifest.Generators), manifest.Conformance...)
	for _, entry := range manifest.EntryPoints {
		commands = append(commands, PackageCommand{Name: entry.Name, Command: entry.Command})
	}
	commandNames := make([]string, 0, len(commands))
	for _, command := range commands {
		if strings.TrimSpace(command.Name) == "" || len(command.Command) == 0 || strings.TrimSpace(command.Command[0]) == "" {
			return errors.New("package commands require a name and a non-empty command")
		}
		commandNames = append(commandNames, command.Name)
		if command.WorkingDirectory != "" {
			if err := validateRelativePath("package command working directory", command.WorkingDirectory); err != nil {
				return err
			}
		}
	}
	if err := uniqueStrings("package command", commandNames); err != nil {
		return err
	}
	if err := uniqueStrings("reserved namespace", manifest.ReservedNamespaces); err != nil {
		return err
	}
	migrationIDs := make([]string, 0, len(manifest.Migrations))
	for _, migration := range manifest.Migrations {
		if strings.TrimSpace(migration.ID) == "" {
			return errors.New("package migration id is required")
		}
		migrationIDs = append(migrationIDs, migration.ID)
		if _, err := semver.NewConstraint(migration.From); err != nil {
			return fmt.Errorf("package migration %s from constraint %q is invalid: %w", migration.ID, migration.From, err)
		}
	}
	if err := uniqueStrings("package migration", migrationIDs); err != nil {
		return err
	}
	if err := uniqueStrings("breaking change", manifest.BreakingChanges); err != nil {
		return err
	}
	for _, claim := range manifest.Claims {
		if claim.Owner == "" {
			claim.Owner = "base"
		}
		if err := claim.validate(); err != nil {
			return err
		}
	}
	return nil
}

func LoadLock(moduleDir string) (*Lock, error) {
	data, err := os.ReadFile(filepath.Join(moduleDir, LockFileName))
	if err != nil {
		return nil, fmt.Errorf("read module lock: %w", err)
	}
	return ParseLock(data)
}

func ParseLock(data []byte) (*Lock, error) {
	var lock Lock
	if err := decodeStrictJSON(data, &lock); err != nil {
		return nil, fmt.Errorf("decode module lock: %w", err)
	}
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	return &lock, nil
}

func MarshalLock(lock *Lock) ([]byte, error) {
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (lock *Lock) Validate() error {
	if lock == nil {
		return errors.New("module lock is required")
	}
	if lock.Schema != LockSchema {
		return fmt.Errorf("module lock schema must be %q", LockSchema)
	}
	if err := validateIdentifier("module", lock.Module); err != nil {
		return err
	}
	if err := validateIdentifier("package", lock.Package); err != nil {
		return err
	}
	if _, err := semver.StrictNewVersion(lock.Version); err != nil {
		return fmt.Errorf("locked version %q is invalid: %w", lock.Version, err)
	}
	if strings.TrimSpace(lock.Source.Repository) == "" || strings.TrimSpace(lock.Source.Ref) == "" || !commitPattern.MatchString(lock.Source.Commit) {
		return errors.New("module lock source requires repository, ref, and a peeled commit")
	}
	if lock.Artifact.MediaType != ArtifactMediaType || !digestPattern.MatchString(lock.Artifact.Digest) || strings.TrimSpace(lock.Artifact.Signature) == "" {
		return errors.New("module lock artifact media type, SHA-256 digest, and signature identity are required")
	}
	if !digestPattern.MatchString(lock.CompositionDigest) {
		return errors.New("module lock composition digest must be a SHA-256 digest")
	}
	if len(lock.Contracts) == 0 {
		return errors.New("module lock contracts are required")
	}
	for contract, version := range lock.Contracts {
		if err := validateContractName(contract); err != nil {
			return err
		}
		if _, err := semver.NewVersion(version); err != nil {
			return fmt.Errorf("locked contract %s version %q is invalid: %w", contract, version, err)
		}
	}
	return nil
}

func ParseProvenance(data []byte) (*Provenance, error) {
	var provenance Provenance
	if err := decodeStrictJSON(data, &provenance); err != nil {
		return nil, fmt.Errorf("decode module provenance: %w", err)
	}
	if provenance.Schema != ProvenanceSchema || provenance.Package == "" || provenance.Version == "" ||
		provenance.Repository == "" || provenance.Ref == "" || !commitPattern.MatchString(provenance.Commit) ||
		provenance.ArtifactMediaType != ArtifactMediaType || !digestPattern.MatchString(provenance.ArtifactDigest) ||
		provenance.SignatureIdentity == "" {
		return nil, errors.New("module provenance is incomplete or invalid")
	}
	return &provenance, nil
}

func decodeStrictYAML(data []byte, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple YAML documents are not allowed")
		}
		return err
	}
	return nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func validateRelativePath(label, value string) error {
	if value == "" || strings.ContainsAny(value, "\\\x00") || filepath.IsAbs(value) || !filepath.IsLocal(value) {
		return fmt.Errorf("%s path %q is unsafe", label, value)
	}
	cleaned := filepath.Clean(value)
	if cleaned == "." || cleaned != value {
		return fmt.Errorf("%s path %q is not canonical", label, value)
	}
	return nil
}

func uniqueStrings(label string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s cannot be empty", label)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate %s %q", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateContractName(value string) error {
	if value == "" {
		return errors.New("contract name cannot be empty")
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (index > 0 && character >= 'A' && character <= 'Z') ||
			(index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return fmt.Errorf("contract name %q is invalid", value)
	}
	return nil
}

func (claim Claim) validate() error {
	if !claim.Kind.valid() || strings.TrimSpace(claim.Key) == "" || strings.TrimSpace(claim.Owner) == "" {
		return fmt.Errorf("invalid composition claim %+v", claim)
	}
	return nil
}

func digestBytes(value string) ([]byte, error) {
	if !digestPattern.MatchString(value) {
		return nil, fmt.Errorf("invalid digest %q", value)
	}
	return hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
}
