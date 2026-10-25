package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

// loadMutatedWordCount loads the fixture runnable after mutate edits its
// declaration, so each case states only what it changes.
func loadMutatedWordCount(t *testing.T, mutate func(declaration map[string]any)) (*resources.Runnable, error) {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(withRunnables, "runnables/word-count", resources.RunnableConfigurationName))
	require.NoError(t, err)
	declaration := map[string]any{}
	require.NoError(t, yaml.Unmarshal(source, &declaration))
	mutate(declaration)
	content, err := yaml.Marshal(declaration)
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.RunnableConfigurationName), content, 0o600))
	return resources.LoadRunnableFromDir(context.Background(), dir)
}

func inputFieldsOf(declaration map[string]any) []any {
	return declaration["contract"].(map[string]any)["input"].(map[string]any)["fields"].([]any)
}

func withFileInput(fileField map[string]any) func(map[string]any) {
	return func(declaration map[string]any) {
		input := declaration["contract"].(map[string]any)["input"].(map[string]any)
		input["fields"] = append(inputFieldsOf(declaration), fileField)
	}
}

// A file field is a document with the exact media types its handler reads,
// and it survives the trip to the wire and back, media types included, so a
// package descriptor states the same contract the declaration did.
func TestRunnableFileFieldRoundTripsItsMediaTypes(t *testing.T) {
	r, err := loadMutatedWordCount(t, withFileInput(map[string]any{
		"name": "pdf", "type": "file", "media-types": []any{"application/pdf"},
	}))
	require.NoError(t, err)
	file := r.Contract.Input.Fields[2]
	require.Equal(t, resources.RunnableFieldFile, file.Type)
	require.Equal(t, []string{"application/pdf"}, file.MediaTypes)

	wire := r.Contract.Proto()
	require.Equal(t, basev0.RunnableField_FILE, wire.GetInput().GetFields()[2].GetType())
	require.Equal(t, []string{"application/pdf"}, wire.GetInput().GetFields()[2].GetMediaTypes())

	back, err := resources.RunnableContractFromProto(wire)
	require.NoError(t, err)
	require.Equal(t, r.Contract.Input.Fields[2], back.Input.Fields[2])
}

func TestRunnableFileFieldNamesExactMediaTypes(t *testing.T) {
	cases := []struct {
		name  string
		field map[string]any
		want  string
	}{
		{"no media types", map[string]any{"name": "doc", "type": "file"}, "file without media-types"},
		{"wildcard", map[string]any{"name": "doc", "type": "file", "media-types": []any{"application/*"}}, "not an exact lowercase type/subtype"},
		{"parameter", map[string]any{"name": "doc", "type": "file", "media-types": []any{"text/html; charset=utf-8"}}, "not an exact lowercase type/subtype"},
		{"upper case", map[string]any{"name": "doc", "type": "file", "media-types": []any{"Application/PDF"}}, "not an exact lowercase type/subtype"},
		{"twice", map[string]any{"name": "doc", "type": "file", "media-types": []any{"application/pdf", "application/pdf"}}, "twice"},
		{"on a string", map[string]any{"name": "doc", "type": "string", "media-types": []any{"application/pdf"}}, "declares media-types but is not a file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadMutatedWordCount(t, withFileInput(tc.field))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// The operation block is the policy a packaged runnable is installed with. A
// service method already declares one on its option, so a second one there is
// refused; its content is read by runnable.AuthoredOperation, not here.
func TestRunnableOperationBelongsToAPackagedRunnable(t *testing.T) {
	operation := map[string]any{"audience": "tools", "tool": map[string]any{"name": "count_words"}}
	r, err := loadMutatedWordCount(t, func(d map[string]any) { d["operation"] = operation })
	require.NoError(t, err)
	require.Equal(t, "tools", r.Operation["audience"])

	_, err = loadMutatedWordCount(t, func(d map[string]any) {
		d["operation"] = operation
		execution := d["execution"].(map[string]any)
		execution["facilities"] = []any{"service"}
		execution["cancellation"] = "none"
		d["contract"].(map[string]any)["protocol"] = resources.RunnableServiceProtocolV1
		delete(d, "entrypoint")
		d["agent"].(map[string]any)["kind"] = "codefly:service"
	})
	require.ErrorContains(t, err, "only a packaged runnable authors its operation policy")
}
