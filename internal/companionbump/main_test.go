package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/companions"
	"github.com/stretchr/testify/require"
)

var proto = companion{
	name:    "proto",
	version: "0.0.17",
	inputs:  []string{"companions/proto/Dockerfile", "companions/proto/facades/python"},
}

func baseAt(versions map[string]string) func(string) (string, bool, error) {
	return func(name string) (string, bool, error) {
		v, ok := versions[name]
		return v, ok, nil
	}
}

// The case that shipped: the Python facade plugin changed and proto stayed
// 0.0.17, so the republished 0.0.17 never reached a machine that had it.
func TestAnInputChangeKeepingTheVersionIsRefused(t *testing.T) {
	refusals, err := unbumped([]companion{proto},
		[]string{"companions/proto/facades/python/protoc_gen_codefly_facade_python.py"},
		baseAt(map[string]string{"proto": "0.0.17"}))
	require.NoError(t, err)
	require.Len(t, refusals, 1)
	require.Contains(t, refusals[0], "companion <proto>")
	require.Contains(t, refusals[0], "companions/proto/facades/python/protoc_gen_codefly_facade_python.py")
	require.Contains(t, refusals[0], "bump it above 0.0.17")
}

func TestABumpedVersionPasses(t *testing.T) {
	bumped := proto
	bumped.version = "0.0.18"
	refusals, err := unbumped([]companion{bumped},
		[]string{"companions/proto/Dockerfile"}, baseAt(map[string]string{"proto": "0.0.17"}))
	require.NoError(t, err)
	require.Empty(t, refusals)
}

// Two branches taking the same new version would publish two images under one
// tag; the base's tip already holding it refuses the second.
func TestAVersionTheBaseAlreadyTookIsRefused(t *testing.T) {
	bumped := proto
	bumped.version = "0.0.18"
	refusals, err := unbumped([]companion{bumped},
		[]string{"companions/proto/Dockerfile"}, baseAt(map[string]string{"proto": "0.0.18"}))
	require.NoError(t, err)
	require.Len(t, refusals, 1)
}

func TestAChangeOutsideTheInputsNeedsNoBump(t *testing.T) {
	refusals, err := unbumped([]companion{proto}, []string{
		"companions/proto/README.md",
		"companions/proto/testdata/facade/test_facade_gen.py",
		"companions/proto/facades/pythonic/x.py", // a sibling sharing the prefix, not under the input
	}, baseAt(map[string]string{"proto": "0.0.17"}))
	require.NoError(t, err)
	require.Empty(t, refusals)
}

func TestANewCompanionNeedsNoBump(t *testing.T) {
	refusals, err := unbumped([]companion{proto},
		[]string{"companions/proto/Dockerfile"}, baseAt(map[string]string{}))
	require.NoError(t, err)
	require.Empty(t, refusals)
}

// The real specs: every companion's Dockerfile is an input, and proto's facade
// plugins (the files 08716e9 changed) are inputs of proto.
func TestTheCheckoutsInputsCoverTheFacadePlugins(t *testing.T) {
	specs, err := companions.BuildSpecs()
	require.NoError(t, err)
	byName := map[string][]string{}
	for _, spec := range specs {
		inputs, err := spec.Inputs("../..")
		require.NoError(t, err)
		require.Contains(t, inputs, spec.Dockerfile)
		byName[spec.Name] = inputs
	}
	require.NotEmpty(t, touchedInputs(byName["proto"],
		[]string{"companions/proto/facades/python/protoc_gen_codefly_facade_python.py"}))
	require.Empty(t, touchedInputs(byName["proto"], []string{"companions/proto/README.md"}))
	require.NotContains(t, byName["codefly"], "bin/linux/${TARGETARCH}/codefly")
}

// Moving a file out of an input changes the image: git's rename detection
// would list only the destination, outside every input, and let it through.
func TestAFileMovedOutOfAnInputIsAChangeToIt(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run("init", "-q", "-b", "main")
	plugin := filepath.Join("companions", "proto", "facades", "python", "plugin.py")
	require.NoError(t, os.MkdirAll(filepath.Dir(plugin), 0o755))
	require.NoError(t, os.WriteFile(plugin, []byte("print('the facade plugin, long enough to be detected as a rename')\n"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "base")
	run("checkout", "-q", "-b", "change")
	require.NoError(t, os.MkdirAll("elsewhere", 0o755))
	run("mv", plugin, filepath.Join("elsewhere", "plugin.py"))
	run("commit", "-q", "-m", "move")

	changed, err := changedFiles("main")
	require.NoError(t, err)
	require.Contains(t, changed, "companions/proto/facades/python/plugin.py")
	refusals, err := unbumped([]companion{proto}, changed, baseAt(map[string]string{"proto": "0.0.17"}))
	require.NoError(t, err)
	require.Len(t, refusals, 1)
}
