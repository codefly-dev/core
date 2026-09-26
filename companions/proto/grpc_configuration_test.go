package proto

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/codefly-dev/core/languages"
)

func TestGoClientGenerationPreservesProtovalidatePackage(t *testing.T) {
	dir := t.TempDir()
	if err := CreateBufConfiguration(context.Background(), dir, "users-api", languages.GO, FacadeOptions{}); err != nil {
		t.Fatalf("CreateBufConfiguration: %v", err)
	}
	configuration, err := os.ReadFile(filepath.Join(dir, "buf.gen.yaml"))
	if err != nil {
		t.Fatalf("read generated buf config: %v", err)
	}
	if !strings.Contains(string(configuration), "buf.build/bufbuild/protovalidate") {
		t.Fatal("Go client generation rewrites Protovalidate into the client package")
	}
	if strings.Contains(string(configuration), "plugin: buf.build/protocolbuffers/go") ||
		strings.Contains(string(configuration), "plugin: buf.build/grpc/go") {
		t.Fatal("Go client generation depends on mutable remote plugins")
	}
	if !strings.Contains(string(configuration), "- name: go\n") ||
		!strings.Contains(string(configuration), "- name: go-grpc\n") {
		t.Fatal("Go client generation does not use companion-pinned local plugins")
	}
}

func TestFacadePluginsUseAllStrategy(t *testing.T) {
	// A facade plugin groups services by package and validates --services
	// across the whole descriptor, so buf must invoke it once over every file
	// (strategy: all). buf's default per-directory strategy invokes it once per
	// directory, so a --services subset is reported as "not found" by every
	// invocation except the one that owns those services, and the run fails.
	facade := FacadeOptions{Facade: true, Services: []string{"AuditService"}, Module: "accounts"}
	for _, lang := range []languages.Language{languages.GO, languages.PYTHON, languages.TYPESCRIPT} {
		dir := t.TempDir()
		if err := CreateBufConfiguration(context.Background(), dir, "accounts", lang, facade); err != nil {
			t.Fatalf("CreateBufConfiguration(%s): %v", lang, err)
		}
		configuration, err := os.ReadFile(filepath.Join(dir, "buf.gen.yaml"))
		if err != nil {
			t.Fatalf("read generated buf config (%s): %v", lang, err)
		}
		if !strings.Contains(string(configuration), "strategy: all") {
			t.Fatalf("%s facade config must invoke the facade plugin with `strategy: all`:\n%s", lang, configuration)
		}

		// strategy: all only belongs on the facade plugin. The base plugins are
		// per-file and correct under buf's default per-directory strategy, so a
		// non-facade config must not carry it (over-applying it silently changes
		// how every plugin is invoked).
		plainDir := t.TempDir()
		if err := CreateBufConfiguration(context.Background(), plainDir, "accounts", lang, FacadeOptions{}); err != nil {
			t.Fatalf("CreateBufConfiguration(%s, non-facade): %v", lang, err)
		}
		plain, err := os.ReadFile(filepath.Join(plainDir, "buf.gen.yaml"))
		if err != nil {
			t.Fatalf("read non-facade buf config (%s): %v", lang, err)
		}
		if strings.Contains(string(plain), "strategy: all") {
			t.Fatalf("%s non-facade config must not carry `strategy: all`:\n%s", lang, plain)
		}
	}
}

// TestGoConfigurationPinsForeignGoPackages covers the half of the descriptor-set
// fix that marking alone does not achieve. managed mode rewrites go_package for
// every file in the image, imports included, and its `except` list matches by buf
// module identity — which a plain FileDescriptorSet does not carry, so the
// `except` entries above are inert on that path. Without a per-file override the
// module's bindings import <prefix>/google/api, a package nothing generated.
func TestGoConfigurationPinsForeignGoPackages(t *testing.T) {
	dir := t.TempDir()
	overrides := map[string]string{
		"google/api/annotations.proto": "google.golang.org/genproto/googleapis/api/annotations;annotations",
		"buf/validate/validate.proto":  "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate",
	}
	if err := CreateBufConfiguration(context.Background(), dir, "accounts", languages.GO, FacadeOptions{},
		WithGoPackageOverrides(overrides)); err != nil {
		t.Fatalf("CreateBufConfiguration: %v", err)
	}
	configuration, err := os.ReadFile(filepath.Join(dir, "buf.gen.yaml"))
	if err != nil {
		t.Fatalf("read generated buf config: %v", err)
	}
	for path, pkg := range overrides {
		if !strings.Contains(string(configuration), "\""+path+"\": \""+pkg+"\"") {
			t.Fatalf("go_package override for %s missing:\n%s", path, configuration)
		}
	}
	if !strings.Contains(string(configuration), "  override:\n    GO_PACKAGE:\n") {
		t.Fatalf("overrides must sit under managed.override.GO_PACKAGE:\n%s", configuration)
	}
}

// The Sources path resolves shared protos from buf.yaml dependencies, where
// managed mode's module-identity `except` already applies, so it passes no
// overrides. An empty map must leave the configuration exactly as it was rather
// than emitting a dangling `override:` key that buf rejects.
func TestGoConfigurationOmitsEmptyGoPackageOverrides(t *testing.T) {
	dir := t.TempDir()
	if err := CreateBufConfiguration(context.Background(), dir, "accounts", languages.GO, FacadeOptions{},
		WithGoPackageOverrides(nil)); err != nil {
		t.Fatalf("CreateBufConfiguration: %v", err)
	}
	configuration, err := os.ReadFile(filepath.Join(dir, "buf.gen.yaml"))
	if err != nil {
		t.Fatalf("read generated buf config: %v", err)
	}
	if strings.Contains(string(configuration), "override:") {
		t.Fatalf("no overrides were requested, none must be rendered:\n%s", configuration)
	}
}

// TestTypeScriptIncludeImportsIsScopedToTheBindingsPlugin pins the reason the
// TypeScript template is version v2.
//
// The bindings need the imported files generated: protoc-gen-es names them by a
// path relative to the file it emits, so without them the library imports files
// nothing wrote. The facade must not see them — a CodeGeneratorRequest carries
// no is_import, so a dependency that declares a service is indistinguishable
// from one of ours and gets a facade of its own, while its package joins the
// module's in the count that decides whether module= is honoured. As a
// command-line flag there is no way to say one and not the other.
func TestTypeScriptIncludeImportsIsScopedToTheBindingsPlugin(t *testing.T) {
	dir := t.TempDir()
	if err := CreateBufConfiguration(context.Background(), dir, "rest", languages.TYPESCRIPT,
		FacadeOptions{Facade: true, Module: "rest"}, WithIncludeImports(true)); err != nil {
		t.Fatalf("CreateBufConfiguration: %v", err)
	}
	configuration, err := os.ReadFile(filepath.Join(dir, "buf.gen.yaml"))
	if err != nil {
		t.Fatalf("read generated buf config: %v", err)
	}
	bindings, facade, found := strings.Cut(string(configuration), "protoc-gen-codefly-facade-ts")
	if !found {
		t.Fatal("the TypeScript template no longer runs the facade plugin")
	}
	if !strings.Contains(bindings, "include_imports: true") {
		t.Error("the bindings plugin is not asked for the imports, so every google/api and buf/validate reference dangles")
	}
	if strings.Contains(facade, "include_imports") {
		t.Error("the facade plugin is given the imports, so a dependency's service becomes one of ours")
	}
}

// TestTypeScriptDescriptorSetKeepsImportsOut is the other half: an image built
// as a codefly contract carries its foreign files already marked, and asking
// for the imports re-targets them — a local google/protobuf/timestamp_pb.ts
// beside the consumer's @bufbuild/protobuf/wkt one.
func TestTypeScriptDescriptorSetKeepsImportsOut(t *testing.T) {
	dir := t.TempDir()
	if err := CreateBufConfiguration(context.Background(), dir, "rest", languages.TYPESCRIPT,
		FacadeOptions{}, WithIncludeImports(false)); err != nil {
		t.Fatalf("CreateBufConfiguration: %v", err)
	}
	configuration, err := os.ReadFile(filepath.Join(dir, "buf.gen.yaml"))
	if err != nil {
		t.Fatalf("read generated buf config: %v", err)
	}
	if strings.Contains(string(configuration), "include_imports: true") {
		t.Error("the descriptor-set path asks for the imports it was handed already marked")
	}
}

// pluginOptions returns the `opt:` value of one plugin's block in a rendered
// buf.gen.yaml. Assertions name the plugin they are about rather than counting
// the file's `opt:` lines, so adding a plugin to a template does not fail a test
// that has nothing to say about it.
func pluginOptions(t *testing.T, configuration, plugin string) string {
	t.Helper()
	_, block, found := strings.Cut(configuration, "- local: "+plugin+"\n")
	if !found {
		t.Fatalf("the template does not run %s:\n%s", plugin, configuration)
	}
	// A plugin's block ends where the next entry in the sequence begins.
	if next, _, more := strings.Cut(block, "\n  - "); more {
		block = next
	}
	for _, line := range strings.Split(block, "\n") {
		if opt, ok := strings.CutPrefix(strings.TrimSpace(line), "opt:"); ok {
			return strings.TrimSpace(opt)
		}
	}
	t.Fatalf("%s carries no opt: line:\n%s", plugin, block)
	return ""
}

// TestTypeScriptClientImportsCarryTheJsExtension pins the extension on the
// specifier, for every plugin that emits one and on both paths through the
// template.
//
// protoplugin defaults import_extension to none, so without it the bindings and
// the facade import each other with no extension. That resolves under tsc and
// under a bundler and nowhere else: Node's ESM loader throws
// ERR_MODULE_NOT_FOUND on the first relative import, and so does vitest run
// outside a bundle. TypeScript maps `.js` back to the `.ts` source in every
// moduleResolution mode, so the suffix costs the bundler consumers nothing.
//
// Both paths are asserted because the bindings are the larger half. GenerateGRPC
// renders this same template with Facade false, and bindings in a multi-file
// proto package import each other relatively whether a facade exists or not — so
// guarding the option behind `{{ if .Facade }}` would break every non-facade
// consumer while a facade-only test stayed green.
//
// This asserts the rendered configuration. The generated specifiers themselves
// are asserted by the tsc pass in facade_test.go, which typechecks the tree as
// an ESM package under nodenext — the only mode that diagnoses a missing
// extension.
func TestTypeScriptClientImportsCarryTheJsExtension(t *testing.T) {
	extension := regexp.MustCompile(`(^|,)import_extension=js(,|$)`)
	for _, test := range []struct {
		name    string
		facade  FacadeOptions
		plugins []string
	}{
		{"bindings only", FacadeOptions{}, []string{"protoc-gen-es"}},
		{
			"bindings and facade",
			FacadeOptions{Facade: true, Services: []string{"AuditService"}, Module: "accounts"},
			[]string{"protoc-gen-es", "protoc-gen-codefly-facade-ts"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := CreateBufConfiguration(context.Background(), dir, "accounts", languages.TYPESCRIPT, test.facade); err != nil {
				t.Fatalf("CreateBufConfiguration: %v", err)
			}
			configuration, err := os.ReadFile(filepath.Join(dir, "buf.gen.yaml"))
			if err != nil {
				t.Fatalf("read generated buf config: %v", err)
			}
			for _, plugin := range test.plugins {
				if opt := pluginOptions(t, string(configuration), plugin); !extension.MatchString(opt) {
					t.Errorf("%s options %q put no extension on relative imports, so every Node-ESM consumer fails on its first import", plugin, opt)
				}
			}
			if !test.facade.Facade {
				return
			}
			// The extension is appended to the facade's own options, so a
			// mistake there drops them instead.
			opt := pluginOptions(t, string(configuration), "protoc-gen-codefly-facade-ts")
			if !strings.Contains(opt, "services=AuditService") || !strings.Contains(opt, "module=accounts") {
				t.Errorf("the facade lost its own options: %q", opt)
			}
		})
	}
}
