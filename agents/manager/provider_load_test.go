package manager

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/provider/artifact"
	"github.com/codefly-dev/core/resources"
)

func TestProviderBuildInstallResolveAndLoad(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	agent := providerFixtureAgent()
	layout := buildProviderFixture(t)
	target, err := agent.Path(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := artifact.InstallLayout(layout, target, agent); err != nil {
		t.Fatal(err)
	}

	conn, err := Load(
		context.Background(),
		agent,
		WithoutSandbox(),
		WithoutPrincipal(),
		WithStartupTimeout(10*time.Second),
		WithDialTimeout(10*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if conn.ProcessInfo() == nil || conn.ProcessInfo().PID <= 0 {
		t.Fatalf("provider process info is invalid: %+v", conn.ProcessInfo())
	}
	conn.Close()
}

func TestProviderLoadRejectsUnverifiedAndTamperedExecutable(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	agent := providerFixtureAgent()
	layout := buildProviderFixture(t)
	target, err := agent.Path(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(filepath.Join(layout, "provider-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = Load(context.Background(), agent, WithoutSandbox(), WithoutPrincipal())
	if err == nil || !artifactErrorContains(err, "descriptor") {
		t.Fatalf("absent artifact descriptor was not rejected: %v", err)
	}

	if _, err := artifact.InstallLayout(layout, target, agent); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, append(binary, byte('\n')), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = Load(context.Background(), agent, WithoutSandbox(), WithoutPrincipal())
	if err == nil || !artifactErrorContains(err, "binary digest mismatch") {
		t.Fatalf("tampered provider binary was not rejected: %v", err)
	}
}

func buildProviderFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	binaryPath := filepath.Join(root, "provider-fixture")
	command := exec.Command("go", "build", "-o", binaryPath, "./testdata/provideragent")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build provider fixture: %v\n%s", err, output)
	}
	manifestBytes := []byte(providerFixtureManifest)
	if err := os.WriteFile(filepath.Join(root, "provider.codefly.yaml"), manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := artifact.BuildDescriptor("provider-fixture", binary, manifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	descriptorBytes, err := artifact.MarshalDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, artifact.DescriptorFileName), descriptorBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func providerFixtureAgent() *resources.Agent {
	return &resources.Agent{
		Kind: resources.ProviderAgent, Publisher: "codefly.dev", Name: "fixture", Version: "1.2.3",
	}
}

func artifactErrorContains(err error, text string) bool {
	return err != nil && (artifact.IsIntegrityError(err) || strings.Contains(err.Error(), text))
}

const providerFixtureManifest = `
schema_version: codefly.provider-manifest/v0
protocol_version: codefly.provider/v0
state_schema_versions: [1]
agent:
  kind: codefly:provider
  publisher: codefly.dev
  name: fixture
  version: 1.2.3
default_deletion_policy: retain
permissions:
  required:
    - action: account.observe
      resource: account
      resource_type: account
      reason: Observe the bound account.
      risk: low
      credential_purpose: management
resource_types:
  - id: account
    actions: [observe]
requests:
  - id: account.observe
    resource_type: account
    action: observe
    origin_rule: api
    operation: observe
    method: GET
    path_template: /v1/accounts/{account_id}
    remote_id_parameters: [account_id]
    request_byte_budget: 4096
    response_byte_budget: 4096
    read_only: true
    response_schema: account
    credential_purposes: [management]
origin_rules:
  - id: api
    defaults: [https://api.example.com]
    schemes: [https]
    host_patterns: [api.example.com]
    ports: [443]
    binding_override: deny
    private_network_classes: [public]
credential_purposes:
  - id: management
    minimum_scope: Read the bound account.
    permitted_consumer: management
response_schemas:
  - id: account
    fields:
      - selector: {version: v1, path: "$.id"}
        disposition: FORWARD_SAFE
projections: []
sandbox:
  network: deny
state:
  schema_versions: [1]
  import_identity: false
  replace: false
  delete: false
  stepwise_upgrade: true
diagnostic_namespace: provider.fixture.
`
