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

// Load the shipped action through an actual caller and real manifest files.
// Mutations are re-recorded so the execution judgement must catch them itself.
func TestCompositePublisherEstablishesExecutionAcrossTheCallBoundary(t *testing.T) {
	caller := yamlOf(t, filepath.Join(repoRoot(t), ".github/workflows", publishWorkflow))
	action := yamlOf(t, filepath.Join(repoRoot(t), publishAction))
	// A reviewed composite may itself check out the triggering commit. Its
	// refusal must establish that new tree before login or any other execution.
	withCheckout := strings.Replace(action, "  steps:\n", "  steps:\n    - uses: actions/checkout@v7\n      with:\n        persist-credentials: false\n", 1)
	for _, tc := range []struct {
		name, caller, action, reason string
	}{
		{"shipped caller", caller, action, ""},
		{"triggering checkout inside reviewed action", caller, withCheckout, ""},
		{"unreviewed local manifest cannot certify itself", strings.Replace(caller, "          ref: main\n", "", 1), action, "refusal ahead of everything"},
		{"conditional composite", strings.Replace(caller, "        id: publish", "        if: success()\n        id: publish", 1), withCheckout, "composite call can skip"},
		{"nonfatal composite", strings.Replace(caller, "        id: publish", "        continue-on-error: true\n        id: publish", 1), withCheckout, "composite call can skip"},
		{"composite environment", strings.Replace(caller, "        id: publish", "        env:\n          BASH_ENV: ./hostile.sh\n        id: publish", 1), withCheckout, "execution-changing environment"},
		{"conditional nested refusal", caller, strings.Replace(withCheckout, "      env:\n", "      if: success()\n      env:\n", 1), "so it can be skipped"},
		{"nonfatal nested refusal", caller, strings.Replace(withCheckout, "      env:\n", "      continue-on-error: true\n      env:\n", 1), "exit status stops nothing"},
		{"nested wrong checkout", caller, strings.Replace(withCheckout, "        persist-credentials: false", "        ref: ${{ github.head_ref }}\n        persist-credentials: false", 1), "tree holds a different ref"},
		{"nested execution before refusal", caller, strings.Replace(withCheckout, "    - name: require", "    - run: ./unreviewed.sh\n      shell: bash\n    - name: require", 1), "refusal ahead of everything"},
		{"nested repoint after refusal", caller, strings.Replace(withCheckout, "    - name: Validate", "    - run: git checkout FETCH_HEAD\n      shell: bash\n    - name: Validate", 1), "without showing it is reachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := parseIsolatedDocument(t, tc.caller, publishWorkflow)
			wf.actionRoot = t.TempDir()
			path := filepath.Join(wf.actionRoot, publishAction)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(t, os.WriteFile(path, []byte(tc.action), 0o600))
			previous := recordedJobDigests
			t.Cleanup(func() { recordedJobDigests = previous })
			recordedJobDigests = maps.Clone(previous)
			recordedJobDigests[recordKey(wf.name, publishJob)] = canonicalJobDigest(t, wf, publishJob)
			accepted, why := credentialJobIsAccepted(t, wf, publishJob)
			require.Equal(t, tc.reason == "", accepted, why)
			if tc.reason != "" {
				require.Contains(t, why, tc.reason)
				require.NotContains(t, why, "changed shape")
			}
		})
	}
}

func TestTheCallerRecordCoversTheCompositeManifest(t *testing.T) {
	wf := publishCaller(t)
	wf.actionRoot = t.TempDir()
	path := filepath.Join(wf.actionRoot, publishAction)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	source := yamlOf(t, filepath.Join(repoRoot(t), publishAction))
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	original := canonicalJobDigest(t, wf, publishJob)
	for _, change := range []string{
		strings.Replace(source, "exit 1", "exit 0", 1),
		strings.Replace(source, "value: ${{ steps.build.outputs.digest }}", "value: wrong", 1),
		strings.Replace(source, "default: gateway-image.json", "default: wrong-image.json", 1),
	} {
		require.NoError(t, os.WriteFile(path, []byte(change), 0o600))
		require.NotEqual(t, original, canonicalJobDigest(t, wf, publishJob))
	}
}

func TestRepeatedCompositeCallsAreExpandedEveryTime(t *testing.T) {
	job := isolatedJob{Steps: []isolatedStep{
		{Uses: "./.github/actions/publish-service-image"},
		{Uses: "./.github/actions/publish-service-image"},
	}}
	steps, unreadable := stepsIncludingLocalActions(t, job)
	require.Empty(t, unreadable)
	require.Len(t, steps, 2*(1+len(publishSteps(t))))
}

func TestTheCompositeIsTheOnlyPublisherAndReturnsTheLock(t *testing.T) {
	_, err := os.Stat(filepath.Join(repoRoot(t), ".github/workflows/publish-service-image.yml"))
	require.True(t, os.IsNotExist(err), "the reusable publisher must not remain alongside the action")
	source := yamlOf(t, filepath.Join(repoRoot(t), publishAction))
	require.Equal(t, 1, strings.Count(source, "${{ inputs.registry-token }}"))
	var action struct {
		Outputs map[string]struct{ Value string } `yaml:"outputs"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(source), &action))
	require.Equal(t, "${{ steps.build.outputs.digest }}", action.Outputs["digest"].Value)
	require.Equal(t, "${{ steps.lock.outputs.json }}", action.Outputs["lock"].Value)
	for _, step := range publishSteps(t) {
		switch {
		case strings.HasPrefix(step.Uses, "docker/login-action@"):
			require.Equal(t, "${{ inputs.registry-token }}", step.With["password"])
		case strings.HasPrefix(step.Uses, "docker/build-push-action@"):
			require.Equal(t, "type=image,name=${{ steps.inputs.outputs.image }},push-by-digest=true,name-canonical=true,push=true", step.With["outputs"])
			require.NotContains(t, step.With, "tags")
		case strings.HasPrefix(step.Uses, "actions/upload-artifact@"):
			require.Equal(t, "release-evidence/${{ inputs.lock-file }}", step.With["path"])
			require.Equal(t, "error", step.With["if-no-files-found"])
		}
	}
}
