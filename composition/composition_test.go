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
	stale := filepath.Join(materializer.Root, ".tmp-"+strings.TrimPrefix(fixture.verified.Digest, "sha256:")+"-interrupted")
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
	offlineProjection, err := offline.Materialize(ctx, moduleDir, MaterializeOptions{CI: true})
	require.NoError(t, err)
	require.Equal(t, applied.Projection, offlineProjection)
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
	require.Equal(t, applied.Projection, rolledBackProjection)
	rolledBackLock, err := os.ReadFile(filepath.Join(moduleDir, LockFileName))
	require.NoError(t, err)
	require.JSONEq(t, string(priorLock), string(rolledBackLock))
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
	stable, err := engine.Update(ctx, moduleDir, "0.1.0", true)
	require.NoError(t, err)
	lockBefore, err := os.ReadFile(filepath.Join(moduleDir, LockFileName))
	require.NoError(t, err)

	local := newPackageRoot(t, "0.1.1")
	writeFile(t, filepath.Join(local, "services", "local.txt"), "local")
	_, err = SetDevelopOverride(ctx, projectRoot, moduleDir, local)
	require.NoError(t, err)
	devProjection, err := engine.Materialize(ctx, moduleDir, MaterializeOptions{Namespace: "dev"})
	require.NoError(t, err)
	require.NotEqual(t, stable.Projection, devProjection)
	require.FileExists(t, filepath.Join(devProjection, "services", "local.txt"))
	devSource, err := engine.Source(ctx, moduleDir, MaterializeOptions{Namespace: "dev"})
	require.NoError(t, err)
	require.Equal(t, local, devSource)
	ciSource, err := engine.Source(ctx, moduleDir, MaterializeOptions{Namespace: "dev", CI: true})
	require.NoError(t, err)
	require.NotEqual(t, local, ciSource)
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
	manifest := fixture.verified.Manifest
	manifest.Generators = []PackageCommand{{Name: "generate", Command: []string{"generator"}}}
	manifest.Conformance = []PackageCommand{{Name: "conformance", Command: []string{"conformance"}}}
	descriptorDir := writeDescriptor(t, t.TempDir(), "^0.1")
	descriptor := mustDescriptor(t, descriptorDir)
	descriptor.Contributions.Tests = []IntegrationContribution{{Path: "contributions/tests/integration", Command: []string{"integration"}}}
	require.NoError(t, os.MkdirAll(filepath.Join(descriptorDir, "contributions", "tests", "integration"), 0o755))
	runner := &recordingRunner{}
	_, validations, err := (Renderer{Runner: runner}).Render(context.Background(), cache, descriptorDir, filepath.Join(t.TempDir(), "projection"), descriptor, manifest, map[string]string{ContractComposition: "2.0", ContractFrontendPlugin: "1.0"})
	require.NoError(t, err)
	require.Equal(t, []string{"generate", "conformance", "contributions/tests/integration"}, runner.names)
	require.Len(t, validations, 4)
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
	plan, err := PlanV1Migration(dir, newBase, bridge)
	require.NoError(t, err)
	require.Equal(t, V1MigrationDrop, plan[0].Classification)
	require.Equal(t, V1MigrationBlocked, plan[1].Classification)
	data, err := os.ReadFile(filepath.Join(toolsDir, "base-manifest.json"))
	require.NoError(t, err)
	require.Equal(t, manifest, string(data))
}

func TestGitHubResolverFetchesAssetsAndPeeledCommit(t *testing.T) {
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
			_, _ = fmt.Fprint(writer, `{"id":10,"tag_name":"v0.1.0","draft":false,"assets":[{"id":1,"name":"module.tar","size":7},{"id":2,"name":"provenance.json","size":10},{"id":3,"name":"provenance.sig","size":9}]}`)
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
}

func (runner *recordingRunner) Run(_ context.Context, spec CommandSpec) error {
	runner.names = append(runner.names, spec.Name)
	return nil
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
