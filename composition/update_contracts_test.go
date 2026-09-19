package composition

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	updatev0 "github.com/codefly-dev/core/generated/go/codefly/update/v0"
	"github.com/codefly-dev/core/moduleupdate"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"gopkg.in/yaml.v3"
)

func writeUpdateSources(t *testing.T, root, version string, watch bool) {
	t.Helper()
	data, err := os.ReadFile("testdata/update/openapi.json")
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(data, &document))
	routes := []APIContractRoute{{Method: "GET", Path: "/accounts"}}
	if watch {
		paths := document["paths"].(map[string]any)
		paths["/watch"] = paths["/accounts"]
		routes = append(routes, APIContractRoute{Method: "GET", Path: "/watch"})
	}
	data, err = json.Marshal(document)
	require.NoError(t, err)
	manifest := &PackageManifest{
		Kind: PackageKind, Schema: PackageSchema, ID: testPackage, Version: version,
		MinimumCodeflyVersion: ">=0.3.32", ArtifactRoots: []string{"contracts"}, Contracts: map[string]string{ContractComposition: ">=2.0 <3.0"},
		Services: []ProvidedService{{Name: "accounts", Endpoints: []string{"rest"}, APIContracts: []ProvidedAPIContract{{Endpoint: "rest", Kind: APIContractKindOpenAPI, Package: "accounts", Path: "contracts/api/openapi.json", Digest: APIContractDigest(data)}}}},
	}
	manifestData, err := yaml.Marshal(manifest)
	require.NoError(t, err)
	writeFile(t, filepath.Join(root, PackageManifestFileName), string(manifestData))
	catalog := &APIContractCatalog{Schema: APIContractCatalogSchema, Package: manifest.ID, Version: version,
		Endpoints: []APIContractEndpoint{{Service: "accounts", Endpoint: "rest", API: "rest", Kind: APIContractKindOpenAPI, Package: "accounts", Path: "contracts/api/openapi.json", Digest: APIContractDigest(data), Routes: routes}},
	}
	catalogData, err := catalog.CanonicalBytes()
	require.NoError(t, err)
	writeFile(t, filepath.Join(root, APIContractCatalogFileName), string(catalogData))
	writeFile(t, filepath.Join(root, "contracts/api/openapi.json"), string(data))
	behavior, err := os.ReadFile("testdata/update/behavior.json")
	require.NoError(t, err)
	writeFile(t, filepath.Join(root, BehavioralContractsFileName), string(behavior))
}

func TestUpdateEvidenceMustMatchPackagedSources(t *testing.T) {
	beforeRoot, afterRoot := t.TempDir(), t.TempDir()
	writeUpdateSources(t, beforeRoot, "0.1.0", false)
	writeUpdateSources(t, afterRoot, "0.2.0", false)
	before, err := BuildPackageContractSnapshot(beforeRoot)
	require.NoError(t, err)
	after, err := BuildPackageContractSnapshot(afterRoot)
	require.NoError(t, err)
	diff, err := moduleupdate.BuildReleaseDiff(before, after)
	require.NoError(t, err)
	data, err := moduleupdate.MarshalReleaseDiff(diff)
	require.NoError(t, err)
	writeFile(t, filepath.Join(afterRoot, moduleupdate.ReleaseDiffFileName), string(data))
	pin := &updatev0.ConsumerPin{SchemaVersion: 1, Consumer: "deployment", Module: testPackage, Version: before.Version, SnapshotDigest: before.Digest, UsageComplete: true}
	for _, item := range before.Items {
		pin.Uses = append(pin.Uses, &updatev0.ContractUse{Item: item.Id, Digest: item.Digest})
	}
	check := func(root string) *updatev0.UpdateResult {
		release, trust := buildRelease(t, root, "0.2.0", strings.Repeat("a", 40))
		verified, err := VerifyRelease(release, testPackage, "0.2.0", trust)
		require.NoError(t, err)
		return verified.EvaluateUpdate(pin)
	}
	require.Equal(t, updatev0.Verdict_VERDICT_SAFE, check(afterRoot).Verdict)

	// Keep source/catalog/manifest mutually consistent while leaving update evidence stale.
	for _, name := range []string{APIContractCatalogFileName, "contracts/api/openapi.json"} {
		data := strings.ReplaceAll(readFile(t, filepath.Join(afterRoot, name)), `"/accounts"`, `"/health"`)
		writeFile(t, filepath.Join(afterRoot, name), data)
	}
	updateAPIDigest(t, afterRoot)
	result := check(afterRoot)
	require.Equal(t, updatev0.Verdict_VERDICT_BREAKING, result.Verdict)
	require.Contains(t, result.Breaking[0].Reason, "does not match packaged contract sources")

	writeUpdateSources(t, afterRoot, "0.2.0", false)
	behaviorPath := filepath.Join(afterRoot, BehavioralContractsFileName)
	writeFile(t, behaviorPath, strings.ReplaceAll(readFile(t, behaviorPath), `"publisher_bound":true`, `"publisher_bound":false`))
	require.Equal(t, updatev0.Verdict_VERDICT_BREAKING, check(afterRoot).Verdict)
	writeFile(t, behaviorPath, `{"schema_version":1}`)
	require.Contains(t, check(afterRoot).Breaking[0].Reason, "complete coverage")
}

func updateAPIDigest(t *testing.T, root string) {
	t.Helper()
	manifest, err := LoadPackageManifest(root)
	require.NoError(t, err)
	catalog, err := LoadAPIContractCatalog(root)
	require.NoError(t, err)
	digest := APIContractDigest([]byte(readFile(t, filepath.Join(root, "contracts/api/openapi.json"))))
	manifest.Services[0].APIContracts[0].Digest = digest
	catalog.Endpoints[0].Digest = digest
	data, err := yaml.Marshal(manifest)
	require.NoError(t, err)
	writeFile(t, filepath.Join(root, PackageManifestFileName), string(data))
	data, err = catalog.CanonicalBytes()
	require.NoError(t, err)
	writeFile(t, filepath.Join(root, APIContractCatalogFileName), string(data))
}

func TestOpenAPIContractSourcesAndReferences(t *testing.T) {
	for name, mutate := range map[string]func(string) string{
		"unresolved reference": func(s string) string {
			return strings.ReplaceAll(s, "#/components/schemas/Account", "#/components/schemas/Missing")
		},
		"external reference": func(s string) string {
			return strings.ReplaceAll(s, "#/components/schemas/Account", "https://example.com/schema.json")
		},
		"stale catalog": func(s string) string { return strings.ReplaceAll(s, `"/accounts"`, `"/health"`) },
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeUpdateSources(t, root, "0.1.0", false)
			path := filepath.Join(root, "contracts/api/openapi.json")
			writeFile(t, path, mutate(readFile(t, path)))
			updateAPIDigest(t, root)
			_, err := BuildPackageContractSnapshot(root)
			require.Error(t, err)
		})
	}
}

func TestProtobufContractSourceDerivesTypesAndRejectsStaleCatalog(t *testing.T) {
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{Name: proto.String("accounts.proto"), Package: proto.String("accounts.v1"), Syntax: proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Account"), Field: []*descriptorpb.FieldDescriptorProto{{Name: proto.String("name"), Number: proto.Int32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()}}}},
		Service:     []*descriptorpb.ServiceDescriptorProto{{Name: proto.String("Accounts"), Method: []*descriptorpb.MethodDescriptorProto{{Name: proto.String("Get"), InputType: proto.String(".accounts.v1.Account"), OutputType: proto.String(".accounts.v1.Account")}}}},
	}}}
	data, err := proto.Marshal(set)
	require.NoError(t, err)
	endpoint := APIContractEndpoint{Package: "accounts.v1", Services: ProtobufServices(set, "accounts.v1")}
	var items []*updatev0.ContractItem
	require.NoError(t, protobufContractItems(endpoint, "api/accounts/grpc", data, &items))
	require.Len(t, items, 2)
	require.Equal(t, "api/accounts/grpc#accounts.v1.Account", items[0].Id)
	require.Contains(t, items[1].Dependencies, items[0].Id)
	original := items[0].Digest
	set.File[0].MessageType[0].Field[0].Type = descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum()
	data, err = proto.Marshal(set)
	require.NoError(t, err)
	items = nil
	require.NoError(t, protobufContractItems(endpoint, "api/accounts/grpc", data, &items))
	require.NotEqual(t, original, items[0].Digest)
	set.File[0].Service[0].Method = nil
	data, err = proto.Marshal(set)
	require.NoError(t, err)
	require.ErrorContains(t, protobufContractItems(endpoint, "api/accounts/grpc", data, &items), "catalog procedures")
}
