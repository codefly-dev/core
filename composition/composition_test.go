package composition

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v89/github"
	"github.com/stretchr/testify/require"
)

const testRepository = "https://github.com/codefly-dev/module-saas-starter.git"
const testPackage = "codefly/saas-starter"
const testSigner = "https://github.com/codefly-dev/module-saas-starter/.github/workflows/release.yml@refs/heads/main"

type releaseFixture struct {
	release  *Release
	verified *VerifiedRelease
	trust    TrustPolicy
	root     string
}

func TestArchiveRejectsMaliciousEntries(t *testing.T) {
	tests := map[string][]tarEntry{
		"traversal": {{name: "../escape", body: "bad", kind: tar.TypeReg}},
		"absolute":  {{name: "/escape", body: "bad", kind: tar.TypeReg}},
		"symlink":   {{name: "escape", link: "target", kind: tar.TypeSymlink}},
		"duplicate": {
			{name: "same", body: "one", kind: tar.TypeReg},
			{name: "same", body: "two", kind: tar.TypeReg},
		},
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			err := ExtractArchive(context.Background(), makeTar(t, entries), t.TempDir())
			require.ErrorIs(t, err, ErrUnsafeArchive)
		})
	}
}

func TestCanonicalArchiveAndCompositionDigestsAreDeterministic(t *testing.T) {
	fixture := newReleaseFixture(t, "0.1.0", strings.Repeat("a", 40), nil)
	first, firstDigest, err := CanonicalArchive(fixture.root)
	require.NoError(t, err)
	require.NoError(t, os.Chtimes(filepath.Join(fixture.root, "services", "frontend.txt"), time.Now(), time.Now()))
	second, secondDigest, err := CanonicalArchive(fixture.root)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, firstDigest, secondDigest)

	moduleDir := writeDescriptor(t, t.TempDir(), "^0.1")
	firstComposition, err := CompositionDigest(moduleDir, mustDescriptor(t, moduleDir))
	require.NoError(t, err)
	secondComposition, err := CompositionDigest(moduleDir, mustDescriptor(t, moduleDir))
	require.NoError(t, err)
	require.Equal(t, firstComposition, secondComposition)
	require.NoError(t, os.Chmod(filepath.Join(moduleDir, "contributions", "frontend", "plugin", "index.ts"), 0o755))
	executableComposition, err := CompositionDigest(moduleDir, mustDescriptor(t, moduleDir))
	require.NoError(t, err)
	require.NotEqual(t, firstComposition, executableComposition)
	writeFile(t, filepath.Join(moduleDir, "contributions", "frontend", "plugin", "index.ts"), "changed")
	changed, err := CompositionDigest(moduleDir, mustDescriptor(t, moduleDir))
	require.NoError(t, err)
	require.NotEqual(t, firstComposition, changed)
}

func TestReleaseVerificationRejectsIntegrityAndIdentityFailures(t *testing.T) {
	fixture := newReleaseFixture(t, "0.1.0", strings.Repeat("a", 40), nil)
	_, err := VerifyRelease(fixture.release, testPackage, "0.1.0", fixture.trust)
	require.NoError(t, err)

	t.Run("digest", func(t *testing.T) {
		candidate := *fixture.release
		candidate.Artifact = append(bytes.Clone(candidate.Artifact), 1)
		_, err := VerifyRelease(&candidate, testPackage, "0.1.0", fixture.trust)
		require.ErrorIs(t, err, ErrDigestMismatch)
	})
	t.Run("signature", func(t *testing.T) {
		candidate := *fixture.release
		candidate.Signature = bytes.Repeat([]byte{1}, ed25519.SignatureSize)
		_, err := VerifyRelease(&candidate, testPackage, "0.1.0", fixture.trust)
		require.ErrorIs(t, err, ErrSignature)
	})
	t.Run("repository", func(t *testing.T) {
		candidate := *fixture.release
		candidate.Repository = "https://example.invalid/repository.git"
		_, err := VerifyRelease(&candidate, testPackage, "0.1.0", fixture.trust)
		require.ErrorContains(t, err, "not trusted")
	})
	t.Run("package identity", func(t *testing.T) {
		other := newReleaseFixture(t, "0.1.0", strings.Repeat("a", 40), func(root string) {
			manifestPath := filepath.Join(root, PackageManifestFileName)
			data, readErr := os.ReadFile(manifestPath)
			require.NoError(t, readErr)
			require.NoError(t, os.WriteFile(manifestPath, bytes.Replace(data, []byte("id: "+testPackage), []byte("id: codefly/other"), 1), 0o644))
		})
		_, err := VerifyRelease(other.release, testPackage, "0.1.0", other.trust)
		require.ErrorIs(t, err, ErrPackageIdentity)
	})
}

func TestVerifiedReleaseCannotBeChangedAfterVerification(t *testing.T) {
	fixture := newReleaseFixture(t, "0.1.0", strings.Repeat("a", 40), nil)
	release := *fixture.release
	release.Artifact = bytes.Clone(fixture.release.Artifact)
	release.Provenance = bytes.Clone(fixture.release.Provenance)
	release.Signature = bytes.Clone(fixture.release.Signature)
	verified, err := VerifyRelease(&release, testPackage, "0.1.0", fixture.trust)
	require.NoError(t, err)
	release.Artifact[0] ^= 0xff
	release.Provenance[0] ^= 0xff
	release.Signature[0] ^= 0xff
	projectRoot := t.TempDir()
	t.Cleanup(func() { _ = removeCacheTree(projectRoot) })
	materialized, err := NewMaterializer(projectRoot).Materialize(context.Background(), verified)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(materialized, "services", "frontend.txt"))
}

func TestMovedTagIsRejected(t *testing.T) {
	first := newReleaseFixture(t, "0.1.0", strings.Repeat("a", 40), nil)
	second := newReleaseFixture(t, "0.1.0", strings.Repeat("b", 40), func(root string) {
		writeFile(t, filepath.Join(root, "services", "new.txt"), "new")
	})
	current := lockForRelease(&Descriptor{Name: "saas"}, first.verified, map[string]string{ContractComposition: "2.0"}, "sha256:"+strings.Repeat("0", 64))
	require.ErrorIs(t, rejectMovedTag(current, second.verified), ErrMovedTag)
	_, err := VerifyLockedRelease(second.release, current, second.trust)
	require.ErrorIs(t, err, ErrMovedTag)
}

func TestMaterializationIsConcurrentReadOnlyAndRecovers(t *testing.T) {
	fixture := newReleaseFixture(t, "0.1.0", strings.Repeat("a", 40), nil)
	projectRoot := t.TempDir()
	t.Cleanup(func() { _ = removeCacheTree(projectRoot) })
	materializer := NewMaterializer(projectRoot)
	stale := filepath.Join(materializer.Root, ".tmp-"+strings.TrimPrefix(fixture.verified.digest, "sha256:")+"-interrupted")
	require.NoError(t, os.MkdirAll(stale, 0o755))
	writeFile(t, filepath.Join(stale, "partial"), "partial")

	const callers = 12
	paths := make(chan string, callers)
	errorsChannel := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			path, err := materializer.Materialize(context.Background(), fixture.verified)
			paths <- path
			errorsChannel <- err
		}()
	}
	wait.Wait()
	close(paths)
	close(errorsChannel)
	var expected string
	for err := range errorsChannel {
		require.NoError(t, err)
	}
	for path := range paths {
		if expected == "" {
			expected = path
		}
		require.Equal(t, expected, path)
	}
	_, err := os.Stat(stale)
	require.ErrorIs(t, err, os.ErrNotExist)
	info, err := os.Stat(filepath.Join(expected, "services", "frontend.txt"))
	require.NoError(t, err)
	require.Zero(t, info.Mode().Perm()&0o222)
	require.NoError(t, os.Chmod(filepath.Join(expected, "services", "frontend.txt"), 0o644))
	_, err = materializer.Cached(lockForRelease(&Descriptor{Name: "saas"}, fixture.verified, map[string]string{ContractComposition: "2.0"}, "sha256:"+strings.Repeat("0", 64)))
	require.ErrorIs(t, err, ErrCacheVerification)
	_, err = materializer.Materialize(context.Background(), fixture.verified)
	require.NoError(t, err)

	require.NoError(t, os.Chmod(filepath.Join(expected, "services"), 0o755))
	require.NoError(t, os.Chmod(filepath.Join(expected, "services", "frontend.txt"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(expected, "services", "frontend.txt"), []byte("corrupt"), 0o644))
	_, err = materializer.Cached(lockForRelease(&Descriptor{Name: "saas"}, fixture.verified, map[string]string{ContractComposition: "2.0"}, "sha256:"+strings.Repeat("0", 64)))
	require.ErrorIs(t, err, ErrCacheVerification)
	recovered, err := materializer.Materialize(context.Background(), fixture.verified)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(recovered, "services", "frontend.txt"))
	require.NoError(t, err)
	require.Equal(t, "frontend", string(data))
}

func TestDescriptorStrictnessAndContractNegotiation(t *testing.T) {
	t.Run("unknown field", func(t *testing.T) {
		dir := writeDescriptor(t, t.TempDir(), "^0.1")
		path := filepath.Join(dir, DescriptorFileName)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, append(data, []byte("unknown: true\n")...), 0o644))
		_, err = LoadDescriptor(dir)
		require.ErrorContains(t, err, "field unknown not found")
	})
	t.Run("unsafe contribution", func(t *testing.T) {
		dir := writeDescriptor(t, t.TempDir(), "^0.1")
		path := filepath.Join(dir, DescriptorFileName)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		data = bytes.Replace(data, []byte("contributions/frontend/plugin"), []byte("../plugin"), 1)
		require.NoError(t, os.WriteFile(path, data, 0o644))
		_, err = LoadDescriptor(dir)
		require.ErrorContains(t, err, "unsafe")
	})
	t.Run("unsupported contract", func(t *testing.T) {
		dir := writeDescriptor(t, t.TempDir(), "^0.1")
		descriptor := mustDescriptor(t, dir)
		manifest := mustManifest(t, newReleaseFixture(t, "0.1.0", strings.Repeat("a", 40), nil).root)
		_, err := NegotiateContracts(descriptor, manifest, "0.2.0", map[string][]string{
			ContractComposition: {"2.0"}, ContractFrontendPlugin: {"2.0"},
		})
		require.ErrorIs(t, err, ErrContract)
	})
}

func TestContributionDocumentsAreLoadedBeforeComposition(t *testing.T) {
	t.Run("frontend package", func(t *testing.T) {
		dir := writeDescriptor(t, t.TempDir(), "^0.1")
		writeFile(t, filepath.Join(dir, "contributions", "frontend", "plugin", "package.json"), "{")
		_, err := LoadContributionInputs(dir, mustDescriptor(t, dir))
		require.ErrorContains(t, err, "frontend package.json")
	})
	t.Run("settings message", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, DescriptorFileName), fmt.Sprintf(`kind: composed-module
name: saas
base:
  id: %s
  version: "^0.1"
contributions:
  settings:
    - path: contributions/settings/product.proto
      message: product.settings.v1.Expected
`, testPackage))
		writeFile(t, filepath.Join(dir, "contributions", "settings", "product.proto"), "syntax = \"proto3\"; package product.settings.v1; message Other {}")
		_, err := LoadContributionInputs(dir, mustDescriptor(t, dir))
		require.ErrorContains(t, err, "does not define message")
	})
	t.Run("duplicate YAML key", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, DescriptorFileName), fmt.Sprintf(`kind: composed-module
name: saas
base:
  id: %s
  version: "^0.1"
contributions:
  permissions:
    - path: contributions/permissions/product.yaml
`, testPackage))
		writeFile(t, filepath.Join(dir, "contributions", "permissions", "product.yaml"), "permissions: []\npermissions: []\n")
		_, err := LoadContributionInputs(dir, mustDescriptor(t, dir))
		require.ErrorContains(t, err, "duplicate")
	})
}

func TestPackageAndLockSchemasAreStrict(t *testing.T) {
	root := newPackageRoot(t, "0.1.0")
	manifestPath := filepath.Join(root, PackageManifestFileName)
	manifest := readFile(t, manifestPath)
	writeFile(t, manifestPath, manifest+"unknown: true\n")
	_, err := LoadPackageManifest(root)
	require.ErrorContains(t, err, "field unknown not found")

	data, err := MarshalLock(validLock())
	require.NoError(t, err)
	data = bytes.Replace(data, []byte("\n}"), []byte(",\n  \"unknown\": true\n}"), 1)
	_, err = ParseLock(data)
	require.ErrorContains(t, err, "unknown field")
}

func TestEveryCollisionClassIsRejected(t *testing.T) {
	for _, kind := range collisionKinds {
		t.Run(string(kind), func(t *testing.T) {
			err := ValidateCollisions([]Claim{
				{Kind: kind, Key: "shared", Owner: "one"},
				{Kind: kind, Key: "shared", Owner: "two"},
			}, nil)
			require.ErrorIs(t, err, ErrCollision)
		})
	}
	require.ErrorIs(t, ValidateCollisions([]Claim{{Kind: CollisionRoute, Key: "codefly/admin", Owner: "product"}}, []string{"codefly"}), ErrCollision)
}

func TestRendererRequiresCompleteCatalogEvidence(t *testing.T) {
	fixture := newReleaseFixture(t, "0.1.0", strings.Repeat("a", 40), nil)
	cache := t.TempDir()
	require.NoError(t, ExtractArchive(context.Background(), fixture.release.Artifact, cache))
	moduleDir := writeDescriptor(t, t.TempDir(), "^0.1")
	descriptor := mustDescriptor(t, moduleDir)
	inputs, err := LoadContributionInputs(moduleDir, descriptor)
	require.NoError(t, err)
	namespace, err := ResolveNamespace(t.TempDir(), moduleDir, "test", "", validLock())
	require.NoError(t, err)
	render := func(catalog Catalog, manifest *PackageManifest) error {
		_, _, err := (Renderer{Runner: staticCatalogRunner{catalog: catalog}}).Render(
			context.Background(), cache, moduleDir, filepath.Join(t.TempDir(), "projection"), namespace,
			descriptor, manifest, map[string]string{ContractComposition: "2.0", ContractFrontendPlugin: "1.0"}, inputs,
		)
		return err
	}
	require.ErrorContains(t, render(Catalog{Schema: catalogSchema}, fixture.verified.manifest), "did not validate")
	require.ErrorContains(t, render(Catalog{
		Schema: catalogSchema, Inputs: inputs,
		Claims: []Claim{{Kind: CollisionRoute, Key: "/admin", Owner: "base"}},
	}, fixture.verified.manifest), "undeclared owner")
	manifest := *fixture.verified.manifest
	manifest.Claims = []Claim{{Kind: CollisionRoute, Key: "/admin", Owner: "base"}}
	require.ErrorIs(t, render(Catalog{
		Schema: catalogSchema, Inputs: inputs,
		Claims: []Claim{{Kind: CollisionRoute, Key: "/admin", Owner: inputs[0].Path}},
	}, &manifest), ErrCollision)
}

func TestUpdateDryRunApplyOfflineAndRollback(t *testing.T) {
	ctx := context.Background()
	projectRoot := t.TempDir()
	t.Cleanup(func() { _ = removeCacheTree(projectRoot) })
	moduleDir := writeDescriptor(t, filepath.Join(projectRoot, "modules", "saas"), ">=0.1.0 <1.0.0")
	first := newReleaseFixture(t, "0.1.0", strings.Repeat("a", 40), nil)
	second := newReleaseFixture(t, "0.2.0", strings.Repeat("b", 40), func(root string) {
		writeFile(t, filepath.Join(root, "services", "second.txt"), "second")
	})
	resolver := &fixtureResolver{releases: map[string]*Release{"0.1.0": first.release, "0.2.0": second.release}}
	engine := NewEngine(projectRoot, resolver, first.trust)
	engine.ToolVersion = "0.2.0"
	engine.Renderer.Runner = &recordingRunner{}

	dryRun, err := engine.Update(ctx, moduleDir, "0.1.0", false)
	require.NoError(t, err)
	require.False(t, dryRun.Applied)
	require.NotEmpty(t, dryRun.Report.LockDiff)
	_, err = os.Stat(filepath.Join(moduleDir, LockFileName))
	require.ErrorIs(t, err, os.ErrNotExist)
	reportJSON, err := dryRun.Report.JSON()
	require.NoError(t, err)
	require.Contains(t, string(reportJSON), `"schema": "codefly/module-update-report/v2"`)
	require.Contains(t, dryRun.Report.String(), "Result: ready")

	applied, err := engine.Update(ctx, moduleDir, "0.1.0", true)
	require.NoError(t, err)
	require.True(t, applied.Applied)
	require.True(t, projectionMatches(applied.Projection, applied.Lock))
	priorLock, err := os.ReadFile(filepath.Join(moduleDir, LockFileName))
	require.NoError(t, err)
	_, err = ParseLock(priorLock)
	require.NoError(t, err)

	offline := NewEngine(projectRoot, nil, first.trust)
	offline.ToolVersion = "0.2.0"
	offline.Renderer.Runner = &recordingRunner{}
	offlineProjection, err := offline.Materialize(ctx, moduleDir, MaterializeOptions{CI: true})
	require.NoError(t, err)
	require.Equal(t, applied.Projection, offlineProjection.Projection)
	checks, err := offline.Doctor(ctx, moduleDir, true)
	require.NoError(t, err)
	require.NotEmpty(t, checks)
	for _, check := range checks {
		require.Equal(t, ValidationPassed, check.Status)
	}

	readerErrors := make(chan error, 1)
	stopReaders := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopReaders:
				readerErrors <- nil
				return
			default:
				data, readErr := os.ReadFile(filepath.Join(moduleDir, LockFileName))
				if readErr == nil {
					_, readErr = ParseLock(data)
				}
				if readErr != nil {
					readerErrors <- readErr
					return
				}
			}
		}
	}()
	updated, err := engine.Update(ctx, moduleDir, "0.2.0", true)
	close(stopReaders)
	require.NoError(t, <-readerErrors)
	require.NoError(t, err)
	require.True(t, updated.Applied)
	rolledBackProjection, err := engine.Rollback(ctx, moduleDir, priorLock)
	require.NoError(t, err)
	require.Equal(t, applied.Projection, rolledBackProjection.Projection)
	rolledBackLock, err := os.ReadFile(filepath.Join(moduleDir, LockFileName))
	require.NoError(t, err)
	require.JSONEq(t, string(priorLock), string(rolledBackLock))
}

func TestSemanticReportRendersPriorLockFromCleanCheckout(t *testing.T) {
	ctx := context.Background()
	projectRoot := t.TempDir()
	t.Cleanup(func() { _ = removeCacheTree(projectRoot) })
	moduleDir := writeDescriptor(t, filepath.Join(projectRoot, "modules", "saas"), ">=0.1.0 <1.0.0")
	first := newReleaseFixture(t, "0.1.0", strings.Repeat("a", 40), nil)
	second := newReleaseFixture(t, "0.2.0", strings.Repeat("b", 40), func(root string) {
		path := filepath.Join(root, PackageManifestFileName)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		data = bytes.Replace(data, []byte("services:\n  - name: frontend"), []byte("services:\n  - name: worker\n  - name: frontend"), 1)
		require.NoError(t, os.WriteFile(path, data, 0o644))
	})
	resolver := &fixtureResolver{releases: map[string]*Release{"0.1.0": first.release, "0.2.0": second.release}}
	engine := NewEngine(projectRoot, resolver, first.trust)
	engine.ToolVersion = "0.2.0"
	engine.Renderer.Runner = &recordingRunner{}
	_, err := engine.Update(ctx, moduleDir, "0.1.0", true)
	require.NoError(t, err)
	require.NoError(t, removeCacheTree(filepath.Join(projectRoot, ".codefly", "composed")))
	result, err := engine.Update(ctx, moduleDir, "0.2.0", false)
	require.NoError(t, err)
	require.Equal(t, []string{"worker"}, result.Report.Services.Added)
	require.Empty(t, result.Report.Services.Removed)
}

func TestCorruptProjectionIsRebuiltWithAtomicActivation(t *testing.T) {
	ctx := context.Background()
	projectRoot := t.TempDir()
	t.Cleanup(func() { _ = removeCacheTree(projectRoot) })
	moduleDir := writeDescriptor(t, filepath.Join(projectRoot, "modules", "saas"), "^0.1")
	fixture := newReleaseFixture(t, "0.1.0", strings.Repeat("a", 40), nil)
	resolver := &fixtureResolver{releases: map[string]*Release{"0.1.0": fixture.release}}
	engine := NewEngine(projectRoot, resolver, fixture.trust)
	engine.ToolVersion = "0.2.0"
	engine.Renderer.Runner = &recordingRunner{}
	applied, err := engine.Update(ctx, moduleDir, "0.1.0", true)
	require.NoError(t, err)
	info, err := os.Lstat(applied.Projection)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
	frontend := filepath.Join(applied.Projection, "services", "frontend.txt")
	require.NoError(t, os.Chmod(frontend, 0o644))
	require.False(t, projectionMatches(applied.Projection, applied.Lock))
	require.NoError(t, os.WriteFile(frontend, []byte("corrupt"), 0o644))
	require.False(t, projectionMatches(applied.Projection, applied.Lock))

	started := make(chan struct{})
	stop := make(chan struct{})
	readerResult := make(chan error, 1)
	go func() {
		close(started)
		for {
			select {
			case <-stop:
				readerResult <- nil
				return
			default:
				if _, readErr := os.ReadFile(frontend); readErr != nil {
					readerResult <- readErr
					return
				}
			}
		}
	}()
	<-started
	rebuilt, err := engine.Materialize(ctx, moduleDir, MaterializeOptions{CI: true})
	close(stop)
	require.NoError(t, <-readerResult)
	require.NoError(t, err)
	require.Equal(t, applied.Projection, rebuilt.Projection)
	require.Equal(t, "frontend", readFile(t, filepath.Join(rebuilt.Projection, "services", "frontend.txt")))
	require.True(t, projectionMatches(rebuilt.Projection, applied.Lock))
	_, err = engine.Doctor(ctx, moduleDir, true)
	require.NoError(t, err)
}

func TestDevelopOverrideAndNamespacesStayIndependent(t *testing.T) {
	ctx := context.Background()
	projectRoot := t.TempDir()
	t.Cleanup(func() { _ = removeCacheTree(projectRoot) })
	moduleDir := writeDescriptor(t, filepath.Join(projectRoot, "modules", "saas"), "^0.1")
	fixture := newReleaseFixture(t, "0.1.0", strings.Repeat("a", 40), nil)
	resolver := &fixtureResolver{releases: map[string]*Release{"0.1.0": fixture.release}}
	engine := NewEngine(projectRoot, resolver, fixture.trust)
	engine.ToolVersion = "0.2.0"
	engine.Renderer.Runner = &recordingRunner{}
	stable, err := engine.Update(ctx, moduleDir, "0.1.0", true)
	require.NoError(t, err)
	lockBefore, err := os.ReadFile(filepath.Join(moduleDir, LockFileName))
	require.NoError(t, err)

	local := newPackageRoot(t, "0.2.0")
	writeFile(t, filepath.Join(local, "services", "local.txt"), "local")
	_, err = SetDevelopOverride(ctx, projectRoot, moduleDir, local)
	require.NoError(t, err)
	devProjection, err := engine.Materialize(ctx, moduleDir, MaterializeOptions{Namespace: "dev"})
	require.NoError(t, err)
	require.NotEqual(t, stable.Projection, devProjection.Projection)
	require.FileExists(t, filepath.Join(devProjection.Projection, "services", "local.txt"))
	devSource, err := engine.Source(ctx, moduleDir, MaterializeOptions{Namespace: "dev"})
	require.NoError(t, err)
	require.Equal(t, local, devSource)
	ciSource, err := engine.Source(ctx, moduleDir, MaterializeOptions{Namespace: "dev", CI: true})
	require.NoError(t, err)
	require.NotEqual(t, local, ciSource)
	manifestPath := filepath.Join(local, PackageManifestFileName)
	manifestData, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, bytes.Replace(manifestData, []byte("id: "+testPackage), []byte("id: codefly/other"), 1), 0o644))
	_, err = engine.Source(ctx, moduleDir, MaterializeOptions{Namespace: "dev"})
	require.ErrorIs(t, err, ErrPackageIdentity)
	lockAfter, err := os.ReadFile(filepath.Join(moduleDir, LockFileName))
	require.NoError(t, err)
	require.Equal(t, lockBefore, lockAfter)
	require.FileExists(t, filepath.Join(projectRoot, ".codefly", ".gitignore"))
	require.NoError(t, ClearDevelopOverride(projectRoot, "saas"))
}

func TestNamespaceSeparatesAllRuntimeState(t *testing.T) {
	lock := validLock()
	projectRoot := t.TempDir()
	stable, err := ResolveNamespace(projectRoot, filepath.Join(projectRoot, "modules", "saas"), "stable", "stable.lock", lock)
	require.NoError(t, err)
	dev, err := ResolveNamespace(projectRoot, filepath.Join(projectRoot, "modules", "saas"), "dev", "dev.lock", lock)
	require.NoError(t, err)
	require.NotEqual(t, stable.Digest, dev.Digest)
	require.NotEqual(t, stable.ProjectionDir, dev.ProjectionDir)
	require.NotEqual(t, stable.CacheDir, dev.CacheDir)
	require.NotEqual(t, stable.BuildDir, dev.BuildDir)
	require.NotEqual(t, stable.NextJSDir, dev.NextJSDir)
	require.NotEqual(t, stable.RuntimeConfigDir, dev.RuntimeConfigDir)
	require.NotEqual(t, stable.ContainerSuffix, dev.ContainerSuffix)
	require.NotEqual(t, stable.PortSeed, dev.PortSeed)
}

func TestRendererRunsPackageAndConsumerSuites(t *testing.T) {
	fixture := newReleaseFixture(t, "0.1.0", strings.Repeat("a", 40), nil)
	cache := t.TempDir()
	require.NoError(t, ExtractArchive(context.Background(), fixture.release.Artifact, cache))
	manifest := fixture.verified.manifest
	manifest.Generators = []PackageCommand{{Name: "generate", Command: []string{"generator"}}}
	manifest.Conformance = []PackageCommand{{Name: "conformance", Command: []string{"conformance"}}}
	descriptorDir := writeDescriptor(t, t.TempDir(), "^0.1")
	descriptor := mustDescriptor(t, descriptorDir)
	descriptor.Contributions.Tests = []IntegrationContribution{{Path: "contributions/tests/integration", Command: []string{"integration"}}}
	require.NoError(t, os.MkdirAll(filepath.Join(descriptorDir, "contributions", "tests", "integration"), 0o755))
	runner := &recordingRunner{}
	inputs, err := LoadContributionInputs(descriptorDir, descriptor)
	require.NoError(t, err)
	namespace, err := ResolveNamespace(t.TempDir(), descriptorDir, "test", "", validLock())
	require.NoError(t, err)
	_, validations, err := (Renderer{Runner: runner}).Render(context.Background(), cache, descriptorDir, filepath.Join(t.TempDir(), "projection"), namespace, descriptor, manifest, map[string]string{ContractComposition: "2.0", ContractFrontendPlugin: "1.0"}, inputs)
	require.NoError(t, err)
	require.Equal(t, []string{"generate", "conformance", "contributions/tests/integration"}, runner.names)
	require.Len(t, validations, 4)
	require.Equal(t, namespace.CacheDir, environmentValue(runner.specs[0].Env, "CODEFLY_COMPOSITION_CACHE"))
	require.Equal(t, namespace.BuildDir, environmentValue(runner.specs[0].Env, "CODEFLY_COMPOSITION_BUILD"))
	require.Equal(t, namespace.NextJSDir, environmentValue(runner.specs[0].Env, "CODEFLY_COMPOSITION_NEXTJS"))
	require.Equal(t, namespace.RuntimeConfigDir, environmentValue(runner.specs[0].Env, "CODEFLY_COMPOSITION_RUNTIME_CONFIG"))
	require.Equal(t, namespace.ContainerSuffix, environmentValue(runner.specs[0].Env, "CODEFLY_COMPOSITION_CONTAINER_SUFFIX"))
	require.NotEmpty(t, environmentValue(runner.specs[0].Env, "CODEFLY_COMPOSITION_PORT_SEED"))
	require.DirExists(t, namespace.NextJSDir)
	require.DirExists(t, namespace.RuntimeConfigDir)
}

func TestSemanticReportUsesExplicitDependenciesAndMigrationMetadata(t *testing.T) {
	before := validLock()
	afterValue := *before
	afterValue.Version = "0.2.0"
	after := &afterValue
	beforeManifest := &PackageManifest{
		Services:   []ProvidedService{{Name: "frontend"}},
		Migrations: []PackageMigration{{ID: "001", From: "^0.1"}},
	}
	afterManifest := &PackageManifest{
		Services:        []ProvidedService{{Name: "frontend"}, {Name: "worker"}},
		Migrations:      []PackageMigration{{ID: "001", From: "^0.1"}, {ID: "002", From: "^0.1", Breaking: true}},
		BreakingChanges: []string{"remove legacy settings field"},
	}
	report := newSemanticReport(
		&Descriptor{Name: "saas"}, before, after, beforeManifest, afterManifest,
		&Catalog{Dependencies: []string{"dep-a"}, Claims: []Claim{{Kind: CollisionPackage, Key: "frontendExport", Owner: "frontend"}}},
		&Catalog{Dependencies: []string{"dep-b"}, Claims: []Claim{{Kind: CollisionPackage, Key: "frontendExport", Owner: "frontend"}}},
		nil,
	)
	require.Equal(t, []string{"dep-b"}, report.Dependencies.Added)
	require.Equal(t, []string{"dep-a"}, report.Dependencies.Removed)
	require.NotContains(t, report.Dependencies.Added, "frontendExport")
	require.Equal(t, []string{"002"}, report.Migrations.Added)
	require.Contains(t, report.BreakingChanges, "remove legacy settings field")
	require.NotEmpty(t, report.BlockedReasons)
	require.Contains(t, report.String(), "migrations")
}

func TestV1BridgeLoadsWithoutMutatingFixtures(t *testing.T) {
	dir := t.TempDir()
	newBase := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	require.NoError(t, os.MkdirAll(toolsDir, 0o755))
	source := `{"schema":"codefly/base-source/v1","repository":"` + testRepository + `","ref":"v0.0.37","commit":"` + strings.Repeat("a", 40) + `","subdirectory":"module"}`
	oldContent := "old frontend"
	oldHash := sha256.Sum256([]byte(oldContent))
	manifest := fmt.Sprintf(`{"fileCount":2,"files":{"services/frontend.txt":"%x","services/product.txt":"%x"}}`, oldHash, sha256.Sum256([]byte("released product")))
	writeFile(t, filepath.Join(toolsDir, "base-source.json"), source)
	writeFile(t, filepath.Join(toolsDir, "base-manifest.json"), manifest)
	writeFile(t, filepath.Join(dir, "services", "frontend.txt"), oldContent)
	writeFile(t, filepath.Join(dir, "services", "product.txt"), "product customization")
	writeFile(t, filepath.Join(newBase, "services", "frontend.txt"), "new frontend")
	writeFile(t, filepath.Join(newBase, "services", "product.txt"), "new product")
	bridge, err := LoadV1Bridge(dir)
	require.NoError(t, err)
	require.Equal(t, "v0.0.37", bridge.Source.Ref)
	require.Equal(t, 2, bridge.Manifest.FileCount)
	plan, err := PlanV1Migration(dir, newBase, bridge, nil)
	require.NoError(t, err)
	require.Equal(t, V1MigrationDrop, plan[0].Classification)
	require.Equal(t, V1MigrationBlocked, plan[1].Classification)
	data, err := os.ReadFile(filepath.Join(toolsDir, "base-manifest.json"))
	require.NoError(t, err)
	require.Equal(t, manifest, string(data))
}

func TestV1MigrationClassifiesEveryDivergence(t *testing.T) {
	moduleDir := t.TempDir()
	newBase := t.TempDir()
	old := map[string]string{
		"drop.txt":      "old-drop",
		"upstream.txt":  "old-upstream",
		"generated.txt": "old-generated",
		"blocked.txt":   "old-blocked",
	}
	manifest := &V1BaseManifest{FileCount: len(old), Files: make(map[string]string, len(old))}
	for path, content := range old {
		digest := sha256.Sum256([]byte(content))
		manifest.Files[path] = fmt.Sprintf("%x", digest)
		writeFile(t, filepath.Join(moduleDir, path), content)
		writeFile(t, filepath.Join(newBase, path), "new-"+path)
	}
	writeFile(t, filepath.Join(moduleDir, "upstream.txt"), "generic improvement")
	writeFile(t, filepath.Join(moduleDir, "generated.txt"), "generated output")
	writeFile(t, filepath.Join(moduleDir, "blocked.txt"), "unclassified product behavior")
	writeFile(t, filepath.Join(moduleDir, "contribution.txt"), "product contribution")
	plan, err := PlanV1Migration(moduleDir, newBase, &V1Bridge{Manifest: manifest}, map[string]V1MigrationClassification{
		"upstream.txt":     V1MigrationUpstream,
		"generated.txt":    V1MigrationGenerated,
		"contribution.txt": V1MigrationContribution,
	})
	require.NoError(t, err)
	classifications := make(map[string]V1MigrationClassification, len(plan))
	for _, entry := range plan {
		classifications[entry.Path] = entry.Classification
	}
	require.Equal(t, V1MigrationDrop, classifications["drop.txt"])
	require.Equal(t, V1MigrationUpstream, classifications["upstream.txt"])
	require.Equal(t, V1MigrationGenerated, classifications["generated.txt"])
	require.Equal(t, V1MigrationContribution, classifications["contribution.txt"])
	require.Equal(t, V1MigrationBlocked, classifications["blocked.txt"])
}

func TestGitHubResolverFetchesAssetsAndPeeledCommit(t *testing.T) {
	immutable := true
	assets := map[string]string{
		"/repos/codefly-dev/starter/releases/assets/1": "archive",
		"/repos/codefly-dev/starter/releases/assets/2": "provenance",
		"/repos/codefly-dev/starter/releases/assets/3": "signature",
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestPath := strings.TrimPrefix(request.URL.Path, "/api/v3")
		switch requestPath {
		case "/repos/codefly-dev/starter/releases/tags/v0.1.0":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(writer, `{"id":10,"tag_name":"v0.1.0","draft":false,"immutable":%t,"assets":[{"id":1,"name":"module.tar","size":7},{"id":2,"name":"provenance.json","size":10},{"id":3,"name":"provenance.sig","size":9}]}`, immutable)
		case "/repos/codefly-dev/starter/git/ref/tags/v0.1.0":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(writer, `{"ref":"refs/tags/v0.1.0","object":{"type":"commit","sha":"%s"}}`, strings.Repeat("a", 40))
		default:
			if value, exists := assets[requestPath]; exists {
				writer.Header().Set("Content-Type", "application/octet-stream")
				_, _ = fmt.Fprint(writer, value)
				return
			}
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := github.NewClient(
		github.WithHTTPClient(server.Client()),
		github.WithEnterpriseURLs(server.URL+"/", server.URL+"/"),
	)
	require.NoError(t, err)
	resolver := NewGitHubResolver(client, server.Client(), map[string]GitHubPackage{
		testPackage: {
			Owner: "codefly-dev", RepositoryName: "starter", RepositoryURL: testRepository,
			ArtifactAsset: "module.tar", ProvenanceAsset: "provenance.json", SignatureAsset: "provenance.sig",
		},
	})
	release, err := resolver.Resolve(context.Background(), ResolveRequest{Package: testPackage, Version: "0.1.0"})
	require.NoError(t, err)
	require.Equal(t, "archive", string(release.Artifact))
	require.Equal(t, "provenance", string(release.Provenance))
	require.Equal(t, "signature", string(release.Signature))
	require.Equal(t, strings.Repeat("a", 40), release.Commit)
	immutable = false
	_, err = resolver.Resolve(context.Background(), ResolveRequest{Package: testPackage, Version: "0.1.0"})
	require.ErrorContains(t, err, "immutable")
}

type tarEntry struct {
	name string
	body string
	link string
	kind byte
}

func makeTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Linkname: entry.link, Typeflag: entry.kind, Mode: 0o644, Size: int64(len(entry.body))}
		if entry.kind != tar.TypeReg {
			header.Size = 0
		}
		require.NoError(t, writer.WriteHeader(header))
		if header.Size > 0 {
			_, err := writer.Write([]byte(entry.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, writer.Close())
	return output.Bytes()
}

func newReleaseFixture(t *testing.T, version, commit string, mutate func(string)) *releaseFixture {
	t.Helper()
	root := newPackageRoot(t, version)
	if mutate != nil {
		mutate(root)
	}
	archive, digest, err := CanonicalArchive(root)
	require.NoError(t, err)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x2a}, ed25519.SeedSize))
	publicKey := privateKey.Public().(ed25519.PublicKey)
	provenance := &Provenance{
		Schema: ProvenanceSchema, Package: testPackage, Version: version, Repository: testRepository,
		Ref: "v" + version, Commit: commit, ArtifactMediaType: ArtifactMediaType, ArtifactDigest: digest,
		SignatureIdentity: testSigner,
	}
	provenanceData, err := json.Marshal(provenance)
	require.NoError(t, err)
	release := &Release{
		Repository: testRepository, Ref: "v" + version, Commit: commit, Artifact: archive,
		Provenance: provenanceData, Signature: ed25519.Sign(privateKey, provenanceData),
	}
	trust := TrustPolicy{Repositories: map[string]string{testPackage: testRepository}, Signers: map[string]ed25519.PublicKey{testSigner: publicKey}}
	verified, err := VerifyRelease(release, testPackage, version, trust)
	if mutate == nil || !strings.Contains(readFile(t, filepath.Join(root, PackageManifestFileName)), "codefly/other") {
		require.NoError(t, err)
	}
	return &releaseFixture{release: release, verified: verified, trust: trust, root: root}
}

func newPackageRoot(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	manifest := fmt.Sprintf(`kind: module-package
schema: codefly/module-package/v2
id: %s
version: %s
minimum-codefly-version: ">=0.1.0"
artifact-roots:
  - services
services:
  - name: frontend
    endpoints:
      - http
contracts:
  composition: ">=2.0 <3.0"
  frontendPlugin: ">=1.0 <2.0"
  settings: ">=1.0 <2.0"
  permissions: ">=1.0 <2.0"
  fixtures: ">=1.0 <2.0"
generators:
  - name: compose
    command: ["compose"]
`, testPackage, version)
	writeFile(t, filepath.Join(root, PackageManifestFileName), manifest)
	writeFile(t, filepath.Join(root, "services", "frontend.txt"), "frontend")
	return root
}

func writeDescriptor(t *testing.T, moduleDir, versionRange string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(moduleDir, 0o755))
	descriptor := fmt.Sprintf(`kind: composed-module
name: saas
base:
  id: %s
  version: %q
services:
  include:
    - frontend
contributions:
  frontend:
    - path: contributions/frontend/plugin
      export: productFrontendPlugin
bindings:
  - plugin: product
    alias: api
    target:
      module: product
      service: api
`, testPackage, versionRange)
	writeFile(t, filepath.Join(moduleDir, DescriptorFileName), descriptor)
	writeFile(t, filepath.Join(moduleDir, "contributions", "frontend", "plugin", "package.json"), "{\"name\":\"@product/frontend\"}")
	writeFile(t, filepath.Join(moduleDir, "contributions", "frontend", "plugin", "index.ts"), "export const productFrontendPlugin = {}")
	return moduleDir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func mustDescriptor(t *testing.T, moduleDir string) *Descriptor {
	t.Helper()
	descriptor, err := LoadDescriptor(moduleDir)
	require.NoError(t, err)
	return descriptor
}

func mustManifest(t *testing.T, root string) *PackageManifest {
	t.Helper()
	manifest, err := LoadPackageManifest(root)
	require.NoError(t, err)
	return manifest
}

func validLock() *Lock {
	return &Lock{
		Schema: LockSchema, Module: "saas", Package: testPackage, Version: "0.1.0",
		Source:    SourceLock{Repository: testRepository, Ref: "v0.1.0", Commit: strings.Repeat("a", 40)},
		Artifact:  ArtifactLock{MediaType: ArtifactMediaType, Digest: "sha256:" + strings.Repeat("b", 64), Signature: testSigner},
		Contracts: map[string]string{ContractComposition: "2.0"}, CompositionDigest: "sha256:" + strings.Repeat("c", 64),
	}
}

type fixtureResolver struct {
	releases map[string]*Release
}

func (resolver *fixtureResolver) Resolve(_ context.Context, request ResolveRequest) (*Release, error) {
	release, exists := resolver.releases[request.Version]
	if !exists {
		return nil, errors.New("release not found")
	}
	return release, nil
}

func (resolver *fixtureResolver) Fetch(_ context.Context, lock *Lock) (*Release, error) {
	release, exists := resolver.releases[lock.Version]
	if !exists {
		return nil, errors.New("release not found")
	}
	return release, nil
}

type recordingRunner struct {
	names []string
	specs []CommandSpec
}

type staticCatalogRunner struct {
	catalog Catalog
}

func (runner staticCatalogRunner) Run(_ context.Context, spec CommandSpec) error {
	if spec.Name != "compose" && spec.Name != "generate" {
		return nil
	}
	data, err := json.Marshal(runner.catalog)
	if err != nil {
		return err
	}
	path := filepath.Join(environmentValue(spec.Env, "CODEFLY_COMPOSITION_PROJECTION"), filepath.FromSlash(CompositionCatalogName))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (runner *recordingRunner) Run(_ context.Context, spec CommandSpec) error {
	runner.names = append(runner.names, spec.Name)
	runner.specs = append(runner.specs, spec)
	if spec.Name != "compose" && spec.Name != "generate" {
		return nil
	}
	consumer := environmentValue(spec.Env, "CODEFLY_COMPOSITION_CONSUMER")
	projection := environmentValue(spec.Env, "CODEFLY_COMPOSITION_PROJECTION")
	descriptor, err := LoadDescriptor(consumer)
	if err != nil {
		return err
	}
	inputs, err := LoadContributionInputs(consumer, descriptor)
	if err != nil {
		return err
	}
	data, err := json.Marshal(Catalog{Schema: catalogSchema, Inputs: inputs})
	if err != nil {
		return err
	}
	path := filepath.Join(projection, filepath.FromSlash(CompositionCatalogName))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func environmentValue(environment []string, name string) string {
	prefix := name + "="
	for _, value := range environment {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return ""
}

func TestSignatureEncodingHelper(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	digest := sha256.Sum256([]byte("payload"))
	signature := ed25519.Sign(privateKey, digest[:])
	encoded := []byte(fmt.Sprintf("%s", jsonStringBase64(signature)))
	_, err = DecodeSignature(encoded)
	require.NoError(t, err)
}

func jsonStringBase64(value []byte) string {
	encoded, _ := json.Marshal(value)
	return strings.Trim(string(encoded), `"`)
}
