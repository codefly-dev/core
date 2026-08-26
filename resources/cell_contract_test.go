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
	env, err := c.ToEnvironment("azure", "lodestar")
	if err != nil {
		t.Fatalf("to environment: %v", err)
	}

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
	// The app host suffix is carried onto the environment so the network layer
	// can derive external endpoint hosts from declared config, not a local file.
	if env.Dns == nil || env.Dns.AppHostSuffix != "staging.eastus2.azure.obin.obin.ai" {
		t.Errorf("dns = %+v", env.Dns)
	}
	if got := env.AppHost(&ServiceIdentity{Module: "users", Name: "accounts"}); got != "accounts-users.staging.eastus2.azure.obin.obin.ai" {
		t.Errorf("app host = %q", got)
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
	// Pin the secret-handoff conventions so they can't drift silently.
	if len(ms.SecretReferences) != 1 {
		t.Fatalf("secret references = %+v", ms.SecretReferences)
	}
	ref := ms.SecretReferences[0]
	if ref.Name != "secret-store" || ref.RemoteKey != "lodestar/store" || ref.SecretStore.Kind != "ClusterSecretStore" {
		t.Errorf("secret reference = %+v", ref)
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

// A deploy target needs exactly one registry to push to; zero would silently fall
// back to the legacy hard-coded registry (wrong for a BYOC cell).
func TestRequiresExactlyOneRegistry(t *testing.T) {
	docs := map[string]string{
		"zero": `{"schema":"codefly/cell/v1","cell":"x","cluster":{"context":"c"}}`,
		"two":  `{"schema":"codefly/cell/v1","cell":"x","cluster":{"context":"c"},"registries":[{"url":"a"},{"url":"b"}]}`,
	}
	for name, doc := range docs {
		if _, err := ParseCellContract([]byte(doc)); err == nil {
			t.Errorf("%s registries: expected a registry-count rejection", name)
		}
	}
}

// A cell may carry several databases/secret stores, but this consumer maps a
// single instance of each into Environment's single slots. Parsing must reject the
// multi-instance case loudly rather than silently mapping [0] and dropping the rest
// (a dropped database = its egress CIDRs never applied = silent DB outage). Each
// doc carries the one required registry so the assertion isolates its target.
func TestRejectsMultipleInstances(t *testing.T) {
	const reg = `"registries":[{"url":"a"}],`
	docs := map[string]string{
		"databases":     `{"schema":"codefly/cell/v1","cell":"x","cluster":{"context":"c"},` + reg + `"databases":[{"name":"a","egress_cidrs":["10.0.0.0/28"]},{"name":"b","egress_cidrs":["10.0.1.0/28"]}]}`,
		"secret_stores": `{"schema":"codefly/cell/v1","cell":"x","cluster":{"context":"c"},` + reg + `"secret_stores":[{"name":"a"},{"name":"b"}]}`,
	}
	for name, doc := range docs {
		if _, err := ParseCellContract([]byte(doc)); err == nil {
			t.Errorf("%s: expected a multiple-instance rejection", name)
		}
	}
}

// The egress CIDRs gate all DB traffic; an empty, missing, or malformed value
// silently drops it at runtime, so parsing must reject rather than pass it through.
func TestRejectsBadEgressCIDRs(t *testing.T) {
	const reg = `"registries":[{"url":"a"}],`
	docs := map[string]string{
		"empty":   `{"schema":"codefly/cell/v1","cell":"x","cluster":{"context":"c"},` + reg + `"databases":[{"name":"a","egress_cidrs":[]}]}`,
		"missing": `{"schema":"codefly/cell/v1","cell":"x","cluster":{"context":"c"},` + reg + `"databases":[{"name":"a"}]}`,
		"invalid": `{"schema":"codefly/cell/v1","cell":"x","cluster":{"context":"c"},` + reg + `"databases":[{"name":"a","egress_cidrs":["10.0.0/28"]}]}`,
	}
	for name, doc := range docs {
		if _, err := ParseCellContract([]byte(doc)); err == nil {
			t.Errorf("%s: expected an egress-CIDR rejection", name)
		}
	}
}

// The connectable database name is app-supplied; the cell publishes what exists so
// a caller can reject a typo at config time instead of at runtime.
func TestKnowsDatabase(t *testing.T) {
	c, err := ParseCellContract([]byte(devCellContract))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !c.KnowsDatabase("unleash") || !c.KnowsDatabase("users") {
		t.Error("expected the cell to know its published databases")
	}
	if c.KnowsDatabase("nope") {
		t.Error("expected an unknown database to be rejected")
	}
}

// The namespace becomes a gitops path component and a secret remote-key segment,
// so ToEnvironment must reject one that is not a single safe path component
// instead of building a traversing path.
func TestRejectsBadNamespace(t *testing.T) {
	c, err := ParseCellContract([]byte(devCellContract))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, ns := range []string{"", "..", "../etc", "a/b"} {
		if _, err := c.ToEnvironment("azure", ns); err == nil {
			t.Errorf("namespace %q: expected a path-component rejection", ns)
		}
	}
}
