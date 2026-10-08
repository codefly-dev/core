package ciguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every construction six review rounds produced, asked of the ALLOWLIST.
//
// Under the old rules each of these needed its own answer, and each round found
// routes the answers did not cover: a source outside the context list, a sink
// outside the field list, a verb outside the command list. None of them is
// answered individually here either. None is a registered shape, so all are
// refused by the first conjunct alone -- and so is the next one, which is the
// property six rounds of lists could not provide.
//
// That is this test's whole claim, and it is a narrow one: it says the
// allowlist discriminates, NOT that anything about these constructions was
// evaluated. Treating "the allowlist refuses what is not on it" as the whole
// credential decision is what let a REGISTERED job hold a write token while
// running a pull request's tree -- see
// TestThePlantedWriteTokenViolationStaysCaught below, which asks the judgement
// instead, and TestRepointingTheTagJobsTreeWithoutProvingTheCommitIsRefused,
// which puts several of the constructions below inside a registered job.
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

// The record pins the job and everything the job inherits, so a change to any
// of it stops matching -- including workflow-level `defaults:` and `env:`,
// which choose the interpreter and the environment every step in the job runs
// under.
func TestTheRecordPinsTheJobAndEverythingItInherits(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "go-service-release.yml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	before := digestOfJobIn(t, string(raw), "go-service-release.yml", "goreleaser")

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
			name: "a workflow-level permission the job would inherit",
			from: "jobs:\n",
			to:   "permissions:\n  contents: write\njobs:\n",
		},
		{
			name: "a trigger added, which changes the situations it answers for",
			from: "on:\n  workflow_call:\n",
			to:   "on:\n  pull_request:\n  workflow_call:\n",
		},
		{
			name: "a step added to the registered job",
			from: "      - name: Run GoReleaser\n",
			to:   "      - run: ./probe.sh\n      - name: Run GoReleaser\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Contains(t, string(raw), tc.from)
			mutated := strings.Replace(string(raw), tc.from, tc.to, 1)
			require.NotEqual(t, before,
				digestOfJobIn(t, mutated, "go-service-release.yml", "goreleaser"),
				"the change left the digest unchanged, so the record does not pin it")
		})
	}
}

// And the converse, which is the reason the record is scoped at all. It used to
// hash the whole file, so a Dependabot bump of an action pin in a job that holds
// nothing refused every registered job in the file: #705 moved three pins and
// two digests were re-recorded to clear it. Routine maintenance and a real
// violation then produced the same failure with the same one-line remedy, and a
// reviewer could not tell them apart.
func TestAChangeOutsideTheJobDoesNotForceAReRecord(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "go.yml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	before := digestOfJobIn(t, string(raw), "go.yml", "coverage-badge")

	const pin = "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0"
	require.Contains(t, string(raw), pin,
		"this fixture bumps an action pin that the registered job does not use")
	bumped := strings.ReplaceAll(string(raw), pin,
		"actions/setup-go@0000000000000000000000000000000000000000 # v7.0.1")
	require.NotContains(t, bumped, pin)

	require.Equal(t, before, digestOfJobIn(t, bumped, "go.yml", "coverage-badge"),
		"an action pin in a job that holds no credential changed the record of one "+
			"that does, so a dependency bump and a credential regression are again "+
			"the same failure")
}

// digestOfJobIn digests one job of a workflow given as text, so a mutation need
// not be written to disk to be judged.
func digestOfJobIn(t *testing.T, text, name, job string) string {
	t.Helper()
	wf := parseIsolatedDocument(t, text, name)
	wf.name = name
	return canonicalJobDigest(t, wf, job)
}

// The digest depends on content, not on formatting, or every whitespace change
// would read as a new shape.
func TestTheDigestIgnoresFormatting(t *testing.T) {
	compact := "on: push\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"
	spelled := "on: push\njobs:\n  j:\n    runs-on: \"ubuntu-latest\"\n    steps:\n    - run: 'echo hi'\n"
	require.Equal(t,
		digestOfJobIn(t, compact, "a.yml", "j"),
		digestOfJobIn(t, spelled, "b.yml", "j"))
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

// The planted violation, and every remediation a developer reaches for.
//
// This is the regression that matters most in this package, so it is asserted
// on the JUDGEMENT rather than on the suite: each case below is handed to
// `provablyUnreachable` and `acceptedExecution` directly, with no digest
// anywhere in the question. That is the property the previous design lacked.
// Its decision was `(workflow, job) ∈ allowlist && sha256(file) == constant`,
// so this exact violation went green by re-recording one 64-character string --
// the remediation the failure message printed.
//
// The violation is the one a review of #707 planted and landed: take
// `go.yml` · `coverage-badge`, which is registered and carries
// `contents: write`, give it a condition that no longer excludes
// `pull_request`, point its checkout at the pull request's own branch, and add
// a `run:` step executing a script out of that tree.
func TestThePlantedWriteTokenViolationStaysCaught(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "go.yml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	const (
		gate       = "    if: github.event_name == 'push' && github.ref == 'refs/heads/main'\n"
		admits     = "    if: always() && needs.build.result == 'success'\n"
		pinnedRef  = "          ref: main\n"
		chosenRef  = "          ref: ${{ github.head_ref }}\n"
		publisher  = "      - name: publish the coverage badge\n"
		treeScript = "      - name: render the badge\n        run: bash ./scripts/govulncheck.sh\n\n" +
			"      - name: publish the coverage badge\n"
		soundRefusal = "\n      - name: require the commit to be on the default branch, or refuse\n" +
			"        run: |\n          set -euo pipefail\n" +
			"          if ! git merge-base --is-ancestor \"$GITHUB_SHA\" \"refs/remotes/origin/main\"; then\n" +
			"            echo \"::error::not on main\" >&2\n            exit 1\n          fi\n"
	)
	// `coverage-badge` is the second job in go.yml whose `if:` reads this way
	// (`notify` is the other), and the mutations must land on the badge job, so
	// each replacement is anchored at the badge job's own text.
	badge := strings.Index(string(raw), "  coverage-badge:\n")
	require.NotEqual(t, -1, badge, "go.yml no longer has a coverage-badge job")
	head, tail := string(raw)[:badge], string(raw)[badge:]
	require.Contains(t, tail, gate)
	require.Contains(t, tail, pinnedRef)
	require.Contains(t, tail, publisher)

	plant := func(edits ...[2]string) string {
		out := tail
		for _, edit := range edits {
			replaced := strings.Replace(out, edit[0], edit[1], 1)
			require.NotEqual(t, out, replaced, "mutation did not apply: %q", edit[0])
			out = replaced
		}
		return head + out
	}

	violation := [][2]string{
		{gate, admits},
		{pinnedRef, chosenRef},
		{publisher, treeScript},
	}

	for _, tc := range []struct {
		name  string
		edits [][2]string
		// reason is a substring the refusal must name, so a case cannot pass by
		// being refused for some unrelated reason.
		reason string
	}{
		{
			name:  "the violation as planted",
			edits: violation,
		},
		{
			// The old design's entire remediation. It is not in this table as
			// a mutation because it cannot be: the digest is not consulted by
			// either limb, so re-recording is a no-op against the judgement.
			// The case below is the violation plus a refusal step whose NAME
			// matches the one the previous mechanism matched on.
			name: "a step borrowing the refusal's name",
			edits: append(append([][2]string{}, violation...), [2]string{
				"      - name: render the badge\n",
				"      - name: require the tag to be on the default branch, or refuse\n" +
					"        run: echo checked\n\n      - name: render the badge\n",
			}),
		},
		{
			name: "a real refusal, but skippable by a step-level if:",
			edits: append(append([][2]string{}, violation...), [2]string{
				chosenRef,
				chosenRef + strings.Replace(soundRefusal,
					"        run: |\n", "        if: github.event_name == 'push'\n        run: |\n", 1),
			}),
		},
		{
			name: "a real refusal, but made non-fatal",
			edits: append(append([][2]string{}, violation...), [2]string{
				chosenRef,
				chosenRef + strings.Replace(soundRefusal,
					"        run: |\n", "        continue-on-error: true\n        run: |\n", 1),
			}),
		},
		{
			// The hole a probe of THIS guard found: an ancestry refusal speaks
			// for `$GITHUB_SHA`, and the tree here holds `github.head_ref`.
			name: "a real, unconditional refusal beside a party-chosen checkout ref",
			edits: append(append([][2]string{}, violation...),
				[2]string{chosenRef, chosenRef + soundRefusal}),
		},
		{
			// The one shape whose MECHANISM is sound: the tree is the
			// triggering commit and the refusal speaks for exactly that. On a
			// pull request `$GITHUB_SHA` is the merge commit, which is not an
			// ancestor of the default branch, so the job exits before any step
			// runs tree content -- and the invariant is "no job RUNS pull
			// request code with a write token", not "no write token sits in a
			// reachable job".
			//
			// It is still refused, and by the last thing standing: a refusal
			// written in shell is read here, never executed, so the template
			// has to name a test that executes it. `coverage-badge` names none,
			// because it had no script of its own. A developer taking this
			// route has to write that test, which is the friction the two jobs
			// that DO take it (`version-tag.yml` · `tag` and
			// `go-service-release.yml` · `goreleaser`) already pay.
			name: "the triggering commit, refused before anything runs",
			edits: append(append([][2]string{}, violation[0], violation[2]),
				[2]string{pinnedRef, soundRefusal}),
			reason: "Name the test that executes it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := parseIsolatedDocument(t, plant(tc.edits...), "go.yml")
			wf.name = "go.yml"

			unreachable, why := provablyUnreachable(t,
				wf.Jobs["coverage-badge"].If, hostileScenariosFor(wf.On))
			require.False(t, unreachable,
				"this fixture depends on the job being reachable from a pull request (%s)", why)

			established, how := acceptedExecution(t, wf, "coverage-badge")
			require.False(t, established,
				"this violation was accepted, and no digest was consulted to accept it")
			if tc.reason != "" {
				require.Contains(t, how, tc.reason,
					"refused, but for a different reason than the case is about")
			}
		})
	}

	// And the clean tree, so the table above is discriminating rather than
	// refusing everything.
	wf := parseIsolatedDocument(t, string(raw), "go.yml")
	wf.name = "go.yml"
	accepted, missing := credentialJobIsAccepted(t, wf, "coverage-badge")
	require.True(t, accepted, "the unmodified coverage-badge job was refused: %s", missing)
}

// `version-tag.yml` · `tag` is the one registered job that deliberately puts a
// different commit's content in its tree: it checks out the default branch,
// proves the triggering sha is reachable from it, and only then
// `git switch --detach`es onto it. The order is the whole safety argument, so
// these are the ways of breaking it — each refused, and refused without the
// record being consulted.
//
// Without this, `treeRepointedFromARev` and `treeRepointedFromAStream` would be
// machinery nothing exercises, which is the shape this change exists to remove.
func TestRepointingTheTagJobsTreeWithoutProvingTheCommitIsRefused(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "version-tag.yml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	const (
		refusal  = "          if ! git merge-base --is-ancestor \"$SHA\" \"refs/remotes/origin/${DEFAULT_BRANCH}\"; then\n"
		switchTo = "          git switch --detach \"$SHA\"\n"
	)
	require.Contains(t, string(raw), refusal,
		"the ancestry refusal this test breaks is not where it was")
	require.Contains(t, string(raw), switchTo,
		"the tree selection this test breaks is not where it was")

	// Unbroken, the job is accepted -- so the cases below fail for the reason
	// they are about rather than because this job never passed.
	clean := parseIsolatedDocument(t, string(raw), "version-tag.yml")
	clean.name = "version-tag.yml"
	established, how := acceptedExecution(t, clean, "tag")
	require.True(t, established, "the unmodified tag job was refused: %s", how)

	for _, tc := range []struct{ name, from, to, names string }{
		{
			name: "the refusal removed, leaving the switch",
			from: refusal, to: "          if false; then\n",
			names: "$SHA",
		},
		{
			name: "the switch pointed at a fetched ref the refusal never validated",
			from: switchTo, to: "          git switch --detach FETCH_HEAD\n",
			names: "FETCH_HEAD",
		},
		{
			name: "one file restored from a ref the refusal never validated",
			from: switchTo, to: "          git restore --source=FETCH_HEAD -- version/info.codefly.yaml\n",
			names: "FETCH_HEAD",
		},
		{
			name: "an archive unpacked over the tree, naming no commit to validate",
			from: switchTo, to: "          git archive FETCH_HEAD | tar -x\n",
			names: "without naming a commit",
		},
		{
			name: "a patch applied, naming no commit to validate",
			from: switchTo, to: "          git diff HEAD origin/topic | git apply\n",
			names: "without naming a commit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := strings.Replace(string(raw), tc.from, tc.to, 1)
			require.NotEqual(t, string(raw), mutated, "mutation did not apply")

			wf := parseIsolatedDocument(t, mutated, "version-tag.yml")
			wf.name = "version-tag.yml"
			established, how := acceptedExecution(t, wf, "tag")
			require.False(t, established,
				"the tag job's tree was repointed to something no refusal validated "+
					"and the execution guard accepted it")
			require.Contains(t, how, tc.names,
				"refused, but for a different reason than the case is about")
		})
	}
}
