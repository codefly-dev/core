package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/resources"
)

func TestFlatLayout(t *testing.T) {
	ctx := context.Background()
	_, err := resources.NewFlatLayout(ctx, "root", nil)
	require.NoError(t, err)
}

// A FLAT workspace is one module named after itself, and postLoad derives that
// list. It used to overwrite whatever was declared, silently: a workspace
// declaring `modules: [mod]` loaded carrying `[solo]`. Since core#721 every
// judged edge asks the composition whether it carries both ends, so the
// overwrite answered that question with a module nobody wrote — and a reader
// who declared the list watched judging refuse an edge between modules plainly
// listed in the file, with nothing to show why.
//
// Refusing is the fix: the declaration and the layout disagree, and only the
// author can say which they meant. A list that agrees with the layout — the
// workspace's own name — is still accepted, so a file stating the derived truth
// explicitly is not broken by this.
func TestFlatLayoutRefusesAModuleListItWouldOverwrite(t *testing.T) {
	for _, tc := range []struct {
		name    string
		modules []string
		refused bool
	}{
		{name: "no list is the normal flat workspace", modules: nil},
		{name: "a list naming the workspace agrees with the layout", modules: []string{"solo"}},
		{name: "another name is a contradiction", modules: []string{"mod"}, refused: true},
		{name: "several names cannot be one module", modules: []string{"solo", "mod"}, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			body := "name: solo\nlayout: flat\n"
			if len(tc.modules) > 0 {
				body += "modules:\n"
				for _, m := range tc.modules {
					body += "    - name: " + m + "\n"
				}
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, resources.WorkspaceConfigurationName), []byte(body), 0o600))
			workspace, err := resources.LoadWorkspaceFromDir(context.Background(), dir)
			if tc.refused {
				require.Error(t, err, "a flat workspace declaring other modules must be refused, not silently rewritten")
				return
			}
			require.NoError(t, err)
			require.Len(t, workspace.Modules, 1)
			require.Equal(t, "solo", workspace.Modules[0].Name)
		})
	}
}
