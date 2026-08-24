package resources

import "testing"

// A real cell-contract/v1 document, as emitted by `obinctl cell-contract
// hosted-eastus2-dev`. The egress-CIDR is the load-bearing field: getting it
// wrong silently drops all DB traffic, which is why the contract exists.
const devCellContract = `{
  "schema": "obin-infra/cell-contract/v1",
  "cell": "hosted-eastus2-dev",
  "coordinate": "obin/azure/eastus2/US/staging",
  "cloud": "azure",
  "location": "eastus2",
  "cluster": { "kind": "aks", "name": "obinh-eus2-dev-aks", "context": "obinh-eus2-dev-aks", "private_api": true, "access_mode": "tailscale" },
  "registry": { "url": "obinheus2acr.azurecr.io", "auth": "acr" },
  "namespace_prefix": "obinh-eus2-dev",
  "secret_store": { "name": "azure-keyvault", "kind": "ClusterSecretStore" },
  "dns": { "registrar_zone": "obin.ai", "app_host_suffix": "staging.eastus2.azure.obin.obin.ai" },
  "gitops": { "repo": "https://github.com/obin-ai/obin-fleet.git", "workloads_path_prefix": "workloads/hosted/staging" },
  "postgres": { "kind": "azure-postgres-flexible", "fqdn": "obinh-eus2-dev-shared.postgres.database.azure.com", "egress_cidrs": ["10.21.11.0/28"], "databases": ["unleash"] },
  "object_storage": { "kind": "s3", "backend": "in-cluster-minio" }
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
	if env.Cluster == nil || env.Cluster.Context != "obinh-eus2-dev-aks" {
		t.Errorf("cluster context = %+v", env.Cluster)
	}
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
	if ms.ExternalName != "obinh-eus2-dev-shared.postgres.database.azure.com" {
		t.Errorf("postgres external-name = %q", ms.ExternalName)
	}
	// The silent-failure fact, now sourced from the cell instead of hand-typed.
	if len(ms.EgressCIDRs) != 1 || ms.EgressCIDRs[0] != "10.21.11.0/28" {
		t.Errorf("postgres egress CIDRs = %v (want [10.21.11.0/28])", ms.EgressCIDRs)
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
	doc := `{"schema":"obin-infra/cell-contract/v1","cell":"x","cluster":{}}`
	if _, err := ParseCellContract([]byte(doc)); err == nil {
		t.Fatal("expected a missing-cluster-context error")
	}
}
