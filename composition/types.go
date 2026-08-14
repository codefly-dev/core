package composition

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	DescriptorFileName       = "module.codefly.yaml"
	LockFileName             = "module.codefly.lock"
	PackageManifestFileName  = "module.package.codefly.yaml"
	DescriptorKind           = "composed-module"
	PackageKind              = "module-package"
	PackageSchema            = "codefly/module-package/v2"
	LockSchema               = "codefly/module-lock/v2"
	ProvenanceSchema         = "codefly/module-provenance/v2"
	ArtifactMediaType        = "application/vnd.codefly.module.v2+tar"
	CompositionCatalogName   = ".codefly/composition.catalog.json"
	CompositionInputName     = ".codefly/composition.input.json"
	DevelopOverrideDirectory = ".codefly/develop"
)

const (
	ContractComposition    = "composition"
	ContractFrontendPlugin = "frontendPlugin"
	ContractSettings       = "settings"
	ContractPermissions    = "permissions"
	ContractFixtures       = "fixtures"
)

var (
	ErrUnsafeArchive     = errors.New("unsafe module archive")
	ErrDigestMismatch    = errors.New("module artifact digest mismatch")
	ErrSignature         = errors.New("module provenance signature is invalid")
	ErrPackageIdentity   = errors.New("module package identity mismatch")
	ErrMovedTag          = errors.New("module release tag moved")
	ErrContract          = errors.New("module contract negotiation failed")
	ErrCollision         = errors.New("module composition collision")
	ErrCacheVerification = errors.New("module cache verification failed")
)

type Descriptor struct {
	Kind          string        `yaml:"kind" json:"kind"`
	Name          string        `yaml:"name" json:"name"`
	Base          Base          `yaml:"base" json:"base"`
	Services      Services      `yaml:"services,omitempty" json:"services,omitempty"`
	Contributions Contributions `yaml:"contributions,omitempty" json:"contributions,omitempty"`
	Bindings      []Binding     `yaml:"bindings,omitempty" json:"bindings,omitempty"`
}

type Base struct {
	ID      string `yaml:"id" json:"id"`
	Version string `yaml:"version" json:"version"`
}

type Services struct {
	Include []string `yaml:"include,omitempty" json:"include,omitempty"`
}

type Contributions struct {
	Frontend    []FrontendContribution    `yaml:"frontend,omitempty" json:"frontend,omitempty"`
	Settings    []SettingsContribution    `yaml:"settings,omitempty" json:"settings,omitempty"`
	Permissions []PathContribution        `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	Fixtures    []PathContribution        `yaml:"fixtures,omitempty" json:"fixtures,omitempty"`
	Tests       []IntegrationContribution `yaml:"tests,omitempty" json:"tests,omitempty"`
}

type FrontendContribution struct {
	Path   string `yaml:"path" json:"path"`
	Export string `yaml:"export" json:"export"`
}

type SettingsContribution struct {
	Path    string `yaml:"path" json:"path"`
	Message string `yaml:"message" json:"message"`
}

type PathContribution struct {
	Path string `yaml:"path" json:"path"`
}

type IntegrationContribution struct {
	Path    string   `yaml:"path" json:"path"`
	Command []string `yaml:"command" json:"command"`
}

type Binding struct {
	Plugin string        `yaml:"plugin" json:"plugin"`
	Alias  string        `yaml:"alias" json:"alias"`
	Target BindingTarget `yaml:"target" json:"target"`
}

type BindingTarget struct {
	Module  string `yaml:"module" json:"module"`
	Service string `yaml:"service" json:"service"`
}

type PackageManifest struct {
	Kind                  string             `yaml:"kind" json:"kind"`
	Schema                string             `yaml:"schema" json:"schema"`
	ID                    string             `yaml:"id" json:"id"`
	Version               string             `yaml:"version" json:"version"`
	MinimumCodeflyVersion string             `yaml:"minimum-codefly-version" json:"minimumCodeflyVersion"`
	ArtifactRoots         []string           `yaml:"artifact-roots" json:"artifactRoots"`
	EntryPoints           []EntryPoint       `yaml:"entry-points,omitempty" json:"entryPoints,omitempty"`
	Services              []ProvidedService  `yaml:"services,omitempty" json:"services,omitempty"`
	Contracts             map[string]string  `yaml:"contracts" json:"contracts"`
	Generators            []PackageCommand   `yaml:"generators,omitempty" json:"generators,omitempty"`
	Conformance           []PackageCommand   `yaml:"conformance,omitempty" json:"conformance,omitempty"`
	Migrations            []PackageMigration `yaml:"migrations,omitempty" json:"migrations,omitempty"`
	BreakingChanges       []string           `yaml:"breaking-changes,omitempty" json:"breakingChanges,omitempty"`
	ReservedNamespaces    []string           `yaml:"reserved-namespaces,omitempty" json:"reservedNamespaces,omitempty"`
	Claims                []Claim            `yaml:"claims,omitempty" json:"claims,omitempty"`
}

type EntryPoint struct {
	Name    string   `yaml:"name" json:"name"`
	Command []string `yaml:"command" json:"command"`
}

type ProvidedService struct {
	Name      string   `yaml:"name" json:"name"`
	Endpoints []string `yaml:"endpoints,omitempty" json:"endpoints,omitempty"`
}

type PackageCommand struct {
	Name             string   `yaml:"name" json:"name"`
	Command          []string `yaml:"command" json:"command"`
	WorkingDirectory string   `yaml:"working-directory,omitempty" json:"workingDirectory,omitempty"`
}

type PackageMigration struct {
	ID       string `yaml:"id" json:"id"`
	From     string `yaml:"from" json:"from"`
	Breaking bool   `yaml:"breaking,omitempty" json:"breaking,omitempty"`
}

type Lock struct {
	Schema            string            `json:"schema"`
	Module            string            `json:"module"`
	Package           string            `json:"package"`
	Version           string            `json:"version"`
	Source            SourceLock        `json:"source"`
	Artifact          ArtifactLock      `json:"artifact"`
	Contracts         map[string]string `json:"contracts"`
	CompositionDigest string            `json:"compositionDigest"`
}

type SourceLock struct {
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
	Commit     string `json:"commit"`
}

type ArtifactLock struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Signature string `json:"signature"`
}

type Provenance struct {
	Schema            string `json:"schema"`
	Package           string `json:"package"`
	Version           string `json:"version"`
	Repository        string `json:"repository"`
	Ref               string `json:"ref"`
	Commit            string `json:"commit"`
	ArtifactMediaType string `json:"artifactMediaType"`
	ArtifactDigest    string `json:"artifactDigest"`
	SignatureIdentity string `json:"signatureIdentity"`
}

type Release struct {
	Repository string
	Ref        string
	Commit     string
	Artifact   []byte
	Provenance []byte
	Signature  []byte
}

type VerifiedRelease struct {
	Release    *Release
	Provenance *Provenance
	Manifest   *PackageManifest
	Digest     string
}

type CollisionKind string

const (
	CollisionRoute          CollisionKind = "route"
	CollisionPermission     CollisionKind = "permission"
	CollisionSettingsField  CollisionKind = "settings-field"
	CollisionServiceBinding CollisionKind = "service-binding"
	CollisionMigration      CollisionKind = "migration"
	CollisionPackage        CollisionKind = "package"
	CollisionTopology       CollisionKind = "topology"
)

var collisionKinds = []CollisionKind{
	CollisionRoute,
	CollisionPermission,
	CollisionSettingsField,
	CollisionServiceBinding,
	CollisionMigration,
	CollisionPackage,
	CollisionTopology,
}

type Claim struct {
	Kind  CollisionKind `yaml:"kind" json:"kind"`
	Key   string        `yaml:"key" json:"key"`
	Owner string        `yaml:"owner" json:"owner"`
}

type Catalog struct {
	Schema string  `json:"schema"`
	Claims []Claim `json:"claims"`
}

func (c CollisionKind) valid() bool {
	return slices.Contains(collisionKinds, c)
}

var identifierPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._/-]*[a-z0-9])?$`)

func validateIdentifier(label, value string) error {
	if !identifierPattern.MatchString(value) || strings.Contains(value, "//") || strings.Contains(value, "..") {
		return fmt.Errorf("%s %q is invalid", label, value)
	}
	return nil
}
