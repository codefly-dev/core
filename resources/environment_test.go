package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
)

// writeServiceSecretsWorkspace lays down a modules-layout workspace with a single
// module "saas" owning service "accounts", plus an environment whose
// service-secrets names serviceName. It is the real graph ValidateEnvironments
// cross-checks against.
func writeServiceSecretsWorkspace(t *testing.T, serviceName string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		resources.WorkspaceConfigurationName: `name: platform
layout: modules
modules:
  - name: saas
environments:
  - name: prod
    namespace: platform
    service-secrets:
      secret-store:
        name: azure-keyvault-prod
        kind: ClusterSecretStore
      services:
        ` + serviceName + `:
          remote-keys:
            workos-client-secret: workos/prod/client-secret
`,
		filepath.Join("modules", "saas", resources.ModuleConfigurationName): `kind: module
name: saas
services:
    - name: accounts
`,
		filepath.Join("modules", "saas", "services", "accounts", resources.ServiceConfigurationName): `kind: service
name: accounts
version: 0.0.0
agent:
  kind: runtime::service
  name: go-grpc
  version: 0.0.1
  publisher: codefly.ai
`,
	}
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// A service-secrets override that names a real service passes the graph check.
func TestValidateEnvironmentsAllowsKnownService(t *testing.T) {
	ctx := context.Background()
	root := writeServiceSecretsWorkspace(t, "accounts")
	ws, err := resources.LoadWorkspaceFromDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.ValidateEnvironments(ctx); err != nil {
		t.Fatalf("ValidateEnvironments = %v, want nil", err)
	}
}

// A typo'd service name (here "accunts") would silently drop the override at
// projection time; the graph check must reject it instead.
func TestValidateEnvironmentsRejectsUnknownService(t *testing.T) {
	ctx := context.Background()
	root := writeServiceSecretsWorkspace(t, "accunts")
	ws, err := resources.LoadWorkspaceFromDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	err = ws.ValidateEnvironments(ctx)
	if err == nil {
		t.Fatal("expected ValidateEnvironments to reject unknown service")
	}
	if !strings.Contains(err.Error(), "accunts") {
		t.Fatalf("error = %v, want it to mention the unknown service", err)
	}
}

// An empty list item under environments unmarshals to a nil *Environment;
// postLoad must reject it with an error instead of dereferencing it into a panic.
func TestLoadWorkspaceRejectsEmptyEnvironmentEntry(t *testing.T) {
	root := t.TempDir()
	workspace := `name: platform
layout: flat
environments:
  - name: prod
    namespace: platform
  -
`
	if err := os.WriteFile(filepath.Join(root, resources.WorkspaceConfigurationName), []byte(workspace), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := resources.LoadWorkspaceFromDir(context.Background(), root)
	if err == nil {
		t.Fatal("expected an error for an empty environment entry")
	}
	if !strings.Contains(err.Error(), "empty environment") {
		t.Fatalf("error = %v, want it to name the empty environment entry", err)
	}
}

func TestEnvironmentGitopsTopologyLoadsFromWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := `name: platform
layout: modules
environments:
  - name: aws
    namespace: platform
    cluster:
      kind: eks
    registry:
      url: registry.example.com/platform
      auth: ecr
    gitops:
      repo-url: https://github.com/acme/platform.git
      path: clusters/aws
      branch: production
      revision: release-v1
      checkout: .
      inventory: clusters/aws/deployments/modules/users/.codefly-render.json
    ingress:
      - name: edge
        service: gateway
        endpoint: https
        hosts:
          - api.example.com
    managed-services:
      store:
        kind: rds-postgresql
        external-name: store.internal.example.com
        egress-cidrs:
          - 10.12.0.0/16
        secret-references:
          - name: store-secrets
            remote-key: platform/users/store
            secret-store:
              name: aws-secrets-manager
              kind: ClusterSecretStore
`
	if err := os.WriteFile(filepath.Join(root, resources.WorkspaceConfigurationName), []byte(workspace), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := resources.LoadWorkspaceFromDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Environments) != 1 {
		t.Fatalf("environments = %d, want 1", len(loaded.Environments))
	}
	environment := loaded.Environments[0]
	if environment.Gitops == nil || environment.Gitops.Revision != "release-v1" {
		t.Fatalf("gitops = %+v", environment.Gitops)
	}
	if len(environment.Ingress) != 1 || environment.Ingress[0].Hosts[0] != "api.example.com" {
		t.Fatalf("ingress = %+v", environment.Ingress)
	}
	store, ok := environment.ManagedServices["store"]
	if !ok || store.Kind != "rds-postgresql" || len(store.SecretReferences) != 1 {
		t.Fatalf("managed store = %+v, present = %t", store, ok)
	}
}

func TestEnvironmentServiceSecretsLoadFromWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := `name: platform
layout: modules
environments:
  - name: prod
    namespace: platform
    cluster:
      kind: aks
    service-secrets:
      secret-store:
        name: azure-keyvault-prod
        kind: ClusterSecretStore
      services:
        accounts:
          remote-keys:
            workos-client-secret: workos/prod/client-secret
        billing:
          secret-store:
            name: aws-secrets-billing
            kind: SecretStore
          remote-keys:
            stripe-key: stripe/prod/key
`
	if err := os.WriteFile(filepath.Join(root, resources.WorkspaceConfigurationName), []byte(workspace), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := resources.LoadWorkspaceFromDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Environments) != 1 {
		t.Fatalf("environments = %d, want 1", len(loaded.Environments))
	}
	secrets := loaded.Environments[0].ServiceSecrets
	if secrets == nil {
		t.Fatal("service-secrets did not load")
	}
	if secrets.SecretStore.Name != "azure-keyvault-prod" || secrets.SecretStore.Kind != "ClusterSecretStore" {
		t.Fatalf("service secret store = %+v", secrets.SecretStore)
	}
	if got := secrets.Services["accounts"].RemoteKeys["workos-client-secret"]; got != "workos/prod/client-secret" {
		t.Fatalf("accounts remote key override = %q", got)
	}
	// accounts inherits the environment-wide store.
	if secrets.Services["accounts"].SecretStore != nil {
		t.Fatalf("accounts secret store override = %+v, want nil", secrets.Services["accounts"].SecretStore)
	}
	// billing overrides the store so it can resolve from a different backend.
	billing := secrets.Services["billing"].SecretStore
	if billing == nil {
		t.Fatal("billing secret store override did not load")
	}
	if billing.Name != "aws-secrets-billing" || billing.Kind != "SecretStore" {
		t.Fatalf("billing secret store override = %+v", billing)
	}
}

// A workspace with no service-secrets block must load with a nil ServiceSecrets,
// which is the "no service secret projection is rendered" default — validation
// must not treat absence as an error.
func TestEnvironmentServiceSecretsAbsentIsNil(t *testing.T) {
	root := t.TempDir()
	workspace := `name: platform
layout: modules
environments:
  - name: prod
    namespace: platform
    cluster:
      kind: aks
`
	if err := os.WriteFile(filepath.Join(root, resources.WorkspaceConfigurationName), []byte(workspace), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := resources.LoadWorkspaceFromDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Environments[0].ServiceSecrets != nil {
		t.Fatalf("service-secrets = %+v, want nil", loaded.Environments[0].ServiceSecrets)
	}
}

// A declared service-secrets block whose store name/kind is missing (here a
// mistyped `secret-stores:` key that YAML silently drops) must fail at load
// rather than reach the cluster as an ExternalSecret pointing at store "".
func TestEnvironmentServiceSecretsRejectsEmptyStore(t *testing.T) {
	root := t.TempDir()
	workspace := `name: platform
layout: modules
environments:
  - name: prod
    namespace: platform
    service-secrets:
      secret-stores:
        name: azure-keyvault-prod
        kind: ClusterSecretStore
      services:
        accounts:
          remote-keys:
            workos-client-secret: workos/prod/client-secret
`
	if err := os.WriteFile(filepath.Join(root, resources.WorkspaceConfigurationName), []byte(workspace), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := resources.LoadWorkspaceFromDir(context.Background(), root)
	if err == nil {
		t.Fatal("expected load to fail for empty service-secrets store")
	}
	if !strings.Contains(err.Error(), "secret-store") {
		t.Fatalf("error = %v, want it to mention secret-store", err)
	}
}

// A remote-key mapping to an empty path resolves to no remote at all; it must
// fail at load instead of projecting a key with a blank remoteRef.
func TestEnvironmentServiceSecretsRejectsEmptyRemoteKeyPath(t *testing.T) {
	root := t.TempDir()
	workspace := `name: platform
layout: modules
environments:
  - name: prod
    namespace: platform
    service-secrets:
      secret-store:
        name: azure-keyvault-prod
        kind: ClusterSecretStore
      services:
        accounts:
          remote-keys:
            workos-client-secret: ""
`
	if err := os.WriteFile(filepath.Join(root, resources.WorkspaceConfigurationName), []byte(workspace), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := resources.LoadWorkspaceFromDir(context.Background(), root)
	if err == nil {
		t.Fatal("expected load to fail for empty remote-key path")
	}
	if !strings.Contains(err.Error(), "workos-client-secret") {
		t.Fatalf("error = %v, want it to mention the offending key", err)
	}
}

//
//func TestEnvironment(t *testing.T) {
//	ctx := context.Background()
//	Dir := t.TempDir()
//
//	defer func() {
//		os.RemoveAll(Dir)
//	}()
//
//	var action actions.Action
//	var err error
//
//	action, err = action.NewActionNew(ctx, &actionsv0.New{
//		Name: "test-",
//		Path: Dir,
//	})
//	out, err := action.Run(ctx)
//require.NoError(t, err)
//	 := shared.Must(actions.As[resources.](out))
//
//	action, err = actionenviroment.NewActionAddEnvironment(ctx, &actionsv0.AddEnvironment{
//		Name:        "test-environment",
//		Path: .Dir(),
//	})
//require.NoError(t, err)
//	_, err = action.Run(ctx)
//require.NoError(t, err)
//
//	// Make sure the environment is created
//	content, err := os.ReadFile(path.Join(.Dir(), resources.ConfigurationName))
//require.NoError(t, err)
//	require.Contains(t, string(content), "name: test-environment")
//}

func TestEnvironmentAppHost(t *testing.T) {
	service := &resources.ServiceIdentity{Module: "users", Name: "accounts"}

	suffixed := &resources.Environment{Dns: &resources.EnvironmentDNS{AppHostSuffix: "staging.eastus2.azure.example.com"}}
	if got := suffixed.AppHost(service); got != "accounts-users.staging.eastus2.azure.example.com" {
		t.Errorf("AppHost = %q", got)
	}

	// No suffix declared: no derived host (falls back to a local dns.codefly.yaml).
	if got := (&resources.Environment{}).AppHost(service); got != "" {
		t.Errorf("AppHost without suffix = %q, want empty", got)
	}
	if got := (&resources.Environment{Dns: &resources.EnvironmentDNS{}}).AppHost(service); got != "" {
		t.Errorf("AppHost with empty suffix = %q, want empty", got)
	}
}
