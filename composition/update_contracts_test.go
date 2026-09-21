package composition

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	updatev0 "github.com/codefly-dev/core/generated/go/codefly/update/v0"
	"github.com/codefly-dev/core/internal/testgit"
	"github.com/codefly-dev/core/moduleupdate"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"gopkg.in/yaml.v3"
)

func TestIndependentLocalCheckoutsBindDirtyContentAndRestoreWithoutDeletingFiles(t *testing.T) {
	project, released := t.TempDir(), t.TempDir()
	t.Cleanup(func() { _ = removeCacheTree(project) })
	writeUpdateSources(t, released, "0.1.0", false)
	release, trust := buildRelease(t, released, "0.1.0", strings.Repeat("a", 40))
	engine := NewEngine(project, &fixtureResolver{releases: map[string]*Release{"0.1.0": release}}, trust)
	engine.ToolVersion = "0.3.40"
	var modules, sources, locks []string
	var local []*Materialization
	for _, name := range []string{"saas", "documents"} {
		moduleDir, source := filepath.Join(project, name), t.TempDir()
		writeFile(t, filepath.Join(moduleDir, DescriptorFileName), "kind: composed-module\nname: "+name+"\nbase:\n  id: "+testPackage+"\n  version: '^0.1'\n")
		initial, err := engine.Update(t.Context(), moduleDir, "0.1.0", true)
		require.NoError(t, err)
		require.True(t, initial.Applied)
		writeUpdateSources(t, source, "0.1.0", false)
		for _, args := range [][]string{{"init", "--quiet"}, {"add", "."}, {"commit", "--quiet", "-m", "fixture"}} {
			output, err := testgit.Run(t.Context(), source, nil, args...)
			require.NoError(t, err, "%s", output)
		}
		if name == "documents" {
			checkout := filepath.Join(t.TempDir(), "checkout")
			output, err := testgit.Run(t.Context(), source, nil, "worktree", "add", "--detach", checkout, "HEAD")
			require.NoError(t, err, "%s", output)
			source = checkout
		}
		writeFile(t, filepath.Join(source, "contracts", "dirty.txt"), "uncommitted "+name)
		_, err = SetDevelopOverride(t.Context(), project, moduleDir, source)
		require.NoError(t, err)
		materialized, err := engine.Materialize(t.Context(), moduleDir, MaterializeOptions{Namespace: "dev"})
		require.NoError(t, err)
		require.Equal(t, "uncommitted "+name, readFile(t, filepath.Join(materialized.Projection, "contracts", "dirty.txt")))
		require.NoDirExists(t, filepath.Join(materialized.Projection, ".git"))
		require.NoFileExists(t, filepath.Join(materialized.Projection, ".git"))
		modules, sources = append(modules, moduleDir), append(sources, source)
		locks = append(locks, readFile(t, filepath.Join(moduleDir, LockFileName)))
		local = append(local, materialized)
	}
	writeFile(t, filepath.Join(sources[0], ".git", "objects", "maintenance.lock"), "maintenance")
	maintained, err := engine.Materialize(t.Context(), modules[0], MaterializeOptions{Namespace: "dev"})
	require.NoError(t, err)
	require.Equal(t, local[0].Namespace.Digest, maintained.Namespace.Digest)
	writeFile(t, filepath.Join(sources[0], "contracts", "dirty.txt"), "second edit")
	changed, err := engine.Materialize(t.Context(), modules[0], MaterializeOptions{Namespace: "dev"})
	require.NoError(t, err)
	require.NotEqual(t, local[0].Namespace.Digest, changed.Namespace.Digest)
	require.Equal(t, "second edit", readFile(t, filepath.Join(changed.Projection, "contracts", "dirty.txt")))
	unchanged, err := engine.Materialize(t.Context(), modules[1], MaterializeOptions{Namespace: "dev"})
	require.NoError(t, err)
	require.Equal(t, local[1].Namespace.Digest, unchanged.Namespace.Digest)
	for i, name := range []string{"saas", "documents"} {
		before, err := sourceTreeDigest(sources[i])
		require.NoError(t, err)
		require.NoError(t, ClearDevelopOverride(project, name))
		restored, err := engine.Materialize(t.Context(), modules[i], MaterializeOptions{Namespace: "dev"})
		require.NoError(t, err)
		require.NotEqual(t, sources[i], restored.Source)
		require.NoFileExists(t, filepath.Join(restored.Projection, "contracts", "dirty.txt"))
		after, err := sourceTreeDigest(sources[i])
		require.NoError(t, err)
		require.Equal(t, before, after, "restoration cannot mutate the developer checkout")
		require.Equal(t, locks[i], readFile(t, filepath.Join(modules[i], LockFileName)))
		output, err := testgit.Run(t.Context(), sources[i], nil, "rev-parse", "--verify", "HEAD")
		require.NoError(t, err, "%s", output)
	}
}

func TestLocalMaterializationRejectsSourceMutationDuringGeneration(t *testing.T) {
	project, released, source := t.TempDir(), t.TempDir(), t.TempDir()
	t.Cleanup(func() { _ = removeCacheTree(project) })
	writeUpdateSources(t, released, "0.1.0", false)
	writeUpdateSources(t, source, "0.1.0", false)
	release, trust := buildRelease(t, released, "0.1.0", strings.Repeat("a", 40))
	engine := NewEngine(project, &fixtureResolver{releases: map[string]*Release{"0.1.0": release}}, trust)
	engine.ToolVersion = "0.3.40"
	moduleDir := filepath.Join(project, "saas")
	writeFile(t, filepath.Join(moduleDir, DescriptorFileName), "kind: composed-module\nname: saas\nbase:\n  id: "+testPackage+"\n  version: '^0.1'\n")
	_, err := engine.Update(t.Context(), moduleDir, "0.1.0", true)
	require.NoError(t, err)
	originalLock := readFile(t, filepath.Join(moduleDir, LockFileName))
	_, err = SetDevelopOverride(t.Context(), project, moduleDir, source)
	require.NoError(t, err)
	initial, err := engine.Materialize(t.Context(), moduleDir, MaterializeOptions{Namespace: "dev"})
	require.NoError(t, err)
	manifest, err := LoadPackageManifest(source)
	require.NoError(t, err)
	manifest.Generators = []PackageCommand{{Name: "edit-source", Command: []string{"sh", "-c", `printf changed > "$1"`, "edit", filepath.Join(source, "contracts", "edit.txt")}}}
	data, err := yaml.Marshal(manifest)
	require.NoError(t, err)
	writeFile(t, filepath.Join(source, PackageManifestFileName), string(data))
	result, err := engine.Materialize(t.Context(), moduleDir, MaterializeOptions{Namespace: "dev"})
	require.ErrorContains(t, err, "local module content changed during materialization")
	require.Nil(t, result)
	require.Equal(t, "changed", readFile(t, filepath.Join(source, "contracts", "edit.txt")))
	require.Equal(t, originalLock, readFile(t, filepath.Join(moduleDir, LockFileName)))
	require.FileExists(t, filepath.Join(initial.Projection, PackageManifestFileName))
}

func TestEngineUpdateUsesAuthenticatedConsumerBaseline(t *testing.T) {
	for _, scenario := range []string{"compatible skipped release", "optional REST parameter", "required REST parameter", "narrowed inherited REST parameter", "transitive REST type", "removed route", "configuration changed", "missing usage", "stale usage", "tampered baseline", "unsigned usage", "other consumer", "other instance", "expired usage", "stale inputs"} {
		t.Run(scenario, func(t *testing.T) {
			project := t.TempDir()
			t.Cleanup(func() { _ = removeCacheTree(project) })
			moduleDir := filepath.Join(project, "module")
			writeFile(t, filepath.Join(moduleDir, DescriptorFileName), "kind: composed-module\nname: saas\nbase:\n  id: "+testPackage+"\n  version: '>=0.1.0 <1.0.0'\n")
			beforeRoot, afterRoot := t.TempDir(), t.TempDir()
			writeUpdateSources(t, beforeRoot, "0.1.0", false)
			writeUpdateSources(t, afterRoot, "0.9.0", true)
			if scenario == "narrowed inherited REST parameter" {
				for _, root := range []string{beforeRoot, afterRoot} {
					path := filepath.Join(root, "contracts/api/openapi.json")
					var document map[string]any
					require.NoError(t, json.Unmarshal([]byte(readFile(t, path)), &document))
					route := document["paths"].(map[string]any)["/accounts"].(map[string]any)
					route["parameters"] = []any{map[string]any{"name": "mode", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"a", "b"}}}}
					if root == afterRoot {
						route["get"].(map[string]any)["parameters"] = []any{map[string]any{"name": "mode", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"a"}}}}
					}
					data, err := json.Marshal(document)
					require.NoError(t, err)
					writeFile(t, path, string(data))
					updateAPIDigest(t, root)
				}
			}
			if scenario == "optional REST parameter" || scenario == "required REST parameter" {
				path := filepath.Join(afterRoot, "contracts/api/openapi.json")
				var document map[string]any
				require.NoError(t, json.Unmarshal([]byte(readFile(t, path)), &document))
				operation := document["paths"].(map[string]any)["/accounts"].(map[string]any)["get"].(map[string]any)
				operation["parameters"] = []any{map[string]any{"name": "limit", "in": "query", "required": scenario == "required REST parameter", "schema": map[string]any{"type": "integer"}}}
				data, err := json.Marshal(document)
				require.NoError(t, err)
				writeFile(t, path, string(data))
				updateAPIDigest(t, afterRoot)
			}
			if scenario == "transitive REST type" {
				path := filepath.Join(afterRoot, "contracts/api/openapi.json")
				writeFile(t, path, strings.ReplaceAll(readFile(t, path), `"type":"string"`, `"type":"integer"`))
				updateAPIDigest(t, afterRoot)
			}
			if scenario == "removed route" {
				for _, name := range []string{APIContractCatalogFileName, "contracts/api/openapi.json"} {
					writeFile(t, filepath.Join(afterRoot, name), strings.ReplaceAll(readFile(t, filepath.Join(afterRoot, name)), `"/accounts"`, `"/health"`))
				}
				updateAPIDigest(t, afterRoot)
			}
			if scenario == "configuration changed" {
				path := filepath.Join(afterRoot, BehavioralContractsFileName)
				writeFile(t, path, strings.ReplaceAll(readFile(t, path), `"publisher_bound":true`, `"publisher_bound":false`))
			}
			before, err := BuildPackageContractSnapshot(beforeRoot)
			require.NoError(t, err)
			marker := filepath.Join(project, "generator-invoked")
			candidateManifest, err := LoadPackageManifest(afterRoot)
			require.NoError(t, err)
			candidateManifest.Generators = []PackageCommand{{Name: "mark-generation", Command: []string{"sh", "-c", `printf invoked > "$1"`, "sh", marker}}}
			manifestData, err := yaml.Marshal(candidateManifest)
			require.NoError(t, err)
			writeFile(t, filepath.Join(afterRoot, PackageManifestFileName), string(manifestData))
			first, trust := buildRelease(t, beforeRoot, "0.1.0", strings.Repeat("a", 40))
			second, _ := buildRelease(t, afterRoot, "0.9.0", strings.Repeat("b", 40))
			resolver := &fixtureResolver{releases: map[string]*Release{"0.1.0": first, "0.9.0": second}}
			engine := NewEngine(project, resolver, trust)
			engine.ToolVersion = "0.3.40"
			initial, err := engine.Update(t.Context(), moduleDir, "0.1.0", true)
			require.NoError(t, err)
			require.True(t, initial.Applied)
			original := readFile(t, filepath.Join(moduleDir, LockFileName))
			pin := &updatev0.ConsumerPin{SchemaVersion: 1, Consumer: "product", Module: testPackage, Version: before.Version, SnapshotDigest: before.Digest, UsageComplete: true}
			for _, item := range before.Items {
				pin.Uses = append(pin.Uses, &updatev0.ContractUse{Item: item.Id, Digest: item.Digest, Dependencies: item.Dependencies})
			}
			if scenario == "stale usage" {
				pin.Version = "0.8.0"
			}
			if scenario != "missing usage" {
				key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{91}, 32))
				pinData, err := protojson.Marshal(pin)
				require.NoError(t, err)
				statement := ConsumerUsageStatement{Schema: "codefly/consumer-usage/v1", Instance: "saas", CompositionDigest: initial.Lock.CompositionDigest, Pin: pinData, Signer: "consumer-workflow", ExpiresAt: time.Now().Add(time.Hour)}
				switch scenario {
				case "other instance":
					statement.Instance = "documents"
				case "expired usage":
					statement.ExpiresAt = time.Now().Add(-time.Hour)
				case "stale inputs":
					statement.CompositionDigest = "sha256:" + strings.Repeat("b", 64)
				}
				data, err := json.Marshal(statement)
				require.NoError(t, err)
				signed := SignedConsumerUsage{Statement: data, Signature: ed25519.Sign(key, data)}
				if scenario == "unsigned usage" {
					signed.Signature = nil
				}
				engine.ConsumerPins = map[string]SignedConsumerUsage{"saas": signed}
				consumer := "product"
				if scenario == "other consumer" {
					consumer = "other-product"
				}
				engine.ConsumerAuthorities = map[string]ConsumerUsageAuthority{"saas": {Consumer: consumer, Signers: map[string]ed25519.PublicKey{"consumer-workflow": key.Public().(ed25519.PublicKey)}}}
			}
			if scenario == "tampered baseline" {
				first.Signature[0] ^= 0xff
			}
			result, err := engine.Update(t.Context(), moduleDir, "0.9.0", true)
			require.NoError(t, err)
			switch scenario {
			case "compatible skipped release", "optional REST parameter":
				require.True(t, result.Applied, result.Report.String())
				require.Equal(t, updatev0.Verdict_VERDICT_NEW_CAPABILITY, result.Report.ConsumerCompatibility.Verdict)
				require.Equal(t, "0.9.0", result.Lock.Version)
			case "removed route", "required REST parameter", "transitive REST type":
				require.False(t, result.Applied)
				require.Equal(t, updatev0.Verdict_VERDICT_BREAKING, result.Report.ConsumerCompatibility.Verdict)
			default:
				require.False(t, result.Applied)
				require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, result.Report.ConsumerCompatibility.Verdict)
			}
			if !result.Applied {
				require.NoFileExists(t, marker, "rejected usage/compatibility must not run candidate generators")
				require.Equal(t, original, readFile(t, filepath.Join(moduleDir, LockFileName)))
				require.True(t, projectionMatches(initial.Projection, initial.Lock))
			} else {
				require.FileExists(t, marker)
			}
		})
	}
}

func TestQualificationCannotPublishAfterConsumerInputsChange(t *testing.T) {
	for _, change := range []string{"fixture", "descriptor"} {
		t.Run(change, func(t *testing.T) {
			project, moduleDir, packageDir := t.TempDir(), t.TempDir(), t.TempDir()
			t.Cleanup(func() { _ = removeCacheTree(project) })
			writeUpdateSources(t, packageDir, "0.1.0", false)
			release, trust := buildRelease(t, packageDir, "0.1.0", strings.Repeat("a", 40))
			engine := NewEngine(project, &fixtureResolver{releases: map[string]*Release{"0.1.0": release}}, trust)
			engine.ToolVersion = "0.3.40"
			marker := filepath.Join(project, "change-inputs")
			replacementPath := filepath.Join(project, "changed-descriptor.yaml")
			command := `if test -f "$1"; then printf changed > fixture.txt; fi`
			if change == "descriptor" {
				command = `if test -f "$1"; then cp "$2" "$CODEFLY_COMPOSITION_CONSUMER/module.codefly.yaml"; fi`
			}
			descriptor := &Descriptor{Kind: DescriptorKind, Name: "saas", Base: Base{ID: testPackage, Version: "^0.1"}, Contributions: Contributions{Tests: []IntegrationContribution{{Path: "suite", Command: []string{"sh", "-c", command, "sh", marker, replacementPath}}}}}
			data, err := yaml.Marshal(descriptor)
			require.NoError(t, err)
			changedDescriptor := *descriptor
			changedDescriptor.Services.Include = []string{"accounts"}
			changedData, err := yaml.Marshal(changedDescriptor)
			require.NoError(t, err)
			writeFile(t, replacementPath, string(changedData))
			writeFile(t, filepath.Join(moduleDir, DescriptorFileName), string(data))
			writeFile(t, filepath.Join(moduleDir, "suite", "fixture.txt"), "original")
			initial, err := engine.Update(t.Context(), moduleDir, "0.1.0", true)
			require.NoError(t, err)
			require.True(t, initial.Applied)
			prior := readFile(t, filepath.Join(moduleDir, LockFileName))
			writeFile(t, marker, "change")
			result, err := engine.Update(t.Context(), moduleDir, "0.1.0", true)
			require.NoError(t, err)
			require.False(t, result.Applied)
			require.Contains(t, result.Report.BlockedReasons, "composition inputs changed during qualification")
			require.Equal(t, prior, readFile(t, filepath.Join(moduleDir, LockFileName)))
			require.True(t, projectionMatches(initial.Projection, initial.Lock))
			writeFile(t, filepath.Join(moduleDir, DescriptorFileName), string(data))
			writeFile(t, filepath.Join(moduleDir, "suite", "fixture.txt"), "original")
			_, err = engine.Materialize(t.Context(), moduleDir, MaterializeOptions{Namespace: "verification"})
			require.ErrorContains(t, err, "composition inputs changed during materialization")
			require.Equal(t, prior, readFile(t, filepath.Join(moduleDir, LockFileName)))
		})
	}
}

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
		pin.Uses = append(pin.Uses, &updatev0.ContractUse{Item: item.Id, Digest: item.Digest, Dependencies: item.Dependencies})
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
	require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, result.Verdict)
	require.Contains(t, result.Undetermined[0].Reason, "does not match packaged contract sources")

	writeUpdateSources(t, afterRoot, "0.2.0", false)
	behaviorPath := filepath.Join(afterRoot, BehavioralContractsFileName)
	writeFile(t, behaviorPath, strings.ReplaceAll(readFile(t, behaviorPath), `"publisher_bound":true`, `"publisher_bound":false`))
	require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, check(afterRoot).Verdict)
	writeFile(t, behaviorPath, `{"schema_version":1}`)
	require.Contains(t, check(afterRoot).Undetermined[0].Reason, "complete coverage")
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
	require.NoError(t, protobufContractItems(endpoint, "api/accounts/grpc", data, &items, nil))
	require.Len(t, items, 2)
	require.Equal(t, "api/accounts/grpc#accounts.v1.Account", items[0].Id)
	require.Contains(t, items[1].Dependencies, items[0].Id)
	original := items[0].Digest
	set.File[0].MessageType[0].Field[0].Type = descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum()
	data, err = proto.Marshal(set)
	require.NoError(t, err)
	items = nil
	require.NoError(t, protobufContractItems(endpoint, "api/accounts/grpc", data, &items, nil))
	require.NotEqual(t, original, items[0].Digest)
	set.File[0].Service[0].Method = nil
	data, err = proto.Marshal(set)
	require.NoError(t, err)
	require.ErrorContains(t, protobufContractItems(endpoint, "api/accounts/grpc", data, &items, nil), "catalog procedures")
}
