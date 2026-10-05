package ciguard

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// untrusted_code_test.go answers what the BUILT-IN token can do. That is one
// credential, and `permissions:` is the only thing that governs it.
//
// A repository secret is a different object and `permissions:` says nothing
// about it. A PAT handed to a step is as privileged as whoever issued it, so a
// job that runs code under review and references ANY secret has handed that
// credential to the code — whatever its `permissions:` block says. Narrowing
// GITHUB_TOKEN to read while a release PAT sits in the same job is not
// isolation; it is a narrower token beside a wider one.
//
// So this file asks a different question of every job: can code the author of a
// pull request wrote reach a secret from here? The answer has to be no, and the
// only structure that makes it no is the one the write split already needs —
// the credential lives in a job gated to a merged ref that runs no checked-out
// code of the pull request.
//
// Three places failed this while passing every check in untrusted_code_test.go:
// a reusable release workflow whose publishing job took the caller's PAT with
// no event or ref condition, a dependency-combining job that held its token
// while executing a tree it had just assembled from unmerged heads, and the
// job running the suite, which named a webhook secret in a step it skips.

// secretExpression finds a `secrets.NAME` reference anywhere in a workflow,
// including inside a `run:` script, which is where a credential is most easily
// smuggled past a guard that only reads `env:`.
var secretExpression = regexp.MustCompile(`secrets\.([A-Za-z_][A-Za-z0-9_-]*)`)

// builtInToken is exempt. It is injected into every job whether or not it is
// named, so refusing the name would refuse the thing that is already there;
// what it can DO is governed by `permissions:`, which untrusted_code_test.go
// holds. Every other name is a repository or organisation secret that would
// not be present unless this workflow asked for it.
const builtInToken = "GITHUB_TOKEN"

// isolatedJob is a job carrying everything needed to decide whether a
// credential in it is reachable from code under review.
type isolatedJob struct {
	If          string            `yaml:"if"`
	Permissions yaml.Node         `yaml:"permissions"`
	Env         map[string]string `yaml:"env"`
	Secrets     yaml.Node         `yaml:"secrets"`
	Uses        string            `yaml:"uses"`
	Steps       []struct {
		Name string            `yaml:"name"`
		Uses string            `yaml:"uses"`
		If   string            `yaml:"if"`
		Run  string            `yaml:"run"`
		Env  map[string]string `yaml:"env"`
		With map[string]any    `yaml:"with"`
	} `yaml:"steps"`
}

type isolatedWorkflow struct {
	On   yaml.Node              `yaml:"on"`
	Jobs map[string]isolatedJob `yaml:"jobs"`
}

// secretsIn returns every non-built-in secret name the job references, with
// where each was found, so a failure points at the line to change.
func secretsIn(job isolatedJob) map[string][]string {
	found := map[string][]string{}
	note := func(where, text string) {
		for _, m := range secretExpression.FindAllStringSubmatch(text, -1) {
			if m[1] == builtInToken {
				continue
			}
			found[m[1]] = append(found[m[1]], where)
		}
	}

	for name, value := range job.Env {
		note("the job's env "+name, value)
	}
	// `secrets: inherit` on a job that calls a reusable workflow hands over
	// every secret the caller holds, which is the widest form there is.
	if job.Secrets.Kind != 0 {
		var rendered strings.Builder
		if err := yaml.NewEncoder(&rendered).Encode(job.Secrets); err == nil {
			note("the job's secrets:", rendered.String())
		}
		if strings.TrimSpace(job.Secrets.Value) == "inherit" {
			found["(inherit: every secret the caller holds)"] = []string{"the job's secrets:"}
		}
	}
	for _, step := range job.Steps {
		where := "step " + step.Name
		if step.Name == "" {
			where = "step `uses: " + step.Uses + "`"
		}
		for name, value := range step.Env {
			note(where+" env "+name, value)
		}
		for key, value := range step.With {
			if s, ok := value.(string); ok {
				note(where+" with."+key, s)
			}
		}
		note(where+" run:", step.Run)
		note(where+" if:", step.If)
	}
	return found
}

// clausesOf splits an `if:` into its top-level `&&` terms, and reports whether
// the expression contains an `||`.
//
// The guards in untrusted_code_test.go originally asked whether a condition
// CONTAINED a required substring, which `(<required>) || true` satisfies while
// meaning the opposite. A security condition is read as a conjunction or it is
// not read at all.
func clausesOf(gate string) (clauses []string, hasAlternative bool) {
	flat := strings.Join(strings.Fields(gate), " ")
	if flat == "" {
		return nil, false
	}
	if strings.Contains(flat, "||") {
		return nil, true
	}
	for _, clause := range strings.Split(flat, "&&") {
		if trimmed := unwrap(strings.TrimSpace(clause)); trimmed != "" {
			clauses = append(clauses, trimmed)
		}
	}
	return clauses, false
}

// unwrap removes parentheses that enclose the WHOLE clause, and only those.
// Trimming a leading "(" and a trailing ")" blindly mangles any clause ending
// in a call -- `startsWith(github.ref, 'refs/tags/')` becomes unparseable and
// then never matches, which would silently exempt every job gated on a tag.
func unwrap(clause string) string {
	for len(clause) > 1 && clause[0] == '(' && clause[len(clause)-1] == ')' {
		depth := 0
		enclosing := true
		for i, r := range clause {
			switch r {
			case '(':
				depth++
			case ')':
				depth--
			}
			// Depth returning to zero before the end means the opening paren
			// closed early, so the pair does not enclose the clause.
			if depth == 0 && i < len(clause)-1 {
				enclosing = false
				break
			}
		}
		if !enclosing {
			return clause
		}
		clause = strings.TrimSpace(clause[1 : len(clause)-1])
	}
	return clause
}

// hasClause reports whether gate requires want as one of its conjuncts. An
// expression with an `||` requires nothing: one branch of it is enough.
func hasClause(gate, want string) bool {
	clauses, hasAlternative := clausesOf(gate)
	if hasAlternative {
		return false
	}
	for _, clause := range clauses {
		if clause == want {
			return true
		}
	}
	return false
}

// trustedRefClauses are the conditions that confine a job to a ref this
// repository has reviewed: a push of the default branch, or a pushed tag. Each
// has to appear beside `github.event_name == 'push'`, because a ref comparison
// alone is satisfied by other events that set the same ref.
var trustedRefClauses = []string{
	"github.ref == 'refs/heads/main'",
	"startsWith(github.ref, 'refs/tags/')",
}

// runsOnlyOnATrustedRef reports whether gate confines a job to a merged ref.
func runsOnlyOnATrustedRef(gate string) bool {
	if !hasClause(gate, "github.event_name == 'push'") {
		return false
	}
	for _, clause := range trustedRefClauses {
		if hasClause(gate, clause) {
			return true
		}
	}
	return false
}

// reachableFromAPullRequest reports whether code an author of a pull request
// wrote can run in this workflow.
//
// `workflow_call` counts. A reusable workflow runs the CALLER's tree at a ref
// the caller picks, and nothing here can see whether that caller dispatches it
// from a pull request — so it has to be assumed, which is also what makes a
// secret in such a workflow the caller's secret to lose.
func reachableFromAPullRequest(on yaml.Node) bool {
	for _, trigger := range triggers(on) {
		switch trigger {
		case "pull_request", "pull_request_target", "workflow_call":
			return true
		}
	}
	return false
}

func loadIsolatedWorkflows(t *testing.T) ([]string, map[string]isolatedWorkflow) {
	t.Helper()

	paths := workflowFiles(t)
	out := make(map[string]isolatedWorkflow, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		var wf isolatedWorkflow
		require.NoError(t, yaml.Unmarshal(raw, &wf), path)
		out[path] = wf
	}
	return paths, out
}

func isolatedJobIDs(wf isolatedWorkflow) []string {
	ids := make([]string, 0, len(wf.Jobs))
	for id := range wf.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// The rule, stated once: a job that can run code under review references no
// secret, and a job that references a secret runs on a merged ref and checks
// out nothing of the pull request.
//
// A step-level `if:` does not satisfy this. It decides whether that STEP runs,
// not what the job is; the credential is still named in a job whose other
// steps execute the author's code, and a reader has to reason about step order
// to see whether it is safe. The split into two jobs is what makes it legible
// as well as true.
func TestNoSecretIsReachableFromAJobThatRunsCodeUnderReview(t *testing.T) {
	paths, workflows := loadIsolatedWorkflows(t)

	checked := 0
	for _, path := range paths {
		wf := workflows[path]
		if !reachableFromAPullRequest(wf.On) {
			continue
		}
		checked++

		for _, id := range isolatedJobIDs(wf) {
			job := wf.Jobs[id]
			secrets := secretsIn(job)
			if len(secrets) == 0 {
				continue
			}
			names := make([]string, 0, len(secrets))
			for name := range secrets {
				names = append(names, name+" ("+strings.Join(secrets[name], ", ")+")")
			}
			sort.Strings(names)

			require.True(t, runsOnlyOnATrustedRef(job.If),
				"%s: job %q can run code under review and references %s. "+
					"`permissions:` does not govern a repository secret, so narrowing "+
					"the built-in token leaves this credential exactly as reachable as "+
					"before. Move it into a job that runs on a merged ref -- "+
					"`github.event_name == 'push'` with either %s -- and that checks out "+
					"none of the pull request's code. A step-level `if:` is not enough: "+
					"it gates the step, not the job the credential lives in.",
				filepath.Base(path), id, strings.Join(names, "; "),
				strings.Join(trustedRefClauses, " or "))
		}
	}
	require.NotZero(t, checked,
		"no workflow is reachable from a pull request, so this guard proves nothing")
}

// A credential-bearing job must also not SELECT the pull request's code, which
// is a separate thing from not holding a write token: `actions/checkout` with a
// caller-chosen ref, or a bare checkout in a reusable workflow, takes whatever
// tree the caller pointed at.
//
// The one legitimate case -- version-tag's tag job, which must read the version
// file from the commit that passed -- selects it only after proving it is
// reachable from the default branch. That proof is what this requires of any
// job that reaches for a triggering or caller-supplied sha.
func TestAJobThatSelectsASuppliedCommitProvesItIsOnTheDefaultBranch(t *testing.T) {
	paths, workflows := loadIsolatedWorkflows(t)

	for _, path := range paths {
		wf := workflows[path]
		for _, id := range isolatedJobIDs(wf) {
			job := wf.Jobs[id]

			selects := false
			for _, step := range job.Steps {
				if ref, ok := step.With["ref"].(string); ok {
					for _, field := range triggeringCommitRefs {
						if strings.Contains(ref, field) {
							selects = true
						}
					}
				}
				// A shell checkout reaches the same tree without ever touching
				// `with.ref`, which is the gap a guard reading only the action's
				// inputs leaves open.
				for _, verb := range []string{"git checkout", "git switch", "git reset --hard"} {
					if strings.Contains(step.Run, verb) {
						selects = true
					}
				}
			}
			if !selects {
				continue
			}

			proves := false
			for _, step := range job.Steps {
				if strings.Contains(step.Run, "merge-base --is-ancestor") {
					proves = true
				}
			}
			require.True(t, proves,
				"%s: job %q selects a commit it was handed (an action `ref:` or a "+
					"shell checkout) but no step proves that commit is reachable from "+
					"the default branch. Add a `git merge-base --is-ancestor` check "+
					"that refuses before the commit is selected, as version-tag.yml's "+
					"tag job does.",
				filepath.Base(path), id)
		}
	}
}

// clausesOf and hasClause decide every condition assertion in this package, so
// a form they misread is an escape hatch. `|| true` is the one that matters:
// appending it to any required condition makes the condition vacuous while
// leaving the required text in place for a substring check to find.
func TestConditionReadingRefusesAnAlternative(t *testing.T) {
	const required = "github.event_name == 'push'"

	for _, tc := range []struct {
		name string
		gate string
		has  bool
	}{
		{name: "the clause alone", gate: required, has: true},
		{
			name: "one conjunct among several",
			gate: "github.event_name == 'push' && github.ref == 'refs/heads/main'",
			has:  true,
		},
		{
			name: "wrapped across lines, as a folded block leaves it",
			gate: "github.event.workflow_run.conclusion == 'success' &&\n  " + required,
			has:  true,
		},
		{name: "parenthesised", gate: "(" + required + ") && always()", has: true},
		{name: "absent", gate: "always()", has: false},
		{name: "no condition at all", gate: "", has: false},

		// The escape hatches. Each leaves the required text present, so a
		// substring check passes it; each makes the condition mean nothing.
		{name: "neutralised with || true", gate: required + " || true", has: false},
		{name: "neutralised with a second branch", gate: required + " || github.event_name == 'pull_request'", has: false},
		{name: "an alternative anywhere in the expression", gate: "always() && (" + required + " || true)", has: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.has, hasClause(tc.gate, required))
		})
	}
}

// runsOnlyOnATrustedRef needs BOTH halves: an event and a ref. A ref
// comparison alone is satisfied by events other than a push that set the same
// ref, and an event alone says nothing about which ref.
func TestATrustedRefNeedsBothAnEventAndARef(t *testing.T) {
	for _, tc := range []struct {
		name    string
		gate    string
		trusted bool
	}{
		{name: "push of the default branch", gate: "github.event_name == 'push' && github.ref == 'refs/heads/main'", trusted: true},
		{name: "pushed tag", gate: "github.event_name == 'push' && startsWith(github.ref, 'refs/tags/')", trusted: true},
		{name: "with always(), as a notify job needs", gate: "always() && github.event_name == 'push' && github.ref == 'refs/heads/main'", trusted: true},
		{name: "the ref without the event", gate: "github.ref == 'refs/heads/main'", trusted: false},
		{name: "the event without a ref", gate: "github.event_name == 'push'", trusted: false},
		{name: "a different branch", gate: "github.event_name == 'push' && github.ref == 'refs/heads/wip'", trusted: false},
		{name: "neutralised", gate: "github.event_name == 'push' && github.ref == 'refs/heads/main' || true", trusted: false},
		{name: "nothing", gate: "", trusted: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.trusted, runsOnlyOnATrustedRef(tc.gate))
		})
	}
}

// secretsIn decides the first assertion, so a place it does not look is a
// place a credential can sit unnoticed. These are the shapes that carry one.
func TestSecretsInFindsEveryPlaceACredentialCanSit(t *testing.T) {
	const doc = `
jobs:
  everything:
    env:
      JOB_LEVEL: ${{ secrets.JOB_ENV_SECRET }}
    steps:
      - name: step env
        env:
          STEP_LEVEL: ${{ secrets.STEP_ENV_SECRET }}
        run: echo hi
      - name: action input
        uses: some/action@v1
        with:
          token: ${{ secrets.WITH_SECRET }}
      - name: inline in a script
        run: |
          curl -H "Authorization: ${{ secrets.RUN_SECRET }}" https://example.test
      - name: the built-in token is exempt
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: gh api /rate_limit
  calls:
    uses: ./.github/workflows/other.yml
    secrets: inherit
`
	var wf isolatedWorkflow
	require.NoError(t, yaml.Unmarshal([]byte(doc), &wf))

	found := secretsIn(wf.Jobs["everything"])
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	require.Equal(t,
		[]string{"JOB_ENV_SECRET", "RUN_SECRET", "STEP_ENV_SECRET", "WITH_SECRET"},
		names,
		"a shape that carries a credential is not being read; GITHUB_TOKEN is "+
			"exempt on purpose because it is present whether or not it is named")

	require.Contains(t, secretsIn(wf.Jobs["calls"]),
		"(inherit: every secret the caller holds)",
		"`secrets: inherit` hands over every secret the caller has, which is "+
			"wider than any single name and must not read as no secret at all")
}

// scriptReference finds a repository script a step invokes, in the forms the
// workflows here use: `bash .github/scripts/x.sh` and `.github/scripts/x.sh`.
// A guard that reads only the workflow stops at the `run:` line and never sees
// what the script does, which is where both dependency-combining routes lived.
var scriptReference = regexp.MustCompile(`\.github/scripts/[A-Za-z0-9_.-]+\.sh`)

// commandsOf returns everything a job executes: each step's `run:`, plus the
// contents of every repository script those runs invoke, transitively.
func commandsOf(t *testing.T, job isolatedJob) string {
	t.Helper()

	var all strings.Builder
	pending := []string{}
	for _, step := range job.Steps {
		all.WriteString(step.Run)
		all.WriteString("\n")
		pending = append(pending, scriptReference.FindAllString(step.Run, -1)...)
	}

	seen := map[string]bool{}
	for len(pending) > 0 {
		rel := pending[0]
		pending = pending[1:]
		if seen[rel] {
			continue
		}
		seen[rel] = true

		body, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
		if os.IsNotExist(err) {
			// A script the repository does not ship cannot be read, and that is
			// itself worth knowing: the dependency-combining script invokes an
			// optional hook that is absent here, so the route it opens is
			// latent rather than live. Recorded, not treated as clean.
			all.WriteString("\n# absent: " + rel + "\n")
			continue
		}
		require.NoError(t, err, rel)
		all.Write(body)
		all.WriteString("\n")
		pending = append(pending, scriptReference.FindAllString(string(body), -1)...)
	}
	return all.String()
}

// A job holding a privileged credential must not reach for a pull request's
// refs at all.
//
// `refs/pull/<n>/head` is whatever was last pushed to that branch. Filtering
// the pull requests by author establishes who OPENED each one, not who wrote
// the commits now on it, so a tree assembled from those refs is unreviewed by
// construction -- and a job that assembles it while holding a token is one
// script away from executing it. Assembling the tree is legitimate work; it
// belongs in a job with no credential, which hands the result on as an
// artifact.
func TestNoCredentialBearingJobFetchesPullRequestRefs(t *testing.T) {
	paths, workflows := loadIsolatedWorkflows(t)

	for _, path := range paths {
		wf := workflows[path]
		for _, id := range isolatedJobIDs(wf) {
			job := wf.Jobs[id]
			if len(secretsIn(job)) == 0 {
				continue
			}
			commands := commandsOf(t, job)
			for _, reach := range []string{"refs/pull/", "git cherry-pick"} {
				require.NotContains(t, commands, reach,
					"%s: job %q holds a repository secret and its commands (including "+
						"the repository scripts they invoke) reach for %q. A pull "+
						"request's head is whatever was last pushed to that branch, so "+
						"the tree this assembles is unreviewed; the credential must not "+
						"be in the job that assembles it. Split the assembly into a job "+
						"with no secret and pass the result on as an artifact.",
					filepath.Base(path), id, reach)
			}
		}
	}
}

// `workflow_dispatch` is not restricted to the default branch: a dispatch names
// a ref, the workflow and the repository content both come from it, and the
// secrets come from the repository regardless. So a credential-bearing job in a
// dispatchable workflow must select the default branch explicitly rather than
// taking the dispatched ref as its tree.
//
// This cannot be complete, and the limit is worth stating where the guard is: a
// dispatch against a branch runs THAT branch's copy of the workflow file, so a
// branch that edits the workflow is outside what any assertion about the file
// on the default branch can reach. What this does close is the realistic case
// -- a branch whose scripts differ while the workflow does not -- and the rest
// is a restriction on who may dispatch, which lives in repository settings.
func TestADispatchableCredentialJobChecksOutTheDefaultBranch(t *testing.T) {
	paths, workflows := loadIsolatedWorkflows(t)

	for _, path := range paths {
		wf := workflows[path]
		if !contains(triggers(wf.On), "workflow_dispatch") {
			continue
		}

		for _, id := range isolatedJobIDs(wf) {
			job := wf.Jobs[id]
			if len(secretsIn(job)) == 0 {
				continue
			}
			for _, step := range job.Steps {
				if !strings.Contains(step.Uses, "actions/checkout@") {
					continue
				}
				ref, _ := step.With["ref"].(string)
				require.Equal(t, "main", strings.TrimSpace(ref),
					"%s: job %q holds a repository secret in a workflow that can be "+
						"dispatched against any ref, and its checkout takes %q rather "+
						"than `ref: main`. A dispatch against a feature branch would "+
						"then run that branch's scripts with the credential. Pin the "+
						"checkout to the default branch.",
					filepath.Base(path), id, ref)
			}
		}
	}
}

// commandsOf decides the two assertions above, so a script it fails to follow
// is a script whose contents are exempt from them.
func TestCommandsOfFollowsScriptsAJobInvokes(t *testing.T) {
	const doc = `
jobs:
  invokes:
    steps:
      - run: bash .github/scripts/combine-deps-plan.sh /tmp/out
      - run: echo inline-only
`
	var wf isolatedWorkflow
	require.NoError(t, yaml.Unmarshal([]byte(doc), &wf))

	commands := commandsOf(t, wf.Jobs["invokes"])
	require.Contains(t, commands, "echo inline-only",
		"a step's own run: must be read")
	require.Contains(t, commands, "refs/pull/",
		"the invoked script's CONTENTS must be read, or everything a job does "+
			"inside a script is exempt from the guards above -- and this is the "+
			"exact string the pull-request-ref guard looks for")
}

// A tag is not a statement about review. `git push origin <tag>` points a name
// at any commit in the repository, merged or not, and the ref condition that
// keeps a credential-bearing job off pull requests is satisfied the moment such
// a tag exists.
//
// So a job gated on a tag, holding a secret, has to establish the one thing the
// tag does not: that the commit is reachable from the default branch. Without
// that the gate moves the problem rather than closing it — a release would run
// the hooks and configuration of a tree nobody merged, with the credential.
func TestATagGatedCredentialJobProvesTheTagIsOnTheDefaultBranch(t *testing.T) {
	paths, workflows := loadIsolatedWorkflows(t)

	checked := 0
	for _, path := range paths {
		wf := workflows[path]
		for _, id := range isolatedJobIDs(wf) {
			job := wf.Jobs[id]
			if len(secretsIn(job)) == 0 {
				continue
			}
			if !hasClause(job.If, "startsWith(github.ref, 'refs/tags/')") {
				continue
			}
			checked++

			proves := false
			for _, step := range job.Steps {
				if strings.Contains(step.Run, "merge-base --is-ancestor") {
					proves = true
				}
			}
			require.True(t, proves,
				"%s: job %q holds a repository secret and is gated on a pushed tag, "+
					"but nothing checks that the tag's commit is on the default "+
					"branch. A tag can be pushed to any commit, so the gate alone "+
					"admits an unmerged tree -- and this job runs that tree's release "+
					"hooks with the credential. Add a `git merge-base --is-ancestor` "+
					"refusal before anything else runs.",
				filepath.Base(path), id)
		}
	}
	require.NotZero(t, checked,
		"no credential-bearing job is gated on a tag, so this guard proves nothing")
}
