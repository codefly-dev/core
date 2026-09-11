//go:build ciinputs_conformance

package ciinputs

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	agent "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

func writeFixture(t *testing.T, root, name, body string) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}
func native(t *testing.T, root, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NEXT_TELEMETRY_DISABLED=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return out
}
func fileInput(t *testing.T, root, name string) *agent.EffectiveInput {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	in := input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_SOURCE, name, string(body))
	in.Path = true
	in.Mode = 0100644
	return in
}

// Native discovery remains in the fixture agent; Core receives only identities.
func TestGoNativeConsumption(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module fixture\n\ngo 1.25\n")
	writeFixture(t, root, "main.go", "package main\nimport _ \"embed\"\n//go:embed value.txt\nvar value string\nfunc main() { print(value) }\n")
	writeFixture(t, root, "value.txt", "production")
	writeFixture(t, root, "main_test.go", "package main\nimport (\"testing\";\"os\")\nfunc TestValue(t *testing.T){b,e:=os.ReadFile(\"testdata/value.txt\");if e!=nil||string(b)!=value{t.Fatal(string(b),e)}}\n")
	writeFixture(t, root, "testdata/value.txt", "production")
	discover := func() *agent.GetEffectiveInputsResponse {
		data := native(t, root, "go", "list", "-json", ".")
		var pkg struct{ GoFiles, EmbedFiles, TestGoFiles []string }
		if err := json.Unmarshal(data, &pkg); err != nil {
			t.Fatal(err)
		}
		var prod, tests []*agent.EffectiveInput
		for _, file := range append(append(pkg.GoFiles, pkg.EmbedFiles...), "go.mod") {
			prod = append(prod, fileInput(t, root, file))
		}
		tests = append(tests, prod...)
		for _, file := range append(pkg.TestGoFiles, "testdata/value.txt") {
			tests = append(tests, fileInput(t, root, file))
		}
		return response(declaration(build, prod...), declaration(unit, tests...))
	}
	before := nativeEvaluate(t, discover(), build, unit)
	native(t, root, "go", "build", "-o", filepath.Join(root, "app"), ".")
	native(t, root, "go", "test", "-count=1", ".")
	writeFixture(t, root, "main_test.go", "package main\nimport \"testing\"\nfunc TestValue(t *testing.T){if value!=\"production\"{t.Fatal(value)}}\n")
	after := nativeEvaluate(t, discover(), build, unit)
	if got := Changed(before, after); len(got) != 1 || got[0] != unit {
		t.Fatalf("test-only edit selected %v", got)
	}
	native(t, root, "go", "test", "-count=1", ".")
	writeFixture(t, root, "value.txt", "updated")
	if got := Changed(after, nativeEvaluate(t, discover(), build, unit)); len(got) != 2 {
		t.Fatalf("embedded production input lost: %v", got)
	}
}

func TestNextNativeConsumption(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"private":true,"dependencies":{"next":"15.5.25","react":"19.1.0","react-dom":"19.1.0"}}`)
	writeFixture(t, root, "pages/index.js", `import value from '../value.test.js'; export default function Page(){return <main>{value}</main>}`)
	writeFixture(t, root, "value.test.js", `export default 'production'`)
	writeFixture(t, root, "tests/unit.cjs", `require('node:assert').equal(1+1,2)`)
	writeFixture(t, root, "next.config.js", `const fs = require('fs'); const path = require('path');
module.exports = {experimental:{cpus:1}, webpack(config, {isServer}) {
config.plugins.push({apply(compiler){compiler.hooks.afterEmit.tap('InputConformance', compilation => {
const files = [...compilation.fileDependencies].filter(p => p.startsWith(__dirname+path.sep) && !p.includes(path.sep+'node_modules'+path.sep) && !p.includes(path.sep+'.next'+path.sep) && fs.statSync(p).isFile()).map(p=>path.relative(__dirname,p));
fs.writeFileSync('consumed-'+(isServer?'server':'client')+'.json',JSON.stringify(files));
});}}); return config; }};`)
	native(t, root, "npm", "install", "--no-audit", "--no-fund")
	native(t, root, "node", "node_modules/next/dist/bin/next", "build")
	names := map[string]bool{"package.json": true, "package-lock.json": true, "next.config.js": true}
	for _, file := range []string{"consumed-server.json", "consumed-client.json"} {
		body, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		var files []string
		if err := json.Unmarshal(body, &files); err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			names[filepath.ToSlash(name)] = true
		}
	}
	if !names["value.test.js"] || names["tests/unit.cjs"] {
		t.Fatalf("unexpected native consumption: %v", names)
	}
	discover := func() *agent.GetEffectiveInputsResponse {
		var files []*agent.EffectiveInput
		for name := range names {
			files = append(files, fileInput(t, root, name))
		}
		return response(declaration(build, files...), declaration(unit, fileInput(t, root, "tests/unit.cjs")))
	}
	before := nativeEvaluate(t, discover(), build, unit)
	native(t, root, "node", "tests/unit.cjs")
	writeFixture(t, root, "tests/unit.cjs", `require('node:assert').equal(2+2,4)`)
	native(t, root, "node", "tests/unit.cjs")
	if got := Changed(before, nativeEvaluate(t, discover(), build, unit)); len(got) != 1 || got[0] != unit {
		t.Fatalf("unrelated test selected %v", got)
	}
	writeFixture(t, root, "value.test.js", `export default 'changed production'`)
	if got := Changed(before, nativeEvaluate(t, discover(), build, unit)); len(got) != 2 {
		t.Fatalf("production import excluded by name: %v", got)
	}
	native(t, root, "node", "node_modules/next/dist/bin/next", "build")
	html, err := os.Open(filepath.Join(root, ".next/server/pages/index.html"))
	if err != nil {
		t.Fatal(err)
	}
	defer html.Close()
	body, err := io.ReadAll(html)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "changed production") {
		t.Fatal("build did not consume declared import")
	}
}

func nativeEvaluate(t *testing.T, r *agent.GetEffectiveInputsResponse, keys ...Key) []Task {
	t.Helper()
	got, err := Discover(context.Background(), wireClient(t, &wireAgent{reply: r}), &agent.AgentInformation{EffectiveInputsVersions: []uint32{Version}}, &agent.GetEffectiveInputsRequest{SchemaVersion: Version, Snapshot: r.Snapshot}, keys)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
