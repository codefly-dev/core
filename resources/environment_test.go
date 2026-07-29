package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
)

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
