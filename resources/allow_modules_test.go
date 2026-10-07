package resources_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// askingServiceManifest writes a service manifest with the given endpoint and
// dependency blocks (indented YAML lists, or "").
func askingServiceManifest(name, endpoints, dependencies string) string {
	manifest := "kind: service\nname: " + name + "\nversion: 0.0.1\nagent:\n  kind: runtime::service\n  name: go-grpc\n  version: 0.0.1\n  publisher: codefly.ai\n"
	if endpoints != "" {
		manifest += "endpoints:\n" + endpoints
	}
	if dependencies != "" {
		manifest += "service-dependencies:\n" + dependencies
	}
	return manifest
}

// moduleDir writes a module with its services under root/<relative>.
func moduleDir(t *testing.T, root, relative, module string, services map[string]string) {
	t.Helper()
	names := ""
	for name := range services {
		names += "  - name: " + name + "\n"
	}
	compositionFile(t, root, filepath.Join(relative, "module.codefly.yaml"), "kind: module\nname: "+module+"\nservices:\n"+names)
	for name, manifest := range services {
		compositionFile(t, root, filepath.Join(relative, "services", name, "service.codefly.yaml"), manifest)
	}
}

// The composition under test: a product composing a platform workspace (the
// host: modules saas and data) and declaring one solution (reporting), with
// its own module (app).
//
//	saas/accounts   authority internal, events public, rest private, unasked internal
//	data/migrator   http internal
//	data/store      http internal
//	wiki/pages      runtime -> saas/accounts [authority]      (platform module)
//	wiki/search     runtime -> saas/accounts [authority]      (same module, listed once)
//	billing/ledger  untyped -> saas/accounts (all permitted)
//	saas/frontend   runtime -> saas/accounts [rest]           (own module, private)
//	codegen/schemas build   -> saas/accounts [authority]      (contract, never calls)
//	app/api         completion -> data/migrator               (waits, consumes nothing)
//	app/api         external   -> vendor/stripe               (outside the composition)
//	reporting/report (solution) is written by each test.
func productComposition(t *testing.T, reporting string) string {
	t.Helper()
	root := t.TempDir()
	compositionFile(t, root, "core/workspace.codefly.yaml", "name: platform-core\nlayout: modules\nmodules:\n  - name: saas\n  - name: data\n  - name: wiki\n  - name: billing\n  - name: codegen\n")
	moduleDir(t, root, "core/modules/saas", "saas", map[string]string{
		"accounts": askingServiceManifest("accounts", "  - name: authority\n    api: grpc\n    visibility: internal\n  - name: events\n    api: http\n    visibility: public\n    exposure: none\n  - name: rest\n    api: rest\n    visibility: private\n  - name: unasked\n    api: tcp\n    visibility: internal\n", ""),
		"frontend": askingServiceManifest("frontend", "", "  - name: accounts\n    kind: runtime\n    endpoints:\n      - name: rest\n      - name: authority\n"),
	})
	moduleDir(t, root, "core/modules/data", "data", map[string]string{
		"migrator": askingServiceManifest("migrator", "  - name: http\n    api: http\n    visibility: internal\n", ""),
		"store":    askingServiceManifest("store", "  - name: http\n    api: http\n    visibility: internal\n", ""),
	})
	moduleDir(t, root, "core/modules/wiki", "wiki", map[string]string{
		"pages":  askingServiceManifest("pages", "", "  - name: accounts\n    module: saas\n    kind: runtime\n    endpoints:\n      - name: authority\n"),
		"search": askingServiceManifest("search", "", "  - name: accounts\n    module: saas\n    kind: runtime\n    endpoints:\n      - name: authority\n"),
	})
	moduleDir(t, root, "core/modules/billing", "billing", map[string]string{
		"ledger": askingServiceManifest("ledger", "", "  - name: accounts\n    module: saas\n"),
	})
	moduleDir(t, root, "core/modules/codegen", "codegen", map[string]string{
		"schemas": askingServiceManifest("schemas", "", "  - name: accounts\n    module: saas\n    kind: build\n    endpoints:\n      - name: authority\n"),
	})
	compositionFile(t, root, "product/workspace.codefly.yaml", "name: product\nlayout: modules\nmodules:\n  - name: app\nworkspaces:\n  - name: platform-core\n    path: ../core\nsolutions:\n  - name: reporting\n    path: solutions/reporting\n")
	moduleDir(t, root, "product/modules/app", "app", map[string]string{
		"api": askingServiceManifest("api", "", "  - name: migrator\n    module: data\n    kind: completion\n  - name: stripe\n    module: vendor\n    kind: external\n"),
	})
	moduleDir(t, root, "product/solutions/reporting", "reporting", map[string]string{
		"report": askingServiceManifest("report", "  - name: http\n    api: http\n    visibility: internal\n", reporting),
	})
	return filepath.Join(root, "product")
}

func loadProduct(t *testing.T, dir string) *resources.Workspace {
	t.Helper()
	workspace, err := resources.LoadWorkspaceFromDir(context.Background(), dir)
	require.NoError(t, err)
	return workspace
}

// The composition knows what each of its modules is: the product's own, the
// host's, or a solution — and what it does not carry.
func TestCompositionCarriesTheProvenanceOfEveryMember(t *testing.T) {
	workspace := loadProduct(t, productComposition(t, ""))
	for name, want := range map[string]resources.Member{
		"app":       {Name: "app", Role: resources.MemberRoleModule, Workspace: "product"},
		"saas":      {Name: "saas", Role: resources.MemberRoleModule, Workspace: "platform-core"},
		"data":      {Name: "data", Role: resources.MemberRoleModule, Workspace: "platform-core"},
		"reporting": {Name: "reporting", Role: resources.MemberRoleSolution, Workspace: "product"},
	} {
		member, ok := workspace.Member(name)
		require.True(t, ok, name)
		require.Equal(t, want, member)
	}
	_, ok := workspace.Member("ghost")
	require.False(t, ok, "a module the composition does not carry has no provenance")
	require.Len(t, workspace.Members(), 7)
}

// The allow-list of an internal endpoint is the set of asks: every module
// whose services declare an edge that REACHES it at run time, the producer's
// own module included, each once, sorted — and nothing a host wrote about
// itself. A build edge reads the contract and derives nothing; a completion
// edge waits and derives nothing, its run-stage participation notwithstanding;
// an external edge has no producer here. A public endpoint is open and a
// private one closed: neither carries a list.
func TestDeriveAllowModulesIsTheSetOfAsks(t *testing.T) {
	ctx := context.Background()
	workspace := loadProduct(t, productComposition(t, ""))
	derived, err := workspace.DeriveAllowModules(ctx)
	require.NoError(t, err)

	authority, declared := derived.Of("saas", "accounts", "authority")
	require.True(t, declared)
	require.Equal(t, resources.AllowList{Visibility: resources.VisibilityInternal, Modules: []string{"billing", "saas", "wiki"}}, authority,
		"the asks, sorted, each module once, the owning module when it asks; the build edge from codegen derives nothing")

	unasked, _ := derived.Of("saas", "accounts", "unasked")
	require.Equal(t, []string{"billing"}, unasked.Modules, "an untyped unnamed edge consumes all it is permitted, so it asks for every internal endpoint")

	rest, _ := derived.Of("saas", "accounts", "rest")
	require.Equal(t, resources.AllowList{Visibility: resources.VisibilityPrivate}, rest, "private is closed: no list, even for the own module's ask")

	events, _ := derived.Of("saas", "accounts", "events")
	require.Equal(t, resources.AllowList{Visibility: resources.VisibilityPublic}, events, "public is open: no list")

	// The producer's own module appears when one of its services asks for an
	// internal endpoint — saas/frontend asks for authority below — like any
	// other module; its ask for the private rest records nothing, since a
	// private endpoint carries no list.

	// The completion witness: app/api waits for data/migrator and names no
	// endpoint; stage participation is not consumption, so it asks for none.
	migrator, declared := derived.Of("data", "migrator", "http")
	require.True(t, declared)
	require.Equal(t, resources.AllowList{Visibility: resources.VisibilityInternal, Modules: []string{}}, migrator,
		"a completion prerequisite derives no grant")

	store, _ := derived.Of("data", "store", "http")
	require.Equal(t, []string{}, store.Modules, "an internal endpoint nobody asks for grants nobody")

	_, declared = derived.Of("saas", "accounts", "nope")
	require.False(t, declared, "an endpoint the composition does not declare is a different fact from one nobody asks for")
}

// A solution reaches modules only through the host. A solution's run-stage
// edge onto a module's endpoint is refused by the composition — in the
// derivation and in the static pass, by the same rule — whatever the endpoint
// grants; a build edge reads the contract and is not that route; and the same
// edge from a module, or onto another solution, is judged by visibility alone.
func TestASolutionReachesModulesOnlyThroughTheHost(t *testing.T) {
	ctx := context.Background()
	direct := "  - name: store\n    module: data\n    kind: runtime\n    endpoints:\n      - name: http\n"
	workspace := loadProduct(t, productComposition(t, direct))
	_, err := workspace.DeriveAllowModules(ctx)
	require.ErrorIs(t, err, resources.ErrSolutionReachesThroughHost)
	require.ErrorContains(t, err, `solution "reporting" (of workspace "product") declares data/store on module "data" (of workspace "platform-core")`)
	err = workspace.ValidateServiceDependencies(ctx)
	require.ErrorIs(t, err, resources.ErrSolutionReachesThroughHost, "the static pass refuses the same edge")

	contract := "  - name: store\n    module: data\n    kind: build\n    endpoints:\n      - name: http\n"
	workspace = loadProduct(t, productComposition(t, contract))
	derived, err := workspace.DeriveAllowModules(ctx)
	require.NoError(t, err, "a build edge reads the module's contract and calls nothing")
	store, _ := derived.Of("data", "store", "http")
	require.Empty(t, store.Modules)
	require.NoError(t, workspace.ValidateServiceDependencies(ctx))

	// The judgement is the composition's, available to every pass that holds
	// one, and it refuses what it cannot judge: an end the composition does
	// not carry.
	require.NoError(t, resources.JudgeCompositionEdge(workspace, "wiki", "saas", &resources.ServiceDependency{Module: "saas", Name: "accounts", Kind: resources.DependencyKindRuntime}), "module to module: visibility decides")
	require.NoError(t, resources.JudgeCompositionEdge(workspace, "saas", "reporting", &resources.ServiceDependency{Module: "reporting", Name: "report", Kind: resources.DependencyKindRuntime}), "module to solution: visibility decides")
	require.ErrorIs(t, resources.JudgeCompositionEdge(workspace, "reporting", "app", &resources.ServiceDependency{Module: "app", Name: "api", Kind: resources.DependencyKindRuntime}), resources.ErrSolutionReachesThroughHost, "the product's own module is a module")
	require.NoError(t, resources.JudgeCompositionEdge(workspace, "reporting", "app", &resources.ServiceDependency{Module: "app", Name: "api", Kind: resources.DependencyKindCompletion}), "waiting for a module's job reaches no endpoint")
	err = resources.JudgeCompositionEdge(workspace, "reporting", "ghost", &resources.ServiceDependency{Module: "ghost", Name: "x", Kind: resources.DependencyKindRuntime})
	require.ErrorIs(t, err, resources.ErrUnjudgedProvenance)
}

// A dependency the export boundary refuses is the same refusal the static
// validation makes: nothing is derived for a composition that does not
// validate, so the rendered list can never grant what the model denies.
func TestDeriveAllowModulesRefusesWhatTheBoundaryRefuses(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	compositionFile(t, root, "workspace.codefly.yaml", "name: plain\nlayout: modules\nmodules:\n  - name: saas\n  - name: wiki\n")
	moduleDir(t, root, "modules/saas", "saas", map[string]string{
		"accounts": askingServiceManifest("accounts", "  - name: rest\n    api: rest\n    visibility: private\n", ""),
	})
	moduleDir(t, root, "modules/wiki", "wiki", map[string]string{
		"pages": askingServiceManifest("pages", "", "  - name: accounts\n    module: saas\n    kind: runtime\n    endpoints:\n      - name: rest\n"),
	})
	workspace := loadProduct(t, root)
	_, err := workspace.DeriveAllowModules(ctx)
	require.ErrorContains(t, err, `is private to module "saas"`)
	require.True(t, strings.Contains(err.Error(), "wiki/pages"), err.Error())
	require.Error(t, workspace.ValidateServiceDependencies(ctx))
}

// A workspace that composes nothing is a composition of one owner: every
// module is its own, and the join is the same join.
func TestDeriveAllowModulesOverAPlainWorkspace(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/allowed-dependency-visibility")
	require.NoError(t, err)
	member, ok := workspace.Member("saas")
	require.True(t, ok)
	require.Equal(t, resources.Member{Name: "saas", Role: resources.MemberRoleModule, Workspace: workspace.Name}, member)
	derived, err := workspace.DeriveAllowModules(ctx)
	require.NoError(t, err)
	internal, declared := derived.Of("saas", "gateway", "internal")
	require.True(t, declared)
	require.Equal(t, []string{"platform"}, internal.Modules, "the host's allow-list is the platform module's declared ask, which the host never wrote")
}

// A composed workspace's own solutions keep their role and their owner in the
// product that composes it, and a module two compositions down is owned by
// the workspace that declared it — so a solution cannot become a module by
// being composed one level up, and a refusal names the right workspace.
func TestProvenanceIsInheritedThroughComposition(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	compositionFile(t, root, "core/workspace.codefly.yaml", "name: platform-core\nlayout: modules\nmodules:\n  - name: data\n")
	moduleDir(t, root, "core/modules/data", "data", map[string]string{
		"store": askingServiceManifest("store", "  - name: http\n    api: http\n    visibility: internal\n", ""),
	})
	compositionFile(t, root, "mid/workspace.codefly.yaml", "name: mid\nlayout: modules\nworkspaces:\n  - name: platform-core\n    path: ../core\nsolutions:\n  - name: reporting\n    path: solutions/reporting\n")
	moduleDir(t, root, "mid/solutions/reporting", "reporting", map[string]string{
		"report": askingServiceManifest("report", "", "  - name: store\n    module: data\n    kind: runtime\n    endpoints:\n      - name: http\n"),
	})
	compositionFile(t, root, "product/workspace.codefly.yaml", "name: product\nlayout: modules\nworkspaces:\n  - name: mid\n    path: ../mid\n")

	product := loadProduct(t, filepath.Join(root, "product"))
	reporting, ok := product.Member("reporting")
	require.True(t, ok)
	require.Equal(t, resources.Member{Name: "reporting", Role: resources.MemberRoleSolution, Workspace: "mid"}, reporting, "a composed workspace's solution stays its solution")
	data, ok := product.Member("data")
	require.True(t, ok)
	require.Equal(t, resources.Member{Name: "data", Role: resources.MemberRoleModule, Workspace: "platform-core"}, data, "a grandchild's module is owned by the workspace that declared it")

	// So the solution's direct route is refused in the product exactly as in
	// the workspace that declared it, by every pass.
	err := product.ValidateServiceDependencies(ctx)
	require.ErrorIs(t, err, resources.ErrSolutionReachesThroughHost)
	require.ErrorContains(t, err, `solution "reporting" (of workspace "mid")`)
	_, err = product.DeriveAllowModules(ctx)
	require.ErrorIs(t, err, resources.ErrSolutionReachesThroughHost)
	mid := loadProduct(t, filepath.Join(root, "mid"))
	require.ErrorIs(t, mid.ValidateServiceDependencies(ctx), resources.ErrSolutionReachesThroughHost)
}

// Every declarer asks and is judged alike: a module's runnable that calls an
// internal endpoint gets its entry, and a solution's runnable that would reach
// a module's endpoint gets the refusal a service's edge gets.
func TestEveryDeclarerIsJudgedAndAsks(t *testing.T) {
	ctx := context.Background()
	runnable := func(name, dependencies string) string {
		return "kind: runnable\nname: " + name + "\nversion: 0.0.1\nagent:\n  kind: codefly:runnable\n  name: python\n  version: 0.0.1\n  publisher: codefly.dev\ncontract:\n  protocol: codefly.runnable.served/v1\n  input:\n    fields:\n      - name: text\n        type: string\n  output:\n    fields:\n      - name: count\n        type: integer\nentrypoint:\n  handler: handler.py\nexecution:\n  facilities: [generated-service, kubernetes]\n  timeout: 2m\n  cancellation: signal\n  recovery: recompute\n  payload:\n    max-input-bytes: 65536\nservice-dependencies:\n" + dependencies
	}
	build := func(root string, reportingRunnable bool) string {
		compositionFile(t, root, "core/workspace.codefly.yaml", "name: platform-core\nlayout: modules\nmodules:\n  - name: data\n  - name: app\n")
		moduleDir(t, root, "core/modules/data", "data", map[string]string{
			"store": askingServiceManifest("store", "  - name: http\n    api: http\n    visibility: internal\n", ""),
		})
		compositionFile(t, root, "core/modules/app/module.codefly.yaml", "kind: module\nname: app\nservices: []\nrunnables:\n  - name: indexer\n")
		compositionFile(t, root, "core/modules/app/runnables/indexer/runnable.codefly.yaml", runnable("indexer", "  - name: store\n    module: data\n    kind: runtime\n    endpoints:\n      - name: http\n"))
		solutions := ""
		if reportingRunnable {
			solutions = "solutions:\n  - name: reporting\n    path: solutions/reporting\n"
			compositionFile(t, root, "product/solutions/reporting/module.codefly.yaml", "kind: module\nname: reporting\nservices: []\nrunnables:\n  - name: digest\n")
			compositionFile(t, root, "product/solutions/reporting/runnables/digest/runnable.codefly.yaml", runnable("digest", "  - name: store\n    module: data\n    kind: runtime\n    endpoints:\n      - name: http\n"))
		}
		compositionFile(t, root, "product/workspace.codefly.yaml", "name: product\nlayout: modules\nworkspaces:\n  - name: platform-core\n    path: ../core\n"+solutions)
		return filepath.Join(root, "product")
	}

	product := loadProduct(t, build(t.TempDir(), false))
	require.NoError(t, product.ValidateServiceDependencies(ctx))
	derived, err := product.DeriveAllowModules(ctx)
	require.NoError(t, err)
	store, declared := derived.Of("data", "store", "http")
	require.True(t, declared)
	require.Equal(t, []string{"app"}, store.Modules, "a module's runnable asks like a service")

	product = loadProduct(t, build(t.TempDir(), true))
	err = product.ValidateServiceDependencies(ctx)
	require.ErrorIs(t, err, resources.ErrSolutionReachesThroughHost, "a solution's runnable is refused like a service's edge")
	require.ErrorContains(t, err, "runnable reporting/digest")
	_, err = product.DeriveAllowModules(ctx)
	require.ErrorIs(t, err, resources.ErrSolutionReachesThroughHost)
}

// The derivation accepts nothing the static pass refuses: a dependency naming
// an endpoint the producer does not declare is refused by both, in the
// author's words.
func TestDeriveAllowModulesRefusesWhatTheStaticPassRefuses(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	compositionFile(t, root, "workspace.codefly.yaml", "name: plain\nlayout: modules\nmodules:\n  - name: saas\n  - name: wiki\n")
	moduleDir(t, root, "modules/saas", "saas", map[string]string{
		"accounts": askingServiceManifest("accounts", "  - name: authority\n    api: grpc\n    visibility: internal\n", ""),
	})
	moduleDir(t, root, "modules/wiki", "wiki", map[string]string{
		"pages": askingServiceManifest("pages", "", "  - name: accounts\n    module: saas\n    kind: runtime\n    endpoints:\n      - name: nope\n"),
	})
	workspace := loadProduct(t, root)
	static := workspace.ValidateServiceDependencies(ctx)
	require.ErrorContains(t, static, "undeclared")
	_, err := workspace.DeriveAllowModules(ctx)
	require.Error(t, err)
	require.Equal(t, static.Error() != "", err.Error() != "")
	require.ErrorContains(t, err, "undeclared")
}

// An endpoint the module's interface exports at a reach its own exposure
// contradicts is refused at module load, naming the entry and the author's
// words — never later by the conversion in the exported ones.
func TestAnInterfaceExportTheExposureContradictsIsRefusedAtLoad(t *testing.T) {
	ctx := context.Background()
	for name, entry := range map[string]string{
		"exported internal": "interface:\n    endpoints:\n        - service: web\n          endpoint: http\n          visibility: internal\n",
		"omitted":           "interface:\n    endpoints:\n        - service: web\n          endpoint: other\n          visibility: public\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			compositionFile(t, root, "workspace.codefly.yaml", "name: plain\nlayout: modules\nmodules:\n  - name: front\n")
			compositionFile(t, root, "modules/front/module.codefly.yaml", "kind: module\nname: front\nservices:\n  - name: web\n"+entry)
			compositionFile(t, root, "modules/front/services/web/service.codefly.yaml", askingServiceManifest("web", "  - name: http\n    api: http\n    visibility: public\n    exposure: public\n  - name: other\n    api: http\n    visibility: internal\n", ""))
			workspace := loadProduct(t, root)
			_, err := workspace.LoadModuleFromName(ctx, "front")
			require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration, "refused at module load, which judges the interface against its services")
			require.ErrorContains(t, err, `endpoint web/http declares exposure "public"`)
			require.ErrorContains(t, err, "export it public, or drop its exposure")
		})
	}
}
