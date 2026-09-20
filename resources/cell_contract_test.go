package resources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// loadCellFixture reads a real producer descriptor from testdata. The fixtures
// are what `obinctl cell-contract <coordinate>` emits (infra-base is one
// producer; the schema is codefly's), so the tests exercise the same bytes a
// deployment parses rather than a shape restated in Go.
func loadCellFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "cells", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func TestParseAndMapCellContract(t *testing.T) {
	c, err := ParseCellContract(loadCellFixture(t, "password-auth.json"))
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
	c, err := ParseCellContract(loadCellFixture(t, "password-auth.json"))
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
	c, err := ParseCellContract(loadCellFixture(t, "password-auth.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, ns := range []string{"", "..", "../etc", "a/b"} {
		if _, err := c.ToEnvironment("azure", ns); err == nil {
			t.Errorf("namespace %q: expected a path-component rejection", ns)
		}
	}
}

// A cell whose database takes no password must carry the transport and identity
// through to the environment: the endpoint and its port, the proxy the pod runs,
// and the exact principal the workload authenticates as. None of it is derived
// from the customer, account or region names in the coordinate.
func TestParseAndMapManagedIdentityCell(t *testing.T) {
	c, err := ParseCellContract(loadCellFixture(t, "managed-identity.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	env, err := c.ToEnvironment("staging", "lodestar")
	if err != nil {
		t.Fatalf("to environment: %v", err)
	}

	ms, ok := env.ManagedServices["store"]
	if !ok {
		t.Fatalf("no managed store service; got %+v", env.ManagedServices)
	}
	if ms.ExternalName != "10.20.11.7" || ms.Port != 5432 {
		t.Errorf("endpoint = %q:%d", ms.ExternalName, ms.Port)
	}
	if ms.Transport == nil || ms.Transport.Mode != TransportModeProxy || ms.Transport.LocalPort != 5432 {
		t.Fatalf("transport = %+v", ms.Transport)
	}
	if ms.Transport.Image != "us-central1-docker.pkg.dev/obinh-usc1/images/db-proxy:1.33.9" {
		t.Errorf("transport image = %q", ms.Transport.Image)
	}
	if len(ms.Transport.Args) != 3 {
		t.Errorf("transport args = %v", ms.Transport.Args)
	}
	if ms.Identity == nil || ms.Identity.Principal != "platform-db@obinh-usc1.iam.gserviceaccount.com" {
		t.Fatalf("identity = %+v", ms.Identity)
	}
	// The attachment keys are the platform's, carried verbatim — codefly never
	// interprets them, which is what keeps it free of per-platform branches.
	if got := ms.Identity.Annotations["iam.gke.io/gcp-service-account"]; got != ms.Identity.Principal {
		t.Errorf("identity annotation = %q", got)
	}
	if got := ms.Identity.Labels["obin.ai/workload-identity"]; got != "true" {
		t.Errorf("identity label = %q", got)
	}
	// A passwordless instance has no secret to project: an ExternalSecret against
	// a key the cell never writes would leave the pod blocked on a Secret that
	// never materializes.
	if len(ms.SecretReferences) != 0 {
		t.Errorf("secret references = %+v (want none for a passwordless instance)", ms.SecretReferences)
	}
	// The app's own secret store is still declared; only the database handoff drops.
	if env.ServiceSecrets == nil || env.ServiceSecrets.SecretStore.Name != "gcp-secret-manager" {
		t.Errorf("service-secrets store = %+v", env.ServiceSecrets)
	}

	if len(env.AuditSinks) != 1 {
		t.Fatalf("audit sinks = %+v", env.AuditSinks)
	}
	sink := env.AuditSinks[0]
	if sink.Target != "obinh-usc1:audit_us" || sink.Kind != "bigquery-dataset" {
		t.Errorf("audit sink destination = %+v", sink)
	}
	if sink.Writer == nil || sink.Writer.Principal != "audit-writer@obinh-usc1.iam.gserviceaccount.com" {
		t.Errorf("audit writer = %+v", sink.Writer)
	}
	if sink.Residency != "US" {
		t.Errorf("audit residency = %q", sink.Residency)
	}
	if sink.Retention == nil || sink.Retention.Days != 400 || !sink.Retention.Locked {
		t.Errorf("audit retention = %+v", sink.Retention)
	}
	// The delivery target is the one the producer declared, not a default.
	if env.Gitops == nil || env.Gitops.RepoURL != "https://github.com/obin-ai/infra-base.git" {
		t.Fatalf("delivery repo = %+v", env.Gitops)
	}
	if env.Gitops.Path != "delivery/hosted/staging/lodestar" {
		t.Errorf("delivery path = %q", env.Gitops.Path)
	}
}

// The environment is written to workspace.codefly.yaml and read back by a later
// command, so a transport or audit fact that does not survive that round-trip is
// one the renderer never sees.
func TestEnvironmentSerializationPreservesTransportAndAudit(t *testing.T) {
	c, err := ParseCellContract(loadCellFixture(t, "managed-identity.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	env, err := c.ToEnvironment("staging", "lodestar")
	if err != nil {
		t.Fatalf("to environment: %v", err)
	}
	serialized, err := yaml.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var reloaded Environment
	if err = yaml.Unmarshal(serialized, &reloaded); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, serialized)
	}

	ms, ok := reloaded.ManagedServices["store"]
	if !ok {
		t.Fatalf("no managed store service after round-trip:\n%s", serialized)
	}
	if ms.Port != 5432 {
		t.Errorf("port = %d", ms.Port)
	}
	if ms.Transport == nil || ms.Transport.Mode != TransportModeProxy || ms.Transport.Image == "" || ms.Transport.LocalPort != 5432 {
		t.Errorf("transport = %+v", ms.Transport)
	}
	if ms.Identity == nil || ms.Identity.Principal != "platform-db@obinh-usc1.iam.gserviceaccount.com" {
		t.Fatalf("identity = %+v", ms.Identity)
	}
	if ms.Identity.Annotations["iam.gke.io/gcp-service-account"] == "" || ms.Identity.Labels["obin.ai/workload-identity"] == "" {
		t.Errorf("identity attachment = %+v", ms.Identity)
	}
	if len(reloaded.AuditSinks) != 1 {
		t.Fatalf("audit sinks = %+v", reloaded.AuditSinks)
	}
	sink := reloaded.AuditSinks[0]
	if sink.Target != "obinh-usc1:audit_us" || sink.Writer == nil || sink.Writer.Principal == "" {
		t.Errorf("audit sink = %+v", sink)
	}
	if sink.Retention == nil || sink.Retention.Days != 400 || !sink.Retention.Locked {
		t.Errorf("audit retention = %+v", sink.Retention)
	}
}

// An unknown JSON field decodes to nothing, so a descriptor that needs a
// behaviour this binary predates must say so and be refused — otherwise the
// deployment comes up missing exactly the part that makes it work.
func TestRefusesUnimplementedCapability(t *testing.T) {
	doc := `{"schema":"codefly/cell/v1","cell":"x","cluster":{"context":"c"},"registries":[{"url":"a"}],"requires_capabilities":["managed-identity-transport","quantum-transport"]}`
	_, err := ParseCellContract([]byte(doc))
	if err == nil {
		t.Fatal("expected a capability rejection")
	}
	if !strings.Contains(err.Error(), "quantum-transport") {
		t.Errorf("error does not name the capability: %v", err)
	}
}

// Every case here would otherwise render and start: a workload with no principal
// to authenticate as, a proxy with no image to run, or a connection with no port
// to dial. They are refused at the seam rather than deployed.
func TestRefusesIncompleteTransportBinding(t *testing.T) {
	const prefix = `{"schema":"codefly/cell/v1","cell":"x","cluster":{"context":"c"},"registries":[{"url":"a"}],"databases":[{"name":"a","egress_cidrs":["10.0.0.0/28"],`
	docs := map[string]string{
		"passwordless without identity": prefix + `"password_auth":false,"port":5432}]}`,
		"identity without principal":    prefix + `"password_auth":false,"port":5432,"identity":{"kind":"k"}}]}`,
		"transport without port":        prefix + `"password_auth":true,"transport":{"mode":"direct"}}]}`,
		"proxy without image":           prefix + `"password_auth":true,"port":5432,"transport":{"mode":"proxy","local_port":5432}}]}`,
		"proxy without local port":      prefix + `"password_auth":true,"port":5432,"transport":{"mode":"proxy","image":"i"}}]}`,
		"unimplemented mode":            prefix + `"password_auth":true,"port":5432,"transport":{"mode":"carrier-pigeon"}}]}`,
		"out of range port":             prefix + `"password_auth":true,"port":70000}]}`,
	}
	for name, doc := range docs {
		if _, err := ParseCellContract([]byte(doc)); err == nil {
			t.Errorf("%s: expected a transport-binding rejection", name)
		}
	}
}

// A sink codefly could only complete by inventing a destination or a writer is
// refused; codefly never synthesizes either from the parts it has.
func TestRefusesIncompleteAuditSink(t *testing.T) {
	const prefix = `{"schema":"codefly/cell/v1","cell":"x","cluster":{"context":"c"},"registries":[{"url":"a"}],"audit_sinks":[{`
	docs := map[string]string{
		"no name":             prefix + `"kind":"k","target":"t","writer":{"principal":"p"}}]}`,
		"no kind":             prefix + `"name":"audit","target":"t","writer":{"principal":"p"}}]}`,
		"no target":           prefix + `"name":"audit","kind":"k","writer":{"principal":"p"}}]}`,
		"no writer":           prefix + `"name":"audit","kind":"k","target":"t"}]}`,
		"writer no principal": prefix + `"name":"audit","kind":"k","target":"t","writer":{"kind":"k"}}]}`,
		"unusable retention":  prefix + `"name":"audit","kind":"k","target":"t","writer":{"principal":"p"},"retention":{"days":0}}]}`,
	}
	for name, doc := range docs {
		if _, err := ParseCellContract([]byte(doc)); err == nil {
			t.Errorf("%s: expected an audit-sink rejection", name)
		}
	}
}

// A retention lock is an approval fact only the producer holds, so an unlocked
// retention stays unlocked through the mapping.
func TestDoesNotInferRetentionLock(t *testing.T) {
	doc := `{"schema":"codefly/cell/v1","cell":"x","cluster":{"context":"c"},"registries":[{"url":"a"}],"audit_sinks":[{"name":"audit","kind":"k","target":"t","writer":{"principal":"p"},"retention":{"days":30}}]}`
	c, err := ParseCellContract([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	env, err := c.ToEnvironment("staging", "lodestar")
	if err != nil {
		t.Fatalf("to environment: %v", err)
	}
	if len(env.AuditSinks) != 1 || env.AuditSinks[0].Retention == nil {
		t.Fatalf("audit sinks = %+v", env.AuditSinks)
	}
	if env.AuditSinks[0].Retention.Locked {
		t.Error("an unapplied retention lock must not be reported as locked")
	}
}

// Half a delivery target leaves the rendered workloads pointing at an empty repo
// or the cell's root path — reconciled somewhere other than where the owner
// decided they go.
func TestRefusesHalfDeclaredDeliveryTarget(t *testing.T) {
	const prefix = `{"schema":"codefly/cell/v1","cell":"x","cluster":{"context":"c"},"registries":[{"url":"a"}],"gitops":{`
	docs := map[string]string{
		"no repo": prefix + `"workloads_path_prefix":"delivery/x"}}`,
		"no path": prefix + `"repo":"https://example.com/x.git"}}`,
	}
	for name, doc := range docs {
		if _, err := ParseCellContract([]byte(doc)); err == nil {
			t.Errorf("%s: expected a delivery-target rejection", name)
		}
	}
}

// A proxy transport terminates the authenticated connection in the pod, so the
// application dials loopback and the endpoint address never reaches it.
func TestDialHost(t *testing.T) {
	direct := EnvironmentManagedService{ExternalName: "db.example.com", Port: 5432}
	if host, port := direct.DialHost(); host != "db.example.com" || port != 5432 {
		t.Errorf("direct dial = %s:%d", host, port)
	}
	proxied := EnvironmentManagedService{
		ExternalName: "10.20.11.7",
		Port:         5432,
		Transport:    &EnvironmentManagedTransport{Mode: TransportModeProxy, Image: "i", LocalPort: 6543},
	}
	if host, port := proxied.DialHost(); host != "127.0.0.1" || port != 6543 {
		t.Errorf("proxied dial = %s:%d", host, port)
	}
}
