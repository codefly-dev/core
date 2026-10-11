package ciguard

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/internal/testgit"
	"github.com/codefly-dev/core/resources"
)

// The composite publisher runs in the CALLER's credential-bearing job. The shell
// that decides whether that is safe is lifted out of the action and executed,
// the way go-service-release.yml's release refusal is. Reading shell in a YAML
// string establishes nothing about what it does.

const (
	publishWorkflow    = "test-publish-service-image.yml"
	publishAction      = ".github/actions/publish-service-image/action.yml"
	publishJob         = "publish"
	publishRefusalStep = "require the commit to be on the default branch, or refuse"
	publishInputsStep  = "Validate the inputs"
)

// publishSteps reads the real composite, not a copy of its scripts.
func publishSteps(t *testing.T) []isolatedStep {
	t.Helper()
	var action localAction
	require.NoError(t, yaml.Unmarshal([]byte(yamlOf(t, filepath.Join(repoRoot(t), publishAction))), &action))
	require.Equal(t, "composite", action.Runs.Using)
	return action.Runs.Steps
}

func publishCaller(t *testing.T) isolatedWorkflow {
	t.Helper()
	return parseIsolatedDocument(t, yamlOf(t, filepath.Join(repoRoot(t), ".github/workflows", publishWorkflow)), publishWorkflow)
}

// publishStep lifts one named step out of the publish job.
func publishStep(t *testing.T, name string) isolatedStep {
	t.Helper()

	for _, step := range publishSteps(t) {
		if step.Name == name {
			require.NotEmpty(t, step.Run)
			return step
		}
	}
	t.Fatalf("no step named %q, so the shell this test exercises is not the one that runs", name)
	return isolatedStep{}
}

// A commit is published only when it is reachable from the repository's OWN
// default branch: a dispatch can name any branch, and the Dockerfile it builds
// runs with the registry credential.
func TestAServiceImageIsPublishedOnlyFromTheRepositorysOwnDefaultBranch(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-read-token")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	script := executionTestStep(t).Run

	for _, tc := range []struct {
		name   string
		commit string
		admit  bool
	}{
		{name: "the default branch's tip", commit: "tip", admit: true},
		{name: "an older commit on the default branch", commit: "older", admit: true},
		// Genuinely reachable from real branches, and still not the default
		// branch: nominating one's own history as the authority must not admit
		// a commit that was never merged.
		{name: "a commit on no default branch", commit: "unmerged", admit: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPublishFixture(t)
			sha := map[string]string{
				"tip": fixture.tip, "older": fixture.older, "unmerged": fixture.unmerged,
			}[tc.commit]

			admitted, output := fixture.run(t, script, theDefaultBranch, sha)
			require.Equal(t, tc.admit, admitted, "commit %s:\n%s", tc.commit, output)
			if !tc.admit {
				require.Contains(t, output, "refusing to publish",
					"a refusal must say so, with the reason")
			}
		})
	}
}

// The refusal has to be the thing that decides: neutralising it must be
// visible, which makes the test above one of behaviour rather than of text.
func TestNeutralisingThePublishRefusalIsVisible(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-read-token")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	script := publishStep(t, publishRefusalStep).Run
	fixture := newPublishFixture(t)

	admitted, output := fixture.run(t, script, theDefaultBranch, fixture.unmerged)
	require.False(t, admitted, "%s", output)

	admitted, _ = fixture.run(t, strings.ReplaceAll(script, "exit 1", "exit 0"), theDefaultBranch, fixture.unmerged)
	require.True(t, admitted,
		"with every refusal turned into a success the unmerged commit is still "+
			"rejected, so something other than the refusal decided it")
}

// The refusal takes no caller input or secret, so the authority it checks against
// has one source, and it runs before anything the caller's tree can influence:
// the first step after the checkout, ahead of the validation, the login that
// receives the credential and the build that runs the Dockerfile.
func TestThePublishRefusalTakesNoInputAndRunsFirst(t *testing.T) {
	steps := publishSteps(t)
	require.NotEmpty(t, steps)
	require.Equal(t, publishRefusalStep, steps[0].Name, "the action refuses before any other step")
	require.Equal(t, map[string]string{"GH_TOKEN": "${{ github.token }}"}, steps[0].Env,
		"the registry-token input reaches only login; Git remote reads use the job token")
	checkout := publishCaller(t).Jobs[publishJob].Steps[0]
	require.True(t, strings.HasPrefix(checkout.Uses, "actions/checkout@"))
	require.Equal(t, false, checkout.With["persist-credentials"])
	require.Empty(t, steps[0].If)

	for _, step := range steps[1:] {
		for _, text := range []string{step.Run, step.If} {
			require.NotContains(t, text, "inputs.",
				"step %q reads a caller input outside the environment; inputs reach shell only as values", step.Name)
		}
	}
}

// The credential job is reachable by a dispatch and by nothing a pull request
// or a merge queue can produce.
func TestThePublishJobCannotRunOnAPullRequestOrInAMergeQueue(t *testing.T) {
	_, workflows := loadIsolatedWorkflows(t)
	wf := workflows[filepath.Join(repoRoot(t), ".github", "workflows", publishWorkflow)]
	for _, trigger := range []string{"pull_request", "pull_request_target", "merge_group"} {
		unreachable, reason := provablyUnreachable(t, wf.Jobs[publishJob].If, scenariosForTrigger(trigger))
		require.True(t, unreachable, "%s can reach the publish job: %s", trigger, reason)
	}
}

// The workflow cannot import resources.ImageRegistry, so a test holds the two
// together: an image pushed to one registry and a lock read against another is
// a service that never starts.
func TestThePublishWorkflowPushesToCoresImageRegistry(t *testing.T) {
	require.Equal(t, resources.ImageRegistry, publishStep(t, publishInputsStep).Env["REGISTRY"])
}

// Only the inputs' values reach shell, and each is validated before the login
// step receives the credential. The validation is shell in a YAML string, so it
// is executed.
func TestThePublishWorkflowValidatesItsInputsBeforeTheCredentialIsUsed(t *testing.T) {
	steps := publishSteps(t)
	validated, loggedIn := -1, -1
	for i, step := range steps {
		if step.Name == publishInputsStep {
			validated = i
		}
		if strings.HasPrefix(step.Uses, "docker/login-action@") {
			loggedIn = i
		}
	}
	require.NotEqual(t, -1, validated)
	require.NotEqual(t, -1, loggedIn)
	require.Less(t, validated, loggedIn, "the inputs are validated before the credential is used")

	step := publishStep(t, publishInputsStep)
	fixture := newPublishFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(fixture.dir, "untracked-Dockerfile"), []byte("FROM scratch\n"), 0o600))
	valid := map[string]string{
		"IMAGE_NAME":        "",
		"CALLER_REPOSITORY": "codefly-dev/service-warehouse",
		"VERSION":           "1.2.3",
		"LOCK_FILE":         "gateway-image.json",
		"PLATFORMS":         "linux/amd64,linux/arm64",
		"REGISTRY":          resources.ImageRegistry,
		"DOCKERFILE":        "Dockerfile",
	}
	run := func(t *testing.T, overrides map[string]string) (bool, string, string) {
		t.Helper()
		env := append([]string{}, os.Environ()...)
		for key, value := range valid {
			if override, ok := overrides[key]; ok {
				value = override
			}
			env = append(env, key+"="+value)
		}
		output := filepath.Join(t.TempDir(), "output")
		env = append(env, "GITHUB_OUTPUT="+output)
		command := exec.Command("bash", "-c", step.Run)
		command.Dir = fixture.dir
		command.Env = env
		combined, err := command.CombinedOutput()
		written, _ := os.ReadFile(output)
		return err == nil, string(combined), string(written)
	}

	ok, output, written := run(t, nil)
	require.True(t, ok, output)
	require.Equal(t, "image=ghcr.io/codefly-dev/service-warehouse\nversion=1.2.3\n", written)

	for name, overrides := range map[string]map[string]string{
		"an image name with a registry":        {"IMAGE_NAME": "ghcr.io/other/app"},
		"an image name with upper case":        {"IMAGE_NAME": "Service"},
		"an empty derived image name":          {"CALLER_REPOSITORY": ""},
		"an image name injecting an output":    {"IMAGE_NAME": "app\nversion=9.9.9"},
		"a v prefixed version":                 {"VERSION": "v1.2.3"},
		"a two part version":                   {"VERSION": "1.2"},
		"a leading zero":                       {"VERSION": "01.2.3"},
		"a version injecting an output":        {"VERSION": "1.2.3\nimage=evil"},
		"a lock file in another directory":     {"LOCK_FILE": "../gateway-image.json"},
		"a lock file not ending in -image":     {"LOCK_FILE": "gateway.json"},
		"platforms with a shell metacharacter": {"PLATFORMS": "linux/amd64;rm -rf /"},
		"empty platforms":                      {"PLATFORMS": ""},
		"absolute Dockerfile":                  {"DOCKERFILE": "/Dockerfile"},
		"escaping Dockerfile":                  {"DOCKERFILE": "../Dockerfile"},
		"untracked Dockerfile":                 {"DOCKERFILE": "untracked-Dockerfile"},
	} {
		t.Run(name, func(t *testing.T) {
			ok, output, written := run(t, overrides)
			require.False(t, ok, "accepted: %s", output)
			require.Empty(t, written, "a refused input must write no output")
		})
	}
	for name, overrides := range map[string]map[string]string{
		"explicit image override": {"IMAGE_NAME": "another-service"},
		"mixed case repository":   {"CALLER_REPOSITORY": "codefly-dev/Service-Warehouse"},
		"another lock name":       {"LOCK_FILE": "minio-image.json"},
		"one platform":            {"PLATFORMS": "linux/arm64"},
		"a zero version":          {"VERSION": "0.0.0"},
		"a dotted image name":     {"IMAGE_NAME": "service.warehouse"},
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			ok, output, written := run(t, overrides)
			require.True(t, ok, output)
			name := overrides["IMAGE_NAME"]
			if name == "" {
				name = "service-warehouse"
			}
			require.Contains(t, written, "image="+resources.ImageRegistry+"/"+name+"\n")
		})
	}
}

// Execute the authenticated refusal against a real repository. A caller may
// COPY . . without a .dockerignore: no token may remain in its build context.
func TestThePublishRefusalLeavesNoCredentialInTheBuildContext(t *testing.T) {
	const token = "fixture-read-token"
	t.Setenv("GH_TOKEN", token)
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	steps := publishSteps(t)
	require.Equal(t, false, publishCaller(t).Jobs[publishJob].Steps[0].With["persist-credentials"])
	fixture := newPublishFixture(t)
	admitted, output := fixture.run(t, publishStep(t, publishRefusalStep).Run, theDefaultBranch, fixture.tip)
	require.True(t, admitted, output)
	encoded := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	require.NotContains(t, output, token)
	require.NotContains(t, output, encoded)
	err := filepath.WalkDir(fixture.dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		require.NotContains(t, string(data), token, path)
		require.NotContains(t, string(data), encoded, path)
		return nil
	})
	require.NoError(t, err)
	for _, step := range steps[1:] {
		require.NotContains(t, step.Env, "GH_TOKEN", "only the refusal receives the read token")
	}
}

// The action also refuses an incorrectly gated caller, before remote access.
func TestThePublishActionRefusesNonDispatchEvents(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-read-token")
	for _, event := range []string{"pull_request", "pull_request_target", "push", "merge_group", ""} {
		t.Run(event, func(t *testing.T) {
			t.Setenv("GITHUB_EVENT_NAME", event)
			cmd := exec.Command("bash", "-c", publishStep(t, publishRefusalStep).Run)
			cmd.Dir = t.TempDir() // no origin: an event refusal must happen first
			output, err := cmd.CombinedOutput()
			require.Error(t, err)
			require.Contains(t, string(output), "only be published by workflow_dispatch")
		})
	}
}

func TestThePublishActionWritesTheSameLockAsItsOutput(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "output")
	digest := "sha256:" + strings.Repeat("a", 64)
	cmd := exec.Command("bash", "-c", publishStep(t, "Record the digest").Run)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"IMAGE="+resources.ImageRegistry+"/service-test", "DIGEST="+digest,
		"LOCK_FILE=gateway-image.json", "GITHUB_OUTPUT="+outputPath,
		"GITHUB_STEP_SUMMARY="+filepath.Join(dir, "summary"))
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	artifact, err := os.ReadFile(filepath.Join(dir, "release-evidence/gateway-image.json"))
	require.NoError(t, err)
	returned, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(returned), "json="))
	require.JSONEq(t, string(artifact), strings.TrimPrefix(strings.TrimSpace(string(returned)), "json="))
	image, err := resources.ParseImageLock(artifact, "service-test")
	require.NoError(t, err)
	require.Equal(t, digest, image.Digest)
}

// Moving checkout into the caller must not let a proof of GITHUB_SHA bless a
// different checked-out commit, even when that SHA itself was merged.
func TestThePublishActionRefusesACallerCheckoutOfAnotherCommit(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-read-token")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	fixture := newPublishFixture(t)
	script := publishStep(t, publishRefusalStep).Run
	admitted, output := fixture.run(t, script, theDefaultBranch, fixture.unmerged)
	require.False(t, admitted, output) // leaves the unmerged tree checked out
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = fixture.dir
	cmd.Env = append(os.Environ(), "GITHUB_SHA="+fixture.tip, "GITHUB_REF_NAME=main")
	outputBytes, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(outputBytes), "checkout must select the triggering commit")
}

func publishGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	output, err := testgit.Run(context.Background(), dir, nil, args...)
	require.NoError(t, err, "%s", output)
	return strings.TrimSpace(string(output))
}

// Real file:// origin with the same server/repository identity split GitHub
// supplies. No network or Git command is mocked.
func newPublishFixture(t *testing.T) releaseFixture {
	t.Helper()
	fixture := newReleaseFixture(t)
	origin := publishGit(t, fixture.dir, "remote", "get-url", "origin")
	publishGit(t, fixture.dir, "remote", "set-url", "origin", "file://"+origin)
	t.Setenv("GITHUB_SERVER_URL", "file://"+filepath.Dir(origin))
	t.Setenv("GITHUB_REPOSITORY", strings.TrimSuffix(filepath.Base(origin), ".git"))
	require.NoError(t, os.WriteFile(filepath.Join(fixture.dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(fixture.dir, ".gitignore"), []byte("ignored.bin\n"), 0o600))
	publishGit(t, fixture.dir, "add", "Dockerfile", ".gitignore")
	publishGit(t, fixture.dir, "commit", "-m", "committed build context")
	fixture.tip = publishGit(t, fixture.dir, "rev-parse", "HEAD")
	publishGit(t, fixture.dir, "push", "origin", "HEAD:refs/heads/main")
	return fixture
}

func TestThePublishActionRefusesUncommittedBuildContext(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-read-token")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	for _, mutation := range []string{"git restore", "staged", "untracked", "ignored"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := newPublishFixture(t)
			switch mutation {
			case "git restore", "staged":
				publishGit(t, fixture.dir, "checkout", "-b", "unmerged-dockerfile")
				require.NoError(t, os.WriteFile(filepath.Join(fixture.dir, "Dockerfile"), []byte("FROM scratch\nLABEL unmerged=true\n"), 0o600))
				publishGit(t, fixture.dir, "add", "Dockerfile")
				publishGit(t, fixture.dir, "commit", "-m", "unmerged Dockerfile")
				publishGit(t, fixture.dir, "checkout", "main")
				args := []string{"restore", "--source=unmerged-dockerfile"}
				if mutation == "staged" {
					args = append(args, "--staged", "--worktree")
				}
				publishGit(t, fixture.dir, append(args, "--", "Dockerfile")...)
			default:
				name := "injected.bin"
				if mutation == "ignored" {
					name = "ignored.bin"
				}
				require.NoError(t, os.WriteFile(filepath.Join(fixture.dir, name), []byte("unreviewed"), 0o600))
			}
			require.Equal(t, fixture.tip, publishGit(t, fixture.dir, "rev-parse", "HEAD"))
			admitted, output := fixture.run(t, publishStep(t, publishRefusalStep).Run, theDefaultBranch, fixture.tip)
			require.False(t, admitted, output)
			require.Contains(t, output, "build context must be the clean triggering commit")
		})
	}
}

func TestThePublishActionRefusesStoredCheckoutCredentials(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-read-token")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	for _, scope := range []string{"local", "worktree"} {
		t.Run(scope, func(t *testing.T) {
			for key, value := range map[string]string{
				"http.https://github.com/.extraheader": "AUTHORIZATION: basic fixture-secret",
				"http.extraheader":                     "AUTHORIZATION: basic fixture-secret",
				"credential.helper":                    "!echo fixture-secret",
				"include.path":                         "credentials-include",
				"includeIf.gitdir:/tmp/.path":          "credentials-include",
				"github.token":                         "fixture-secret",
				"remote.backup.url":                    "https://fixture-secret@example.com/repo.git",
				"url.https://github.com/.insteadOf":    "https://other.example/",
			} {
				t.Run(key, func(t *testing.T) {
					fixture := newPublishFixture(t)
					if scope == "worktree" {
						publishGit(t, fixture.dir, "config", "extensions.worktreeConfig", "true")
					}
					if strings.HasPrefix(key, "include") {
						value = filepath.Join(t.TempDir(), "credentials")
						require.NoError(t, os.WriteFile(value, []byte("[http]\nextraheader = AUTHORIZATION: basic fixture-secret\n"), 0o600))
					}
					publishGit(t, fixture.dir, "config", "--"+scope, key, value)
					// Run directly: include/helper values must never execute even before
					// the refusal, including through a fixture's checkout command.
					command := exec.Command("bash", "-c", publishStep(t, publishRefusalStep).Run)
					command.Dir = fixture.dir
					command.Env = append(os.Environ(), "GITHUB_SHA="+fixture.tip, "GITHUB_REF_NAME=main")
					output, err := command.CombinedOutput()
					require.Error(t, err)
					require.Contains(t, string(output), "stored credentials or credential indirection")
					require.NotContains(t, string(output), "fixture-secret")
				})
			}
		})
	}
}

func TestThePublishActionRefusesAForkAsDefaultBranchAuthority(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-read-token")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	fixture := newPublishFixture(t)
	fork := filepath.Join(t.TempDir(), "fork.git")
	publishGit(t, fixture.dir, "clone", "--bare", fixture.dir, fork)
	publishGit(t, fixture.dir, "remote", "set-url", "origin", "file://"+fork)
	admitted, output := fixture.run(t, publishStep(t, publishRefusalStep).Run, theDefaultBranch, fixture.tip)
	require.False(t, admitted, output)
	require.Contains(t, output, "origin must be the GitHub repository running this job")
}

// Status reports a clean tree for these flags even with a rewritten Dockerfile.
func TestThePublishActionRefusesIndexFlags(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-read-token")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	for _, flag := range []string{"--skip-worktree", "--assume-unchanged"} {
		t.Run(flag, func(t *testing.T) {
			fixture := newPublishFixture(t)
			publishGit(t, fixture.dir, "update-index", flag, "Dockerfile")
			require.NoError(t, os.WriteFile(filepath.Join(fixture.dir, "Dockerfile"), []byte("FROM scratch\nLABEL unmerged=true\n"), 0o600))
			require.Empty(t, publishGit(t, fixture.dir, "status", "--porcelain=v1"), "demonstrate the status bypass")
			cmd := exec.Command("bash", "-c", publishStep(t, publishRefusalStep).Run)
			cmd.Dir = fixture.dir
			cmd.Env = append(os.Environ(), "GITHUB_SHA="+fixture.tip, "GITHUB_REF_NAME=main")
			output, err := cmd.CombinedOutput()
			require.Error(t, err)
			require.Contains(t, string(output), "tracked paths must not use assume-unchanged or skip-worktree")
		})
	}
}

func TestThePublishActionNeverExecutesRepositoryFSMonitor(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-read-token")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	for _, scope := range []string{"local", "worktree"} {
		t.Run(scope, func(t *testing.T) {
			fixture := newPublishFixture(t)
			if scope == "worktree" {
				publishGit(t, fixture.dir, "config", "extensions.worktreeConfig", "true")
			}
			hook := filepath.Join(t.TempDir(), "fsmonitor")
			marker := filepath.Join(t.TempDir(), "executed")
			t.Setenv("FSMONITOR_MARKER", marker)
			require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nprintf '%s\\n' \"${GH_TOKEN-unset}\" > \"$FSMONITOR_MARKER\"\n"), 0o700))
			publishGit(t, fixture.dir, "config", "--"+scope, "core.fsmonitor", hook)
			publishGit(t, fixture.dir, "status", "--porcelain=v1")
			leaked, err := os.ReadFile(marker)
			require.NoError(t, err, "the real hook must execute without the guard")
			require.Contains(t, string(leaked), "fixture-read-token")
			require.NoError(t, os.Remove(marker))

			script := publishStep(t, publishRefusalStep).Run
			run := func(script string) (string, error) {
				cmd := exec.Command("bash", "-c", script)
				cmd.Dir = fixture.dir
				cmd.Env = append(os.Environ(), "GITHUB_SHA="+fixture.tip, "GITHUB_REF_NAME=main")
				output, err := cmd.CombinedOutput()
				require.NoFileExists(t, marker, "the guard must never execute the hook")
				require.NotContains(t, string(output), "fixture-read-token")
				return string(output), err
			}
			output, err := run(script)
			require.Error(t, err)
			require.Contains(t, output, "fsmonitor in local/worktree git config")
			// Even with the config refusal neutralised, command-scoped fsmonitor=false
			// must prevent execution through ls-files/status and all earlier Git reads.
			output, err = run(strings.ReplaceAll(script, `core\.(sshcommand|fsmonitor)=`, `core\.sshcommand=`))
			require.NoError(t, err, output)
		})
	}
}

// Git's own trace (not a wrapper/mock) observes the environment of every Git
// process, including the authenticated remote reads. The token stays in Bash.
func TestThePublishGuardDoesNotExportItsTokenToGit(t *testing.T) {
	t.Setenv("GH_TOKEN", "fixture-read-token")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	fixture := newPublishFixture(t)
	t.Setenv("GIT_TRACE2_ENV_VARS", "GH_TOKEN")
	control := filepath.Join(t.TempDir(), "control.json")
	t.Setenv("GIT_TRACE2_EVENT", control)
	publishGit(t, fixture.dir, "rev-parse", "HEAD")
	traced, err := os.ReadFile(control)
	require.NoError(t, err)
	require.Contains(t, string(traced), `"param":"GH_TOKEN"`, "prove Git traces exported tokens")
	require.Contains(t, string(traced), "fixture-read-token")

	trace := filepath.Join(t.TempDir(), "guard.json")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	cmd := exec.Command("bash", "-c", publishStep(t, publishRefusalStep).Run)
	cmd.Dir = fixture.dir
	cmd.Env = append(os.Environ(), "GITHUB_SHA="+fixture.tip, "GITHUB_REF_NAME=main")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	traced, err = os.ReadFile(trace)
	require.NoError(t, err)
	require.Contains(t, string(traced), `"ls-remote"`)
	require.Contains(t, string(traced), `"status"`)
	require.NotContains(t, string(traced), `"param":"GH_TOKEN"`)
	require.NotContains(t, string(traced), "fixture-read-token")
}
