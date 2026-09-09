package architecture_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/architecture"
	"github.com/codefly-dev/core/executionplan"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// The fixture seeds this in the postgres service spec: a plan that dumps
// declared data instead of resolved facts would carry it.
const secretSentinel = "codefly-plan-secret-sentinel"

func runOptions() architecture.PlanOptions {
	return architecture.PlanOptions{
		Phase:       executionplan.PhaseRun,
		Environment: "local",
		StatePolicy: executionplan.StatePolicy{Reuse: true, Lifecycle: executionplan.LifecycleStop},
	}
}

func planFrom(t *testing.T, dir string, target string, options architecture.PlanOptions) *executionplan.Plan {
	t.Helper()
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	closure, err := architecture.SelectClosure(ctx, workspace, target)
	require.NoError(t, err)
	plan, err := closure.Plan(ctx, options)
	require.NoError(t, err)
	return plan
}

func copyFixture(t *testing.T, source string) string {
	t.Helper()
	destination := t.TempDir()
	require.NoError(t, filepath.WalkDir(source, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	}))
	return destination
}

func rewriteFixture(t *testing.T, dir string, relative string, old string, replacement string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(relative))
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(content), old)
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(content), old, replacement, 1)), 0o644))
}

func fingerprint(t *testing.T, plan *executionplan.Plan) string {
	t.Helper()
	value, err := plan.SemanticFingerprint()
	require.NoError(t, err)
	return value
}

// The golden plan is the canonical serialization contract: it fixes ordering and
// shows that every node, edge and configuration origin states why it is there.
func TestPlanGolden(t *testing.T) {
	plan := planFrom(t, "testdata/plan-workspace", "api/orders", runOptions())

	serialized, err := plan.MarshalCanonical()
	require.NoError(t, err)
	var indented bytes.Buffer
	require.NoError(t, json.Indent(&indented, serialized, "", "  "))

	golden, err := os.ReadFile("testdata/plan-workspace.golden.json")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(string(golden)), indented.String())

	require.NotContains(t, string(serialized), secretSentinel)
	for _, node := range plan.Nodes {
		require.NotEmpty(t, node.Selection.Reason, node.ID)
		for _, artifact := range node.Artifacts {
			require.NotEmpty(t, artifact.Selection.Reason, artifact.Reference)
		}
	}
	for _, edge := range plan.Edges {
		require.NotEmpty(t, edge.Selection.Reason)
	}
	for _, origin := range plan.Configurations {
		require.NotEmpty(t, origin.Selection.Reason, origin.Key)
	}
	for _, step := range plan.SchemaSteps {
		require.NotEmpty(t, step.Selection.Reason, step.ID)
	}
}

// A schema step is not a node, so no edge can reach it: the plan must carry its
// place in the order itself rather than leave every consumer to re-derive it.
func TestPlanOrdersSchemaStepsAgainstNodes(t *testing.T) {
	plan := planFrom(t, "testdata/plan-workspace", "api/orders", runOptions())

	require.Len(t, plan.SchemaSteps, 1)
	step := plan.SchemaSteps[0]
	require.Equal(t, "data/db-migration", step.ID)
	require.Equal(t, []string{"data/postgres"}, step.After,
		"the migration waits for the service it operates on")
	require.Equal(t, []string{"api/orders"}, step.Before,
		"everything downstream of that service waits for the migration")
}

// Paths are not semantic: the same declarations planned from two checkouts must
// be reusable against each other.
func TestPlanFingerprintIgnoresCheckoutLocationAndInvocation(t *testing.T) {
	first := copyFixture(t, "testdata/plan-workspace")
	second := copyFixture(t, "testdata/plan-workspace")

	firstOptions := runOptions()
	firstOptions.Invocation = &executionplan.Invocation{
		ID:           "invocation-one",
		StartedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		WorkspaceDir: first,
		Actor:        "developer",
	}
	secondOptions := runOptions()
	secondOptions.Invocation = &executionplan.Invocation{
		ID:           "invocation-two",
		StartedAt:    time.Date(2026, 6, 2, 3, 4, 5, 0, time.UTC),
		WorkspaceDir: second,
		Actor:        "ci",
	}

	firstPlan := planFrom(t, first, "api/orders", firstOptions)
	secondPlan := planFrom(t, second, "api/orders", secondOptions)

	require.Equal(t, fingerprint(t, firstPlan), fingerprint(t, secondPlan))

	firstSemantic, err := firstPlan.SemanticBytes()
	require.NoError(t, err)
	secondSemantic, err := secondPlan.SemanticBytes()
	require.NoError(t, err)
	require.Equal(t, firstSemantic, secondSemantic)
	require.NotContains(t, string(firstSemantic), first)
	require.NotContains(t, string(firstSemantic), "invocation-one")

	firstSerialized, err := firstPlan.MarshalCanonical()
	require.NoError(t, err)
	require.Contains(t, string(firstSerialized), "invocation-one")
	require.NotEqual(t, string(firstSerialized), string(secondSemantic))
}

func TestPlanFingerprintTracksResolvedFacts(t *testing.T) {
	baseline := fingerprint(t, planFrom(t, "testdata/plan-workspace", "api/orders", runOptions()))

	t.Run("backend version", func(t *testing.T) {
		dir := copyFixture(t, "testdata/plan-workspace")
		rewriteFixture(t, dir, "modules/data/services/postgres/service.codefly.yaml", "0.0.11", "0.0.12")
		require.NotEqual(t, baseline, fingerprint(t, planFrom(t, dir, "api/orders", runOptions())))
	})

	t.Run("fixture", func(t *testing.T) {
		options := runOptions()
		options.Environment = "staging"
		require.NotEqual(t, baseline, fingerprint(t, planFrom(t, "testdata/plan-workspace", "api/orders", options)))
	})

	t.Run("state policy", func(t *testing.T) {
		options := runOptions()
		options.StatePolicy.Lifecycle = executionplan.LifecycleKeepRunning
		require.NotEqual(t, baseline, fingerprint(t, planFrom(t, "testdata/plan-workspace", "api/orders", options)))
	})

	t.Run("phase", func(t *testing.T) {
		options := runOptions()
		options.Phase = executionplan.PhaseTest
		require.NotEqual(t, baseline, fingerprint(t, planFrom(t, "testdata/plan-workspace", "api/orders", options)))
	})

	t.Run("artifact identity", func(t *testing.T) {
		dir := copyFixture(t, "testdata/plan-workspace")
		rewriteFixture(t, dir, "workspace.codefly.yaml", "    - name: data",
			"    - name: data\n      source: codefly-dev/module-data\n      version: 1.2.3\n      path: modules/data")
		plan := planFrom(t, dir, "api/orders", runOptions())
		require.NotEqual(t, baseline, fingerprint(t, plan))

		artifact := moduleArtifact(t, plan, "data/postgres")
		require.Equal(t, "codefly-dev/module-data", artifact.Reference)
		require.Equal(t, "1.2.3", artifact.Version)
		require.Equal(t, executionplan.ReasonWorkspaceLayout, artifact.Selection.Reason)
		require.Equal(t, []string{string(executionplan.ReasonCommittedPin)}, artifact.Selection.Over)
	})
}

func moduleArtifact(t *testing.T, plan *executionplan.Plan, node string) executionplan.Artifact {
	t.Helper()
	for _, candidate := range plan.Nodes {
		if candidate.ID == node {
			require.Len(t, candidate.Artifacts, 1)
			return candidate.Artifacts[0]
		}
	}
	t.Fatalf("node %s is not in the plan", node)
	return executionplan.Artifact{}
}

// Core never materializes a pinned module, so a closure that reaches one is
// unresolved rather than silently planned without it.
func TestPlanReportsAPinnedModuleAsUnresolved(t *testing.T) {
	ctx := context.Background()
	dir := copyFixture(t, "testdata/plan-workspace")
	rewriteFixture(t, dir, "workspace.codefly.yaml", "    - name: data",
		"    - name: data\n      source: codefly-dev/module-data\n      version: 1.2.3")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	closure, err := architecture.SelectClosure(ctx, workspace, "api/orders")
	require.NoError(t, err)

	_, err = closure.Plan(ctx, runOptions())
	require.Error(t, err)
	require.Contains(t, err.Error(), "data/postgres")
	require.Contains(t, err.Error(), "pinned modules are pulled by the CLI")
}

// An overlay that redirects a composed module is the reason that module resolves
// where it does, and the plan says what it beat.
func TestPlanExplainsWhyAnOverlayWon(t *testing.T) {
	dir := copyFixture(t, "testdata/plan-workspace")
	rewriteFixture(t, dir, "workspace.codefly.yaml", "    - name: data",
		"    - name: data\n      source: codefly-dev/module-data\n      version: 1.2.3")
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  data:\n    path: modules/data\n"), 0o644))

	artifact := moduleArtifact(t, planFrom(t, dir, "api/orders", runOptions()), "data/postgres")
	require.Equal(t, executionplan.ReasonLocalOverlay, artifact.Selection.Reason)
	require.Equal(t, []string{string(executionplan.ReasonCommittedPin)}, artifact.Selection.Over)
	require.Contains(t, artifact.Selection.Detail, resources.LocalOverlayConfigurationName)
	require.Equal(t, executionplan.VerificationLocal, artifact.Verification)
	require.Equal(t, "codefly-dev/module-data", artifact.Reference)
}

// Plan refuses an unresolved closure, but the failure must still be
// describable: collapsing it into an error string left no way to render which
// node failed and why.
func TestDraftDescribesAnUnresolvedClosure(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/unavailable-module")
	require.NoError(t, err)

	closure, err := architecture.SelectClosure(ctx, workspace, "web/portal")
	require.NoError(t, err)

	draft, err := closure.Draft(ctx, runOptions())
	require.NoError(t, err, "a draft describes a closure that cannot run")

	byID := map[string]executionplan.Node{}
	for _, node := range draft.Nodes {
		byID[node.ID] = node
	}
	require.Contains(t, byID, "web/portal")
	require.Equal(t, executionplan.Resolved, byID["web/portal"].Resolution)

	broken, present := byID["vault/secrets"]
	require.True(t, present, "the unresolved node stays in the plan")
	require.Equal(t, executionplan.Unresolved, broken.Resolution)
	require.Contains(t, broken.Unresolved, "cannot load module <vault>")
	require.Equal(t, []string{"web/portal"}, broken.Selection.Via)

	require.Len(t, draft.Edges, 1, "the edge onto the unresolved node is kept")
	require.Equal(t, "vault/secrets", draft.Edges[0].From)

	require.ErrorContains(t, draft.Validate(), "vault/secrets: cannot load module <vault>",
		"a draft is describable but never executable")
}

func TestPlanRefusesAnUnresolvedClosure(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/unavailable-module")
	require.NoError(t, err)

	closure, err := architecture.SelectClosure(ctx, workspace, "web/portal")
	require.NoError(t, err)

	_, err = closure.Plan(ctx, runOptions())
	require.Error(t, err)
	require.Contains(t, err.Error(), "vault/secrets")
}
