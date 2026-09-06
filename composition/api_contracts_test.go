package composition

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func sampleCatalog() *APIContractCatalog {
	return &APIContractCatalog{
		Schema:  APIContractCatalogSchema,
		Package: testPackage,
		Version: "0.1.0",
		Endpoints: []APIContractEndpoint{
			{
				Service:  "accounts",
				Endpoint: "connect",
				API:      "connect",
				Kind:     APIContractKindProtobuf,
				Package:  "saas.accounts.v1",
				Path:     "contracts/api/accounts/connect/descriptor.binpb",
				Digest:   APIContractDigest([]byte("descriptor")),
				Services: []APIContractService{
					{
						Name:       "AuditService",
						FullName:   "saas.accounts.v1.AuditService",
						Procedures: []string{"/saas.accounts.v1.AuditService/QueryAuditLog"},
					},
				},
			},
			{
				Service:  "gateway",
				Endpoint: "rest",
				API:      "rest",
				Kind:     APIContractKindOpenAPI,
				Package:  "Gateway API",
				Path:     "contracts/api/gateway/rest/openapi.json",
				Digest:   APIContractDigest([]byte("openapi")),
				Routes: []APIContractRoute{
					{Method: "GET", Path: "/health"},
				},
			},
		},
	}
}

func TestAPIContractCatalogRoundTrip(t *testing.T) {
	catalog := sampleCatalog()
	data, err := catalog.CanonicalBytes()
	require.NoError(t, err)

	moduleDir := t.TempDir()
	writeFile(t, filepath.Join(moduleDir, filepath.FromSlash(APIContractCatalogFileName)), string(data))
	loaded, err := LoadAPIContractCatalog(moduleDir)
	require.NoError(t, err)
	require.Equal(t, catalog, loaded)

	reversed := sampleCatalog()
	reversed.Endpoints[0], reversed.Endpoints[1] = reversed.Endpoints[1], reversed.Endpoints[0]
	reversedData, err := reversed.CanonicalBytes()
	require.NoError(t, err)
	require.Equal(t, string(data), string(reversedData))
	require.True(t, strings.HasSuffix(string(data), "\n"))
}

func TestAPIContractCatalogValidateRejects(t *testing.T) {
	cases := map[string]struct {
		mutate func(*APIContractCatalog)
		field  string
	}{
		"wrong schema": {
			mutate: func(c *APIContractCatalog) { c.Schema = "codefly/other/v1" },
			field:  "schema",
		},
		"duplicate endpoint": {
			mutate: func(c *APIContractCatalog) { c.Endpoints[1] = c.Endpoints[0] },
			field:  "declared twice",
		},
		"unsupported api": {
			mutate: func(c *APIContractCatalog) { c.Endpoints[0].API = "smtp" },
			field:  "api",
		},
		"invalid kind": {
			mutate: func(c *APIContractCatalog) { c.Endpoints[0].Kind = "grpc" },
			field:  "kind",
		},
		"path traversal": {
			mutate: func(c *APIContractCatalog) { c.Endpoints[0].Path = "../x" },
			field:  "path",
		},
		"digest without prefix": {
			mutate: func(c *APIContractCatalog) { c.Endpoints[0].Digest = strings.Repeat("a", 64) },
			field:  "digest",
		},
		"protobuf without services": {
			mutate: func(c *APIContractCatalog) { c.Endpoints[0].Services = nil },
			field:  "service",
		},
		"procedure not matching full name": {
			mutate: func(c *APIContractCatalog) {
				c.Endpoints[0].Services[0].Procedures = []string{"/wrong/Method"}
			},
			field: "procedure",
		},
		"openapi without routes": {
			mutate: func(c *APIContractCatalog) { c.Endpoints[1].Routes = nil },
			field:  "route",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			catalog := sampleCatalog()
			tc.mutate(catalog)
			err := catalog.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.field)
		})
	}
}

func TestValidatePackageAPIContracts(t *testing.T) {
	descriptor := []byte("descriptor-set-bytes")
	digest := APIContractDigest(descriptor)
	contractPath := "contracts/api/accounts/connect/descriptor.binpb"

	setup := func(t *testing.T) (string, *PackageManifest, *APIContractCatalog) {
		t.Helper()
		moduleDir := t.TempDir()
		writeFile(t, filepath.Join(moduleDir, filepath.FromSlash(contractPath)), string(descriptor))
		manifest := &PackageManifest{
			ArtifactRoots: []string{"contracts"},
			Services: []ProvidedService{
				{
					Name:      "accounts",
					Endpoints: []string{"connect"},
					APIContracts: []ProvidedAPIContract{
						{
							Endpoint: "connect",
							Kind:     APIContractKindProtobuf,
							Package:  "saas.accounts.v1",
							Path:     contractPath,
							Digest:   digest,
						},
					},
				},
			},
		}
		catalog := &APIContractCatalog{
			Schema:  APIContractCatalogSchema,
			Package: testPackage,
			Version: "0.1.0",
			Endpoints: []APIContractEndpoint{
				{
					Service:  "accounts",
					Endpoint: "connect",
					API:      "connect",
					Kind:     APIContractKindProtobuf,
					Package:  "saas.accounts.v1",
					Path:     contractPath,
					Digest:   digest,
					Services: []APIContractService{
						{
							Name:       "AuditService",
							FullName:   "saas.accounts.v1.AuditService",
							Procedures: []string{"/saas.accounts.v1.AuditService/QueryAuditLog"},
						},
					},
				},
			},
		}
		return moduleDir, manifest, catalog
	}

	t.Run("passes", func(t *testing.T) {
		moduleDir, manifest, catalog := setup(t)
		require.NoError(t, ValidatePackageAPIContracts(moduleDir, manifest, catalog))
	})

	t.Run("digest mismatch", func(t *testing.T) {
		moduleDir, manifest, catalog := setup(t)
		writeFile(t, filepath.Join(moduleDir, filepath.FromSlash(contractPath)), "tampered")
		err := ValidatePackageAPIContracts(moduleDir, manifest, catalog)
		require.ErrorIs(t, err, ErrContract)
	})

	t.Run("path outside artifact roots", func(t *testing.T) {
		moduleDir, manifest, catalog := setup(t)
		manifest.Services[0].APIContracts[0].Path = "docs/x.binpb"
		catalog.Endpoints[0].Path = "docs/x.binpb"
		writeFile(t, filepath.Join(moduleDir, "docs", "x.binpb"), string(descriptor))
		err := ValidatePackageAPIContracts(moduleDir, manifest, catalog)
		require.ErrorIs(t, err, ErrContract)
	})

	t.Run("catalog endpoint missing from manifest", func(t *testing.T) {
		moduleDir, manifest, catalog := setup(t)
		manifest.Services[0].APIContracts = nil
		err := ValidatePackageAPIContracts(moduleDir, manifest, catalog)
		require.ErrorIs(t, err, ErrContract)
	})

	t.Run("manifest entry missing from catalog", func(t *testing.T) {
		moduleDir, manifest, catalog := setup(t)
		catalog.Endpoints = nil
		err := ValidatePackageAPIContracts(moduleDir, manifest, catalog)
		require.ErrorIs(t, err, ErrContract)
	})

	t.Run("manifest and catalog disagree", func(t *testing.T) {
		moduleDir, manifest, catalog := setup(t)
		catalog.Endpoints[0].Package = "saas.other.v1"
		err := ValidatePackageAPIContracts(moduleDir, manifest, catalog)
		require.ErrorIs(t, err, ErrContract)
	})
}

func TestProtobufServices(t *testing.T) {
	set := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{
			{
				Name:    proto.String("a.proto"),
				Package: proto.String("a.v1"),
				Service: []*descriptorpb.ServiceDescriptorProto{
					{
						Name: proto.String("S"),
						Method: []*descriptorpb.MethodDescriptorProto{
							{Name: proto.String("M2")},
							{Name: proto.String("M1")},
						},
					},
				},
			},
			{
				Name:    proto.String("b.proto"),
				Package: proto.String("b.v1"),
				Service: []*descriptorpb.ServiceDescriptorProto{
					{Name: proto.String("T")},
				},
			},
		},
	}
	services := ProtobufServices(set, "a.v1")
	require.Len(t, services, 1)
	require.Equal(t, "S", services[0].Name)
	require.Equal(t, "a.v1.S", services[0].FullName)
	require.Equal(t, []string{"/a.v1.S/M1", "/a.v1.S/M2"}, services[0].Procedures)
}

func TestPackageManifestValidateAPIContracts(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	manifest := func(endpoint string) string {
		return `kind: module-package
schema: codefly/module-package/v2
id: ` + testPackage + `
version: 0.1.0
minimum-codefly-version: ">=0.1.0"
artifact-roots:
  - contracts
services:
  - name: accounts
    endpoints:
      - connect
    api-contracts:
      - endpoint: ` + endpoint + `
        kind: protobuf
        package: saas.accounts.v1
        path: contracts/api/accounts/connect/descriptor.binpb
        digest: ` + digest + `
contracts:
  composition: ">=2.0 <3.0"
`
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "contracts", "api", "accounts", "connect", "descriptor.binpb"), "descriptor")

	writeFile(t, filepath.Join(root, PackageManifestFileName), manifest("connect"))
	_, err := LoadPackageManifest(root)
	require.NoError(t, err)

	writeFile(t, filepath.Join(root, PackageManifestFileName), manifest("missing"))
	_, err = LoadPackageManifest(root)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not declared by the service")
}

func TestAPIContractCatalogArchiveRoundTrip(t *testing.T) {
	root := newPackageRoot(t, "0.1.0")
	catalog := &APIContractCatalog{
		Schema:  APIContractCatalogSchema,
		Package: testPackage,
		Version: "0.1.0",
		Endpoints: []APIContractEndpoint{
			{
				Service:  "frontend",
				Endpoint: "http",
				API:      "http",
				Kind:     APIContractKindOpenAPI,
				Package:  "Frontend API",
				Path:     "contracts/api/frontend/http/openapi.json",
				Digest:   APIContractDigest([]byte("openapi")),
				Routes:   []APIContractRoute{{Method: "GET", Path: "/"}},
			},
		},
	}
	data, err := catalog.CanonicalBytes()
	require.NoError(t, err)
	writeFile(t, filepath.Join(root, filepath.FromSlash(APIContractCatalogFileName)), string(data))

	archive, _, err := CanonicalArchive(root)
	require.NoError(t, err)
	extracted := t.TempDir()
	require.NoError(t, ExtractArchive(context.Background(), archive, extracted))

	loaded, err := LoadAPIContractCatalog(extracted)
	require.NoError(t, err)
	require.Equal(t, catalog, loaded)
}
