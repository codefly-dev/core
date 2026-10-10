package ciguard

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Parse shell rather than scanning lines: options, quotes, substitutions and
// continuations must not hide a Git invocation. Unparseable shell is refused.
func executionShell(text string) (*syntax.File, error) {
	// Expressions are values resolved by Actions, not Bash syntax. The separate
	// interpolation guard judges their provenance; retain an opaque shell word.
	for {
		start := strings.Index(text, "${{")
		if start < 0 {
			break
		}
		end := strings.Index(text[start:], "}}")
		if end < 0 {
			return nil, fmt.Errorf("unterminated Actions expression")
		}
		text = text[:start] + "ACTIONS_VALUE" + text[start+end+2:]
	}
	return syntax.NewParser().Parse(strings.NewReader(text), "step")
}

func shellText(node syntax.Node) string {
	var out strings.Builder
	_ = syntax.NewPrinter().Print(&out, node)
	return out.String()
}

func shellArgument(word *syntax.Word) string {
	text := shellText(word)
	if len(text) > 1 && ((text[0] == '"' && text[len(text)-1] == '"') || (text[0] == '\'' && text[len(text)-1] == '\'')) {
		return text[1 : len(text)-1]
	}
	return text
}

// Only these global options have attributed effects. Unknown options and
// dynamic subcommands fail closed, including aliases and command wrappers.
func gitArguments(call *syntax.CallExpr) ([]*syntax.Word, string) {
	args := call.Args[1:]
	for len(args) > 0 && strings.HasPrefix(args[0].Lit(), "-") {
		option := args[0].Lit()
		if len(args) < 2 {
			return nil, "incomplete git option " + option
		}
		value := shellArgument(args[1])
		switch option {
		case "-C":
			if value != "." {
				return nil, "git -C selects an unattributed working tree"
			}
		case "-c":
			if value != "advice.detachedHead=false" && !strings.HasPrefix(value, "http.https://github.com/.extraheader=") {
				return nil, "unattributed git configuration " + value
			}
		default:
			return nil, "unattributed git option " + option
		}
		args = args[2:]
	}
	if len(args) == 0 || args[0].Lit() == "" {
		return nil, "unattributed git verb"
	}
	return args, ""
}

// An ancestry proof is an unconditional top-level `if ! git merge-base ...`
// whose failure arm terminates with exit 1. A matching string inside a comment,
// function, skipped branch, or after a tree change is not a proof.
func ancestryGuard(stmt *syntax.Stmt) string {
	branch, ok := stmt.Cmd.(*syntax.IfClause)
	if !ok || stmt.Background || stmt.Negated || len(stmt.Redirs) > 0 || branch.Else != nil || len(branch.Cond) != 1 {
		return ""
	}
	cond := branch.Cond[0]
	call, ok := cond.Cmd.(*syntax.CallExpr)
	if !ok || !cond.Negated || cond.Background || len(call.Assigns) > 0 || len(call.Args) != 5 {
		return ""
	}
	unsafeExpansion := false
	syntax.Walk(cond, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst:
			unsafeExpansion = true
		}
		return true
	})
	if unsafeExpansion || len(cond.Redirs) > 0 {
		return ""
	}
	if call.Args[0].Lit() != "git" || call.Args[1].Lit() != "merge-base" || call.Args[2].Lit() != "--is-ancestor" || !strings.HasPrefix(shellArgument(call.Args[4]), "refs/remotes/origin/") {
		return ""
	}
	if len(branch.Then) == 0 {
		return ""
	}
	for i, body := range branch.Then {
		c, ok := body.Cmd.(*syntax.CallExpr)
		if !ok || body.Background || body.Negated || len(c.Assigns) > 0 || len(c.Args) == 0 {
			return ""
		}
		if i == len(branch.Then)-1 {
			if len(c.Args) != 2 || c.Args[0].Lit() != "exit" || c.Args[1].Lit() != "1" {
				return ""
			}
		} else if c.Args[0].Lit() != "echo" {
			return ""
		}
		safe := true
		syntax.Walk(c, func(n syntax.Node) bool {
			if _, ok := n.(*syntax.CmdSubst); ok {
				safe = false
			}
			return true
		})
		if !safe {
			return ""
		}
	}
	return shellArgument(call.Args[3])
}

// The one remote-auth wrapper has a bounded implementation and bounded callers.
// Its command-scoped credential cannot change Git's tree selection. A changed
// function must be attributed again, not admitted by its name.
func isRemoteAuthWrapper(fn *syntax.FuncDecl) bool {
	if fn.Name.Value != "git_remote" {
		return false
	}
	expected, err := executionShell(`git_remote() {
 local authorization
 authorization=$(printf 'x-access-token:%s' "$GH_TOKEN" | base64 | tr -d '\n')
 git -c "http.https://github.com/.extraheader=AUTHORIZATION: basic ${authorization}" "$@"
 }`)
	return err == nil && shellText(fn) == shellText(expected.Stmts[0].Cmd)
}

func dangerousExecutionEnv(name string) bool {
	switch name {
	case "BASH_ENV", "ENV", "GIT_DIR", "GIT_WORK_TREE", "PATH", "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "NODE_OPTIONS", "PYTHONPATH":
		return true
	}
	return strings.HasPrefix(name, "GIT_CONFIG_")
}

// Before an ancestry proof, only metadata reads, their formatting, and refusal
// diagnostics are allowed. Inspect substitutions too: an assignment is not a
// licence to run a script. This is deliberately a small accepted language.
func safeRefusalPrelude(stmt *syntax.Stmt, remoteAuth bool) string {
	why := ""
	syntax.Walk(stmt, func(node syntax.Node) bool {
		if why != "" {
			return false
		}
		switch n := node.(type) {
		case *syntax.Stmt:
			if n.Background {
				why = "background execution before the ancestry refusal"
			}
			for _, r := range n.Redirs {
				if r.Op != syntax.DplOut && !(r.Op == syntax.RdrOut && shellArgument(r.Word) == "/dev/null") {
					why = "a file redirection before the ancestry refusal"
				}
			}
		case *syntax.FuncDecl:
			if !isRemoteAuthWrapper(n) {
				why = "an unattributed function before the ancestry refusal"
			}
			return false
		case *syntax.Assign:
			if n.Name != nil && (dangerousExecutionEnv(n.Name.Value) || n.Name.Value == "GITHUB_SHA") {
				why = "assignment to " + n.Name.Value + " before the ancestry refusal"
			}
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				return true
			}
			name := shellArgument(n.Args[0])
			switch name {
			case "set":
				if shellText(n) != "set -euo pipefail" {
					why = "unattributed shell options before the ancestry refusal"
				}
			case "git", "git_remote":
				args, problem := gitArguments(n)
				if problem != "" {
					why = problem
					break
				}
				if name == "git_remote" && !remoteAuth {
					why = "unattributed git_remote"
					break
				}
				switch args[0].Lit() {
				case "fetch", "ls-remote", "cat-file":
				default:
					why = "git " + args[0].Lit() + " before the ancestry refusal"
				}
			case "awk":
				if len(n.Args) != 2 || shellArgument(n.Args[1]) != `$1 == "ref:" && $3 == "HEAD" { sub("refs/heads/", "", $2); print $2 }` {
					why = "unattributed awk before the ancestry refusal"
				}
			case "[", "echo", "exit":
			default:
				why = "unattributed command " + shellText(n.Args[0]) + " before the ancestry refusal"
			}
		}
		return true
	})
	return why
}

func triggeringCommitRefusalIsFirst(text string) string {
	file, err := executionShell(text)
	if err != nil {
		return "has shell this package cannot parse: " + err.Error()
	}
	remoteAuth := false
	for _, stmt := range file.Stmts {
		if rev := ancestryGuard(stmt); rev == "$GITHUB_SHA" || rev == "${GITHUB_SHA}" {
			return ""
		}
		if why := safeRefusalPrelude(stmt, remoteAuth); why != "" {
			return why
		}
		if fn, ok := stmt.Cmd.(*syntax.FuncDecl); ok && isRemoteAuthWrapper(fn) {
			remoteAuth = true
		}
	}
	return "needs a fatal ancestry refusal ahead of everything that runs"
}

// Every Git verb must be attributed. The default is refusal, so adding another
// tree-writing verb does not require finding and extending a blacklist.
func attributedShell(text string) (leaned bool, why string) {
	file, err := executionShell(text)
	if err != nil {
		return false, "shell this package cannot parse: " + err.Error()
	}
	proven := map[string]bool{}
	remoteAuth := false
	for _, stmt := range file.Stmts {
		if fn, ok := stmt.Cmd.(*syntax.FuncDecl); ok && isRemoteAuthWrapper(fn) {
			remoteAuth = true
			continue
		}
		syntax.Walk(stmt, func(node syntax.Node) bool {
			if why != "" {
				return false
			}
			if fn, ok := node.(*syntax.FuncDecl); ok {
				why = "unattributed shell function " + fn.Name.Value
				return false
			}
			call, ok := node.(*syntax.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			name := shellArgument(call.Args[0])
			if name == "" || strings.ContainsAny(name, "$`") || strings.HasSuffix(name, "/git") || name == "command" || name == "env" || name == "exec" || name == "eval" || name == "source" || name == "." {
				why = "unattributed command indirection: " + name
				return false
			}
			if name == "git_remote" && !remoteAuth {
				why = "unattributed git_remote wrapper"
				return false
			}
			if name == "tar" {
				why = "writes content into the tree without naming a commit (tar)"
				return false
			}
			// Downloading and executing a program supplies no reviewed source tree.
			if name == "curl" || name == "wget" {
				why = "unattributed remote content from " + name
				return false
			}
			if name != "git" && name != "git_remote" {
				return true
			}
			args, problem := gitArguments(call)
			if problem != "" {
				why = problem
				return false
			}
			verb := args[0].Lit()
			switch verb {
			case "fetch", "ls-remote", "rev-parse", "cat-file", "merge-base", "log", "diff", "bundle", "push", "tag", "remote", "show":
			case "switch", "checkout":
				if len(args) != 3 || args[1].Lit() != "--detach" || !proven[shellArgument(args[2])] {
					why = "git " + shellText(call) + " puts content in the tree without showing it is reachable from " + theDefaultBranch + " first"
				} else {
					leaned = true
				}
			case "apply", "am", "archive":
				why = "writes content into the tree without naming a commit established by an ancestry refusal (git " + verb + ")"
			default:
				why = "unattributed git " + verb + " invocation: " + shellText(call)
			}
			return true
		})
		if why != "" {
			return false, why
		}
		if rev := ancestryGuard(stmt); rev != "" {
			proven[rev] = true
		}
	}
	return leaned, ""
}

// An action is admitted for an explicit source contract, never merely because
// it isn't actions/checkout. Local composites are expanded by the caller.
func attributedAction(wf isolatedWorkflow, id string, steps []isolatedStep, index int) string {
	step := steps[index]
	action, ref, ok := strings.Cut(step.Uses, "@")
	if strings.HasPrefix(step.Uses, "./") {
		return ""
	}
	if !ok || ref == "" {
		return "unattributed uses: " + step.Uses
	}
	switch action {
	case "actions/setup-go", "actions/upload-artifact", "slackapi/slack-github-action", "vladopajic/go-test-coverage", "anchore/sbom-action/download-syft", "goreleaser/goreleaser-action", "docker/setup-buildx-action", "docker/login-action", "docker/build-push-action":
		// Tool setup, publication and execution of the established tree. None is
		// an attribution for a new repository or for downloaded workflow output.
		return ""
	case "actions/download-artifact":
		// One data-only handoff: outside the workspace, consumed only by the
		// publisher whose real Git bundle fetch is executed by the handoff test.
		// Any extra consumer, action or run command invalidates this attribution.
		if wf.name == "combine-deps.yml" && id == "publish" && len(step.With) == 2 && step.With["name"] == "combined-branch" && step.With["path"] == "${{ runner.temp }}/combined" && index+2 == len(steps) {
			consumer := steps[index+1]
			if consumer.Uses == "" && consumer.WorkingDirectory == "" && consumer.Run == `bash .github/scripts/combine-deps-publish.sh "${{ runner.temp }}/combined"` {
				return ""
			}
		}
	}
	return "unattributed uses: " + step.Uses + " can introduce content whose source is not established"
}
