package composition

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/codefly-dev/core/shared"
	coreversion "github.com/codefly-dev/core/version"
)

type ResolveRequest struct {
	Package string
	Version string
	Current *Lock
}

type Resolver interface {
	Resolve(context.Context, ResolveRequest) (*Release, error)
	Fetch(context.Context, *Lock) (*Release, error)
}

type Engine struct {
	ProjectRoot        string
	ToolVersion        string
	Resolver           Resolver
	Trust              TrustPolicy
	Materializer       *Materializer
	Renderer           Renderer
	SupportedContracts map[string][]string
}

type UpdateResult struct {
	Lock       *Lock
	Projection string
	Report     *SemanticReport
	Applied    bool
}

type MaterializeOptions struct {
	Namespace string
	LockPath  string
	CI        bool
}

func NewEngine(projectRoot string, resolver Resolver, trust TrustPolicy) *Engine {
	return &Engine{
		ProjectRoot:        projectRoot,
		Resolver:           resolver,
		Trust:              trust,
		Materializer:       NewMaterializer(projectRoot),
		Renderer:           Renderer{Runner: ExecCommandRunner{}},
		SupportedContracts: cloneVersions(DefaultSupportedContracts),
	}
}

func (engine *Engine) Update(ctx context.Context, moduleDir, targetVersion string, apply bool) (*UpdateResult, error) {
	descriptor, err := LoadDescriptor(moduleDir)
	if err != nil {
		return nil, err
	}
	target, err := semver.StrictNewVersion(targetVersion)
	if err != nil {
		return nil, fmt.Errorf("target module version %q is invalid: %w", targetVersion, err)
	}
	constraint, _ := semver.NewConstraint(descriptor.Base.Version)
	if !constraint.Check(target) {
		return nil, fmt.Errorf("target module version %s does not satisfy descriptor range %s", targetVersion, descriptor.Base.Version)
	}
	current, err := loadOptionalLock(moduleDir)
	if err != nil {
		return nil, err
	}
	if current != nil && (current.Module != descriptor.Name || current.Package != descriptor.Base.ID) {
		return nil, ErrPackageIdentity
	}
	if engine.Resolver == nil {
		return nil, errors.New("module release resolver is required for update")
	}
	release, err := engine.Resolver.Resolve(ctx, ResolveRequest{Package: descriptor.Base.ID, Version: targetVersion, Current: current})
	if err != nil {
		return nil, fmt.Errorf("resolve module release: %w", err)
	}
	verified, err := VerifyRelease(release, descriptor.Base.ID, targetVersion, engine.Trust)
	if err != nil {
		return nil, err
	}
	if err := rejectMovedTag(current, verified); err != nil {
		return nil, err
	}
	toolVersion, err := engine.toolVersion(ctx)
	if err != nil {
		return nil, err
	}
	contracts, err := NegotiateContracts(descriptor, verified.Manifest, toolVersion, engine.supportedContracts())
	if err != nil {
		return nil, err
	}
	compositionDigest, err := CompositionDigest(moduleDir, descriptor)
	if err != nil {
		return nil, err
	}
	candidate := lockForRelease(descriptor, verified, contracts, compositionDigest)
	base, err := engine.materializer().Materialize(ctx, verified)
	if err != nil {
		return nil, err
	}
	namespace, err := ResolveNamespace(engine.ProjectRoot, moduleDir, "stable", "", candidate)
	if err != nil {
		return nil, err
	}
	staging, err := engine.projectionStaging(namespace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	catalog, validations, renderErr := engine.Renderer.Render(ctx, base, moduleDir, staging, descriptor, verified.Manifest, contracts)
	if renderErr != nil {
		failedReport := newSemanticReport(descriptor, current, candidate, nil, verified.Manifest, nil, nil, validations)
		failedReport.BlockedReasons = append(failedReport.BlockedReasons, renderErr.Error())
		failedReport.LockDiff, _ = LockDiff(current, candidate)
		return &UpdateResult{Lock: candidate, Projection: namespace.ProjectionDir, Report: failedReport}, renderErr
	}
	if err := writeProjectionMetadata(staging, candidate, catalog); err != nil {
		return nil, err
	}
	oldClaims, oldManifest := engine.currentState(moduleDir, current)
	report := newSemanticReport(descriptor, current, candidate, oldManifest, verified.Manifest, oldClaims, catalog.Claims, validations)
	report.LockDiff, err = LockDiff(current, candidate)
	if err != nil {
		return nil, err
	}
	result := &UpdateResult{Lock: candidate, Report: report, Projection: namespace.ProjectionDir}
	if !apply || len(report.BlockedReasons) > 0 {
		return result, nil
	}
	if err := promoteProjection(staging, namespace.ProjectionDir, candidate); err != nil {
		return nil, err
	}
	lockData, err := MarshalLock(candidate)
	if err != nil {
		return nil, err
	}
	if err := shared.WriteFileAtomic(ctx, filepath.Join(moduleDir, LockFileName), lockData, 0o644); err != nil {
		return nil, fmt.Errorf("atomically update module lock: %w", err)
	}
	result.Applied = true
	return result, nil
}

func (engine *Engine) Materialize(ctx context.Context, moduleDir string, options MaterializeOptions) (string, error) {
	descriptor, err := LoadDescriptor(moduleDir)
	if err != nil {
		return "", err
	}
	lockPath := options.LockPath
	if lockPath == "" {
		lockPath = filepath.Join(moduleDir, LockFileName)
	}
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		return "", fmt.Errorf("read module lock: %w", err)
	}
	lock, err := ParseLock(lockData)
	if err != nil {
		return "", err
	}
	if lock.Module != descriptor.Name || lock.Package != descriptor.Base.ID {
		return "", ErrPackageIdentity
	}
	compositionDigest, err := CompositionDigest(moduleDir, descriptor)
	if err != nil {
		return "", err
	}
	if compositionDigest != lock.CompositionDigest {
		return "", fmt.Errorf("composition contribution digest differs from lock: got %s, want %s", compositionDigest, lock.CompositionDigest)
	}
	namespaceName := options.Namespace
	if namespaceName == "" {
		namespaceName = "stable"
	}
	base, manifest, effectiveLock, err := engine.resolveMaterializationSource(ctx, descriptor, lock, namespaceName, options.CI)
	if err != nil {
		return "", err
	}
	toolVersion, err := engine.toolVersion(ctx)
	if err != nil {
		return "", err
	}
	if err := ValidateLockedContracts(descriptor, manifest, effectiveLock, toolVersion, engine.supportedContracts()); err != nil {
		return "", err
	}
	namespace, err := ResolveNamespace(engine.ProjectRoot, moduleDir, namespaceName, lockPath, effectiveLock)
	if err != nil {
		return "", err
	}
	if projectionMatches(namespace.ProjectionDir, effectiveLock) {
		return namespace.ProjectionDir, nil
	}
	staging, err := engine.projectionStaging(namespace)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	catalog, _, err := engine.Renderer.Render(ctx, base, moduleDir, staging, descriptor, manifest, lock.Contracts)
	if err != nil {
		return "", err
	}
	if err := writeProjectionMetadata(staging, effectiveLock, catalog); err != nil {
		return "", err
	}
	if err := promoteProjection(staging, namespace.ProjectionDir, effectiveLock); err != nil {
		return "", err
	}
	return namespace.ProjectionDir, nil
}

func (engine *Engine) Source(ctx context.Context, moduleDir string, options MaterializeOptions) (string, error) {
	descriptor, err := LoadDescriptor(moduleDir)
	if err != nil {
		return "", err
	}
	if options.Namespace == "dev" && !options.CI {
		override, overrideErr := LoadDevelopOverride(engine.ProjectRoot, descriptor.Name)
		if overrideErr == nil {
			return override.Source, nil
		}
		if !errors.Is(overrideErr, os.ErrNotExist) {
			return "", overrideErr
		}
	}
	lock, err := LoadLock(moduleDir)
	if err != nil {
		return "", err
	}
	if cached, cacheErr := engine.materializer().Cached(lock); cacheErr == nil {
		return cached, nil
	}
	if _, err := engine.Materialize(ctx, moduleDir, options); err != nil {
		return "", err
	}
	return engine.materializer().Cached(lock)
}

func (engine *Engine) Rollback(ctx context.Context, moduleDir string, priorLock []byte) (string, error) {
	lock, err := ParseLock(priorLock)
	if err != nil {
		return "", err
	}
	descriptor, err := LoadDescriptor(moduleDir)
	if err != nil {
		return "", err
	}
	if lock.Module != descriptor.Name || lock.Package != descriptor.Base.ID {
		return "", ErrPackageIdentity
	}
	temporaryDirectory := filepath.Join(engine.ProjectRoot, ".codefly", "rollback")
	if err := os.MkdirAll(temporaryDirectory, 0o700); err != nil {
		return "", err
	}
	temporaryFile, err := os.CreateTemp(temporaryDirectory, descriptor.Name+"-*.lock")
	if err != nil {
		return "", err
	}
	temporaryLock := temporaryFile.Name()
	if _, err := temporaryFile.Write(priorLock); err != nil {
		_ = temporaryFile.Close()
		return "", err
	}
	if err := temporaryFile.Close(); err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(temporaryLock) }()
	projection, err := engine.Materialize(ctx, moduleDir, MaterializeOptions{Namespace: "stable", LockPath: temporaryLock, CI: true})
	if err != nil {
		return "", err
	}
	canonical, err := MarshalLock(lock)
	if err != nil {
		return "", err
	}
	if err := shared.WriteFileAtomic(ctx, filepath.Join(moduleDir, LockFileName), canonical, 0o644); err != nil {
		return "", err
	}
	return projection, nil
}

func (engine *Engine) resolveMaterializationSource(ctx context.Context, descriptor *Descriptor, lock *Lock, namespace string, ci bool) (string, *PackageManifest, *Lock, error) {
	if namespace == "dev" && !ci {
		override, err := LoadDevelopOverride(engine.ProjectRoot, descriptor.Name)
		if err == nil {
			manifest, validationErr := validateDevelopSource(override, descriptor, lock)
			if validationErr != nil {
				return "", nil, nil, validationErr
			}
			localDigest, digestErr := sourceTreeDigest(override.Source)
			if digestErr != nil {
				return "", nil, nil, digestErr
			}
			effective := *lock
			effective.Version = manifest.Version
			effective.Artifact.Digest = localDigest
			effective.Artifact.Signature = "local-development"
			return override.Source, manifest, &effective, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", nil, nil, err
		}
	}
	cache, err := engine.materializer().Cached(lock)
	if err == nil {
		manifest, manifestErr := LoadPackageManifest(cache)
		return cache, manifest, lock, manifestErr
	}
	if engine.Resolver == nil {
		return "", nil, nil, fmt.Errorf("verified module cache is unavailable offline: %w", err)
	}
	release, err := engine.Resolver.Fetch(ctx, lock)
	if err != nil {
		return "", nil, nil, fmt.Errorf("fetch locked module release: %w", err)
	}
	verified, err := VerifyLockedRelease(release, lock, engine.Trust)
	if err != nil {
		return "", nil, nil, err
	}
	cache, err = engine.materializer().Materialize(ctx, verified)
	if err != nil {
		return "", nil, nil, err
	}
	return cache, verified.Manifest, lock, nil
}

func validateDevelopSource(override *DevelopOverride, descriptor *Descriptor, lock *Lock) (*PackageManifest, error) {
	if override.Module != descriptor.Name || override.Package != descriptor.Base.ID || override.Package != lock.Package {
		return nil, ErrPackageIdentity
	}
	manifest, err := LoadPackageManifest(override.Source)
	if err != nil {
		return nil, err
	}
	if manifest.ID != override.Package {
		return nil, ErrPackageIdentity
	}
	for contract, versionValue := range lock.Contracts {
		constraintValue, exists := manifest.Contracts[contract]
		if !exists {
			return nil, fmt.Errorf("%w: local source is missing contract %s", ErrContract, contract)
		}
		constraint, _ := semver.NewConstraint(constraintValue)
		version, _ := semver.NewVersion(versionValue)
		if !constraint.Check(version) {
			return nil, fmt.Errorf("%w: local source contract %s does not support %s", ErrContract, contract, versionValue)
		}
	}
	return manifest, nil
}

func sourceTreeDigest(source string) (string, error) {
	hash := sha256.New()
	if err := hashContribution(hash, source); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func lockForRelease(descriptor *Descriptor, release *VerifiedRelease, contracts map[string]string, compositionDigest string) *Lock {
	return &Lock{
		Schema: LockSchema, Module: descriptor.Name, Package: release.Manifest.ID, Version: release.Manifest.Version,
		Source:    SourceLock{Repository: release.Release.Repository, Ref: release.Release.Ref, Commit: release.Release.Commit},
		Artifact:  ArtifactLock{MediaType: ArtifactMediaType, Digest: release.Digest, Signature: release.Provenance.SignatureIdentity},
		Contracts: contracts, CompositionDigest: compositionDigest,
	}
}

func rejectMovedTag(current *Lock, candidate *VerifiedRelease) error {
	if current == nil || current.Package != candidate.Manifest.ID || current.Version != candidate.Manifest.Version || current.Source.Ref != candidate.Release.Ref {
		return nil
	}
	if current.Source.Repository != candidate.Release.Repository || current.Source.Commit != candidate.Release.Commit ||
		current.Artifact.Digest != candidate.Digest || current.Artifact.Signature != candidate.Provenance.SignatureIdentity {
		return ErrMovedTag
	}
	return nil
}

func loadOptionalLock(moduleDir string) (*Lock, error) {
	lock, err := LoadLock(moduleDir)
	if errors.Is(err, os.ErrNotExist) || errors.Is(errors.Unwrap(err), os.ErrNotExist) {
		return nil, nil
	}
	return lock, err
}

func (engine *Engine) projectionStaging(namespace *Namespace) (string, error) {
	parent := filepath.Dir(namespace.ProjectionDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, ".candidate-")
}

func writeProjectionMetadata(projection string, lock *Lock, catalog *Catalog) error {
	if err := os.MkdirAll(filepath.Join(projection, ".codefly"), 0o755); err != nil {
		return err
	}
	lockData, err := MarshalLock(lock)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(projection, ".codefly", "projection.lock"), lockData, 0o644); err != nil {
		return err
	}
	catalog.Schema = catalogSchema
	catalogData, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(projection, filepath.FromSlash(CompositionCatalogName)), append(catalogData, '\n'), 0o644)
}

func projectionMatches(projection string, lock *Lock) bool {
	data, err := os.ReadFile(filepath.Join(projection, ".codefly", "projection.lock"))
	if err != nil {
		return false
	}
	actual, err := ParseLock(data)
	return err == nil && locksEqual(actual, lock)
}

func promoteProjection(staging, destination string, lock *Lock) error {
	if projectionMatches(destination, lock) {
		return nil
	}
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		return fmt.Errorf("promote composed projection: %w", err)
	}
	return nil
}

func locksEqual(left, right *Lock) bool {
	leftData, leftErr := MarshalLock(left)
	rightData, rightErr := MarshalLock(right)
	return leftErr == nil && rightErr == nil && string(leftData) == string(rightData)
}

func (engine *Engine) currentState(moduleDir string, lock *Lock) ([]Claim, *PackageManifest) {
	if lock == nil {
		return nil, nil
	}
	namespace, err := ResolveNamespace(engine.ProjectRoot, moduleDir, "stable", "", lock)
	if err != nil {
		return nil, nil
	}
	catalog, err := LoadCatalog(namespace.ProjectionDir)
	if err != nil {
		return nil, nil
	}
	manifest, _ := LoadPackageManifest(namespace.ProjectionDir)
	return catalog.Claims, manifest
}

func LockDiff(before, after *Lock) (string, error) {
	afterData, err := MarshalLock(after)
	if err != nil {
		return "", err
	}
	var beforeData []byte
	if before != nil {
		beforeData, err = MarshalLock(before)
		if err != nil {
			return "", err
		}
	}
	if string(beforeData) == string(afterData) {
		return "", nil
	}
	var output strings.Builder
	output.WriteString("--- module.codefly.lock\n+++ module.codefly.lock (candidate)\n")
	for _, line := range strings.Split(strings.TrimSuffix(string(beforeData), "\n"), "\n") {
		if line != "" {
			output.WriteString("-" + line + "\n")
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(afterData), "\n"), "\n") {
		output.WriteString("+" + line + "\n")
	}
	return output.String(), nil
}

func (engine *Engine) materializer() *Materializer {
	if engine.Materializer == nil {
		engine.Materializer = NewMaterializer(engine.ProjectRoot)
	}
	return engine.Materializer
}

func (engine *Engine) toolVersion(ctx context.Context) (string, error) {
	if engine.ToolVersion != "" {
		return engine.ToolVersion, nil
	}
	value, err := coreversion.Version(ctx)
	if err != nil {
		return "", fmt.Errorf("load Codefly version: %w", err)
	}
	return value, nil
}

func (engine *Engine) supportedContracts() map[string][]string {
	if engine.SupportedContracts == nil {
		return DefaultSupportedContracts
	}
	return engine.SupportedContracts
}

func cloneVersions(source map[string][]string) map[string][]string {
	clone := make(map[string][]string, len(source))
	for contract, versions := range source {
		clone[contract] = append([]string(nil), versions...)
		sort.Strings(clone[contract])
	}
	return clone
}
