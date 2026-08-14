package composition

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
	Namespace  *Namespace
	Report     *SemanticReport
	Applied    bool
}

type Materialization struct {
	Source     string
	Projection string
	Namespace  *Namespace
}

type resolvedMaterializationSource struct {
	path     string
	manifest *PackageManifest
	lock     *Lock
	local    bool
}

type MaterializeOptions struct {
	Namespace string
	LockPath  string
	CI        bool
}

const projectionMarkerPath = ".codefly/projection.json"

type projectionMarker struct {
	Schema string `json:"schema"`
	Digest string `json:"digest"`
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
	inputs, err := LoadContributionInputs(moduleDir, descriptor)
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
	contracts, err := NegotiateContracts(descriptor, verified.manifest, toolVersion, engine.supportedContracts())
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
	catalog, validations, renderErr := engine.Renderer.Render(ctx, base, moduleDir, staging, namespace, descriptor, verified.manifest, contracts, inputs)
	if renderErr != nil {
		failedReport := newSemanticReport(descriptor, current, candidate, nil, verified.manifest, nil, nil, validations)
		failedReport.BlockedReasons = append(failedReport.BlockedReasons, renderErr.Error())
		failedReport.LockDiff, _ = LockDiff(current, candidate)
		return &UpdateResult{Lock: candidate, Projection: namespace.ProjectionDir, Namespace: namespace, Report: failedReport}, renderErr
	}
	if err := writeProjectionMetadata(staging, candidate, catalog); err != nil {
		return nil, err
	}
	oldCatalog, oldManifest, err := engine.currentState(ctx, moduleDir, descriptor, inputs, current)
	if err != nil {
		return nil, fmt.Errorf("render current module composition for semantic report: %w", err)
	}
	report := newSemanticReport(descriptor, current, candidate, oldManifest, verified.manifest, oldCatalog, catalog, validations)
	report.LockDiff, err = LockDiff(current, candidate)
	if err != nil {
		return nil, err
	}
	result := &UpdateResult{Lock: candidate, Report: report, Projection: namespace.ProjectionDir, Namespace: namespace}
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

func (engine *Engine) Materialize(ctx context.Context, moduleDir string, options MaterializeOptions) (*Materialization, error) {
	descriptor, err := LoadDescriptor(moduleDir)
	if err != nil {
		return nil, err
	}
	inputs, err := LoadContributionInputs(moduleDir, descriptor)
	if err != nil {
		return nil, err
	}
	lockPath := options.LockPath
	if lockPath == "" {
		lockPath = filepath.Join(moduleDir, LockFileName)
	}
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("read module lock: %w", err)
	}
	lock, err := ParseLock(lockData)
	if err != nil {
		return nil, err
	}
	if lock.Module != descriptor.Name || lock.Package != descriptor.Base.ID {
		return nil, ErrPackageIdentity
	}
	compositionDigest, err := CompositionDigest(moduleDir, descriptor)
	if err != nil {
		return nil, err
	}
	namespaceName := options.Namespace
	if namespaceName == "" {
		namespaceName = "stable"
	}
	development := namespaceName == "dev" && !options.CI
	if !development && compositionDigest != lock.CompositionDigest {
		return nil, fmt.Errorf("composition contribution digest differs from lock: got %s, want %s", compositionDigest, lock.CompositionDigest)
	}
	resolved, err := engine.resolveMaterializationSource(ctx, descriptor, lock, namespaceName, options.CI)
	if err != nil {
		return nil, err
	}
	if development {
		resolved.lock.CompositionDigest = compositionDigest
	}
	toolVersion, err := engine.toolVersion(ctx)
	if err != nil {
		return nil, err
	}
	validateContracts := ValidateLockedContracts
	if resolved.local {
		validateContracts = ValidateDevelopContracts
	}
	if err := validateContracts(descriptor, resolved.manifest, resolved.lock, toolVersion, engine.supportedContracts()); err != nil {
		return nil, err
	}
	namespace, err := ResolveNamespace(engine.ProjectRoot, moduleDir, namespaceName, lockPath, resolved.lock)
	if err != nil {
		return nil, err
	}
	if err := namespace.Prepare(); err != nil {
		return nil, err
	}
	if projectionMatches(namespace.ProjectionDir, resolved.lock) {
		return &Materialization{Source: resolved.path, Projection: namespace.ProjectionDir, Namespace: namespace}, nil
	}
	staging, err := engine.projectionStaging(namespace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	catalog, _, err := engine.Renderer.Render(ctx, resolved.path, moduleDir, staging, namespace, descriptor, resolved.manifest, lock.Contracts, inputs)
	if err != nil {
		return nil, err
	}
	if err := writeProjectionMetadata(staging, resolved.lock, catalog); err != nil {
		return nil, err
	}
	if err := promoteProjection(staging, namespace.ProjectionDir, resolved.lock); err != nil {
		return nil, err
	}
	return &Materialization{Source: resolved.path, Projection: namespace.ProjectionDir, Namespace: namespace}, nil
}

func (engine *Engine) Source(ctx context.Context, moduleDir string, options MaterializeOptions) (string, error) {
	descriptor, err := LoadDescriptor(moduleDir)
	if err != nil {
		return "", err
	}
	if _, err := LoadContributionInputs(moduleDir, descriptor); err != nil {
		return "", err
	}
	lockPath := options.LockPath
	if lockPath == "" {
		lockPath = filepath.Join(moduleDir, LockFileName)
	}
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		return "", err
	}
	lock, err := ParseLock(lockData)
	if err != nil {
		return "", err
	}
	if lock.Module != descriptor.Name || lock.Package != descriptor.Base.ID {
		return "", ErrPackageIdentity
	}
	digest, err := CompositionDigest(moduleDir, descriptor)
	if err != nil {
		return "", err
	}
	namespace := options.Namespace
	if namespace == "" {
		namespace = "stable"
	}
	development := namespace == "dev" && !options.CI
	if !development && digest != lock.CompositionDigest {
		return "", fmt.Errorf("composition contribution digest differs from lock: got %s, want %s", digest, lock.CompositionDigest)
	}
	resolved, err := engine.resolveMaterializationSource(ctx, descriptor, lock, namespace, options.CI)
	if err != nil {
		return "", err
	}
	if development {
		resolved.lock.CompositionDigest = digest
	}
	toolVersion, err := engine.toolVersion(ctx)
	if err != nil {
		return "", err
	}
	validateContracts := ValidateLockedContracts
	if resolved.local {
		validateContracts = ValidateDevelopContracts
	}
	if err := validateContracts(descriptor, resolved.manifest, resolved.lock, toolVersion, engine.supportedContracts()); err != nil {
		return "", err
	}
	return resolved.path, nil
}

func (engine *Engine) Rollback(ctx context.Context, moduleDir string, priorLock []byte) (*Materialization, error) {
	lock, err := ParseLock(priorLock)
	if err != nil {
		return nil, err
	}
	descriptor, err := LoadDescriptor(moduleDir)
	if err != nil {
		return nil, err
	}
	if lock.Module != descriptor.Name || lock.Package != descriptor.Base.ID {
		return nil, ErrPackageIdentity
	}
	temporaryDirectory := filepath.Join(engine.ProjectRoot, ".codefly", "rollback")
	if err := os.MkdirAll(temporaryDirectory, 0o700); err != nil {
		return nil, err
	}
	temporaryFile, err := os.CreateTemp(temporaryDirectory, descriptor.Name+"-*.lock")
	if err != nil {
		return nil, err
	}
	temporaryLock := temporaryFile.Name()
	if _, err := temporaryFile.Write(priorLock); err != nil {
		_ = temporaryFile.Close()
		return nil, err
	}
	if err := temporaryFile.Close(); err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(temporaryLock) }()
	materialized, err := engine.Materialize(ctx, moduleDir, MaterializeOptions{Namespace: "stable", LockPath: temporaryLock, CI: true})
	if err != nil {
		return nil, err
	}
	canonical, err := MarshalLock(lock)
	if err != nil {
		return nil, err
	}
	if err := shared.WriteFileAtomic(ctx, filepath.Join(moduleDir, LockFileName), canonical, 0o644); err != nil {
		return nil, err
	}
	return materialized, nil
}

func (engine *Engine) resolveMaterializationSource(ctx context.Context, descriptor *Descriptor, lock *Lock, namespace string, ci bool) (*resolvedMaterializationSource, error) {
	if namespace == "dev" && !ci {
		override, err := LoadDevelopOverride(engine.ProjectRoot, descriptor.Name)
		if err == nil {
			manifest, validationErr := validateDevelopSource(override, descriptor, lock)
			if validationErr != nil {
				return nil, validationErr
			}
			localDigest, digestErr := sourceTreeDigest(override.Source)
			if digestErr != nil {
				return nil, digestErr
			}
			effective := *lock
			effective.Version = manifest.Version
			effective.Artifact.Digest = localDigest
			effective.Artifact.Signature = "local-development"
			return &resolvedMaterializationSource{path: override.Source, manifest: manifest, lock: &effective, local: true}, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	cache, err := engine.materializer().Cached(lock)
	if err == nil {
		manifest, manifestErr := LoadPackageManifest(cache)
		if manifestErr != nil {
			return nil, manifestErr
		}
		return &resolvedMaterializationSource{path: cache, manifest: manifest, lock: lock}, nil
	}
	if engine.Resolver == nil {
		return nil, fmt.Errorf("verified module cache is unavailable offline: %w", err)
	}
	release, err := engine.Resolver.Fetch(ctx, lock)
	if err != nil {
		return nil, fmt.Errorf("fetch locked module release: %w", err)
	}
	verified, err := VerifyLockedRelease(release, lock, engine.Trust)
	if err != nil {
		return nil, err
	}
	cache, err = engine.materializer().Materialize(ctx, verified)
	if err != nil {
		return nil, err
	}
	return &resolvedMaterializationSource{path: cache, manifest: verified.manifest, lock: lock}, nil
}

func validateDevelopSource(override *DevelopOverride, descriptor *Descriptor, lock *Lock) (*PackageManifest, error) {
	if override.Module != descriptor.Name || override.Package != descriptor.Base.ID || override.Package != lock.Package {
		return nil, ErrPackageIdentity
	}
	if !maps.Equal(override.Contracts, lock.Contracts) {
		return nil, fmt.Errorf("%w: local override contracts differ from the lock", ErrContract)
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
		Schema: LockSchema, Module: descriptor.Name, Package: release.manifest.ID, Version: release.manifest.Version,
		Source:    SourceLock{Repository: release.release.Repository, Ref: release.release.Ref, Commit: release.release.Commit},
		Artifact:  ArtifactLock{MediaType: ArtifactMediaType, Digest: release.digest, Signature: release.provenance.SignatureIdentity},
		Contracts: contracts, CompositionDigest: compositionDigest,
	}
}

func rejectMovedTag(current *Lock, candidate *VerifiedRelease) error {
	if current == nil || current.Package != candidate.manifest.ID || current.Version != candidate.manifest.Version || current.Source.Ref != candidate.release.Ref {
		return nil
	}
	if current.Source.Repository != candidate.release.Repository || current.Source.Commit != candidate.release.Commit ||
		current.Artifact.Digest != candidate.digest || current.Artifact.Signature != candidate.provenance.SignatureIdentity {
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
	if err := os.WriteFile(filepath.Join(projection, filepath.FromSlash(CompositionCatalogName)), append(catalogData, '\n'), 0o644); err != nil {
		return err
	}
	digest, err := projectionContentDigest(projection)
	if err != nil {
		return err
	}
	markerData, err := json.Marshal(projectionMarker{Schema: "codefly/composed-projection/v2", Digest: digest})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(projection, filepath.FromSlash(projectionMarkerPath)), append(markerData, '\n'), 0o644)
}

func projectionMatches(projection string, lock *Lock) bool {
	return projectionMatchesMode(projection, lock, true)
}

func projectionMatchesMode(projection string, lock *Lock, requireReadOnly bool) bool {
	data, err := os.ReadFile(filepath.Join(projection, ".codefly", "projection.lock"))
	if err != nil {
		return false
	}
	actual, err := ParseLock(data)
	if err != nil || !locksEqual(actual, lock) {
		return false
	}
	markerData, err := os.ReadFile(filepath.Join(projection, filepath.FromSlash(projectionMarkerPath)))
	if err != nil {
		return false
	}
	var marker projectionMarker
	if err := decodeStrictJSON(markerData, &marker); err != nil || marker.Schema != "codefly/composed-projection/v2" || !digestPattern.MatchString(marker.Digest) {
		return false
	}
	digest, err := projectionContentDigest(projection)
	if err != nil || digest != marker.Digest {
		return false
	}
	return !requireReadOnly || treeReadOnly(projection) == nil
}

func promoteProjection(staging, destination string, lock *Lock) error {
	if !projectionMatchesMode(staging, lock, false) {
		return errors.New("candidate projection failed content verification")
	}
	markerData, err := os.ReadFile(filepath.Join(staging, filepath.FromSlash(projectionMarkerPath)))
	if err != nil {
		return err
	}
	var marker projectionMarker
	if err := decodeStrictJSON(markerData, &marker); err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	revisions := filepath.Join(parent, ".revisions")
	if err := os.MkdirAll(revisions, 0o755); err != nil {
		return err
	}
	revision := filepath.Join(revisions, strings.TrimPrefix(marker.Digest, "sha256:"))
	if projectionMatches(revision, lock) {
		if err := removeCacheTree(staging); err != nil {
			return err
		}
	} else {
		if _, err := os.Lstat(revision); err == nil {
			recovery, err := os.MkdirTemp(revisions, strings.TrimPrefix(marker.Digest, "sha256:")+"-recovered-")
			if err != nil {
				return err
			}
			if err := os.Remove(recovery); err != nil {
				return err
			}
			revision = recovery
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(staging, revision); err != nil {
			return fmt.Errorf("promote composed projection revision: %w", err)
		}
		if err := makeReadOnly(revision); err != nil {
			_ = removeCacheTree(revision)
			return err
		}
	}
	linkFile, err := os.CreateTemp(parent, ".projection-link-")
	if err != nil {
		return err
	}
	linkPath := linkFile.Name()
	if err := linkFile.Close(); err != nil {
		return err
	}
	if err := os.Remove(linkPath); err != nil {
		return err
	}
	target, err := filepath.Rel(parent, revision)
	if err != nil {
		return err
	}
	if err := os.Symlink(target, linkPath); err != nil {
		return err
	}
	defer func() { _ = os.Remove(linkPath) }()
	if info, statErr := os.Lstat(destination); statErr == nil && info.Mode()&os.ModeSymlink == 0 {
		return errors.New("composed projection destination is not an atomic link")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err := os.Rename(linkPath, destination); err != nil {
		return fmt.Errorf("activate composed projection: %w", err)
	}
	return nil
}

func projectionContentDigest(projection string) (string, error) {
	resolved, err := filepath.EvalSymlinks(projection)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if err := writeCanonicalArchive(resolved, hash, map[string]struct{}{projectionMarkerPath: {}}); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func locksEqual(left, right *Lock) bool {
	leftData, leftErr := MarshalLock(left)
	rightData, rightErr := MarshalLock(right)
	return leftErr == nil && rightErr == nil && string(leftData) == string(rightData)
}

func (engine *Engine) currentState(ctx context.Context, moduleDir string, descriptor *Descriptor, inputs []CatalogInput, lock *Lock) (*Catalog, *PackageManifest, error) {
	if lock == nil {
		return nil, nil, nil
	}
	resolved, err := engine.resolveMaterializationSource(ctx, descriptor, lock, "stable", true)
	if err != nil {
		return nil, nil, err
	}
	toolVersion, err := engine.toolVersion(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := ValidateDevelopContracts(descriptor, resolved.manifest, resolved.lock, toolVersion, engine.supportedContracts()); err != nil {
		return nil, nil, err
	}
	namespace, err := ResolveNamespace(engine.ProjectRoot, moduleDir, "report", "", lock)
	if err != nil {
		return nil, nil, err
	}
	staging, err := engine.projectionStaging(namespace)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = removeCacheTree(staging) }()
	catalog, _, err := engine.Renderer.Render(ctx, resolved.path, moduleDir, staging, namespace, descriptor, resolved.manifest, lock.Contracts, inputs)
	if err != nil {
		return nil, nil, err
	}
	return catalog, resolved.manifest, nil
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
