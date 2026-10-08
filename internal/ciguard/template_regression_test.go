package ciguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every construction six review rounds produced, asked of the template rule.
//
// Under the old rules each of these needed its own answer, and each round found
// routes the answers did not cover: a source outside the context list, a sink
// outside the field list, a verb outside the command list. The template rule
// does not answer them individually. None of them is a registered shape, so all
// of them are refused -- and so is the next one, which is the property six
// rounds of lists could not provide.
//
// They are kept as fixtures rather than deleted, because a rule that refuses
// everything is worthless: TestThePermittedShapesAreStillAccepted is the other
// half, and the two together say the rule discriminates.
func TestNoConstructionMatchesAPermittedShape(t *testing.T) {
	for _, construction := range []struct{ name, job string }{
		// Sources outside any context list.
		{"a step output", `    steps:
      - run: echo "task=$(cat payload)" >> $GITHUB_OUTPUT
        id: s
      - run: eval "${{ steps.s.outputs.task }}"`},
		{"the event payload on disk", `    steps:
      - run: eval "$(jq -r .x "$GITHUB_EVENT_PATH")"`},
		{"the head ref", `    steps:
      - run: eval "${{ github.head_ref }}"`},
		{"the whole github context", `    steps:
      - run: eval "${{ toJSON(github) }}"`},

		// Sinks outside any field list.
		{"github-script reading the environment", `    steps:
      - uses: actions/github-script@v7
        with: {script: "eval(process.env.TASK)"}`},
		{"a value written to a file and run", `    steps:
      - run: |
          printf '%s' "$TASK" > f
          bash f`},
		{"a binary on PATH overwritten", `    steps:
      - run: printf '%s' "$TASK" > /usr/local/bin/go
      - run: go test ./...`},
		{"a node preload", `    steps:
      - run: echo "NODE_OPTIONS=--require ./p.js" >> $GITHUB_ENV
      - run: node -e ""`},
		{"a working directory from an input", `    steps:
      - run: go test ./...
        working-directory: ${{ inputs.dir }}`},

		// Verbs outside any command list.
		{"a checkout through -C", `    steps:
      - run: git -C . checkout FETCH_HEAD`},
		{"one file restored from a fetched ref", `    steps:
      - run: git restore --source=FETCH_HEAD -- .goreleaser.yaml`},
		{"a patch applied", `    steps:
      - run: git diff HEAD origin/topic | git apply`},
		{"an archive unpacked over the tree", `    steps:
      - run: git archive FETCH_HEAD | tar -x`},

		// And the shape-level one: a nested composite step reusing the
		// refusal's name.
		{"a step borrowing the refusal's name", `    steps:
      - name: require the tag to be on the default branch, or refuse
        run: echo not the refusal
      - run: go test ./...`},
	} {
		t.Run(construction.name, func(t *testing.T) {
			document := "on: pull_request_target\njobs:\n  probe:\n    runs-on: ubuntu-latest\n" +
				"    env: {CREDENTIAL: \"${{ secrets.BUNDLED_CONFIG }}\"}\n" + construction.job + "\n"
			wf := parseIsolatedDocument(t, document, construction.name)

			require.NotEmpty(t, secretsIn(t, wf, "probe"),
				"this fixture depends on the job holding a credential")
			accepted, missing := credentialJobIsAccepted(t, wf, "probe")
			require.False(t, accepted,
				"a construction was accepted, which under the template rule can only "+
					"mean it matched a registered shape (guard said %q)", missing)
		})
	}
}

// The other half: the shapes this repository does run are accepted, so the rule
// distinguishes rather than simply refusing.
func TestThePermittedShapesAreStillAccepted(t *testing.T) {
	_, workflows := loadIsolatedWorkflows(t)

	for _, template := range permittedCredentialJobs {
		t.Run(template.workflow+"/"+template.job, func(t *testing.T) {
			wf := workflows[filepath.Join(repoRoot(t), ".github", "workflows", template.workflow)]
			accepted, missing := credentialJobIsAccepted(t, wf, template.job)
			require.True(t, accepted, "a permitted shape was refused: %s", missing)
		})
	}
}

// The record pins the WHOLE workflow document, so a change anywhere in it --
// including workflow-level `defaults:` and `env:`, which choose the interpreter
// and the environment every step in the job runs under -- stops matching.
func TestAnyChangeToAPermittedWorkflowStopsItMatching(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "go-service-release.yml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	before := canonicalWorkflowDigest(t, path)

	for _, tc := range []struct{ name, from, to string }{
		{
			name: "a workflow-level shell that neutralises the refusal",
			from: "jobs:\n",
			to:   "defaults:\n  run:\n    shell: 'true {0}'\njobs:\n",
		},
		{
			name: "a workflow-level environment that redefines a command",
			from: "jobs:\n",
			to:   "env:\n  BASH_ENV: .github/release-env.sh\njobs:\n",
		},
		{
			name: "a step added to the registered job",
			from: "      - name: Run GoReleaser\n",
			to:   "      - run: ./probe.sh\n      - name: Run GoReleaser\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Contains(t, string(raw), tc.from)
			mutated := filepath.Join(t.TempDir(), "go-service-release.yml")
			require.NoError(t, os.WriteFile(mutated,
				[]byte(strings.Replace(string(raw), tc.from, tc.to, 1)), 0o600))
			require.NotEqual(t, before, canonicalWorkflowDigest(t, mutated),
				"the change left the digest unchanged, so the record does not pin it")
		})
	}
}

// The digest depends on content, not on formatting, or every whitespace change
// would read as a new shape.
func TestTheDigestIgnoresFormatting(t *testing.T) {
	dir := t.TempDir()
	compact := filepath.Join(dir, "a.yml")
	spelled := filepath.Join(dir, "b.yml")
	require.NoError(t, os.WriteFile(compact,
		[]byte("on: push\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"), 0o600))
	require.NoError(t, os.WriteFile(spelled,
		[]byte("on: push\njobs:\n  j:\n    runs-on: \"ubuntu-latest\"\n    steps:\n    - run: 'echo hi'\n"), 0o600))
	require.Equal(t, canonicalWorkflowDigest(t, compact), canonicalWorkflowDigest(t, spelled))
	_ = sortedTemplateNames()
}

// A registered job must run no repository file that the record does not pin.
// The dependency-combining publish job is the case: it is the one registered job
// that invoked a script from the tree.
func TestARegisteredJobRunsNoUnpinnedRepositoryFile(t *testing.T) {
	_, workflows := loadIsolatedWorkflows(t)

	for _, template := range permittedCredentialJobs {
		t.Run(template.workflow+"/"+template.job, func(t *testing.T) {
			wf := workflows[filepath.Join(repoRoot(t), ".github", "workflows", template.workflow)]
			for _, step := range wf.Jobs[template.job].Steps {
				for _, script := range scriptReference.FindAllString(step.Run, -1) {
					require.Contains(t, recordedFileDigests, script,
						"%s/%s runs %s, which the record does not pin. Either inline it "+
							"into the workflow, so the workflow digest covers it, or "+
							"record its content digest.",
						template.workflow, template.job, script)
					require.Equal(t, recordedFileDigests[script],
						canonicalFileDigest(t, filepath.Join(repoRoot(t), script)),
						"%s has changed and the record does not reflect it", script)
				}
			}
		})
	}
}

// The decision's own comparison, witnessed: a changed workflow handed to it is
// refused. Before this, the digest was recorded by one test and the decision
// compared a field nobody set, so the decision accepted every registered name
// whatever its content.
func TestTheDecisionRefusesAChangedWorkflow(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "go-service-release.yml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	unchanged := parseIsolatedDocument(t, string(raw), "go-service-release.yml")
	unchanged.name = "go-service-release.yml"
	accepted, missing := credentialJobIsAccepted(t, unchanged, "goreleaser")
	require.True(t, accepted, "the unchanged workflow was refused: %s", missing)

	for _, tc := range []struct{ name, from, to string }{
		{"a neutralising workflow shell", "jobs:\n", "defaults:\n  run:\n    shell: 'true {0}'\njobs:\n"},
		{"a workflow environment that redefines a command", "jobs:\n", "env:\n  BASH_ENV: .github/release-env.sh\njobs:\n"},
		{"a step added before the publisher", "      - name: Run GoReleaser\n", "      - run: ./probe.sh\n      - name: Run GoReleaser\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Contains(t, string(raw), tc.from)
			changed := parseIsolatedDocument(t,
				strings.Replace(string(raw), tc.from, tc.to, 1), "go-service-release.yml")
			changed.name = "go-service-release.yml"
			accepted, missing := credentialJobIsAccepted(t, changed, "goreleaser")
			require.False(t, accepted,
				"a changed workflow was accepted, so the decision is not comparing "+
					"what it was handed (guard said %q)", missing)
			require.Contains(t, missing, "has changed shape")
		})
	}
}
