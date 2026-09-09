package proto

import (
	"context"
	"os"
	"path/filepath"
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
