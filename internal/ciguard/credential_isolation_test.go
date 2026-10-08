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
	Steps       []isolatedStep    `yaml:"steps"`
	Defaults    isolatedDefaults  `yaml:"defaults"`
	RunsOn      yaml.Node         `yaml:"runs-on"`
	Strategy    yaml.Node         `yaml:"strategy"`
	Container   yaml.Node         `yaml:"container"`
}

// isolatedStep is one step, named rather than anonymous so that the steps of
// a local composite action can be read into the same shape and judged as
// steps of the job that calls it -- which is what they are at runtime.
// isolatedDefaults carries `defaults.run.shell`, which selects the interpreter
// for every step that does not name one.
type isolatedDefaults struct {
	Run struct {
		Shell string `yaml:"shell"`
	} `yaml:"run"`
}

type isolatedStep struct {
	Name             string            `yaml:"name"`
	Uses             string            `yaml:"uses"`
	If               string            `yaml:"if"`
	Run              string            `yaml:"run"`
	Env              map[string]string `yaml:"env"`
	With             map[string]any    `yaml:"with"`
	ContinueOnError  bool              `yaml:"continue-on-error"`
	Shell            string            `yaml:"shell"`
	WorkingDirectory string            `yaml:"working-directory"`
}

// localAction is a repository-local action's manifest, as far as these guards
// need it: what it runs, and the steps it runs when composite.
type localAction struct {
	Runs struct {
		Using string         `yaml:"using"`
		Steps []isolatedStep `yaml:"steps"`
	} `yaml:"runs"`
}

// stepsIncludingLocalActions returns the steps that actually execute in a
// job: its own, and then -- recursively -- those of every repository-local
// composite action it uses.
//
// A local composite action runs INSIDE the calling job, so its steps are the
// job's steps for every purpose these guards care about. Reading only the
// job's own `steps:` left a hole with nothing in the way: a dispatchable
// credential job could check out `main` itself, pass the secret to a local
// composite as an input, and the composite's own `actions/checkout` -- with no
// `ref:`, which defaults to the TRIGGERING ref -- would select the dispatched
// branch and run its scripts with that credential. The top-level checkout
// passed; the manifest named no secret, so the secrets scan passed; and the
// nested checkout was invisible to both.
//
// An action whose execution cannot be read is a failure, not a pass: a
// JavaScript or Docker action is reported so that the caller refuses it
// rather than silently treating it as stepless.
func stepsIncludingLocalActions(t *testing.T, job isolatedJob) (steps []isolatedStep, unreadable []string) {
	t.Helper()
	return stepsIncludingLocalActionsUnder(repoRoot(t), job)
}

// stepsIncludingLocalActionsUnder is the same walk rooted at a given
// directory, so the recursion can be tested against manifests a test writes
// rather than only against this repository's.
func stepsIncludingLocalActionsUnder(root string, job isolatedJob) (steps []isolatedStep, unreadable []string) {
	seen := map[string]bool{}

	var walk func(own []isolatedStep, depth int)
	walk = func(own []isolatedStep, depth int) {
		for _, step := range own {
			steps = append(steps, step)
			target := strings.TrimSpace(step.Uses)
			if target == "" || !strings.HasPrefix(target, "./") {
				continue
			}
			if depth >= 8 {
				unreadable = append(unreadable, target+" (nested deeper than this guard follows)")
				continue
			}
			if seen[target] {
				continue
			}
			seen[target] = true

			manifests := manifestsOf(root, target)
			if len(manifests) == 0 {
				unreadable = append(unreadable, target+" (no action.yml or action.yaml)")
				continue
			}
			// EVERY manifest present, each on its own. GitHub executes
			// `action.yml` when both names exist, and a loop keeping the LAST
			// match read `action.yaml` while the runner ran `action.yml` -- so
			// the executed file is the one that must be read.
			// Walking both removes the precedence question rather than
			// answering it, and a manifest that will not parse or whose
			// `runs.using` this cannot read is reported unreadable even when
			// its sibling is fine.
			for _, manifest := range manifests {
				body, err := os.ReadFile(manifest)
				if err != nil {
					unreadable = append(unreadable, target+" ("+err.Error()+")")
					continue
				}
				var action localAction
				if err := yaml.Unmarshal(body, &action); err != nil {
					unreadable = append(unreadable, filepath.Base(manifest)+" of "+target+" (manifest does not parse)")
					continue
				}
				if action.Runs.Using != "composite" {
					unreadable = append(unreadable, filepath.Base(manifest)+" of "+target+" (runs.using is "+action.Runs.Using+", whose execution this guard cannot read)")
					continue
				}
				walk(action.Runs.Steps, depth+1)
			}
		}
	}
	walk(job.Steps, 0)
	return steps, unreadable
}

type isolatedWorkflow struct {
	On yaml.Node `yaml:"on"`
	// Workflow-level env is inherited by EVERY job, so a secret put here is a
	// secret in the job running the suite. Omitting it from the model is the
	// same as exempting it, and a model that cannot see a credential cannot
	// report one.
	Env      map[string]string      `yaml:"env"`
	Defaults isolatedDefaults       `yaml:"defaults"`
	Jobs     map[string]isolatedJob `yaml:"jobs"`
	// raw is each job as written, filled by a second decode. The typed model
	// above names the fields a credential is USUALLY in; a job can also carry
	// one in `with:`, `strategy.matrix`, `services`, `container`, `outputs`,
	// `environment` or `concurrency`, and a model that names fields exempts
	// every field it does not name. This is the backstop that turns such a
	// field into a loud failure instead of a silent gap.
	raw map[string]yaml.Node
	// name is the workflow's file name, so a registry keyed by workflow can be
	// consulted without threading the path through every call.
	name string
	// document is the whole workflow as parsed. The template digest is taken
	// from this rather than re-read from disk, so a caller can hand over a
	// changed workflow and see it refused -- which is what makes the
	// comparison testable at all.
	document yaml.Node
}

// secretsIn returns every non-built-in secret the job can reach, and where
// each was found, so a failure names the line to change.
//
// "Can reach" includes the WORKFLOW's env, because every job inherits it. The
// names come from the expression parser rather than a pattern, so
// `secrets['NAME']`, `secrets[format(...)]` and `toJSON(secrets)` count as
// surely as `secrets.NAME` does; an expression that cannot be parsed fails the
// test rather than reporting nothing.
func secretsIn(t *testing.T, wf isolatedWorkflow, id string) map[string][]string {
	t.Helper()

	job := wf.Jobs[id]
	found := map[string][]string{}
	note := func(where, text string) {
		names, err := secretsReferencedIn(text)
		require.NoError(t, err,
			"%s: an expression here cannot be parsed, so a credential in it "+
				"would be invisible to this guard", where)
		for _, name := range names {
			if name == builtInToken {
				continue
			}
			found[name] = append(found[name], where)
		}
	}

	for name, value := range wf.Env {
		note("the WORKFLOW's env "+name+" (inherited by every job)", value)
	}
	for name, value := range job.Env {
		note("the job's env "+name, value)
	}
	// `secrets: inherit` on a job that calls a reusable workflow hands over
	// every secret the caller holds, which is the widest form there is.
	if job.Secrets.Kind != 0 {
		var rendered strings.Builder
		// Not `if err == nil`: skipping on an encode failure would mean a
		// `secrets:` block this guard could not read counted as no secrets.
		require.NoError(t, yaml.NewEncoder(&rendered).Encode(job.Secrets),
			"job %q has a `secrets:` block that cannot be re-encoded, so what it "+
				"hands over cannot be read", id)
		note("the job's secrets:", rendered.String())
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
			if str, ok := value.(string); ok {
				note(where+" with."+key, str)
			}
		}
		note(where+" run:", step.Run)
		note(where+" if:", step.If)
	}

	// The backstop. Everything above reads a field by name; this reads the job
	// as written, so a credential in a field the model does not know about is
	// still reported -- without a name for where it is, which is the honest
	// answer and enough to find it.
	raw, ok := wf.raw[id]
	require.True(t, ok,
		"job %q was not captured verbatim, so the backstop below cannot read it "+
			"and every field this guard does not name would be exempt", id)

	var rendered strings.Builder
	require.NoError(t, yaml.NewEncoder(&rendered).Encode(raw),
		"job %q cannot be re-encoded, so the backstop cannot read it", id)
	names, err := secretsReferencedIn(rendered.String())
	require.NoError(t, err,
		"job %q contains an expression that cannot be parsed, so a credential "+
			"in it would be invisible to this guard", id)
	for _, name := range names {
		if name == builtInToken {
			continue
		}
		if _, already := found[name]; !already {
			found[name] = []string{
				"a job field this guard does not model by name (grep job " +
					id + " for it): `with`, `strategy`, `services`, " +
					"`container`, `outputs`, `environment` and `concurrency` " +
					"can all carry one",
			}
		}
	}
	return found
}

// triggersThatCannotCarryAPullRequestsCode is the list, and the direction of
// the list is the point.
//
// This used to read the other way -- `pull_request`, `pull_request_target`,
// `workflow_call`, `merge_group` were listed as the reachable ones, and
// anything else was treated as safe. That is the shape two review rounds found
// twice elsewhere: an enumeration exempts whatever it does not name. GitHub has
// some thirty-five trigger events and adds more; `issue_comment` and
// `pull_request_review` both carry a pull request's context, and a workflow
// reacting to either is a well-known way to reach unreviewed code. Naming the
// reachable ones means a trigger nobody here thought about is exempt by
// default, silently.
//
// Inverted, a trigger has to be ARGUED safe to be treated as safe. These are
// the ones that cannot carry a pull request's code at all:
//
//   - `push`, `create`, `delete`, `release`: a ref event in this repository,
//     which requires write access.
//   - `schedule`: no triggering party.
//   - `workflow_dispatch`: requires write access, and has a guard of its own
//     (TestADispatchableCredentialJobChecksOutTheDefaultBranch) because the
//     dispatched ref is chosen.
//   - `workflow_run`: has its own two hostile scenarios, which are stricter
//     than this question.
//
// Everything else -- named today or added by GitHub next year -- counts as
// reachable, and a credential-bearing job in such a workflow has to prove its
// condition false.
var triggersThatCannotCarryAPullRequestsCode = []string{
	"push",
	"create",
	"delete",
	"release",
	"schedule",
	"workflow_dispatch",
	"workflow_run",
}

// reachableFromAPullRequest reports whether code an author of a pull request
// wrote can run in this workflow.
//
// `workflow_call` counts, and not only because it is in no list above: a
// reusable workflow runs the CALLER's tree at a ref the caller picks, and
// nothing here can see whether that caller dispatches it from a pull request --
// so it has to be assumed, which is also what makes a secret in such a workflow
// the caller's secret to lose.
func reachableFromAPullRequest(on yaml.Node) bool {
	for _, trigger := range triggers(on) {
		if !contains(triggersThatCannotCarryAPullRequestsCode, trigger) {
			return true
		}
	}
	return false
}

// yamlOf returns a workflow file's raw text.
func yamlOf(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

// parseIsolatedDocument decodes a workflow twice: into the typed model, and
// into the raw job nodes the backstop in secretsIn reads. One decode cannot do
// both -- a typed struct drops the fields it does not name, which is precisely
// what the backstop exists to notice.
func parseIsolatedDocument(t *testing.T, text, where string) isolatedWorkflow {
	t.Helper()

	var wf isolatedWorkflow
	require.NoError(t, yaml.Unmarshal([]byte(text), &wf), where)

	var verbatim struct {
		Jobs map[string]yaml.Node `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(text), &verbatim), where)
	wf.raw = verbatim.Jobs
	wf.name = filepath.Base(where)
	require.NoError(t, yaml.Unmarshal([]byte(text), &wf.document), where)
	return wf
}

func loadIsolatedWorkflows(t *testing.T) ([]string, map[string]isolatedWorkflow) {
	t.Helper()

	paths := workflowFiles(t)
	out := make(map[string]isolatedWorkflow, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		out[path] = parseIsolatedDocument(t, string(raw), path)
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

// mustNotRunUnder asserts a job's condition is PROVABLY false in a situation,
// and says why when it is not. Unknown is a failure, not a pass: a condition
// that turns on something unknowable has not established anything.
func mustNotRunUnder(t *testing.T, gate string, s scenario) (ok bool, reason string) {
	t.Helper()

	reached, err := canRunUnder(gate, s)
	require.NoError(t, err,
		"a condition this package cannot parse is one it cannot judge: %q", gate)
	switch reached {
	case triFalse:
		return true, ""
	case triTrue:
		return false, "its condition is TRUE under " + s.name
	}
	return false, "its condition is UNKNOWN under " + s.name +
		" (it turns on something no condition here fixes, which establishes nothing)"
}

// The rule, stated once: a job that can run code under review references no
// secret, and a job that references a secret runs on a merged ref and checks
// out nothing of the pull request.
//
// A step-level `if:` does not satisfy this. It decides whether that STEP runs,
// not what the job is; the credential is still named in a job whose other
// steps execute the author's code, and a reader has to reason about step order
// to see whether it is safe. The split into two jobs is what makes it legible
// reachabilityNote renders the reachability half of a failure, so a message
// cannot read as "unreachable, therefore fine".
func reachabilityNote(unreachable bool, why string) string {
	if unreachable {
		return "it is unreachable from every hostile situation, which does not " +
			"establish what it runs when triggered legitimately"
	}
	return "it is not provably unreachable: " + why
}

// provablyUnreachable reports whether a condition is false in EVERY hostile
// situation, and names the first one it is not.
func provablyUnreachable(t *testing.T, gate string, hostile []scenario) (bool, string) {
	t.Helper()

	for _, situation := range hostile {
		ok, reason := mustNotRunUnder(t, gate, situation)
		if !ok {
			return false, reason
		}
	}
	return true, ""
}

// as well as true.
func TestNoSecretIsReachableFromAJobThatRunsCodeUnderReview(t *testing.T) {
	paths, workflows := loadIsolatedWorkflows(t)

	checked := 0
	for _, path := range paths {
		wf := workflows[path]
		hostile, _ := hostileScenariosFor(wf.On)
		require.NotEmpty(t, hostile,
			"%s yielded no hostile scenario at all. Every trigger yields one now, so "+
				"an empty set means the workflow declares no trigger this package can "+
				"read -- which is an unanswered question, not a safe workflow.",
			filepath.Base(path))
		checked++

		for _, id := range isolatedJobIDs(wf) {
			secrets := secretsIn(t, wf, id)
			if len(secrets) == 0 {
				continue
			}
			names := make([]string, 0, len(secrets))
			for name := range secrets {
				names = append(names, name+" ("+strings.Join(secrets[name], ", ")+")")
			}
			sort.Strings(names)

			accepted, missing := credentialJobIsAccepted(t, wf, id)
			require.True(t, accepted,
				"%s: job %q references %s, and %s.\n"+
					"`permissions:` does not govern a repository secret, so narrowing "+
					"the built-in token leaves this credential exactly as reachable as "+
					"before. The job must be unreachable from every hostile situation "+
					"AND establish what it executes: pin every checkout to %q with no "+
					"party-chosen execution input, or put a `git merge-base "+
					"--is-ancestor` refusal ahead of everything that runs.",
				filepath.Base(path), id, strings.Join(names, "; "), missing,
				theDefaultBranch)
		}
	}
	require.NotZero(t, checked,
		"no workflow is reachable from a pull request, so this guard proves nothing")
}

// secretsIn decides the first assertion, so a place it does not look is a
// place a credential can sit unnoticed. These are the shapes that carry one,
// including the two a review is refused by the earlier pattern-matching version:
// workflow-level env, which every job inherits, and the index and
// whole-context forms of reading the secrets context.
func TestSecretsInFindsEveryPlaceACredentialCanSit(t *testing.T) {
	const doc = `
env:
  WORKFLOW_LEVEL: ${{ secrets.WORKFLOW_ENV_SECRET }}
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
      - name: read by index rather than by property
        env:
          INDEXED: ${{ secrets['INDEXED_SECRET'] }}
        run: echo hi
      - name: the whole context handed to a function
        env:
          EVERYTHING: ${{ toJSON(secrets) }}
        run: echo hi
      - name: an index computed at run time
        env:
          COMPUTED: ${{ secrets[format('A_{0}', github.ref_name)] }}
        run: echo hi
      - name: in a condition
        if: ${{ secrets.CONDITION_SECRET != '' }}
        run: echo hi
      - name: the built-in token is exempt
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: gh api /rate_limit
  calls:
    uses: ./.github/workflows/other.yml
    secrets: inherit
`
	wf := parseIsolatedDocument(t, doc, "the fixture above")

	found := secretsIn(t, wf, "everything")
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	require.Equal(t, []string{
		wholeSecretsContext, // `(` sorts ahead of the names
		"CONDITION_SECRET",
		"INDEXED_SECRET",
		"JOB_ENV_SECRET",
		"RUN_SECRET",
		"STEP_ENV_SECRET",
		"WITH_SECRET",
		"WORKFLOW_ENV_SECRET",
	}, names,
		"a shape that carries a credential is not being read. GITHUB_TOKEN is "+
			"exempt on purpose because it is present whether or not it is named; "+
			"everything else here is a credential this workflow asked for.")

	require.Contains(t, found["WORKFLOW_ENV_SECRET"][0], "WORKFLOW's env",
		"a workflow-level secret must be reported as inherited, so a reader "+
			"knows it is not in the job's own text")

	require.Contains(t, secretsIn(t, wf, "calls"),
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
//
// WHAT IT DOES NOT FOLLOW, stated here rather than left to be discovered: only
// `.github/scripts/*.sh`. A credential-bearing job that reached a pull
// request's refs through `make`, through a Go program, through a script
// elsewhere in the tree, or through a tool's own configuration (GoReleaser runs
// the hooks in `.goreleaser.yaml`) would not be seen by the two guards below.
//
// No job here does that today, and the alternative -- refusing every
// indirection a credential-bearing job cannot be read through -- would have
// needed an exemption for GoReleaser on the day it was written, which is the
// kind of guard that is green because of its exemptions. What protects that
// case instead is the tag condition and the ancestry refusal on the job: the
// tree whose hooks run has to be one that was merged.
func commandsOf(t *testing.T, job isolatedJob) string {
	t.Helper()

	var all strings.Builder
	pending := []string{}
	// The steps that actually execute, a local composite action's included:
	// its `run:` is this job's command, and a guard reading only the job's
	// own steps would not see it reach for a pull request's ref.
	executed, _ := stepsIncludingLocalActions(t, job)
	for _, step := range executed {
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

// manifestsOf returns every action manifest present for a local action, in the
// order GitHub prefers them. Both names can exist; the runner executes
// `action.yml`, so a guard that reads one of the two can read a different file
// from the one that runs.
func manifestsOf(root, target string) []string {
	var found []string
	for _, name := range []string{"action.yml", "action.yaml"} {
		candidate := filepath.Join(root, strings.TrimPrefix(target, "./"), name)
		if _, err := os.Stat(candidate); err == nil {
			found = append(found, candidate)
		}
	}
	return found
}

// readAll concatenates several files, so one search covers all of them.
func readAll(paths []string) (string, error) {
	var joined strings.Builder
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		joined.Write(body)
		joined.WriteString("\n")
	}
	return joined.String(), nil
}

// localActionReference matches a step that uses an action from this repository
// (`uses: ./path`), whose definition is a file here rather than a pinned
// upstream release.
var localActionReference = regexp.MustCompile(`^\./([A-Za-z0-9_./-]+)$`)

// A repository-local action is code this repository ships and a step's `with:`
// flows straight into it, so a secret passed to one is a secret inside it --
// and a composite action's `runs.steps` can do anything a job step can.
//
// There are none here today. That is exactly why this exists: the guards above
// read workflows and `.github/scripts/*.sh`, so the day somebody adds
// `.github/actions/x/action.yml` and passes it a credential, nothing would have
// noticed. Rather than enumerate what such an action may do, this refuses the
// case it cannot read: a local action must have a definition this package can
// load, and its definition is then searched for secret references like any
// other file.
func TestEveryRepositoryLocalActionCanBeReadByTheseGuards(t *testing.T) {
	paths, workflows := loadIsolatedWorkflows(t)

	for _, path := range paths {
		wf := workflows[path]
		for _, id := range isolatedJobIDs(wf) {
			job := wf.Jobs[id]

			// A job can also call a reusable workflow by local path, which is
			// itself guarded by every assertion in this package.
			for _, reference := range append([]string{job.Uses}, stepUses(job)...) {
				match := localActionReference.FindStringSubmatch(strings.TrimSpace(reference))
				if match == nil {
					continue
				}
				target := match[1]
				if strings.HasSuffix(target, ".yml") || strings.HasSuffix(target, ".yaml") {
					require.FileExists(t, filepath.Join(repoRoot(t), target),
						"%s: job %q calls local workflow %q, which does not exist",
						filepath.Base(path), id, target)
					continue
				}

				// A composite or JavaScript action: a manifest must be
				// readable, and EVERY spelling present is searched for
				// credentials -- see manifestsOf.
				manifests := manifestsOf(repoRoot(t), target)
				manifest := strings.Join(manifests, ", ")
				require.NotEmpty(t, manifest,
					"%s: job %q uses local action %q but neither %s/action.yml nor "+
						"%s/action.yaml exists. A local action this package cannot "+
						"load is one whose steps and inputs are exempt from every "+
						"guard here.",
					filepath.Base(path), id, target, target, target)

				body, err := readAll(manifests)
				require.NoError(t, err)
				names, err := secretsReferencedIn(body)
				require.NoError(t, err,
					"%s contains an expression this package cannot parse", manifest)
				for _, name := range names {
					require.Equal(t, builtInToken, name,
						"%s names secret %q. A local action runs inside the calling "+
							"job, so a credential it reads is a credential in that "+
							"job -- pass it as an input from a job the guards above "+
							"have already judged.",
						manifest, name)
				}
			}
		}
	}
}

// stepUses returns each step's `uses:`.
func stepUses(job isolatedJob) []string {
	out := make([]string, 0, len(job.Steps))
	for _, step := range job.Steps {
		out = append(out, step.Uses)
	}
	return out
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

// reachableOnATagPush reports whether a job could run for a pushed tag --
// either in a workflow this repository triggers on a push, or in a reusable one
// a caller may invoke from its own tag push.
func reachableOnATagPush(t *testing.T, wf isolatedWorkflow, job isolatedJob) bool {
	t.Helper()

	onATagEvent := false
	for _, trigger := range triggers(wf.On) {
		if trigger == "push" || trigger == "workflow_call" {
			onATagEvent = true
		}
	}
	if !onATagEvent {
		return false
	}
	reached, err := canRunUnder(job.If, pushOfATag)
	require.NoError(t, err)
	return reached != triFalse
}
