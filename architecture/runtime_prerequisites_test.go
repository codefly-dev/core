package architecture

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func prerequisiteWorkspace(t *testing.T) (*resources.Workspace, map[string]string) {
	t.Helper()
	w, err := resources.LoadWorkspaceFromDir(context.Background(), "testdata/configuration-references")
	require.NoError(t, err)
	ids := map[string]string{}
	for _, name := range []string{"host", "worker", "gateway", "relay"} {
		s, err := w.FindUniqueServiceByName(context.Background(), name)
		require.NoError(t, err)
		ids[name] = s.MustUnique()
	}
	return w, ids
}

func TestRuntimePrerequisitesOrderOnlyTheRunAndKeepTransitiveClosure(t *testing.T) {
	ctx := context.Background()
	w, id := prerequisiteWorkspace(t)
	plain, err := NewServiceDependencies(ctx, w)
	require.NoError(t, err)
	order, err := plain.OrderTo(ctx, id["worker"])
	require.NoError(t, err)
	require.Empty(t, order)
	deps, err := NewServiceDependencies(ctx, w, WithRuntimePrerequisites(map[string][]string{
		id["worker"]: {id["host"], id["host"]},
	}))
	require.NoError(t, err)
	run, err := deps.ForStage(resources.StageRun)
	require.NoError(t, err)
	order, err = run.OrderTo(ctx, id["gateway"])
	require.NoError(t, err)
	require.Equal(t, []Service{{Unique: id["host"]}, {Unique: id["worker"]}}, order)
	restricted, err := run.Restrict(ctx, id["gateway"])
	require.NoError(t, err)
	got, err := restricted.OrderTo(ctx, id["gateway"])
	require.NoError(t, err)
	require.Equal(t, order, got)
	build, err := deps.ForStage(resources.StageBuild)
	require.NoError(t, err)
	order, err = build.OrderTo(ctx, id["worker"])
	require.NoError(t, err)
	require.Empty(t, order)
	require.Empty(t, deps.ConfigurationReferenceDependencies(id["worker"]))
	worker, err := deps.ServiceFromUnique(id["worker"])
	require.NoError(t, err)
	require.Empty(t, worker.ServiceDependencies, "ordering must not become endpoint access")
}

func TestRuntimePrerequisitesValidateLoadedIdentitiesAndScope(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name                   string
		consumer, prerequisite string
		exclude, skip          []string
		wantError              bool
	}{
		{"missing consumer", "absent", "host", nil, nil, true},
		{"excluded typo still missing", "absent", "host", []string{"absent"}, nil, true},
		{"missing prerequisite", "worker", "absent", nil, nil, true},
		{"self", "worker", "worker", nil, nil, true},
		{"excluded required", "worker", "host", []string{"host"}, nil, true},
		{"excluded consumer", "worker", "absent", []string{"worker"}, nil, false},
		{"skipped consumer", "worker", "absent", nil, []string{"worker"}, false},
		{"skipped prerequisite still required", "worker", "host", nil, []string{"host"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, id := prerequisiteWorkspace(t)
			id["absent"] = "missing/service"
			options := []DependencyOption{WithRuntimePrerequisites(map[string][]string{id[tc.consumer]: {id[tc.prerequisite]}})}
			for _, name := range tc.exclude {
				options = append(options, ExcludeServices(id[name]))
			}
			for _, name := range tc.skip {
				options = append(options, SkipDependencyFor(id[name]))
			}
			deps, err := NewServiceDependencies(ctx, w, options...)
			if tc.wantError {
				require.ErrorContains(t, err, "runtime prerequisite")
				return
			}
			require.NoError(t, err)
			if tc.name == "skipped prerequisite still required" {
				run, err := deps.ForStage(resources.StageRun)
				require.NoError(t, err)
				order, err := run.OrderTo(ctx, id["worker"])
				require.NoError(t, err)
				require.Equal(t, []Service{{Unique: id["host"]}}, order)
			}
		})
	}
	w, id := prerequisiteWorkspace(t)
	for _, invalid := range []string{"", "host", "/host", "m/", "m/s/endpoint", " m/s"} {
		_, err := NewServiceDependencies(ctx, w, WithRuntimePrerequisites(map[string][]string{id["worker"]: {invalid}}))
		require.ErrorContains(t, err, "runtime prerequisite")
	}
	// A dangling authored dependency creates a graph node, not a loaded service.
	worker, err := w.FindUniqueServiceByName(ctx, "worker")
	require.NoError(t, err)
	worker.ServiceDependencies = []*resources.ServiceDependency{{Module: "missing", Name: "service"}}
	_, err = NewServiceDependencies(ctx, w, WithRuntimePrerequisites(map[string][]string{id["worker"]: {"missing/service"}}))
	require.ErrorContains(t, err, "not a loaded module/service")
}

func TestRuntimePrerequisitesRejectCyclesAndSupersedeExternalEdges(t *testing.T) {
	ctx := context.Background()
	w, id := prerequisiteWorkspace(t)
	for _, prerequisites := range []map[string][]string{
		{id["worker"]: {id["host"]}, id["host"]: {id["worker"]}},
		{id["worker"]: {id["gateway"]}}, // gateway already depends on worker
	} {
		_, err := NewServiceDependencies(ctx, w, WithRuntimePrerequisites(prerequisites))
		require.ErrorContains(t, err, "run dependencies have a cycle")
	}
	deps, err := NewServiceDependencies(ctx, w,
		WithRuntimePrerequisites(map[string][]string{id["relay"]: {id["host"]}}),
		WithConfigurationReferences(map[string][]string{"platform": {id["relay"] + "/grpc"}}))
	require.NoError(t, err)
	require.Equal(t, []resources.DependencyKind{resources.DependencyKindRuntime}, deps.graph.EdgeKinds(id["host"], id["relay"]))
	require.Empty(t, deps.ConfigurationReferenceDependencies(id["relay"]))
	relay, err := deps.ServiceFromUnique(id["relay"])
	require.NoError(t, err)
	require.Equal(t, resources.DependencyKindExternal, relay.ServiceDependencies[0].Kind)
	// Stage projection, not the union, judges a reverse build relationship.
	host, err := w.FindUniqueServiceByName(ctx, "host")
	require.NoError(t, err)
	module, service := resources.SplitUnique(id["worker"])
	host.ServiceDependencies = []*resources.ServiceDependency{{Module: module, Name: service, Kind: resources.DependencyKindBuild}}
	_, err = NewServiceDependencies(ctx, w, WithRuntimePrerequisites(map[string][]string{id["worker"]: {id["host"]}}))
	require.NoError(t, err)
}

func TestRuntimePrerequisitesSnapshotOptionsAndRejectUnsupportedClosure(t *testing.T) {
	ctx := context.Background()
	w, id := prerequisiteWorkspace(t)
	input := map[string][]string{id["worker"]: {id["host"]}}
	option := WithRuntimePrerequisites(input)
	input[id["worker"]][0] = "missing/service"
	delete(input, id["worker"])
	deps, err := NewServiceDependencies(ctx, w, option)
	require.NoError(t, err)
	view, err := deps.ForStage(resources.StageRun)
	require.NoError(t, err)
	view.options.RuntimePrerequisites[id["worker"]][0] = "changed/service"
	require.Equal(t, id["host"], deps.options.RuntimePrerequisites[id["worker"]][0])
	deps.options.RuntimePrerequisites[id["worker"]][0] = "changed/service"
	again, err := NewServiceDependencies(ctx, w, option)
	require.NoError(t, err)
	require.Equal(t, id["host"], again.options.RuntimePrerequisites[id["worker"]][0])
	// A multi-root scope rebuild retains options for the real excluded consumer.
	_, err = NewServiceDependencies(ctx, w, option, ExcludeServices(id["worker"], id["host"]))
	require.NoError(t, err)
	_, err = SelectClosure(ctx, w, id["worker"], option)
	require.ErrorContains(t, err, "SelectClosure does not support runtime prerequisites")
	_, err = SelectClosure(ctx, w, id["worker"], WithRuntimePrerequisites(nil))
	require.NoError(t, err)
	_, err = NewServiceDependencies(ctx, w, WithRuntimePrerequisites(map[string][]string{"missing/service": {id["host"]}}), WithRuntimePrerequisites(nil))
	require.NoError(t, err)
}
