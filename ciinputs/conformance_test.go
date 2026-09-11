//go:build ciinputs_conformance

package ciinputs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	agentservices "github.com/codefly-dev/core/agents/services"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

// Native discovery remains in the fixture agent; Core receives only identities.
func TestGoNativeConsumption(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module fixture\n\ngo 1.25\n")
	writeFixture(t, root, "value.txt", "production")
	writeFixture(t, root, "main_test.go", "package main\nimport (\"testing\";\"os\")\nfunc TestValue(t *testing.T){b,e:=os.ReadFile(\"testdata/value.txt\");if e!=nil||string(b)!=value{t.Fatal(string(b),e)}}\n")
	writeFixture(t, root, "testdata/value.txt", "production")
	writeFixture(t, root, "lib/value.go", "package lib\nconst Value=\"production\"\n")
	writeFixture(t, root, "main.go", "package main\nimport(\"fmt\";\"fixture/lib\";_ \"embed\")\n//go:embed value.txt\nvar value string\nfunc main(){fmt.Print(lib.Value)}\n")
	discovery := &nativeInputAgent{root: root, language: "go"}
	discover := func() []Task { return discoverNative(t, discovery) }

	before := discover()
	native(t, root, "go", "build", "-o", filepath.Join(root, "app"), ".")
	native(t, root, "go", "test", "-count=1", ".")
	writeFixture(t, root, "main_test.go", "package main\nimport \"testing\"\nfunc TestValue(t *testing.T){if value!=\"production\"{t.Fatal(value)}}\n")
	after := discover()
	if got := Changed(before, after); len(got) != 1 || got[0] != unit {
		t.Fatalf("test-only edit selected %v", got)
	}
	native(t, root, "go", "test", "-count=1", ".")
	writeFixture(t, root, "value.txt", "updated")
	if got := Changed(after, discover()); len(got) != 2 {
		t.Fatalf("embedded production input lost: %v", got)
	}

	before = discover()
	writeFixture(t, root, "lib/value.go", "package lib\nconst Value=\"changed library\"\n")
	if got := Changed(before, discover()); len(got) != 2 {
		t.Fatalf("imported package was omitted: %v", got)
	}
	if out := native(t, root, "go", "run", "."); string(out) != "changed library" {
		t.Fatalf("unexpected output: %s", out)
	}
	before = discover()
	writeFixture(t, root, "other/value.go", "package other\nconst Value=\"new dependency\"\n")
	writeFixture(t, root, "lib/value.go", "package lib\nimport \"fixture/other\"\nconst Value=other.Value\n")
	after = discover()
	if len(Changed(before, after)) != 2 {
		t.Fatal("new dependency edge was omitted")
	}
	writeFixture(t, root, "other/value.go", "package other\nconst Value=\"edited dependency\"\n")
	if len(Changed(after, discover())) != 2 {
		t.Fatal("new dependency content was omitted")
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
	discovery := &nativeInputAgent{root: root, language: "next"}
	discover := func() []Task {
		native(t, root, "node", "node_modules/next/dist/bin/next", "build")
		return discoverNative(t, discovery)
	}

	before := discover()
	native(t, root, "node", "tests/unit.cjs")
	writeFixture(t, root, "tests/unit.cjs", `require('node:assert').equal(2+2,4)`)
	native(t, root, "node", "tests/unit.cjs")
	if got := Changed(before, discover()); len(got) != 1 || got[0] != unit {
		t.Fatalf("unrelated test selected %v", got)
	}
	writeFixture(t, root, "value.test.js", `export default 'changed production'`)
	if got := Changed(before, discover()); len(got) != 2 {
		t.Fatalf("production import excluded by name: %v", got)
	}
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
	before = discover()
	writeFixture(t, root, "added.js", `export default 'new dependency'`)
	writeFixture(t, root, "value.test.js", `export {default} from './added.js'`)
	after := discover()
	if len(Changed(before, after)) != 1 {
		t.Fatal("new production dependency edge was omitted")
	}
	writeFixture(t, root, "added.js", `export default 'edited dependency'`)
	if len(Changed(after, discover())) != 1 {
		t.Fatal("new production dependency content was omitted")
	}

}

type nativeInputAgent struct {
	agent.UnimplementedAgentServer
	root, language string
}

func (s *nativeInputAgent) GetAgentInformation(context.Context, *agent.AgentInformationRequest) (*agent.AgentInformation, error) {
	return (agentservices.Advertisement{EffectiveInputsVersions: []uint32{Version}, Validation: &agent.ValidationCapabilities{
		ArtifactBuild: &agent.ValidationOperationCapability{Supported: true},
		Test:          &agent.TestValidationCapability{Supported: true, Suites: []*agent.TestSuiteCapability{{Name: "unit", DependencyMode: agent.TestDependencyMode_TEST_DEPENDENCY_MODE_NONE}}},
	}}).Build(), nil
}

func (s *nativeInputAgent) GetEffectiveInputs(ctx context.Context, req *agent.GetEffectiveInputsRequest) (*agent.GetEffectiveInputsResponse, error) {
	if req.SchemaVersion != Version || req.Revision != "" {
		return nil, fmt.Errorf("fixture requires current v1 snapshot")
	}
	production, tests := map[string]bool{}, map[string]bool{}
	tool := "go"
	if s.language == "go" {
		cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-test", "-json", ".")
		cmd.Dir = s.root
		data, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		for {
			var pkg struct {
				Dir                                                                                                                         string
				Standard                                                                                                                    bool
				ForTest                                                                                                                     string
				GoFiles, CgoFiles, CFiles, CXXFiles, HFiles, SFiles, EmbedFiles, TestGoFiles, XTestGoFiles, TestEmbedFiles, XTestEmbedFiles []string
			}
			if err := dec.Decode(&pkg); err == io.EOF {
				break
			} else if err != nil {
				return nil, err
			}
			if pkg.Standard || pkg.Dir == "" {
				continue
			}
			rel, err := filepath.Rel(s.root, pkg.Dir)
			if err != nil {
				return nil, err
			}
			if !filepath.IsLocal(rel) {
				return nil, fmt.Errorf("fixture has external dependency")
			}
			for _, group := range [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.HFiles, pkg.SFiles, pkg.EmbedFiles} {
				for _, file := range group {
					if filepath.IsAbs(file) {
						continue
					} // Synthetic test mains are covered by the resolved Go toolchain.
					name := filepath.ToSlash(filepath.Join(rel, file))
					tests[name] = true
					if pkg.ForTest == "" && !strings.HasSuffix(file, "_test.go") {
						production[name] = true
					}
				}
			}
			for _, group := range [][]string{pkg.TestGoFiles, pkg.XTestGoFiles, pkg.TestEmbedFiles, pkg.XTestEmbedFiles} {
				for _, file := range group {
					tests[filepath.ToSlash(filepath.Join(rel, file))] = true
				}
			}
		}
		production["go.mod"] = true
		tests["go.mod"] = true
		tests["testdata/value.txt"] = true
	} else {
		tool = "node"
		for _, file := range []string{"consumed-server.json", "consumed-client.json"} {
			body, err := os.ReadFile(filepath.Join(s.root, file))
			if err != nil {
				return nil, err
			}
			var files []string
			if err := json.Unmarshal(body, &files); err != nil {
				return nil, err
			}
			for _, file := range files {
				production[filepath.ToSlash(file)] = true
			}
		}
		for _, file := range []string{"package.json", "package-lock.json", "next.config.js"} {
			production[file] = true
			tests[file] = true
		}
		tests["tests/unit.cjs"] = true
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	pluginBytes, err := os.ReadFile(exe)
	if err != nil {
		return nil, err
	}
	toolPath, err := exec.LookPath(tool)
	if err != nil {
		return nil, err
	}
	toolBytes, err := os.ReadFile(toolPath)
	if err != nil {
		return nil, err
	}
	resolved := []*agent.EffectiveInput{
		input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_PLUGIN, "agent", string(pluginBytes)),
		input(agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_TOOLCHAIN, tool, string(toolBytes)),
	}
	tasks := []*agent.TaskInputs{}
	for _, target := range []struct {
		key   Key
		files map[string]bool
	}{{build, production}, {unit, tests}} {
		inputs := append([]*agent.EffectiveInput{}, resolved...)
		names := []string{}
		for name := range target.files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			body, err := os.ReadFile(filepath.Join(s.root, name))
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(filepath.Join(s.root, name))
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256(body)
			inputs = append(inputs, &agent.EffectiveInput{Kind: agent.EffectiveInputKind_EFFECTIVE_INPUT_KIND_SOURCE, Owner: "frontend", Name: name, Path: true, Mode: 0100000 | uint32(info.Mode().Perm()), Identity: &agent.EffectiveIdentity{Kind: agent.EffectiveIdentityKind_EFFECTIVE_IDENTITY_KIND_SHA256, Digest: hex.EncodeToString(sum[:])}})
		}
		tasks = append(tasks, &agent.TaskInputs{Task: &agent.TaskKey{Phase: target.key.Phase, Suite: target.key.Suite}, Complete: true, Inputs: inputs})
	}
	return &agent.GetEffectiveInputsResponse{SchemaVersion: Version, Snapshot: req.Snapshot, Tasks: tasks}, nil
}

func discoverNative(t *testing.T, server *nativeInputAgent) []Task {
	t.Helper()
	client := wireClient(t, server)
	info, err := client.GetAgentInformation(context.Background(), &agent.AgentInformationRequest{})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := Required(info.Validation)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := Discover(context.Background(), client, info, &agent.GetEffectiveInputsRequest{SchemaVersion: Version, Snapshot: "snapshot"}, keys)
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}
