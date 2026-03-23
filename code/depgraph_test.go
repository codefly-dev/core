package code

import (
	"strings"
	"testing"
)

func TestBuildDepGraph_Basic(t *testing.T) {
	pkgs := []PackageInput{
		{Name: "github.com/example/app/pkg/api", Path: "pkg/api", Imports: []string{"github.com/example/app/pkg/store", "net/http"}, Files: []string{"handler.go"}},
		{Name: "github.com/example/app/pkg/store", Path: "pkg/store", Imports: []string{"database/sql"}, Files: []string{"store.go"}},
		{Name: "github.com/example/app/cmd", Path: "cmd", Imports: []string{"github.com/example/app/pkg/api"}, Files: []string{"main.go"}},
	}

	g := BuildDepGraph("github.com/example/app", pkgs)
	if g.Module != "github.com/example/app" {
		t.Errorf("unexpected module: %s", g.Module)
	}
	if len(g.Packages) != 3 {
		t.Fatalf("expected 3 packages, got %d", len(g.Packages))
	}
}

func TestDepGraph_InternalEdges(t *testing.T) {
	pkgs := []PackageInput{
		{Name: "mod/api", Path: "api", Imports: []string{"mod/store", "net/http"}},
		{Name: "mod/store", Path: "store", Imports: []string{"database/sql"}},
		{Name: "mod/cmd", Path: "cmd", Imports: []string{"mod/api", "mod/store"}},
	}

	g := BuildDepGraph("mod", pkgs)
	edges := g.InternalEdges()

	if len(edges) != 3 {
		t.Fatalf("expected 3 internal edges, got %d", len(edges))
	}
}

func TestDepGraph_Roots(t *testing.T) {
	pkgs := []PackageInput{
		{Name: "mod/api", Path: "api", Imports: []string{"mod/store"}},
		{Name: "mod/store", Path: "store", Imports: []string{}},
		{Name: "mod/cmd", Path: "cmd", Imports: []string{"mod/api"}},
	}

	g := BuildDepGraph("mod", pkgs)
	roots := g.Roots()

	if len(roots) != 1 || roots[0] != "cmd" {
		t.Errorf("expected roots=[cmd], got %v", roots)
	}
}

func TestDepGraph_Leaves(t *testing.T) {
	pkgs := []PackageInput{
		{Name: "mod/api", Path: "api", Imports: []string{"mod/store"}},
		{Name: "mod/store", Path: "store", Imports: []string{}},
		{Name: "mod/cmd", Path: "cmd", Imports: []string{"mod/api"}},
	}

	g := BuildDepGraph("mod", pkgs)
	leaves := g.Leaves()

	if len(leaves) != 1 || leaves[0] != "store" {
		t.Errorf("expected leaves=[store], got %v", leaves)
	}
}

func TestDepGraph_Format(t *testing.T) {
	pkgs := []PackageInput{
		{Name: "mod/api", Path: "api", Imports: []string{"mod/store"}, Files: []string{"handler.go"}, Doc: "HTTP handlers"},
	}

	g := BuildDepGraph("mod", pkgs)
	output := g.Format()

	if !strings.Contains(output, "# Dependency Graph: mod") {
		t.Error("expected header")
	}
	if !strings.Contains(output, "## api") {
		t.Error("expected package section")
	}
	if !strings.Contains(output, "handler.go") {
		t.Error("expected files")
	}
	if !strings.Contains(output, "HTTP handlers") {
		t.Error("expected doc")
	}
}

func TestDepGraph_ConnectedComponents(t *testing.T) {
	pkgs := []PackageInput{
		{Name: "a", Path: "a", Imports: []string{"mod/b"}},
		{Name: "b", Path: "b", Imports: []string{}},
		{Name: "c", Path: "c", Imports: []string{"mod/d"}},
		{Name: "d", Path: "d", Imports: []string{}},
	}
	g := BuildDepGraph("mod", pkgs)
	comps := g.ConnectedComponents()
	if len(comps) != 2 {
		t.Fatalf("expected 2 components, got %d: %v", len(comps), comps)
	}
}

func TestDepGraph_ConnectedComponents_Single(t *testing.T) {
	pkgs := []PackageInput{
		{Name: "a", Path: "a", Imports: []string{"mod/b"}},
		{Name: "b", Path: "b", Imports: []string{"mod/a"}},
	}
	g := BuildDepGraph("mod", pkgs)
	comps := g.ConnectedComponents()
	if len(comps) != 1 {
		t.Fatalf("expected 1 component, got %d", len(comps))
	}
	if len(comps[0]) != 2 {
		t.Fatalf("expected 2 packages in component, got %d", len(comps[0]))
	}
}

func TestDepGraph_Empty(t *testing.T) {
	g := BuildDepGraph("mod", nil)
	if len(g.Packages) != 0 {
		t.Error("expected no packages")
	}
	if len(g.Roots()) != 0 {
		t.Error("expected no roots")
	}
	if len(g.Leaves()) != 0 {
		t.Error("expected no leaves")
	}
}
