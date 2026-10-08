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

// A `run:` script is the one workflow field whose content is a PROGRAM. Every
// other field is read by something: `with:` becomes an action's input, `env:`
// becomes a variable the script may dereference, `if:` is evaluated. A `run:`
// is assembled by GitHub before any shell sees it, so an expression
// interpolated there does not arrive as a value the program reads -- it
// arrives as part of the program's text, already past every quoting rule the
// author wrote.
//
// That distinction is why `env: X: ${{ inputs.x }}` plus `eval "$X"` is not a
// longer spelling of `run: ${{ inputs.x }}`. The first keeps the program fixed
// and the value a value; the second lets whoever supplies the value choose the
// program.
//
// This guard is deliberately NOT the credential model in
// docs/ci-credentials.md. That model answers "could this job's credential
// reach code nobody reviewed", which the doc records as a question no analysis
// can finish -- six rounds of asking what a job DOES each closed the routes it
// had found and left the ones it had not, which is why permitted credential
// jobs are pinned by shape instead. This asks something far smaller and
// closed: does any `run:` in this repository interpolate a value whose content
// is chosen by the party that triggered the run. That set is finite, it is
// decided by reading the parsed script, and a new workflow is covered the day
// it is added rather than when someone remembers to record it.
//
// It exists because two workflows claimed this property in a comment while
// nothing held it. Reverting go-service-ci.yml's `eval "$SETUP_RUN"` to
// `run: ${{ inputs.setup-run }}` left `go test ./internal/ciguard/` green,
// while the identical change to go-service-release.yml was refused by seven
// tests -- that one is a permitted credential job, so its shape is pinned, and
// the other is not a credential job at all and nothing looked at it.

// partyChosenContexts are the expression roots whose content is chosen by
// whoever triggered the run, rather than fixed by the event or by the runner.
//
// `inputs` is the caller of a reusable workflow -- for go-service-ci.yml and
// agent-ci.yml, a repository this one cannot see. `github.event` is the webhook
// payload, which carries a pull request's title, body and branch name.
// `github.head_ref` is the source branch of a pull request, chosen by whoever
// opened it, and is the oldest script-injection vector GitHub documents.
//
// Deliberately absent: `runner.*` and `matrix.*`, which the runner and this
// repository's own workflow fix respectively, and `env.*` and `steps.*`, which
// are a level of indirection this rule does not reach -- a tainted value can
// still arrive through them, and that is what the credential model is for.
var partyChosenContexts = []string{
	"inputs.",
	"github.event.",
	"github.head_ref",
}

// interpolation finds every `${{ ... }}` in a script, with its inner text.
var runInterpolation = regexp.MustCompile(`\$\{\{([^}]*)\}\}`)

// partyChosenInterpolations returns the expressions in script whose root is a
// party-chosen context, in stable order.
func partyChosenInterpolations(script string) []string {
	var found []string
	for _, match := range runInterpolation.FindAllStringSubmatch(script, -1) {
		expression := strings.TrimSpace(match[1])
		normalized := strings.ToLower(strings.Join(strings.Fields(expression), ""))
		for _, context := range partyChosenContexts {
			if strings.Contains(normalized, context) {
				found = append(found, expression)
				break
			}
		}
	}
	sort.Strings(found)
	return found
}

// TestNoRunScriptInterpolatesAPartyChosenValue is the rule: a party-chosen
// value reaches a script through the environment, never through the script's
// own text.
//
// The fix is always the same shape, and it is not a workaround -- it is the
// difference between a program that reads a value and a program assembled by
// whoever supplied it:
//
//	env:
//	  SETUP_RUN: ${{ inputs.setup-run }}
//	run: eval "$SETUP_RUN"
func TestNoRunScriptInterpolatesAPartyChosenValue(t *testing.T) {
	for _, path := range workflowFiles(t) {
		body, err := os.ReadFile(path)
		require.NoError(t, err)

		var parsed workflow
		require.NoError(t, yaml.Unmarshal(body, &parsed), "%s does not parse", filepath.Base(path))

		jobs := make([]string, 0, len(parsed.Jobs))
		for name := range parsed.Jobs {
			jobs = append(jobs, name)
		}
		sort.Strings(jobs)

		for _, name := range jobs {
			for index, step := range parsed.Jobs[name].Steps {
				if step.Run == "" {
					continue
				}
				found := partyChosenInterpolations(step.Run)
				if len(found) == 0 {
					continue
				}
				require.Failf(t, "a party-chosen value is interpolated into a `run:` script",
					"%s job %q step %d (%s) interpolates %v.\n"+
						"A `run:` is assembled before any shell sees it, so this is not a value the "+
						"script reads -- it is a line of the script, chosen by whoever triggered the "+
						"run.\nPass it through the environment instead:\n"+
						"    env:\n      NAME: ${{ %s }}\n    run: eval \"$NAME\"",
					filepath.Base(path), name, index, step.Name, found, found[0])
			}
		}
	}
}

// TestRunInterpolationDetectorFires is the self-check, and it is the reason
// this file exists at all. A guard is worth exactly what it refuses: the gap
// this rule closes was found by reverting a fix and watching a 3,800-line
// suite stay green, so this one states what it catches and proves it on each
// case rather than asserting a clean tree and being believed.
func TestRunInterpolationDetectorFires(t *testing.T) {
	for name, tc := range map[string]struct {
		script string
		caught bool
	}{
		"a reusable workflow's caller input":  {"${{ inputs.setup-run }}", true},
		"the same input inside a longer line": {"curl -o x \"https://h/v${{ inputs.version }}/x.tar.gz\"", true},
		"odd spacing and case":                {"${{   INPUTS.Setup_Run   }}", true},
		"a pull request's branch name":        {"echo ${{ github.head_ref }}", true},
		"a webhook payload field":             {"echo ${{ github.event.pull_request.title }}", true},
		"one of several, the rest benign":     {"cp ${{ runner.temp }}/a ${{ inputs.dest }}", true},
		"a runner path":                       {"mkdir -p ${{ runner.temp }}/combined", false},
		"this repository's own matrix":        {"make build-${{ matrix.language }}", false},
		"an environment variable":             {"echo \"$SETUP_RUN\"", false},
		"the documented fix":                  {"eval \"$SETUP_RUN\"", false},
		"no interpolation at all":             {"go test ./...", false},
	} {
		t.Run(name, func(t *testing.T) {
			found := partyChosenInterpolations(tc.script)
			if tc.caught {
				require.NotEmpty(t, found, "should have been refused: %s", tc.script)
				return
			}
			require.Empty(t, found, "should have been permitted: %s", tc.script)
		})
	}
}

// TestEveryWorkflowIsReadByThisRule guards the guard's REACH. A rule that
// walks a glob is only as good as the glob: a workflow this rule never opened
// is one it cannot refuse, and nothing else in this package would report the
// gap for a non-credential workflow.
func TestEveryWorkflowIsReadByThisRule(t *testing.T) {
	paths := workflowFiles(t)

	entries, err := os.ReadDir(filepath.Join(repoRoot(t), ".github", "workflows"))
	require.NoError(t, err)

	var onDisk []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if name := entry.Name(); strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") {
			onDisk = append(onDisk, name)
		}
	}

	read := make([]string, 0, len(paths))
	for _, path := range paths {
		read = append(read, filepath.Base(path))
	}
	sort.Strings(onDisk)
	sort.Strings(read)
	require.Equal(t, onDisk, read,
		"this rule reads %d of the %d workflow files on disk; the ones it never opened cannot be refused by it",
		len(read), len(onDisk))
}
