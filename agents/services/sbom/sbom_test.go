package sbom

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/stretchr/testify/require"
)

func TestParseGoModulesAndGraph(t *testing.T) {
	modules := strings.Join([]string{
		`{"Path":"example.com/app","Main":true}`,
		`{"Path":"example.com/lib","Version":"v1.2.3"}`,
		`{"Path":"example.com/indirect","Version":"v0.4.0"}`,
	}, "\n")
	components, root, byToken, err := parseGoModules([]byte(modules))
	require.NoError(t, err)
	require.Equal(t, "pkg:golang/example.com/app", root.GetBomRef())
	require.Len(t, components, 2)

	dependencies := parseGoGraph([]byte("example.com/app example.com/lib@v1.2.3\nexample.com/lib@v1.2.3 example.com/indirect@v0.4.0\n"), byToken)
	require.Len(t, dependencies, 3)
	require.Equal(t, "pkg:golang/example.com/lib@v1.2.3", dependencies[0].GetDependsOn()[0])

	first, err := finish(root, components, dependencies, "go-list", "GO")
	require.NoError(t, err)
	second, err := finish(root, components, dependencies, "go-list", "GO")
	require.NoError(t, err)
	require.Equal(t, first.SHA256, second.SHA256)
	require.Equal(t, first.Bom.GetSerialNumber(), second.Bom.GetSerialNumber())
}

func TestManagedSyftArgsRemainHardenedWithBoundedScratch(t *testing.T) {
	args := strings.Join(managedSyftArgs("redis@sha256:abc"), " ")
	require.Contains(t, args, "--read-only")
	require.Contains(t, args, "--cap-drop ALL")
	require.Contains(t, args, "--security-opt no-new-privileges")
	require.Contains(t, args, "size="+SyftScratchSize)
	require.Contains(t, args, SyftImage)
	require.NotContains(t, args, "/var/run/docker.sock")
	require.NotContains(t, args, "--privileged")
}

func TestParseGoModulesPreservesVersionForLocalReplacement(t *testing.T) {
	modules := strings.Join([]string{
		`{"Path":"example.com/app","Main":true}`,
		`{"Path":"github.com/codefly-dev/core","Version":"v0.2.20","Replace":{"Path":"/workspace/core"}}`,
	}, "\n")
	components, _, byToken, err := parseGoModules([]byte(modules))
	require.NoError(t, err)
	require.Len(t, components, 1)
	require.Equal(t, "v0.2.20", components[0].GetVersion())
	require.Equal(t, "pkg:golang/github.com/codefly-dev/core@v0.2.20", components[0].GetPurl())
	require.Equal(t, components[0].GetBomRef(), byToken["github.com/codefly-dev/core@v0.2.20"])
}

func TestPackageLockResultHonorsDevAndDependencyGraph(t *testing.T) {
	hash := base64.StdEncoding.EncodeToString(make([]byte, 64))
	lock := &packageLock{
		Name:            "demo",
		Version:         "1.0.0",
		LockfileVersion: 3,
		Packages: map[string]packageLockPackage{
			"": {
				Dependencies: map[string]string{"prod": "1.0.0", "dev": "2.0.0"},
			},
			"node_modules/prod": {
				Version:      "1.0.0",
				License:      "MIT",
				Integrity:    "sha512-" + hash,
				Dependencies: map[string]string{"nested": "3.0.0"},
			},
			"node_modules/prod/node_modules/nested": {Version: "3.0.0"},
			"node_modules/dev":                      {Version: "2.0.0", Dev: true},
		},
	}
	result, err := packageLockResult(lock, false)
	require.NoError(t, err)
	require.Len(t, result.Bom.GetComponents(), 2)
	require.Equal(t, "TYPESCRIPT", result.Language)
	require.Equal(t, []string{"MIT"}, result.Bom.GetComponents()[1].GetLicenses())
	require.Len(t, result.Bom.GetComponents()[1].GetHashes(), 1)

	root := result.Bom.GetMetadata().GetComponent().GetBomRef()
	var rootDependency *agentv0.Dependency
	for _, dependency := range result.Bom.GetDependencies() {
		if dependency.GetRef() == root {
			rootDependency = dependency
		}
	}
	require.NotNil(t, rootDependency)
	require.Equal(t, []string{"pkg:npm/prod@1.0.0"}, rootDependency.GetDependsOn())
}

func TestParseCycloneDX(t *testing.T) {
	document := `{
  "bomFormat":"CycloneDX",
  "specVersion":"1.5",
  "metadata":{"component":{"type":"application","bom-ref":"root","name":"api","version":"1.0.0","purl":"pkg:pypi/api@1.0.0"}},
  "components":[{"type":"library","bom-ref":"dep","name":"fastapi","version":"1.2.3","purl":"pkg:pypi/fastapi@1.2.3","licenses":[{"license":{"id":"MIT"}}]}],
  "dependencies":[{"ref":"root","dependsOn":["dep"]},{"ref":"dep","dependsOn":[]}]
}`
	result, err := parseCycloneDX([]byte(document), "uv-cyclonedx1.5", "PYTHON")
	require.NoError(t, err)
	require.Equal(t, agentv0.ComponentType_MODULE, result.Bom.GetMetadata().GetComponent().GetType())
	require.Equal(t, []string{"MIT"}, result.Bom.GetComponents()[0].GetLicenses())
	require.NotEmpty(t, result.SHA256)
}

func TestParseCargoMetadataExcludesDevOnlyGraph(t *testing.T) {
	metadata := `{
  "packages": [
    {"name":"app","version":"0.1.0","id":"app 0.1.0 (path+file:///app)","license":"MIT OR Apache-2.0"},
    {"name":"serde","version":"1.0.0","id":"serde 1.0.0 (registry+https://github.com/rust-lang/crates.io-index)","source":"registry+https://github.com/rust-lang/crates.io-index","checksum":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
    {"name":"pretty_assertions","version":"1.4.0","id":"pretty_assertions 1.4.0 (registry+https://github.com/rust-lang/crates.io-index)","source":"registry+https://github.com/rust-lang/crates.io-index"}
  ],
  "workspace_members": ["app 0.1.0 (path+file:///app)"],
  "resolve": {
    "root":"app 0.1.0 (path+file:///app)",
    "nodes": [
      {"id":"app 0.1.0 (path+file:///app)","deps":[
        {"pkg":"serde 1.0.0 (registry+https://github.com/rust-lang/crates.io-index)","dep_kinds":[{"kind":null}]},
        {"pkg":"pretty_assertions 1.4.0 (registry+https://github.com/rust-lang/crates.io-index)","dep_kinds":[{"kind":"dev"}]}
      ]},
      {"id":"serde 1.0.0 (registry+https://github.com/rust-lang/crates.io-index)","deps":[]},
      {"id":"pretty_assertions 1.4.0 (registry+https://github.com/rust-lang/crates.io-index)","deps":[]}
    ]
  }
}`
	result, err := parseCargoMetadata([]byte(metadata), "/app", false)
	require.NoError(t, err)
	require.Equal(t, "RUST", result.Language)
	require.Len(t, result.Bom.GetComponents(), 1)
	require.Equal(t, "serde", result.Bom.GetComponents()[0].GetName())
	require.Equal(t, "SHA-256", result.Bom.GetComponents()[0].GetHashes()[0].GetAlgorithm())
	require.Equal(t, []string{"pkg:cargo/serde@1.0.0"}, result.Bom.GetDependencies()[0].GetDependsOn())

	withDev, err := parseCargoMetadata([]byte(metadata), "/app", true)
	require.NoError(t, err)
	require.Len(t, withDev.Bom.GetComponents(), 2)
}

func TestParseSwiftDependenciesProducesResolvedGraph(t *testing.T) {
	graph := `{
  "identity":"demo","name":"Demo","url":"/src/demo","version":"unspecified",
  "dependencies":[{
    "identity":"vapor","name":"vapor","url":"https://github.com/vapor/vapor.git","version":"4.100.0",
    "dependencies":[{"identity":"swift-nio","name":"swift-nio","url":"https://github.com/apple/swift-nio.git","version":"2.70.0","dependencies":[]}]
  }]
}`
	result, err := parseSwiftDependencies([]byte(graph))
	require.NoError(t, err)
	require.Equal(t, "SWIFT", result.Language)
	require.Equal(t, "pkg:swift/demo", result.Bom.GetMetadata().GetComponent().GetBomRef())
	require.Len(t, result.Bom.GetComponents(), 2)
	require.Len(t, result.Bom.GetDependencies(), 3)
}

func TestAttachArtifactAndMarshalCycloneDXJSON(t *testing.T) {
	root := &agentv0.Component{Name: "module", Type: agentv0.ComponentType_MODULE, Purl: "pkg:golang/example.com/module", BomRef: "pkg:golang/example.com/module"}
	base, err := finish(root, nil, []*agentv0.Dependency{{Ref: root.BomRef}}, "go-list", "GO")
	require.NoError(t, err)
	release, err := AttachArtifact(base, Artifact{
		Publisher: "codefly.dev",
		Name:      "demo",
		Version:   "1.2.3",
		Target:    "linux/amd64",
		SHA256:    strings.Repeat("a", 64),
	})
	require.NoError(t, err)
	require.Equal(t, agentv0.ComponentType_APPLICATION, release.Bom.GetMetadata().GetComponent().GetType())
	require.Equal(t, "SHA-256", release.Bom.GetMetadata().GetComponent().GetHashes()[0].GetAlgorithm())

	payload, err := MarshalCycloneDXJSON(release.Bom)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(payload, &document))
	require.Equal(t, "CycloneDX", document["bomFormat"])
	metadata := document["metadata"].(map[string]any)
	component := metadata["component"].(map[string]any)
	require.Equal(t, "application", component["type"])
	require.Contains(t, component["purl"], "arch=amd64&os=linux")
}

func TestMarshalCycloneDXJSONPreservesLicenseExpression(t *testing.T) {
	root := &agentv0.Component{Name: "crate", Type: agentv0.ComponentType_MODULE, Purl: "pkg:cargo/crate@1.0.0", BomRef: "pkg:cargo/crate@1.0.0", Licenses: []string{"MIT OR Apache-2.0"}}
	result, err := finish(root, nil, nil, "cargo-metadata", "RUST")
	require.NoError(t, err)
	payload, err := MarshalCycloneDXJSON(result.Bom)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(payload, &document))
	metadata := document["metadata"].(map[string]any)
	component := metadata["component"].(map[string]any)
	licenses := component["licenses"].([]any)
	require.Equal(t, "MIT OR Apache-2.0", licenses[0].(map[string]any)["expression"])
}
