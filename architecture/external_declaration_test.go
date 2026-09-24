package architecture_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/architecture"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

// externalDeclaredRealServiceWorkspace loads a workspace where accounts is a
// real service, backend consumes it at run time and chat declares the same
// producer `kind: external`, with its services listed in the given order. The
// graph is built by walking that list, so the order is what used to decide
// whether the external declaration or the real service typed the node.
func externalDeclaredRealServiceWorkspace(t *testing.T, order []string) (*resources.Workspace, map[string]string) {
	t.Helper()
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/external-declared-real-service")
	require.NoError(t, err)

	byName := make(map[string]*resources.ServiceReference, len(workspace.Services))
	for _, ref := range workspace.Services {
		byName[ref.Name] = ref
	}
	reordered := make([]*resources.ServiceReference, 0, len(order))
	for _, name := range order {
		require.Contains(t, byName, name)
		reordered = append(reordered, byName[name])
	}
	workspace.Services = reordered

	unique := make(map[string]string)
	for _, name := range []string{"accounts", "backend", "chat"} {
		svc := shared.Must(workspace.FindUniqueServiceByName(ctx, name))
		require.NotNil(t, svc)
		unique[name] = svc.MustUnique()
	}
	return workspace, unique
}

// Every declaration order: the real node loaded before the external
// declaration, after it, and with the normal consumer on either side.
var externalDeclarationOrders = [][]string{
	{"accounts", "backend", "chat"},
	{"accounts", "chat", "backend"},
	{"backend", "accounts", "chat"},
	{"backend", "chat", "accounts"},
	{"chat", "accounts", "backend"},
	{"chat", "backend", "accounts"},
}

func TestExternalDeclarationNeverDowngradesAWorkspaceService(t *testing.T) {
	ctx := context.Background()
	for _, order := range externalDeclarationOrders {
		t.Run(joinOrder(order), func(t *testing.T) {
			workspace, unique := externalDeclaredRealServiceWorkspace(t, order)
			dep, err := architecture.NewServiceDependencies(ctx, workspace)
			require.NoError(t, err)

			// accounts is a workspace service whatever chat calls it.
			require.Contains(t, dep.Services(), architecture.Service{Unique: unique["accounts"]})
			_, err = dep.ServiceFromUnique(unique["accounts"])
			require.NoError(t, err)

			// backend's normal consumption still pulls accounts into its run.
			run, err := dep.ForStage(resources.StageRun)
			require.NoError(t, err)
			order, err := run.OrderTo(ctx, unique["backend"])
			require.NoError(t, err)
			require.Equal(t, createServices(unique["accounts"]), order)

			// Running both consumers together runs accounts once, before backend.
			closure, err := dep.OrderTo(ctx, unique["backend"])
			require.NoError(t, err)
			require.Contains(t, closure, architecture.Service{Unique: unique["accounts"]})

			// chat's external declaration still constrains no stage: chat alone
			// neither builds, starts nor waits for accounts.
			for _, stage := range resources.Stages() {
				restricted, err := dep.ForStage(stage)
				require.NoError(t, err)
				order, err := restricted.OrderTo(ctx, unique["chat"])
				require.NoError(t, err)
				require.Empty(t, order, "stage %s", stage)
			}
		})
	}
}

// Without any normal consumer, a producer the workspace actually loads is
// still a service: `kind: external` describes the edge, never the node.
func TestExternalDeclarationAloneKeepsAWorkspaceServiceReal(t *testing.T) {
	ctx := context.Background()
	for _, order := range [][]string{{"accounts", "chat", "backend"}, {"chat", "accounts", "backend"}} {
		t.Run(joinOrder(order), func(t *testing.T) {
			workspace, unique := externalDeclaredRealServiceWorkspace(t, order)
			dep, err := architecture.NewServiceDependencies(ctx, workspace, architecture.ExcludeServices(unique["backend"]))
			require.NoError(t, err)

			require.Contains(t, dep.Services(), architecture.Service{Unique: unique["accounts"]})
			_, err = dep.ServiceFromUnique(unique["accounts"])
			require.NoError(t, err)
		})
	}
}

func joinOrder(order []string) string {
	out := ""
	for i, name := range order {
		if i > 0 {
			out += "-then-"
		}
		out += name
	}
	return out
}
