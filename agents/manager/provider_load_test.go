package manager

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/codefly-dev/core/provider/artifact"
	"github.com/codefly-dev/core/resources"
	runnersbase "github.com/codefly-dev/core/runners/base"
)

const providerProcessGroupLifecycleHelperEnv = "CODEFLY_PROVIDER_PROCESS_GROUP_LIFECYCLE_HELPER"

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

func TestProviderProcessGroupTeardownAcrossTwoLifecycles(t *testing.T) {
	codeflyHome := t.TempDir()
	processHome := t.TempDir()
	t.Setenv(resources.CodeflyHomeEnv, codeflyHome)
	agent := providerFixtureAgent()
	layout := buildProviderFixture(t)
	target, err := agent.Path(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := artifact.InstallLayout(layout, target, agent); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", processHome)

	for lifecycle := 1; lifecycle <= 2; lifecycle++ {
		assertNoProcessGroupRecords(t, processHome)
		if err := runnersbase.ReapStaleProcessGroups(context.Background()); err != nil {
			t.Fatalf("lifecycle %d startup reap: %v", lifecycle, err)
		}
		assertNoProcessGroupRecords(t, processHome)

		runtimeDir := t.TempDir()
		pidFile := filepath.Join(runtimeDir, "pid")
		stoppedFile := filepath.Join(runtimeDir, "stopped")
		command := exec.Command(os.Args[0], "-test.run=^TestProviderProcessGroupLifecycleHelper$")
		command.Env = append(os.Environ(),
			providerProcessGroupLifecycleHelperEnv+"=1",
			"CODEFLY_PROVIDER_FIXTURE_PROCESS_GROUP_PID_FILE="+pidFile,
			"CODEFLY_PROVIDER_FIXTURE_PROCESS_GROUP_STOPPED_FILE="+stoppedFile,
		)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("lifecycle %d: %v\n%s", lifecycle, err, output)
		}
		pidBytes, err := os.ReadFile(pidFile)
		if err != nil {
			t.Fatalf("lifecycle %d read descendant pid: %v", lifecycle, err)
		}
		pid, err := strconv.Atoi(string(pidBytes))
		if err != nil {
			t.Fatalf("lifecycle %d parse descendant pid: %v", lifecycle, err)
		}
		defer syscall.Kill(pid, syscall.SIGKILL)
		if _, err := os.Stat(stoppedFile); err != nil {
			t.Fatalf("lifecycle %d descendant was not terminated: %v", lifecycle, err)
		}
		assertProcessExited(t, pid)
		assertNoProcessGroupRecords(t, processHome)
	}
}

func TestProviderProcessGroupLifecycleHelper(t *testing.T) {
	if os.Getenv(providerProcessGroupLifecycleHelperEnv) == "" {
		t.Skip("subprocess helper")
	}
	conn, err := Load(context.Background(), providerFixtureAgent(), WithoutSandbox(), WithoutPrincipal())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func assertProcessExited(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d is still alive", pid)
}

func assertNoProcessGroupRecords(t *testing.T, processHome string) {
	t.Helper()
	var records []string
	err := filepath.WalkDir(filepath.Join(processHome, ".codefly", "runs"), func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return filepath.SkipDir
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".pgid") {
			records = append(records, path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("process-group records remain after lifecycle: %v", records)
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
    - id: account-observe
      action: account.observe
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
    permissions: [account-observe]
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
