package resources

import "testing"

// A real codefly/cell/v1 descriptor, as emitted by `obinctl cell-contract
// hosted-eastus2` (infra-base is one producer; the schema is codefly's). Resource
// collections are lists; kinds are open strings. The egress-CIDR is the
// load-bearing field: getting it wrong silently drops all DB traffic, which is why
// the descriptor exists. The gitops block is an optional producer extra.
const devCellContract = `{
  "schema": "codefly/cell/v1",
  "cell": "hosted-eastus2",
  "coordinate": "hosted/azure/eastus2/US/staging",
  "cluster": { "kind": "aks", "context": "obinh-eus2-aks", "private_api": true },
  "dns": { "app_host_suffix": "staging.eastus2.azure.obin.obin.ai", "public_ip": "20.1.2.3", "internal_ip": "10.0.0.4" },
  "registries": [ { "kind": "acr", "url": "obinheus2acr.azurecr.io", "public": false } ],
  "databases": [ { "engine": "postgres", "kind": "azure-postgres-flexible", "name": "platform", "fqdn": "obinh-eus2-platform.postgres.database.azure.com", "egress_cidrs": ["10.20.11.0/28"], "database_names": ["unleash", "users"], "password_auth": true } ],
  "secret_stores": [ { "kind": "ClusterSecretStore", "name": "azure-keyvault" } ],
  "gitops": { "repo": "https://github.com/obin-ai/obin-fleet.git", "workloads_path_prefix": "workloads/hosted/staging" }
}`

func TestParseAndMapCellContract(t *testing.T) {
	c, err := ParseCellContract([]byte(devCellContract))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	env := c.ToEnvironment("azure", "lodestar")

	if env.Name != "azure" || env.Namespace != "lodestar" {
		t.Errorf("name/namespace = %q/%q", env.Name, env.Namespace)
	}
	if env.Cluster == nil || env.Cluster.Context != "obinh-eus2-aks" {
		t.Errorf("cluster context = %+v", env.Cluster)
	}
	// Registry Auth is sourced from the (opaque) registry kind: acr -> az acr login.
	if env.Registry == nil || env.Registry.URL != "obinheus2acr.azurecr.io" || env.Registry.Auth != "acr" {
		t.Errorf("registry = %+v", env.Registry)
	}
	if env.ServiceSecrets == nil || env.ServiceSecrets.SecretStore.Name != "azure-keyvault" {
		t.Errorf("service-secrets store = %+v", env.ServiceSecrets)
	}
	if env.Gitops == nil || env.Gitops.Path != "workloads/hosted/staging/lodestar" {
		t.Errorf("gitops path = %+v", env.Gitops)
	}

	ms, ok := env.ManagedServices["store"]
	if !ok {
		t.Fatalf("no managed store service; got %+v", env.ManagedServices)
	}
	if ms.ExternalName != "obinh-eus2-platform.postgres.database.azure.com" {
		t.Errorf("database external-name = %q", ms.ExternalName)
	}
	// The silent-failure fact, now sourced from the cell instead of hand-typed.
	if len(ms.EgressCIDRs) != 1 || ms.EgressCIDRs[0] != "10.20.11.0/28" {
		t.Errorf("database egress CIDRs = %v (want [10.20.11.0/28])", ms.EgressCIDRs)
	}
	if len(ms.SecretReferences) != 1 || ms.SecretReferences[0].SecretStore.Kind != "ClusterSecretStore" {
		t.Errorf("secret references = %+v", ms.SecretReferences)
	}
}

func TestRejectsUnknownSchema(t *testing.T) {
	if _, err := ParseCellContract([]byte(`{"schema":"nope/v9"}`)); err == nil {
		t.Fatal("expected an unsupported-schema error")
	}
}

func TestRequiresClusterContext(t *testing.T) {
	doc := `{"schema":"codefly/cell/v1","cell":"x","cluster":{}}`
	if _, err := ParseCellContract([]byte(doc)); err == nil {
		t.Fatal("expected a missing-cluster-context error")
	}
}
