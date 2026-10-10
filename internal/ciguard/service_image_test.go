package ciguard

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/resources"
)

// publish-service-image.yml hands a registry credential to a job that builds
// the CALLER's Dockerfile, so it is a registered credential job and the shell
// that decides whether that is safe is lifted out of the workflow and executed,
// the way go-service-release.yml's release refusal is. Reading shell in a YAML
// string establishes nothing about what it does.

const (
	publishWorkflow    = "publish-service-image.yml"
	publishJob         = "publish"
	publishRefusalStep = "require the commit to be on the default branch, or refuse"
	publishInputsStep  = "Validate the inputs"
)

// publishSteps returns the steps of the publish job.
func publishSteps(t *testing.T) []isolatedStep {
	t.Helper()

	_, workflows := loadIsolatedWorkflows(t)
	wf, ok := workflows[filepath.Join(repoRoot(t), ".github", "workflows", publishWorkflow)]
	require.True(t, ok, "%s did not load", publishWorkflow)
	job, ok := wf.Jobs[publishJob]
	require.True(t, ok, "%s has no job %q", publishWorkflow, publishJob)
	return job.Steps
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
	script := publishStep(t, publishRefusalStep).Run

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
			fixture := newReleaseFixture(t)
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
	script := publishStep(t, publishRefusalStep).Run
	fixture := newReleaseFixture(t)

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
	require.GreaterOrEqual(t, len(steps), 2)
	require.True(t, strings.HasPrefix(steps[0].Uses, "actions/checkout@"), "the first step is the checkout")
	require.Equal(t, publishRefusalStep, steps[1].Name,
		"the refusal must be the first thing that runs after the checkout")
	require.Equal(t, map[string]string{"GH_TOKEN": "${{ github.token }}"}, steps[1].Env,
		"only the job read token reaches the refusal, never caller inputs or secrets")
	require.Equal(t, false, steps[0].With["persist-credentials"], "the caller build context must contain no persisted checkout credential")
	require.Empty(t, steps[1].If, "a condition on the refusal is a way to skip it")

	for _, step := range steps[2:] {
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
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", publishWorkflow))
	require.NoError(t, err)
	require.Contains(t, string(raw), "\n  REGISTRY: "+resources.ImageRegistry+"\n")
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
	valid := map[string]string{
		"IMAGE_NAME":        "",
		"CALLER_REPOSITORY": "codefly-dev/service-warehouse",
		"VERSION":           "1.2.3",
		"LOCK_FILE":         "gateway-image.json",
		"PLATFORMS":         "linux/amd64,linux/arm64",
		"REGISTRY":          resources.ImageRegistry,
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
	steps := publishSteps(t)
	require.Equal(t, false, steps[0].With["persist-credentials"])
	fixture := newReleaseFixture(t)
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
	for _, step := range steps[2:] {
		require.NotContains(t, step.Env, "GH_TOKEN", "only the refusal receives the read token")
	}
}
