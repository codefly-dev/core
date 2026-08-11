package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/resources"
)

type sourceSettings struct {
	SourceDir string `yaml:"source-dir"`
}

func (s *sourceSettings) sourceDir() string {
	if s.SourceDir == "" {
		return "default-source"
	}
	return s.SourceDir
}

func TestResolveSourceLocationLoadsRealDeclarationBeforeRuntime(t *testing.T) {
	serviceRoot := t.TempDir()
	physicalSource := t.TempDir()
	if err := os.WriteFile(filepath.Join(physicalSource, "README.md"), []byte("attached source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := &resources.Service{
		Name: "source", Version: "0.0.0",
		Agent: &resources.Agent{Kind: resources.ServiceAgent, Publisher: "codefly.dev", Name: "generic", Version: "v0.0.0"},
		Spec:  map[string]any{"source-dir": "attached"},
	}
	service.WithDir(serviceRoot)
	if err := service.Save(t.Context()); err != nil {
		t.Fatalf("save service declaration: %v", err)
	}
	if err := os.Symlink(physicalSource, filepath.Join(serviceRoot, "attached")); err != nil {
		t.Fatalf("attach source: %v", err)
	}
	t.Setenv(agents.WorkDirEnvironment, serviceRoot)

	settings := &sourceSettings{}
	base := NewServiceBase(t.Context(), service.Agent)
	base.Location = t.TempDir()
	got, err := base.ResolveSourceLocation(t.Context(), settings, settings.sourceDir)
	if err != nil {
		t.Fatalf("resolve source location: %v", err)
	}
	want, err := filepath.EvalSymlinks(physicalSource)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("source location = %q, want physical attachment %q", got, want)
	}
	if settings.SourceDir != "attached" {
		t.Fatalf("settings source-dir = %q, want declaration value", settings.SourceDir)
	}
}

func TestResolveSourceLocationRejectsMalformedDeclaration(t *testing.T) {
	serviceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(serviceRoot, resources.ServiceConfigurationName), []byte("kind: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(agents.WorkDirEnvironment, serviceRoot)

	settings := &sourceSettings{}
	base := NewServiceBase(t.Context(), &resources.Agent{Kind: resources.ServiceAgent, Name: "generic"})
	if _, err := base.ResolveSourceLocation(t.Context(), settings, settings.sourceDir); err == nil {
		t.Fatal("malformed source declaration must fail instead of binding the wrong directory")
	}
}

func TestResolveSourceLocationRejectsEscapingSourceDir(t *testing.T) {
	serviceRoot := t.TempDir()
	service := &resources.Service{
		Name: "source", Version: "0.0.0",
		Agent: &resources.Agent{Kind: resources.ServiceAgent, Publisher: "codefly.dev", Name: "generic", Version: "v0.0.0"},
		Spec:  map[string]any{"source-dir": "../outside"},
	}
	service.WithDir(serviceRoot)
	if err := service.Save(t.Context()); err != nil {
		t.Fatalf("save service declaration: %v", err)
	}
	t.Setenv(agents.WorkDirEnvironment, serviceRoot)

	settings := &sourceSettings{}
	base := NewServiceBase(t.Context(), service.Agent)
	if _, err := base.ResolveSourceLocation(t.Context(), settings, settings.sourceDir); err == nil {
		t.Fatal("escaping source-dir must fail before any project inspection")
	}
}
