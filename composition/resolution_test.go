package composition

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v89/github"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type selectionRegistry struct {
	root      string
	engine    *Engine
	key       ed25519.PrivateKey
	nextAsset int
	mu        sync.Mutex
	requests  []string
	metadata  map[ReleaseSelection]*ReleaseMetadata
}

func newSelectionRegistry(t *testing.T) *selectionRegistry {
	t.Helper()
	registry := &selectionRegistry{root: t.TempDir(), key: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{42}, 32)), metadata: make(map[ReleaseSelection]*ReleaseMetadata)}
	files := http.FileServer(http.Dir(registry.root))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registry.mu.Lock()
		registry.requests = append(registry.requests, r.URL.Path)
		registry.mu.Unlock()
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/v3")
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := github.NewClient(github.WithHTTPClient(server.Client()), github.WithEnterpriseURLs(server.URL+"/", server.URL+"/"))
	require.NoError(t, err)
	registry.engine = NewEngine(t.TempDir(), NewGitHubResolver(client, server.Client(), make(map[string]GitHubPackage)), TrustPolicy{Repositories: make(map[string]string), Signers: make(map[string]map[string]ed25519.PublicKey), BuildSigners: make(map[string]map[string]ed25519.PublicKey)})
	return registry
}

func selectionManifest(id, version string) *PackageManifest {
	return &PackageManifest{Kind: PackageKind, Schema: PackageSchema, ID: id, Version: version, MinimumCodeflyVersion: ">=0.3.40", ArtifactRoots: []string{"contracts"}, Contracts: map[string]string{ContractComposition: "1.0"}, Provides: map[string]string{"interface": "1.0.0", "configuration": "1.0.0"}, RequiredQualifications: &[]string{"functional"}}
}

func TestCompositionRequiresActualHostVersionInsteadOfLinkedCore(t *testing.T) {
	registry := newSelectionRegistry(t)
	t.Cleanup(func() { require.NoError(t, removeCacheTree(registry.engine.ProjectRoot)) })
	manifest := selectionManifest("example/independent", "1.0.0")
	manifest.Contracts[ContractComposition] = "2.0"
	manifest.MinimumCodeflyVersion = ">=0.3.0"
	selection := registry.publish(t, manifest)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, DescriptorFileName), "kind: composed-module\nname: consumer\nbase:\n  id: example/independent\n  version: '^1.0.0'\n")
	_, err := registry.engine.Update(t.Context(), dir, selection.Version, true)
	require.ErrorContains(t, err, "host tool version is required")
	require.NoFileExists(t, filepath.Join(dir, LockFileName))
}

func TestConcurrentUpdatesRejectStaleQualifiedBaseline(t *testing.T) {
	r := newSelectionRegistry(t)
	t.Cleanup(func() { require.NoError(t, removeCacheTree(r.engine.ProjectRoot)) })
	r.engine.ToolVersion = "1.0.0"
	barrier := t.TempDir()
	entered, release := filepath.Join(barrier, "entered"), filepath.Join(barrier, "release")
	manifest := selectionManifest("example/concurrent", "1.0.0")
	manifest.Contracts[ContractComposition] = "2.0"
	manifest.Generators = []PackageCommand{{Name: "wait", Command: []string{"sh", "-c", fmt.Sprintf("touch %q; while [ ! -f %q ]; do sleep 0.01; done", entered, release)}}}
	first := r.publish(t, manifest)
	manifest.Version = "1.1.0"
	manifest.Generators = nil
	second := r.publish(t, manifest)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, DescriptorFileName), "kind: composed-module\nname: consumer\nbase:\n  id: example/concurrent\n  version: '^1.0.0'\n")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.engine.Update(ctx, dir, first.Version, true); done <- err }()
	t.Cleanup(func() { writeFile(t, release, "") })
	require.Eventually(t, func() bool { _, err := os.Stat(entered); return err == nil }, 5*time.Second, 10*time.Millisecond)
	result, err := r.engine.Update(ctx, dir, second.Version, true)
	require.NoError(t, err)
	require.True(t, result.Applied)
	completed := readFile(t, filepath.Join(dir, LockFileName))
	writeFile(t, release, "")
	require.ErrorContains(t, <-done, "module lock changed during qualification")
	require.Equal(t, completed, readFile(t, filepath.Join(dir, LockFileName)))
}

func TestCompositionToolRequirementUsesHostVersionIndependentlyOfCore(t *testing.T) {
	registry := newSelectionRegistry(t)
	t.Cleanup(func() { require.NoError(t, removeCacheTree(registry.engine.ProjectRoot)) })
	manifest := selectionManifest("example/independent", "1.0.0")
	manifest.Contracts[ContractComposition] = "2.0"
	manifest.MinimumCodeflyVersion = ">=900.0.0"
	selection := registry.publish(t, manifest)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, DescriptorFileName), "kind: composed-module\nname: consumer\nbase:\n  id: example/independent\n  version: '^1.0.0'\n")
	registry.engine.ToolVersion = "899.0.0"
	_, err := registry.engine.Update(t.Context(), dir, selection.Version, true)
	require.ErrorIs(t, err, ErrContract)
	require.NoFileExists(t, filepath.Join(dir, LockFileName))
	registry.engine.ToolVersion = "900.0.0"
	result, err := registry.engine.Update(t.Context(), dir, selection.Version, true)
	require.NoError(t, err)
	require.True(t, result.Applied)
	before := readFile(t, filepath.Join(dir, LockFileName))
	registry.engine.ToolVersion = "901.0.0"
	materialized, err := registry.engine.Materialize(t.Context(), dir, MaterializeOptions{CI: true})
	require.NoError(t, err)
	require.Equal(t, result.Projection, materialized.Projection)
	require.Equal(t, before, readFile(t, filepath.Join(dir, LockFileName)))
}

func fixtureArtifact(name string, purpose ArtifactPurpose, content string) ReleaseArtifact {
	return ReleaseArtifact{Name: name, Purpose: purpose, URI: "https://artifacts.example.test/" + name, Digest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content)))}
}

func (registry *selectionRegistry) publish(t *testing.T, manifest *PackageManifest) ReleaseSelection {
	t.Helper()
	data, err := yaml.Marshal(manifest)
	require.NoError(t, err)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, PackageManifestFileName), string(data))
	writeFile(t, filepath.Join(root, "contracts", "README"), "released contracts")
	archive, digest, err := CanonicalArchive(root)
	require.NoError(t, err)
	repository := "https://github.com/" + manifest.ID
	commit := strings.Repeat("a", 40)
	provenance := &Provenance{Schema: ProvenanceSchema, Package: manifest.ID, Version: manifest.Version, Repository: repository, Ref: "v" + manifest.Version, Commit: commit, ArtifactMediaType: ArtifactMediaType, ArtifactDigest: digest, ManifestDigest: fmt.Sprintf("sha256:%x", sha256.Sum256(data)), SignatureIdentity: "owner-release"}
	provenanceData, err := json.Marshal(provenance)
	require.NoError(t, err)
	signature := ed25519.Sign(registry.key, provenanceData)
	selection := ReleaseSelection{ID: manifest.ID, Version: manifest.Version, Digest: digest}
	registry.metadata[selection] = &ReleaseMetadata{Repository: repository, Ref: provenance.Ref, Commit: commit, Manifest: data, Provenance: provenanceData, Signature: signature}
	parts := strings.Split(manifest.ID, "/")
	require.Len(t, parts, 2)
	assets := make([]map[string]any, 0, 4)
	for _, asset := range []struct {
		name string
		data []byte
	}{
		{PackageManifestFileName, data}, {"provenance.json", provenanceData}, {"provenance.sig", signature}, {"module.tar", archive},
	} {
		registry.nextAsset++
		assets = append(assets, map[string]any{"id": registry.nextAsset, "name": asset.name, "size": len(asset.data)})
		writeFile(t, filepath.Join(registry.root, "repos", manifest.ID, "releases", "assets", fmt.Sprint(registry.nextAsset)), string(asset.data))
	}
	releaseJSON, err := json.Marshal(map[string]any{"id": registry.nextAsset, "tag_name": provenance.Ref, "draft": false, "immutable": true, "assets": assets})
	require.NoError(t, err)
	writeFile(t, filepath.Join(registry.root, "repos", manifest.ID, "releases", "tags", provenance.Ref), string(releaseJSON))
	writeFile(t, filepath.Join(registry.root, "repos", manifest.ID, "git", "ref", "tags", provenance.Ref), fmt.Sprintf(`{"object":{"type":"commit","sha":%q}}`, commit))
	registry.engine.Resolver.(*GitHubResolver).Packages[manifest.ID] = GitHubPackage{Owner: parts[0], RepositoryName: parts[1], RepositoryURL: repository, ArtifactAsset: "module.tar", MetadataAsset: PackageManifestFileName, ProvenanceAsset: "provenance.json", SignatureAsset: "provenance.sig"}
	registry.engine.Trust.Repositories[manifest.ID] = repository
	registry.engine.Trust.Signers[manifest.ID] = map[string]ed25519.PublicKey{"owner-release": registry.key.Public().(ed25519.PublicKey)}
	registry.engine.Trust.BuildSigners[manifest.ID] = map[string]ed25519.PublicKey{"owner-builder": registry.key.Public().(ed25519.PublicKey)}
	return selection
}

type nestedFixture struct {
	registry                         *selectionRegistry
	root, module, oldAgent, newAgent ReleaseSelection
	descriptor                       *Descriptor
	options                          ResolutionOptions
	moduleManifest                   *PackageManifest
}

func nestedSelectionFixture(t *testing.T, usage string) *nestedFixture {
	t.Helper()
	r := newSelectionRegistry(t)
	agent := selectionManifest("example/agent", "5.5.0")
	agent.ReleaseArtifacts = []ReleaseArtifact{fixtureArtifact("tool", ArtifactLifecycleAgent, "agent 5.5.0"), fixtureArtifact("builder", ArtifactBuildAgent, "builder 5.5.0")}
	oldAgent := r.publish(t, agent)
	agent.Version = "5.6.0"
	agent.ReleaseArtifacts = []ReleaseArtifact{fixtureArtifact("tool", ArtifactLifecycleAgent, "agent 5.6.0"), fixtureArtifact("builder", ArtifactBuildAgent, "builder 5.6.0")}
	newAgent := r.publish(t, agent)
	module := selectionManifest("example/saas", "4.5.0")
	module.AllowDerivedBuilds = true
	module.ReleaseArtifacts = []ReleaseArtifact{fixtureArtifact("store", ArtifactRuntime, "owner runtime"), fixtureArtifact("source", ArtifactSource, "owner source"), fixtureArtifact("sdk", ArtifactClient, "owner SDK")}
	module.Services = []ProvidedService{{Name: "store", AgentUsage: usage, Agent: &ComponentDefault{Release: oldAgent, Requirements: map[string]string{"configuration": "^1.0.0"}}, RuntimeArtifacts: []string{"store"}}}
	selectedModule := r.publish(t, module)
	base := selectionManifest("example/lodestar", "1.3.0")
	for _, name := range []string{"saas", "documents", "unused"} {
		base.Modules = append(base.Modules, ProvidedModule{Name: name, Default: ComponentDefault{Release: selectedModule, Requirements: map[string]string{"interface": "^1.0.0"}}, Services: Services{Include: []string{"store"}}})
	}
	base.Modules[2].Default.Release = ReleaseSelection{ID: "example/missing", Version: "1.0.0", Digest: fixtureArtifact("unused", ArtifactSource, "unused").Digest}
	root := r.publish(t, base)
	configuration, err := ConfigurationIdentity(bytes.Repeat([]byte{7}, 32), map[string]string{"token": "private-value"})
	require.NoError(t, err)
	return &nestedFixture{registry: r, root: root, module: selectedModule, oldAgent: oldAgent, newAgent: newAgent, moduleManifest: module,
		descriptor: &Descriptor{Kind: DescriptorKind, Name: "product", Base: Base{ID: root.ID, Version: "^1.0.0"}, Modules: ModuleInstances{Include: []string{"saas", "documents"}}}, options: ResolutionOptions{ConfigurationIdentity: configuration}}
}

func TestNestedSelectionsAreScopedAndAcquireOnlyDeclaredMetadata(t *testing.T) {
	f := nestedSelectionFixture(t, "lifecycle")
	ctx := context.Background()
	baseline, err := f.registry.engine.ResolveComposition(ctx, f.descriptor, f.root, f.options)
	require.NoError(t, err)
	f.descriptor.Replacements = []Replacement{{Target: "modules/saas/services/store/agent", Release: f.newAgent, Rationale: "qualify replacement"}}
	a, err := f.registry.engine.ResolveComposition(ctx, f.descriptor, f.root, f.options)
	require.NoError(t, err)
	require.NotEqual(t, baseline.Identity(), a.Identity())
	for _, component := range a.Record().Components {
		switch component.Target {
		case "modules/saas/services/store/agent":
			require.Equal(t, f.oldAgent, component.Inherited)
			require.Equal(t, f.newAgent, component.Selected)
		case "modules/documents/services/store/agent":
			require.Equal(t, f.oldAgent, component.Selected)
		case "":
			require.Equal(t, f.root, component.Selected)
		default:
			require.Equal(t, f.module, component.Selected)
		}
	}
	f.descriptor.Replacements[0].Target = "modules/documents/services/store/agent"
	b, err := f.registry.engine.ResolveComposition(ctx, f.descriptor, f.root, f.options)
	require.NoError(t, err)
	require.NotEqual(t, a.Identity(), b.Identity())
	f.descriptor.Modules.Include = []string{"documents", "saas"}
	again, err := f.registry.engine.ResolveComposition(ctx, f.descriptor, f.root, f.options)
	require.NoError(t, err)
	require.Equal(t, b.Identity(), again.Identity())
	for _, acquisition := range a.Record().Acquisitions {
		require.NotEqual(t, ArtifactSource, acquisition.Artifact.Purpose)
		require.NotEqual(t, ArtifactClient, acquisition.Artifact.Purpose)
	}
	f.registry.mu.Lock()
	defer f.registry.mu.Unlock()
	for _, request := range f.registry.requests {
		require.NotContains(t, request, "/example/missing/")
		if strings.Contains(request, "/assets/") {
			id, err := strconv.Atoi(filepath.Base(request))
			require.NoError(t, err)
			require.NotZero(t, id%4, "implementation archive was downloaded: %s", request)
		}
	}
	metadata := f.registry.metadata[f.module]
	verified, err := verifyMetadata(metadata, f.module, f.registry.engine.Trust)
	require.NoError(t, err)
	require.Equal(t, f.oldAgent, verified.manifest.Services[0].Agent.Release)
	copy := a.Record()
	copy.Components[0].Selected = f.newAgent
	require.Equal(t, f.root, a.Record().Components[0].Selected)
}

func TestNestedSelectionsRejectAmbiguityMissingArtifactsAndConfigurationConflicts(t *testing.T) {
	for _, scenario := range []string{"ambiguous", "duplicate", "missing target", "wrong authority", "config conflict", "missing artifact", "source fallback"} {
		t.Run(scenario, func(t *testing.T) {
			f := nestedSelectionFixture(t, "lifecycle")
			replacement := Replacement{Target: "modules/saas/services/store/agent", Release: f.newAgent, Rationale: "qualified"}
			f.descriptor.Replacements = []Replacement{replacement}
			var want string
			switch scenario {
			case "ambiguous":
				f.descriptor.Replacements[0].Target = "store"
				want = "must name"
			case "duplicate":
				f.descriptor.Replacements = append(f.descriptor.Replacements, replacement)
				want = "conflicting replacements"
			case "missing target":
				f.descriptor.Replacements[0].Target = "modules/removed/services/store/agent"
				want = "does not participate"
			case "wrong authority":
				f.descriptor.Replacements[0].Release.ID = "example/private"
				want = "release authority"
			case "config conflict":
				f.descriptor.Replacements[0].Requirements = map[string]string{"configuration": "^2.0.0"}
				want = "requires configuration"
			case "missing artifact":
				f.options.Artifacts = []ArtifactRequest{{Target: "modules/saas", Name: "absent"}}
				want = "not published"
			case "source fallback":
				f.options.Artifacts = []ArtifactRequest{{Target: "modules/saas", Name: "source"}}
				want = "explicit source-build"
			}
			_, err := f.registry.engine.ResolveComposition(context.Background(), f.descriptor, f.root, f.options)
			require.ErrorContains(t, err, want)
		})
	}
}

func TestMetadataAuthenticationBindsOwnerManifestAndArchive(t *testing.T) {
	f := nestedSelectionFixture(t, "lifecycle")
	original := f.registry.metadata[f.module]
	for _, scenario := range []string{"manifest bytes", "release bytes", "wrong signer", "no metadata binding"} {
		t.Run(scenario, func(t *testing.T) {
			metadata := *original
			selected := f.module
			switch scenario {
			case "manifest bytes":
				metadata.Manifest = append(bytes.Clone(metadata.Manifest), '\n')
			case "release bytes":
				selected.Digest = fixtureArtifact("changed", ArtifactSource, "changed").Digest
			case "wrong signer":
				metadata.Signature = ed25519.Sign(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, 32)), metadata.Provenance)
			case "no metadata binding":
				var provenance Provenance
				require.NoError(t, json.Unmarshal(metadata.Provenance, &provenance))
				provenance.ManifestDigest = ""
				var err error
				metadata.Provenance, err = json.Marshal(provenance)
				require.NoError(t, err)
				metadata.Signature = ed25519.Sign(f.registry.key, metadata.Provenance)
			}
			_, err := verifyMetadata(&metadata, selected, f.registry.engine.Trust)
			require.Error(t, err)
		})
	}
	release, err := f.registry.engine.Resolver.Resolve(context.Background(), ResolveRequest{Package: f.module.ID, Version: f.module.Version})
	require.NoError(t, err)
	_, err = VerifyRelease(release, f.module.ID, f.module.Version, f.registry.engine.Trust)
	require.NoError(t, err)
}

func TestNestedLocalCheckoutsAndBuildAgentChangesRequireActualBuilds(t *testing.T) {
	f := nestedSelectionFixture(t, "build")
	ctx := context.Background()
	baseline, err := f.registry.engine.ResolveComposition(ctx, f.descriptor, f.root, f.options)
	require.NoError(t, err)
	f.descriptor.Replacements = []Replacement{{Target: "modules/saas/services/store/agent", Release: f.newAgent, Rationale: "new compiler"}}
	_, err = f.registry.engine.ResolveComposition(ctx, f.descriptor, f.root, f.options)
	require.ErrorContains(t, err, "explicit source-build")
	f.options.SourceBuilds = []string{"modules/saas"}
	rebuilt, err := f.registry.engine.ResolveComposition(ctx, f.descriptor, f.root, f.options)
	require.NoError(t, err)
	require.Len(t, rebuilt.Record().Builds, 1)
	for _, acquisition := range rebuilt.Record().Acquisitions {
		if acquisition.Target == "modules/saas" {
			require.NotEqual(t, ArtifactRuntime, acquisition.Artifact.Purpose)
		}
	}
	f.descriptor.Replacements = nil
	f.options.SourceBuilds = nil
	f.options.LocalCheckouts = make(map[string]string)
	for _, target := range []string{"modules/saas", "modules/documents"} {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, PackageManifestFileName), string(f.registry.metadata[f.module].Manifest))
		writeFile(t, filepath.Join(root, "contracts", "README"), "local edits")
		f.options.LocalCheckouts[target] = root
	}
	local, err := f.registry.engine.ResolveComposition(ctx, f.descriptor, f.root, f.options)
	require.NoError(t, err)
	require.Len(t, local.Record().Builds, 2)
	_, err = f.registry.engine.CheckDeploymentInputs(ctx, local, DeploymentInputs{})
	require.ErrorContains(t, err, "cannot be deployed")
	path := filepath.Join(f.options.LocalCheckouts["modules/saas"], "contracts", "README")
	writeFile(t, path, "same path, new content")
	require.ErrorIs(t, local.CheckLocalInputs(), ErrDigestMismatch)
	changed, err := f.registry.engine.ResolveComposition(ctx, f.descriptor, f.root, f.options)
	require.NoError(t, err)
	require.NotEqual(t, local.Identity(), changed.Identity())
	f.options.LocalCheckouts = nil
	restored, err := f.registry.engine.ResolveComposition(ctx, f.descriptor, f.root, f.options)
	require.NoError(t, err)
	require.Equal(t, baseline.Identity(), restored.Identity())
	require.Equal(t, "same path, new content", readFile(t, path))
}

func runtimeInputs() DeploymentInputs {
	return DeploymentInputs{Runtime: []RuntimeInput{{Target: "modules/saas", Name: "store", Content: strings.NewReader("owner runtime")}, {Target: "modules/documents", Name: "store", Content: strings.NewReader("owner runtime")}}, Bindings: map[string]string{"environment": fixtureArtifact("target", ArtifactRuntime, "production").Digest}}
}

func TestDeploymentAdmissionBindsActualBytesQualificationAndTarget(t *testing.T) {
	f := nestedSelectionFixture(t, "lifecycle")
	ctx := context.Background()
	resolved, err := f.registry.engine.ResolveComposition(ctx, f.descriptor, f.root, f.options)
	require.NoError(t, err)
	record, err := f.registry.engine.CheckDeploymentInputs(ctx, resolved, runtimeInputs())
	require.NoError(t, err)
	now := time.Now().UTC()
	qualification := Qualification{Schema: "codefly/deployment-qualification/v1", SelectionIdentity: resolved.Identity(), RuntimeIdentity: record.RuntimeIdentity, BindingIdentity: record.BindingIdentity, Kind: "functional", Signer: "reviewer", ExpiresAt: now.Add(time.Hour)}
	statement, err := json.Marshal(qualification)
	require.NoError(t, err)
	signed := SignedQualification{Statement: statement, Signature: ed25519.Sign(f.registry.key, statement)}
	policy := DeploymentPolicy{RequiredQualifications: []string{"functional"}, QualificationSigners: map[string]map[string]ed25519.PublicKey{"functional": {"reviewer": f.registry.key.Public().(ed25519.PublicKey)}}}
	inputs := runtimeInputs()
	inputs.Qualifications = []SignedQualification{signed}
	approved, err := f.registry.engine.AdmitDeployment(ctx, resolved, inputs, policy, now)
	require.NoError(t, err)
	require.NotEmpty(t, approved.Identity())
	approvalRecord := approved.Record()
	require.Equal(t, now, approvalRecord.ApprovedAt)
	require.Equal(t, qualification.ExpiresAt, approvalRecord.ValidUntil)
	require.Len(t, approvalRecord.Qualifications, 1)
	for _, scenario := range []string{"private image", "missing", "duplicate", "stale evidence", "target change", "revoked signer"} {
		t.Run(scenario, func(t *testing.T) {
			inputs := runtimeInputs()
			inputs.Qualifications = []SignedQualification{signed}
			switch scenario {
			case "private image":
				inputs.Runtime[0].Content = strings.NewReader("privately patched output")
			case "missing":
				inputs.Runtime = inputs.Runtime[:1]
			case "duplicate":
				inputs.Runtime = append(inputs.Runtime, RuntimeInput{Target: "modules/saas", Name: "store", Content: strings.NewReader("owner runtime")})
			case "stale evidence":
				inputs.Qualifications = nil
			case "target change":
				inputs.Bindings["environment"] = fixtureArtifact("target", ArtifactRuntime, "other production").Digest
			case "revoked signer":
				f.registry.engine.Trust.Signers[f.module.ID]["owner-release"] = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, 32)).Public().(ed25519.PublicKey)
				t.Cleanup(func() {
					f.registry.engine.Trust.Signers[f.module.ID]["owner-release"] = f.registry.key.Public().(ed25519.PublicKey)
				})
			}
			_, err := f.registry.engine.AdmitDeployment(ctx, resolved, inputs, policy, now)
			require.Error(t, err)
		})
	}
	inputs = runtimeInputs()
	inputs.Qualifications = []SignedQualification{signed}
	_, err = f.registry.engine.AdmitDeployment(ctx, resolved, inputs, policy, now.Add(2*time.Hour))
	require.ErrorContains(t, err, "expired")
}

func TestConfigurationIdentityDoesNotExposeSecretValues(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	first, err := ConfigurationIdentity(key, map[string]string{"token": "secret", "region": "east"})
	require.NoError(t, err)
	second, err := ConfigurationIdentity(key, map[string]string{"region": "east", "token": "secret"})
	require.NoError(t, err)
	require.Equal(t, first, second)
	other, err := ConfigurationIdentity(bytes.Repeat([]byte{2}, 32), map[string]string{"token": "secret", "region": "east"})
	require.NoError(t, err)
	require.NotEqual(t, first, other)
	require.NotContains(t, first, "secret")
	_, err = ConfigurationIdentity(nil, nil)
	require.Error(t, err)
}

func TestProductInputsInvalidateDeploymentQualification(t *testing.T) {
	for _, change := range []string{"binding", "removed suite", "command", "edited file", "deleted file", "renamed file"} {
		t.Run(change, func(t *testing.T) {
			f := nestedSelectionFixture(t, "lifecycle")
			f.options.ProductRoot = t.TempDir()
			input := filepath.Join(f.options.ProductRoot, "tests", "input.txt")
			writeFile(t, input, "original")
			f.descriptor.Contributions.Tests = []IntegrationContribution{{Path: "tests", Command: []string{"go", "test", "./..."}}}
			f.descriptor.Bindings = []Binding{{Plugin: "frontend", Alias: "store", Target: BindingTarget{Module: "saas", Service: "store"}}}
			before, err := f.registry.engine.ResolveComposition(t.Context(), f.descriptor, f.root, f.options)
			require.NoError(t, err)
			record, err := f.registry.engine.CheckDeploymentInputs(t.Context(), before, runtimeInputs())
			require.NoError(t, err)
			now := time.Now()
			statement, err := json.Marshal(Qualification{Schema: "codefly/deployment-qualification/v1", SelectionIdentity: before.Identity(), RuntimeIdentity: record.RuntimeIdentity, BindingIdentity: record.BindingIdentity, Kind: "functional", Signer: "reviewer", ExpiresAt: now.Add(time.Hour)})
			require.NoError(t, err)
			policy := DeploymentPolicy{RequiredQualifications: []string{"functional"}, QualificationSigners: map[string]map[string]ed25519.PublicKey{"functional": {"reviewer": f.registry.key.Public().(ed25519.PublicKey)}}}
			switch change {
			case "binding":
				f.descriptor.Bindings[0].Target.Module = "documents"
			case "removed suite":
				f.descriptor.Contributions.Tests = nil
			case "command":
				f.descriptor.Contributions.Tests[0].Command[0] = "different-runner"
			case "edited file":
				writeFile(t, input, "edited")
			case "deleted file":
				require.NoError(t, os.Remove(input))
			case "renamed file":
				require.NoError(t, os.Rename(input, input+".renamed"))
			}
			after, err := f.registry.engine.ResolveComposition(t.Context(), f.descriptor, f.root, f.options)
			require.NoError(t, err)
			require.NotEqual(t, before.Identity(), after.Identity())
			inputs := runtimeInputs()
			inputs.Qualifications = []SignedQualification{{Statement: statement, Signature: ed25519.Sign(f.registry.key, statement)}}
			_, err = f.registry.engine.AdmitDeployment(t.Context(), after, inputs, policy, now)
			require.ErrorContains(t, err, "different inputs")
			if strings.HasSuffix(change, "file") {
				_, err = f.registry.engine.CheckDeploymentInputs(t.Context(), before, runtimeInputs())
				require.ErrorIs(t, err, ErrDigestMismatch)
			} else {
				require.NoError(t, before.CheckLocalInputs(), "resolved descriptor must not alias the caller")
			}
		})
	}
}

func TestProductContributionsRequireActualFiles(t *testing.T) {
	f := nestedSelectionFixture(t, "lifecycle")
	f.descriptor.Contributions.Tests = []IntegrationContribution{{Path: "tests", Command: []string{"go", "test"}}}
	_, err := f.registry.engine.ResolveComposition(t.Context(), f.descriptor, f.root, f.options)
	require.ErrorContains(t, err, "absolute product root")
	f.options.ProductRoot = t.TempDir()
	_, err = f.registry.engine.ResolveComposition(t.Context(), f.descriptor, f.root, f.options)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(f.options.ProductRoot, "tests")))
	_, err = f.registry.engine.ResolveComposition(t.Context(), f.descriptor, f.root, f.options)
	require.ErrorContains(t, err, "symlink")
}

func TestDerivedOutputsRequireOwnerAuthorizedExactBuildInputs(t *testing.T) {
	f := nestedSelectionFixture(t, "build")
	f.descriptor.Replacements = []Replacement{{Target: "modules/saas/services/store/agent", Release: f.newAgent, Rationale: "new compiler"}}
	f.options.SourceBuilds = []string{"modules/saas"}
	ctx := context.Background()
	resolved, err := f.registry.engine.ResolveComposition(ctx, f.descriptor, f.root, f.options)
	require.NoError(t, err)
	build := resolved.Record().Builds[0]
	statement := DerivedOutput{Schema: "codefly/derived-output/v1", SelectionIdentity: resolved.Identity(), Target: build.Target, Artifact: build.ReplacesArtifact, SourceIdentity: build.SourceIdentity, Digest: fixtureArtifact("derived", ArtifactRuntime, "derived output").Digest, Signer: "owner-builder"}
	for _, scenario := range []string{"authorized", "private source", "different selection", "unauthorized builder", "tampered bytes", "missing attestation"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := statement
			inputs := runtimeInputs()
			inputs.Runtime[0].Content = strings.NewReader("derived output")
			key := f.registry.key
			switch scenario {
			case "private source":
				candidate.SourceIdentity = fixtureArtifact("source", ArtifactSource, "private patch").Digest
			case "different selection":
				candidate.SelectionIdentity = f.root.Digest
			case "unauthorized builder":
				key = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))
			case "tampered bytes":
				inputs.Runtime[0].Content = strings.NewReader("private runtime")
			}
			data, err := json.Marshal(candidate)
			require.NoError(t, err)
			if scenario != "missing attestation" {
				inputs.Derived = []SignedDerivedOutput{{Statement: data, Signature: ed25519.Sign(key, data)}}
			}
			record, err := f.registry.engine.CheckDeploymentInputs(ctx, resolved, inputs)
			if scenario != "authorized" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			for _, artifact := range record.Artifacts {
				if artifact.Target == build.Target {
					require.Equal(t, &statement, artifact.Derived)
				}
			}
		})
	}
}

func TestUpstreamCatchUpRequiresFullResolutionAndPreservesRequirements(t *testing.T) {
	f := nestedSelectionFixture(t, "lifecycle")
	target := "modules/saas/services/store/agent"
	f.descriptor.Replacements = []Replacement{{Target: target, Release: f.newAgent, Rationale: "tested replacement"}}
	ctx := context.Background()
	previous, err := f.registry.engine.ResolveComposition(ctx, f.descriptor, f.root, f.options)
	require.NoError(t, err)
	facts, err := previous.UpstreamAdoptions(nil)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.Equal(t, "https://github.com/example/saas", facts[0].OwnerRepository)
	require.Equal(t, f.oldAgent, facts[0].Difference.Inherited)
	require.Equal(t, f.newAgent, facts[0].Difference.Selected)
	_, err = f.registry.engine.ProposeOverrideRemoval(ctx, previous, f.descriptor, f.root, f.options, target)
	require.ErrorContains(t, err, "not equivalent")
	f.moduleManifest.Version = "4.6.0"
	f.moduleManifest.Services[0].Agent.Release = f.newAgent
	newModule := f.registry.publish(t, f.moduleManifest)
	rootMetadata, err := verifyMetadata(f.registry.metadata[f.root], f.root, f.registry.engine.Trust)
	require.NoError(t, err)
	rootMetadata.manifest.Version = "1.4.0"
	rootMetadata.manifest.Modules[0].Default.Release = newModule
	newRoot := f.registry.publish(t, rootMetadata.manifest)
	proposal, err := f.registry.engine.ProposeOverrideRemoval(ctx, previous, f.descriptor, newRoot, f.options, target)
	require.NoError(t, err)
	require.Empty(t, proposal.Descriptor.Replacements)
	require.Len(t, f.descriptor.Replacements, 1)
	require.NotEqual(t, previous.Identity(), proposal.WithoutOverrideIdentity)
	f.descriptor.Replacements[0].Requirements = map[string]string{"configuration": "^1.0.0"}
	proposal, err = f.registry.engine.ProposeOverrideRemoval(ctx, previous, f.descriptor, newRoot, f.options, target)
	require.NoError(t, err, "repeating an inherited requirement does not change effective constraints")
	require.Empty(t, proposal.Descriptor.Replacements)
	require.Equal(t, "^1.0.0", f.descriptor.Replacements[0].Requirements["configuration"])
	f.descriptor.Replacements[0].Requirements = map[string]string{"configuration": "1.0.0"}
	_, err = f.registry.engine.ProposeOverrideRemoval(ctx, previous, f.descriptor, newRoot, f.options, target)
	require.ErrorContains(t, err, "not equivalent")
	f.descriptor.Replacements[0].Requirements = nil
	rootMetadata.manifest.Version = "1.5.0"
	rootMetadata.manifest.Modules[0].Name = "moved"
	moved := f.registry.publish(t, rootMetadata.manifest)
	_, err = f.registry.engine.ProposeOverrideRemoval(ctx, previous, f.descriptor, moved, f.options, target)
	require.ErrorContains(t, err, "included module saas is not declared")
}

func TestSinglePackageEntryPointsCannotIgnoreNestedSelections(t *testing.T) {
	f := nestedSelectionFixture(t, "lifecycle")
	root := t.TempDir()
	data, err := yaml.Marshal(f.descriptor)
	require.NoError(t, err)
	writeFile(t, filepath.Join(root, DescriptorFileName), string(data))
	_, err = f.registry.engine.Update(context.Background(), root, f.root.Version, true)
	require.ErrorContains(t, err, "cannot apply them")
	_, err = f.registry.engine.Materialize(context.Background(), root, MaterializeOptions{})
	require.ErrorContains(t, err, "cannot apply them")
	_, err = f.registry.engine.Source(context.Background(), root, MaterializeOptions{})
	require.ErrorContains(t, err, "cannot apply them")
	_, err = f.registry.engine.Doctor(context.Background(), root, true)
	require.ErrorContains(t, err, "cannot apply them")
	_, _, err = (Renderer{}).Render(context.Background(), "", "", "", nil, f.descriptor, f.moduleManifest, nil, nil)
	require.ErrorContains(t, err, "cannot apply them")
	f.registry.mu.Lock()
	defer f.registry.mu.Unlock()
	require.Empty(t, f.registry.requests)
}

func TestReplacementAdmissionCannotOmitCompatibilityOrOwnerQualification(t *testing.T) {
	for _, scenario := range []string{"replacement compatibility", "owner stateful", "missing owner declaration", "explicit empty owner declaration"} {
		t.Run(scenario, func(t *testing.T) {
			f := nestedSelectionFixture(t, "lifecycle")
			if scenario == "replacement compatibility" {
				f.descriptor.Replacements = []Replacement{{Target: "modules/saas/services/store/agent", Release: f.newAgent, Rationale: "qualified candidate"}}
			} else {
				metadata, err := verifyMetadata(f.registry.metadata[f.root], f.root, f.registry.engine.Trust)
				require.NoError(t, err)
				metadata.manifest.RequiredQualifications = &[]string{"stateful"}
				if scenario == "missing owner declaration" {
					metadata.manifest.RequiredQualifications = nil
				}
				if scenario == "explicit empty owner declaration" {
					metadata.manifest.RequiredQualifications = &[]string{}
				}
				f.root = f.registry.publish(t, metadata.manifest)
			}
			resolved, err := f.registry.engine.ResolveComposition(t.Context(), f.descriptor, f.root, f.options)
			require.NoError(t, err)
			record, err := f.registry.engine.CheckDeploymentInputs(t.Context(), resolved, runtimeInputs())
			require.NoError(t, err)
			now := time.Now()
			qualification := Qualification{Schema: "codefly/deployment-qualification/v1", SelectionIdentity: resolved.Identity(), RuntimeIdentity: record.RuntimeIdentity, BindingIdentity: record.BindingIdentity, Kind: "functional", Signer: "reviewer", ExpiresAt: now.Add(time.Hour)}
			data, err := json.Marshal(qualification)
			require.NoError(t, err)
			inputs := runtimeInputs()
			inputs.Qualifications = []SignedQualification{{Statement: data, Signature: ed25519.Sign(f.registry.key, data)}}
			policy := DeploymentPolicy{RequiredQualifications: []string{"functional"}, QualificationSigners: map[string]map[string]ed25519.PublicKey{"functional": {"reviewer": f.registry.key.Public().(ed25519.PublicKey)}}}
			_, err = f.registry.engine.AdmitDeployment(t.Context(), resolved, inputs, policy, now)
			switch scenario {
			case "replacement compatibility":
				require.ErrorContains(t, err, "component-compatibility qualification is missing")
			case "owner stateful":
				require.ErrorContains(t, err, "stateful qualification is missing")
			case "explicit empty owner declaration":
				require.NoError(t, err)
			default:
				require.ErrorContains(t, err, "owner has not declared")
			}
		})
	}
}
