package ciguard

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// A job that receives a credential is JUDGED, and the registry is friction on
// top of the judgement rather than a substitute for it.
//
// The history matters, because this file has been both things. Six review
// rounds analysed what jobs do: which contexts are a source, which fields a
// sink, which commands execute a value, which conditions skip a refusal. Each
// round closed the routes it named and the next found more -- `$GITHUB_OUTPUT`,
// `$GITHUB_EVENT_PATH`, `toJSON(github)`, `NODE_OPTIONS`, `working-directory`,
// `git restore --source`, `git archive | tar -x`, a composite step reusing the
// refusal's name. The list of things a workflow can do is not a list this
// package can finish, and a guard built from one is always one construction
// behind.
//
// Round six's answer was to stop analysing: pin each permitted job's bytes and
// accept whatever matches. That removed the enumeration and removed the
// judgement with it. The decision became "is this (workflow, job) in the
// allowlist" and "does sha256 of the workflow file equal a constant" -- so the
// defect the rounds were about became reachable again by editing one 64-char
// constant, which is the remediation the failure message itself printed. A
// review of this package planted exactly that: it took the registered
// `go.yml` · `coverage-badge` job, which carries `contents: write`, let its
// condition admit `pull_request`, pointed its checkout at the pull request's
// own branch and ran a script out of that tree. Five tests failed, one digest
// was re-recorded, and the suite went green with the violation in place.
//
// So the decision is three conjuncts now, and a digest is only the last of
// them:
//
//  1. REGISTERED. The job is one of the shapes this repository knowingly runs
//     with a credential. A new one is refused until somebody adds a template
//     and a test, which is what makes this an allowlist.
//  2. JUDGED. Either the job's condition is PROVABLY FALSE in every hostile
//     situation its workflow's triggers admit (`provablyUnreachable`, over the
//     scenarios `hostileScenariosFor` derives), or the job establishes which
//     tree it executes (`acceptedExecution`). Evaluated, not matched: unknown
//     is not false, and a construction this package cannot read is a refusal.
//  3. RECORDED. The job's shape -- the job block plus the workflow-level `on`,
//     `permissions`, `env` and `defaults` in scope for every step in it --
//     matches a recorded digest.
//
// (3) cannot excuse (2). Re-recording a digest moves a constant; it does not
// make a condition false or a tree reviewed. That is the property the planted
// violation above now fails on, with no digest to re-record.
//
// What (3) is still FOR: the residue (2) cannot read. `acceptedExecution` reads
// checkouts, tree-repointing git verbs and an ancestry refusal; it does not
// read the semantics of a shell script, a third-party action's behaviour, or a
// field no rule here names. The digest means a change to any of that stops
// matching until somebody records the new shape. It is a changelog with teeth,
// not a gate -- `main`'s ruleset requires zero approving reviews, so nothing
// obliges a human to read the line that changed (see
// docs/runbooks/branch-protection.md).
//
// And it is scoped to what it protects. It used to hash the whole workflow
// file, so a Dependabot bump of an action pin in an unrelated job refused every
// registered job in that file and re-recorded two digests -- making "a pin
// moved" and "a write token entered a job that runs pull request code" the same
// failure with the same one-line fix. The record now covers the job and
// everything the job inherits, and nothing else.

// jobTemplate is one permitted shape.
type jobTemplate struct {
	workflow, job string
	// executedBy is the test that runs this job's own script against real
	// repositories. It is REQUIRED for a job whose acceptance rests on a
	// refusal written in shell: this package reads that the refusal is there
	// and reads nothing about whether it works, so something has to execute it.
	// `acceptedExecution` refuses such a job when this field is empty.
	executedBy string
	// why records what this job is for, so a reviewer reading a digest change
	// knows what they are being asked to approve.
	why string
}

// permittedCredentialJobs is the allowlist. Nothing else may hold a credential.
var permittedCredentialJobs = []jobTemplate{
	{
		workflow: "version-tag.yml", job: "tag",
		executedBy: "TestTagSelectionAcceptsOnlyCommitsOnTheDefaultBranch",
		why:        "cuts the release tag from version/info.codefly.yaml on a commit proven to be on the default branch",
	},
	{
		workflow: "go-service-release.yml", job: "goreleaser",
		executedBy: "TestAReleaseIsAdmittedOnlyFromTheRepositorysOwnDefaultBranch",
		why:        "publishes a release for a tag proven to be on the repository's own default branch",
	},
	{
		workflow: "publish-service-image.yml", job: "publish",
		executedBy: "TestAServiceImageIsPublishedOnlyFromTheRepositorysOwnDefaultBranch",
		why:        "pushes a caller's service image by digest with the caller's registry credential, from a commit proven to be on the repository's own default branch",
	},
	{
		workflow: "combine-deps.yml", job: "publish",
		executedBy: "TestTheCombinedBranchTravelsAsABundleWithoutBeingCheckedOut",
		why:        "pushes the branch the unprivileged plan job assembled, without checking it out",
	},
	{
		workflow: "go.yml", job: "coverage-badge",
		why: "pushes the coverage badge README.md renders; runs no script of its own",
	},
	{
		workflow: "go.yml", job: "notify",
		why: "posts the build result to Slack; no checkout, no script",
	},
}

// canonicalDigest renders a value to canonical YAML and hashes it, so the
// digest depends on content and not on formatting.
func canonicalDigest(t *testing.T, value any) string {
	t.Helper()

	rendered, err := yaml.Marshal(value)
	require.NoError(t, err)
	sum := sha256.Sum256(rendered)
	return hex.EncodeToString(sum[:])
}

// recordedShape is what a registered job's digest covers: the job, and the
// workflow-level fields that are in scope for every step in it.
//
// Those four are in scope and nothing else is. `on` decides which hostile
// situations the job answers for, so changing it changes the judgement.
// `permissions` at workflow level is what a job with no block of its own gets.
// `env` is in scope for every step, so `BASH_ENV` written there redefines what
// a step's script does. `defaults.run.shell` chooses the interpreter, so
// `shell: 'true {0}'` turns every refusal in the file into a no-op.
//
// A step of a DIFFERENT job -- an action pin a Dependabot bump moved, a cache
// key, a matrix -- is outside the record, because it is outside what the
// registered job runs.
//
// The field order here is the digest's input order, so it is fixed by the
// struct rather than by map iteration.
type recordedShape struct {
	On          any `yaml:"on"`
	Permissions any `yaml:"permissions"`
	Env         any `yaml:"env"`
	Defaults    any `yaml:"defaults"`
	Job         any `yaml:"job"`
}

// workflowScope holds the workflow-level nodes recordedShape needs, read as
// nodes so an absent key stays distinguishable from an empty one.
type workflowScope struct {
	On          yaml.Node `yaml:"on"`
	Permissions yaml.Node `yaml:"permissions"`
	Env         yaml.Node `yaml:"env"`
	Defaults    yaml.Node `yaml:"defaults"`
}

// decodedOrNil decodes a node, treating an absent key (kind zero) as nil
// rather than as a decode error.
func decodedOrNil(t *testing.T, node yaml.Node) any {
	t.Helper()
	if node.Kind == 0 {
		return nil
	}
	var out any
	require.NoError(t, node.Decode(&out))
	return out
}

// canonicalJobDigest hashes one job's recorded shape.
func canonicalJobDigest(t *testing.T, wf isolatedWorkflow, id string) string {
	t.Helper()

	raw, ok := wf.raw[id]
	require.True(t, ok, "%s has no job %q to record", wf.name, id)

	var scope workflowScope
	require.NoError(t, wf.document.Decode(&scope), wf.name)

	return canonicalDigest(t, recordedShape{
		On:          decodedOrNil(t, scope.On),
		Permissions: decodedOrNil(t, scope.Permissions),
		Env:         decodedOrNil(t, scope.Env),
		Defaults:    decodedOrNil(t, scope.Defaults),
		Job:         decodedOrNil(t, raw),
	})
}

// canonicalFileDigest hashes a file that is not YAML -- a script a registered
// job runs -- so that what runs from the tree is pinned as firmly as the
// workflow that runs it.
func canonicalFileDigest(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// templateFor returns the registered template for a job, if there is one.
func templateFor(workflow, job string) (jobTemplate, bool) {
	for _, candidate := range permittedCredentialJobs {
		if candidate.workflow == workflow && candidate.job == job {
			return candidate, true
		}
	}
	return jobTemplate{}, false
}

// recordKey names a job's record. Keyed by job, not by file, because the
// record covers a job.
func recordKey(workflow, job string) string { return workflow + "/" + job }

// credentialJobIsAccepted is the decision, and it reports EVERY conjunct that
// refused rather than the first. A failure that names only the digest reads as
// "re-record it"; one that names the judgement cannot.
func credentialJobIsAccepted(t *testing.T, wf isolatedWorkflow, id string) (bool, string) {
	t.Helper()

	template, registered := templateFor(wf.name, id)
	if !registered {
		return false, "it is not one of the job shapes this repository runs with a " +
			"credential. Those are " + strings.Join(sortedTemplateNames(), ", ") +
			", registered in permittedCredentialJobs, each with a test that executes " +
			"it; a job that is not one of them is refused rather than analysed, " +
			"because the set of things a workflow can do to reach unreviewed code is " +
			"not a set this package can enumerate"
	}

	var refused []string

	// CONJUNCT TWO -- the judgement. Either nothing a triggering party can do
	// reaches the job, or the job says which tree it executes. Both halves are
	// evaluated; neither is satisfied by the record below.
	hostile := hostileScenariosFor(wf.On)
	if len(hostile) == 0 {
		refused = append(refused,
			"its workflow yields no hostile situation at all, so there is nothing to "+
				"judge its condition against -- which is an unanswered question, not a "+
				"safe workflow")
	} else {
		unreachable, why := provablyUnreachable(t, wf.Jobs[id].If, hostile)
		if !unreachable {
			established, how := acceptedExecution(t, wf, id)
			if !established {
				refused = append(refused, reachabilityNote(why)+", and "+how)
			}
		}
	}

	// CONJUNCT THREE -- the record. Scoped to the job and what it inherits.
	key := recordKey(template.workflow, template.job)
	recorded, present := recordedJobDigests[key]
	switch {
	case !present:
		refused = append(refused, "no shape is recorded for "+key+
			", so nothing pins what this job runs")
	default:
		actual := canonicalJobDigest(t, wf, id)
		if recorded != actual {
			refused = append(refused, key+" has changed shape: recorded "+
				recorded[:12]+", actual "+actual[:12]+" ("+template.why+
				"). Update recordedJobDigests in the same change, so the new shape is "+
				"approved rather than inherited -- and note that re-recording settles "+
				"only this conjunct")
		}
	}

	if len(refused) > 0 {
		return false, strings.Join(refused, "; also ")
	}
	return true, ""
}

// recordedJobDigests is the approved shape of each permitted job, keyed by
// workflow and job. Recorded here rather than in the template literals so that
// updating one is a visible, reviewable line.
//
// Re-recording one of these is NOT a way to admit a job the judgement refuses:
// it is the last of three conjuncts and the only one a constant can satisfy.
var recordedJobDigests = map[string]string{
	"publish-service-image.yml/publish": "de6da0675711da839493b8e2f0a7576e0041af12f0f22328208ecf24ec004bfe",
	"combine-deps.yml/publish":          "96fa1520bff8448fea8f54c7bf6608bda3b218c34099469abeffcf0ab28a9eeb",
	"go-service-release.yml/goreleaser": "236e993f0419411c4093519708ebe76694d7df482bf98b8c26d8396999fd9f25",
	"go.yml/coverage-badge":             "f90499145fe994ba99df79a30c275d1be9396e572f853ab1bd4ea7519ec2ebc7",
	"go.yml/notify":                     "150388315f3784739566d301151e8fc74cc6715e54b2af8420c5fdd6b59aaf75",
	"version-tag.yml/tag":               "447e0de19e05b354745c839d84f533fcfcb58716d7fa120bdb42846270bf0a65",
}

// recordedFileDigests pins the content of each repository file a registered job
// runs. A workflow digest cannot cover a script in the tree, so the script is
// hashed too -- otherwise one line added to it runs unreviewed code with the
// job's credential while the workflow is untouched.
var recordedFileDigests = map[string]string{
	".github/scripts/combine-deps-plan.sh":    "ade2737c41da8b1cc92966a4feb598c9481a9b09dafc643f271dad3aa155fdc1",
	".github/scripts/combine-deps-publish.sh": "19c6833c51700abfbefe80bcd2b98ac4170152922f62659285455446e58e74c0",
}

// ------------------------------------------------- what a job executes

// Checkout is attributed from ref, repository and path; every other action
// needs its own source contract in attributedAction.
const checkoutAction = "actions/checkout@"

// ancestryRefusalOfTheCheckedOutCommit matches the proof a job offers when its
// checkout took the TRIGGERING commit: the commit the tree is on is shown to be
// reachable from the default branch's remote-tracking ref, and the step exits
// otherwise.
//
// Fully qualified on purpose: a bare `origin/<branch>` is an ambiguous rev that
// a tag of the same name can win, so a refusal spelled that way is not one.
var ancestryRefusalOfTheCheckedOutCommit = regexp.MustCompile(
	`git\s+merge-base\s+--is-ancestor\s+"?\$\{?GITHUB_SHA\}?"?\s+"?refs/remotes/origin/`)

// executedText is everything a step executes that this package can read: its
// own `run:`, and the contents of every repository script that run invokes,
// transitively. A repoint hidden in a script is the route the dependency
// combination had, so the script is read rather than the `run:` line alone.
func executedText(t *testing.T, step isolatedStep) string {
	t.Helper()

	var all strings.Builder
	all.WriteString(step.Run)
	all.WriteString("\n")

	pending := scriptReference.FindAllString(step.Run, -1)
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

// absentRefIsTheDefaultBranch reports whether a checkout with no `ref:` selects
// the default branch in this workflow.
//
// It does for `workflow_run` and for nothing else: that event sets GITHUB_SHA
// to the default branch's head whatever produced the run it reacts to, which is
// the property version-tag.yml's checkout relies on. Under every other trigger
// an absent `ref:` means the TRIGGERING ref, which is not a statement about
// what was reviewed.
func absentRefIsTheDefaultBranch(wf isolatedWorkflow) bool {
	declared := triggers(wf.On)
	if len(declared) == 0 {
		return false
	}
	for _, trigger := range declared {
		if trigger != "workflow_run" {
			return false
		}
	}
	return true
}

// treeOrigin says where the content a checkout left in the tree came from, and
// the distinction between the last two is the whole value of this type.
//
// An ancestry refusal proves something about `$GITHUB_SHA` -- the commit the
// RUN is for. That speaks for the tree only when the tree IS that commit, which
// is what a checkout with no `ref:` gives. A checkout that NAMES some other ref
// leaves a tree the refusal says nothing about: the refusal can pass on a
// commit that was merged while the tree holds a branch that was not.
//
// Collapsing those two into one boolean accepted exactly that shape. A probe
// of this guard took the planted `coverage-badge` violation, kept
// `ref: ${{ github.head_ref }}`, added a real unconditional refusal on
// `$GITHUB_SHA` and went green -- a write token, a pull request's branch in
// the tree, and a refusal that was checking a different commit.
type treeOrigin int

const (
	// treeIsTheDefaultBranch: reviewed code, by construction.
	treeIsTheDefaultBranch treeOrigin = iota
	// treeIsTheTriggeringCommit: `$GITHUB_SHA`'s content, so an ancestry
	// refusal on `$GITHUB_SHA` establishes it.
	treeIsTheTriggeringCommit
	// treeIsAPartyChosenRef: a named ref that is neither. Nothing this package
	// reads can establish it, so no refusal rescues it.
	treeIsAPartyChosenRef
)

// checkoutSelectsTheDefaultBranch reads the repository identity and ref.
// acceptedExecution separately refuses additional checkout paths.
func checkoutSelectsTheDefaultBranch(step isolatedStep, absentIsDefault bool) (treeOrigin, string) {
	if repository, named := step.With["repository"]; named && repository != "${{ github.repository }}" && repository != theRepository {
		return treeIsAPartyChosenRef, "selects a party-chosen repository"
	}
	ref, named := step.With["ref"]
	if !named {
		if absentIsDefault {
			return treeIsTheDefaultBranch, ""
		}
		return treeIsTheTriggeringCommit, "names no `ref:`, so it takes the triggering commit"
	}
	text, isString := ref.(string)
	if !isString {
		return treeIsAPartyChosenRef, "has a `ref:` this package cannot read"
	}
	switch strings.TrimSpace(text) {
	case theDefaultBranch, "refs/heads/" + theDefaultBranch:
		return treeIsTheDefaultBranch, ""
	}
	return treeIsAPartyChosenRef, "checks out " + text + " rather than " + theDefaultBranch
}

// acceptedExecution is the half of the judgement that does not depend on a
// condition being right: when this job DOES run, which tree does it execute
// code from?
//
// Two means, and they are the two the workflows here use:
//
//   - every checkout selects the default branch, so the tree is reviewed code
//     throughout (combine-deps.yml `publish`, go.yml `coverage-badge`, and
//     version-tag.yml `tag`, whose workflow_run checkout defaults to it);
//   - or the checkout took the triggering commit and an ancestry refusal shows
//     that commit is reachable from the default branch BEFORE anything else
//     runs (go-service-release.yml `goreleaser`, which cannot be proven
//     unreachable at all because a caller chooses its event).
//
// WHAT THIS DOES NOT READ, stated here rather than left to be discovered,
// because a comment describing machinery that is not there is how the previous
// defect hid:
//
//   - whether a refusal WORKS. It reads that the command is present, exits on
//     failure, and is not skipped or made non-fatal by a step field. Whether
//     the script refuses what it should is answered by executing it, which is
//     what `jobTemplate.executedBy` names -- and a job accepted by the second
//     means with no executing test is refused here.
//   - a third-party action's behaviour. `goreleaser/goreleaser-action` runs
//     `.goreleaser.yaml` out of the tree and `actions/setup-go` reads `go.mod`;
//     both are tree execution, which is why the tree has to be established
//     before any step that is not a checkout or the refusal.
//   - anything in a field no rule here names. That is the residue the recorded
//     digest covers.
func acceptedExecution(t *testing.T, wf isolatedWorkflow, id string) (bool, string) {
	t.Helper()

	job := wf.Jobs[id]
	steps, unreadable := stepsIncludingLocalActions(t, job)
	if len(unreadable) > 0 {
		return false, "it runs " + strings.Join(unreadable, ", ") +
			", whose steps this package cannot read, so what it executes is not established"
	}

	absentIsDefault := absentRefIsTheDefaultBranch(wf)
	// Before the first checkout there is no tree on the runner, so nothing from
	// one can execute. A `run:` here executes the workflow's own text, which
	// the recorded digest covers.
	origin, unreviewedBecause := treeIsTheDefaultBranch, ""

	for index, step := range steps {
		if strings.HasPrefix(step.Uses, checkoutAction) {
			if path, named := step.With["path"]; named && path != "" && path != "." {
				return false, "checkout path is not the workspace root; a later checkout cannot establish that additional tree"
			}
			if origin != treeIsTheDefaultBranch {
				return false, "a later checkout cannot erase an earlier unattributed tree"
			}
			// A checkout executes none of the tree it writes, so an unreviewed
			// one is not yet a refusal; the next step that runs is where it is
			// decided.
			origin, unreviewedBecause = checkoutSelectsTheDefaultBranch(step, absentIsDefault)
			continue
		}

		text := executedText(t, step)

		if origin != treeIsTheDefaultBranch {
			if origin == treeIsAPartyChosenRef {
				return false, "its checkout " + unreviewedBecause + ", and " +
					step.describe() + " then runs with that tree. An ancestry refusal " +
					"cannot rescue this: a refusal speaks for `$GITHUB_SHA`, the commit " +
					"the run is for, while the tree holds a different ref. Pin the " +
					"checkout to " + theDefaultBranch
			}
			if !ancestryRefusalOfTheCheckedOutCommit.MatchString(text) {
				return false, "its checkout " + unreviewedBecause + ", and " +
					step.describe() + " then runs with that tree. Either pin every " +
					"checkout to " + theDefaultBranch + ", or put a `git merge-base " +
					"--is-ancestor \"$GITHUB_SHA\" refs/remotes/origin/<default branch>` " +
					"refusal ahead of everything that runs"
			}
			if unconditional, why := refusalIsUnconditional(step, job, wf); !unconditional {
				return false, "its ancestry refusal " + why +
					", so the tree it runs is not established"
			}
			if why := triggeringCommitRefusalIsFirst(step.Run); why != "" {
				return false, why
			}
			if template, ok := templateFor(wf.name, id); !ok || !executingTestCovers(template, step) {
				return false, "Name the test that executes it in executedBy, bound to this workflow, job and refusal step"
			}
			origin = treeIsTheDefaultBranch
		}

		if step.Uses != "" {
			if why := attributedAction(wf, id, steps, index); why != "" {
				return false, why
			}
		}
		repointed, why := attributedShell(text)
		if why != "" {
			return false, step.describe() + " " + why
		}
		if repointed {
			if unconditional, why := refusalIsUnconditional(step, job, wf); !unconditional {
				return false, why
			}
			if template, ok := templateFor(wf.name, id); !ok || !executingTestCovers(template, step) {
				return false, "Name the test that executes it in executedBy, bound to this workflow, job and refusal step"
			}
		}
	}

	return true, ""
}

// refusalIsUnconditional reports whether a step carrying an ancestry refusal can
// actually stop the job. Each of these makes the command present and the
// refusal absent, which is the shape this whole package is about.
func refusalIsUnconditional(step isolatedStep, job isolatedJob, wf isolatedWorkflow) (bool, string) {
	if step.WorkingDirectory != "" {
		return false, "selects an unattributed working directory"
	}
	if step.If != "" {
		return false, "carries a step-level `if:` (" + step.If + "), so it can be skipped"
	}
	if step.ContinueOnError {
		return false, "sets `continue-on-error: true`, so its exit status stops nothing"
	}
	if !strings.Contains(step.Run, "exit 1") {
		return false, "never exits non-zero, so it reports rather than refuses"
	}
	for _, env := range []map[string]string{wf.Env, job.Env, step.Env} {
		for key := range env {
			if dangerousExecutionEnv(key) {
				return false, "inherits execution-changing environment " + key
			}
		}
	}
	for _, shell := range []string{step.Shell, job.Defaults.Run.Shell, wf.Defaults.Run.Shell} {
		switch strings.TrimSpace(shell) {
		case "", "bash", "sh", "bash -e {0}":
		default:
			return false, "runs under `shell: " + shell +
				"`, which this package cannot judge as one that propagates a failure"
		}
	}
	return true, ""
}

// ------------------------------------------------- the gates

// TestEveryPermittedJobMatchesItsRecordedShape is the record's own gate: each
// permitted job must still be the shape that was approved, and each template
// that claims an executing test must name one that exists.
//
// It is NOT the credential decision. That is TestEveryCredentialBearingJobIsRegistered
// below, which asks all three conjuncts.
func TestEveryPermittedJobMatchesItsRecordedShape(t *testing.T) {
	_, workflows := loadIsolatedWorkflows(t)

	sources, err := filepath.Glob(filepath.Join(repoRoot(t), "internal", "ciguard", "*_test.go"))
	require.NoError(t, err)
	var all strings.Builder
	for _, source := range sources {
		body, err := os.ReadFile(source)
		require.NoError(t, err)
		all.Write(body)
	}
	tests := all.String()

	for _, template := range permittedCredentialJobs {
		t.Run(recordKey(template.workflow, template.job), func(t *testing.T) {
			wf, ok := workflows[filepath.Join(repoRoot(t), ".github", "workflows", template.workflow)]
			require.True(t, ok, "%s does not exist", template.workflow)
			_, ok = wf.raw[template.job]
			require.True(t, ok, "%s has no job %q", template.workflow, template.job)

			key := recordKey(template.workflow, template.job)
			digest := canonicalJobDigest(t, wf, template.job)
			recorded, present := recordedJobDigests[key]
			require.True(t, present,
				"no shape recorded for %s. Add this line to recordedJobDigests:\n"+
					"\t%q: %q,", key, key, digest)
			require.Equal(t, recorded, digest,
				"%s has changed shape (%s). If intended, update recordedJobDigests and "+
					"the test that executes this job in the same change. Re-recording "+
					"settles the record and nothing else: the judgement in "+
					"credentialJobIsAccepted is asked separately and a digest cannot "+
					"satisfy it.",
				key, template.why)

			if template.executedBy != "" {
				require.Contains(t, tests, "func "+template.executedBy+"(",
					"%s/%s names %s as executing it, and no such test exists",
					template.workflow, template.job, template.executedBy)
			}
		})
	}
}

// And the converse, which is what makes this an allowlist: every job that
// receives a credential is registered AND judged. A new one is refused until
// someone adds a template and a test for it.
func TestEveryCredentialBearingJobIsRegistered(t *testing.T) {
	paths, workflows := loadIsolatedWorkflows(t)

	found := 0
	for _, path := range paths {
		wf := workflows[path]
		for _, id := range isolatedJobIDs(wf) {
			holdsSecret := len(secretsIn(t, wf, id)) > 0
			permissioned := parsePermissionedFile(t, path)
			holdsWrite := writeCapable(effectivePermissions(permissioned, permissioned.Jobs[id]))
			if !holdsSecret && !holdsWrite {
				continue
			}
			found++
			accepted, missing := credentialJobIsAccepted(t, wf, id)
			require.True(t, accepted, "%s: job %q %s", filepath.Base(path), id, missing)
		}
	}
	require.NotZero(t, found, "no credential-bearing job found, so this proves nothing")

	// The allowlist carries no entry for a job that does not exist.
	for _, template := range permittedCredentialJobs {
		wf := workflows[filepath.Join(repoRoot(t), ".github", "workflows", template.workflow)]
		_, ok := wf.Jobs[template.job]
		require.True(t, ok,
			"permittedCredentialJobs lists %s/%s, which does not exist",
			template.workflow, template.job)
	}
	require.Len(t, permittedCredentialJobs, found,
		"the allowlist and the set of credential-bearing jobs must be the same size, "+
			"or one of them is carrying an entry the other does not")

	// And the record carries no entry for a job that is not registered, so a
	// digest cannot outlive the job it was recorded for.
	for key := range recordedJobDigests {
		workflow, job, ok := strings.Cut(key, "/")
		require.True(t, ok, "recordedJobDigests key %q is not workflow/job", key)
		_, registered := templateFor(workflow, job)
		require.True(t, registered,
			"recordedJobDigests records %s, which permittedCredentialJobs does not list",
			key)
	}
}

// parsePermissionedFile reads one workflow for its permission blocks.
func parsePermissionedFile(t *testing.T, path string) permissionedWorkflow {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var wf permissionedWorkflow
	require.NoError(t, yaml.Unmarshal(raw, &wf), path)
	return wf
}

// sortedTemplateNames names the permitted shapes in a refusal, so a reader of
// the failure sees what the allowlist actually holds.
func sortedTemplateNames() []string {
	out := make([]string, 0, len(permittedCredentialJobs))
	for _, template := range permittedCredentialJobs {
		out = append(out, recordKey(template.workflow, template.job))
	}
	sort.Strings(out)
	return out
}
