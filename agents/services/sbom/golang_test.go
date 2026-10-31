package sbom

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/stretchr/testify/require"
)

func TestGolangWorkspaceSourceAndGraph(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	write := func(name, content string) {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	}
	write("app/go.mod", "module example.test/app\n\ngo 1.27.0\nrequire rsc.io/quote v1.5.1\n")
	main := "package main\nimport (\"fmt\"; \"rsc.io/quote\")\nfunc main() { fmt.Println(quote.Hello()) }\n"
	write("app/main.go", main)
	write("app/cmd/demo/main.go", main)
	write("sibling/go.mod", "module rsc.io/quote\n\ngo 1.27.0\n")
	write("sibling/quote.go", "package quote\nfunc Hello() string { return \"workspace sibling\" }\n")
	source := filepath.Join(root, "app")
	_, err = runGo(t.Context(), source, GolangOptions{}, "mod", "tidy")
	require.NoError(t, err)
	alias := filepath.Join(root, "alias")
	require.NoError(t, os.Symlink(source, alias))
	work := filepath.Join(root, "go.work")
	t.Setenv("GOWORK", work)
	const appRef = "pkg:golang/example.test/app"
	const localQuote = "pkg:golang/rsc.io/quote"
	const pinnedQuote = localQuote + "@v1.5.1"
	const sampler = "pkg:golang/rsc.io/sampler@v1.3.0"
	for _, order := range []string{"./app\n./sibling", "./sibling\n./app"} {
		write("go.work", "go 1.27.0\nuse (\n"+order+"\n)\n")
		for _, dir := range []string{source, filepath.Join(source, "cmd/demo"), alias} {
			t.Run(strings.ReplaceAll(order, "\n", "_")+"/"+filepath.Base(dir), func(t *testing.T) {
				options := GolangOptions{UseWorkspace: true}
				listed, err := runGo(t.Context(), dir, options, "list", "-m", "-json")
				require.NoError(t, err)
				decoder := json.NewDecoder(strings.NewReader(string(listed)))
				var mains []string
				for decoder.More() {
					var module goModule
					require.NoError(t, decoder.Decode(&module))
					require.True(t, module.Main)
					mains = append(mains, module.Path)
				}
				require.ElementsMatch(t, []string{"example.test/app", "rsc.io/quote"}, mains)
				binary := filepath.Join(t.TempDir(), "demo")
				_, err = runGo(t.Context(), dir, options, "build", "-o", binary, ".")
				require.NoError(t, err)
				output, err := exec.CommandContext(t.Context(), binary).CombinedOutput()
				require.NoError(t, err)
				require.Equal(t, "workspace sibling\n", string(output))
				result, err := GolangWithOptions(t.Context(), dir, options)
				require.NoError(t, err)
				require.Equal(t, appRef, result.Bom.GetMetadata().GetComponent().GetBomRef())
				refs, edges := goBOMGraph(t, result.Bom)
				require.Contains(t, refs, localQuote)
				require.NotContains(t, refs, pinnedQuote)
				require.Contains(t, edges[appRef], localQuote)
				require.NotContains(t, edges[localQuote], sampler, "released-version edges must not be attributed to the local workspace module")
				attached, err := AttachArtifact(result, Artifact{Name: "demo", SHA256: strings.Repeat("a", 64)})
				require.NoError(t, err)
				_, attachedEdges := goBOMGraph(t, attached.Bom)
				require.Equal(t, []string{appRef}, attachedEdges[attached.Bom.GetMetadata().GetComponent().GetBomRef()])
			})
		}
	}
	// The default ignores even an active workspace, retaining the released graph.
	pinned, err := Golang(t.Context(), source)
	require.NoError(t, err)
	require.Equal(t, appRef, pinned.Bom.GetMetadata().GetComponent().GetBomRef())
	refs, edges := goBOMGraph(t, pinned.Bom)
	require.Contains(t, refs, pinnedQuote)
	require.NotContains(t, refs, localQuote)
	require.Contains(t, edges[appRef], pinnedQuote)
	require.Contains(t, edges[pinnedQuote], sampler)
	_, err = GolangWithOptions(t.Context(), root, GolangOptions{UseWorkspace: true})
	require.ErrorContains(t, err, "no Go source module")
}

func goBOMGraph(t *testing.T, bom *agentv0.Bom) (map[string]bool, map[string][]string) {
	t.Helper()
	refs := map[string]bool{bom.GetMetadata().GetComponent().GetBomRef(): true}
	for _, component := range bom.GetComponents() {
		ref := component.GetBomRef()
		require.NotEmpty(t, ref)
		require.False(t, refs[ref], "duplicate component %s", ref)
		refs[ref] = true
	}
	edges := map[string][]string{}
	for _, dependency := range bom.GetDependencies() {
		require.True(t, refs[dependency.GetRef()], "unknown graph source %s", dependency.GetRef())
		for _, ref := range dependency.GetDependsOn() {
			require.True(t, refs[ref], "unknown graph target %s", ref)
		}
		edges[dependency.GetRef()] = dependency.GetDependsOn()
	}
	return refs, edges
}

func TestParseGoModulesRequiresUniqueSource(t *testing.T) {
	goMod := filepath.Join(t.TempDir(), "go.mod")
	require.NoError(t, os.WriteFile(goMod, []byte("module example.test/app\n"), 0600))
	main := fmt.Sprintf(`{"Path":"example.test/app","Main":true,"GoMod":%q}`, goMod)
	for _, test := range []struct{ name, modules, want string }{
		{"missing", `{ "Path":"example.test/lib", "Version":"v1.0.0" }`, "no main module matches"},
		{"duplicate", main + "\n" + main, "multiple main modules match"},
		{"unreadable", `{"Path":"example.test/app","Main":true}`, "stat main module"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, err := parseGoModules([]byte(test.modules), goMod)
			require.ErrorContains(t, err, test.want)
		})
	}
}
