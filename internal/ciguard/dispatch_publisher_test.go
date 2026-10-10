package ciguard

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// This is #759's real publish-service-image.yml at a9bd6d61, including review
// 5479801628's credential-persistence fix. The fixture lets the evaluator's
// contract land independently of that workflow.
// Its distinct logical name keeps its test-scoped registration separate from
// the live registry, including after the workflow lands.
func dispatchPublisherFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "ciguard", "testdata", "dispatch-publisher.yml"))
	require.NoError(t, err)
	return string(raw)
}

func registerDispatchPublisherFixture(t *testing.T, wf isolatedWorkflow) {
	t.Helper()
	previousJobs, previousRecords := permittedCredentialJobs, recordedJobDigests
	t.Cleanup(func() {
		permittedCredentialJobs, recordedJobDigests = previousJobs, previousRecords
	})
	permittedCredentialJobs = append(append([]jobTemplate{}, previousJobs...), jobTemplate{
		workflow: wf.name, job: "publish",
		executedBy: "TestDispatchPublisherFixtureRefusesUnmergedCommits",
		why:        "test fixture for a reusable dispatch publisher's ancestry refusal",
	})
	recordedJobDigests = maps.Clone(previousRecords)
	recordedJobDigests[recordKey(wf.name, "publish")] = canonicalJobDigest(t, wf, "publish")
}

func TestDispatchPublisherNeedsAllThreeConjuncts(t *testing.T) {
	wf := parseIsolatedDocument(t, dispatchPublisherFixture(t), "dispatch-publisher-fixture.yml")
	require.Contains(t, secretsIn(t, wf, "publish"), "registry-token")
	unreachable, why := provablyUnreachable(t, wf.Jobs["publish"].If, hostileScenariosFor(wf.On))
	require.False(t, unreachable, "a workflow_call dispatch gate still answers for unknown callers: %s", why)
	origin, _ := checkoutSelectsTheDefaultBranch(wf.Jobs["publish"].Steps[0], absentRefIsTheDefaultBranch(wf))
	require.Equal(t, treeIsTheTriggeringCommit, origin)
	require.Equal(t, false, wf.Jobs["publish"].Steps[0].With["persist-credentials"])
	require.Equal(t, map[string]string{"GH_TOKEN": "${{ github.token }}"}, wf.Jobs["publish"].Steps[1].Env)

	t.Run("unregistered", func(t *testing.T) {
		accepted, why := credentialJobIsAccepted(t, wf, "publish")
		require.False(t, accepted)
		require.Contains(t, why, "not one of the job shapes")
	})
	t.Run("registered judged and recorded", func(t *testing.T) {
		registerDispatchPublisherFixture(t, wf)
		established, why := acceptedExecution(t, wf, "publish")
		require.True(t, established, why)
		accepted, why := credentialJobIsAccepted(t, wf, "publish")
		require.True(t, accepted, why)
	})
	t.Run("unrecorded", func(t *testing.T) {
		registerDispatchPublisherFixture(t, wf)
		delete(recordedJobDigests, recordKey(wf.name, "publish"))
		accepted, why := credentialJobIsAccepted(t, wf, "publish")
		require.False(t, accepted)
		require.Contains(t, why, "no shape is recorded")
	})
	t.Run("no executing test", func(t *testing.T) {
		registerDispatchPublisherFixture(t, wf)
		permittedCredentialJobs[len(permittedCredentialJobs)-1].executedBy = ""
		accepted, why := credentialJobIsAccepted(t, wf, "publish")
		require.False(t, accepted)
		require.Contains(t, why, "Name the test that executes it")
	})
}

// Every mutation is re-recorded before asking the whole credential decision,
// so the record cannot accidentally be the thing that catches a broken proof.
func TestDispatchPublisherMutationsStayRefusedAfterReRecording(t *testing.T) {
	source := dispatchPublisherFixture(t)
	baseline := parseIsolatedDocument(t, source, "dispatch-publisher-fixture.yml")
	const refusal = "      - name: require the commit to be on the default branch, or refuse\n"
	const validation = "      - name: Validate the inputs\n"
	for _, tc := range []struct{ name, from, to, reason string }{
		{"party-chosen checkout beside a real refusal", "          fetch-depth: 0\n",
			"          fetch-depth: 0\n          ref: ${{ github.head_ref }}\n", "tree holds a different ref"},
		{"refusal speaks for a different commit", `--is-ancestor "$GITHUB_SHA"`,
			`--is-ancestor "$OTHER_SHA"`, "refusal ahead of everything"},
		{"ambiguous remote revision", `--is-ancestor "$GITHUB_SHA" "refs/remotes/origin/`,
			`--is-ancestor "$GITHUB_SHA" "origin/`, "refusal ahead of everything"},
		{"only the refusal's name remains", `if ! git merge-base --is-ancestor "$GITHUB_SHA" "refs/remotes/origin/${authority}"; then`,
			`if false; then`, "refusal ahead of everything"},
		{"conditional refusal", refusal, refusal + "        if: success()\n", "so it can be skipped"},
		{"non-fatal refusal", refusal, refusal + "        continue-on-error: true\n", "exit status stops nothing"},
		{"refusal never exits nonzero", "            exit 1\n", "            exit 0\n", "never exits non-zero"},
		{"step shell swallows refusal", refusal, refusal + "        shell: 'true {0}'\n", "which this package cannot judge"},
		{"job shell swallows refusal", "    runs-on: ubuntu-latest\n",
			"    runs-on: ubuntu-latest\n    defaults:\n      run:\n        shell: 'true {0}'\n", "which this package cannot judge"},
		{"workflow shell swallows refusal", "jobs:\n",
			"defaults:\n  run:\n    shell: 'true {0}'\njobs:\n", "which this package cannot judge"},
		{"execution before refusal", refusal, "      - run: ./build.sh\n" + refusal, "refusal ahead of everything"},
		{"action before refusal", refusal, "      - uses: docker/setup-buildx-action@v4\n" + refusal, "refusal ahead of everything"},
		{"second checkout after refusal", validation,
			"      - uses: actions/checkout@v7\n        with:\n          ref: ${{ github.head_ref }}\n" + validation,
			"tree holds a different ref"},
		{"tree repointed after refusal", validation,
			"      - run: git checkout FETCH_HEAD\n" + validation, "without showing it is reachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Contains(t, source, tc.from)
			mutated := strings.ReplaceAll(source, tc.from, tc.to)
			wf := parseIsolatedDocument(t, mutated, "dispatch-publisher-fixture.yml")
			registerDispatchPublisherFixture(t, baseline)
			key, digest := recordKey(wf.name, "publish"), canonicalJobDigest(t, wf, "publish")
			require.NotEqual(t, recordedJobDigests[key], digest)
			recordedJobDigests[key] = digest
			established, why := acceptedExecution(t, wf, "publish")
			require.False(t, established)
			require.Contains(t, why, tc.reason)
			accepted, why := credentialJobIsAccepted(t, wf, "publish")
			require.False(t, accepted)
			require.Contains(t, why, tc.reason)
			require.NotContains(t, why, "changed shape", "the judgement must decide after re-recording")
		})
	}
}

// Execute the fixture's actual refusal against real repositories. Its
// registration above names this test, not just a plausible-looking function.
func TestDispatchPublisherFixtureRefusesUnmergedCommits(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-read-token")
	wf := parseIsolatedDocument(t, dispatchPublisherFixture(t), "dispatch-publisher-fixture.yml")
	steps := wf.Jobs["publish"].Steps
	require.GreaterOrEqual(t, len(steps), 2)
	require.Equal(t, "require the commit to be on the default branch, or refuse", steps[1].Name)
	script := steps[1].Run
	require.NotEmpty(t, script)
	fixture := newReleaseFixture(t)
	for _, tc := range []struct {
		name, sha string
		admit     bool
	}{
		{"default branch tip", fixture.tip, true},
		{"older merged commit", fixture.older, true},
		{"unmerged commit", fixture.unmerged, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			admitted, output := fixture.run(t, script, theDefaultBranch, tc.sha)
			require.Equal(t, tc.admit, admitted, output)
		})
	}
	admitted, output := fixture.run(t, strings.ReplaceAll(script, "exit 1", "exit 0"), theDefaultBranch, fixture.unmerged)
	require.True(t, admitted, "neutralising the refusal must admit the unmerged commit: %s", output)
}
