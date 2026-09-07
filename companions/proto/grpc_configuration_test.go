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
	}
}
