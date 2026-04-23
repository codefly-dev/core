package upgrade

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

func TestDiffMods(t *testing.T) {
	before := map[string]string{
		"github.com/foo/bar":   "v1.2.3",
		"github.com/baz/qux":   "v0.1.0",
		"github.com/no/change": "v2.0.0",
	}
	after := map[string]string{
		"github.com/foo/bar":   "v1.2.5", // bumped
		"github.com/baz/qux":   "v0.1.0", // unchanged
		"github.com/no/change": "v2.0.0",
		"github.com/new/dep":   "v0.0.1", // added (treated as a change from "")
	}
	got := diffMods(before, after)
	sort.Slice(got, func(i, j int) bool { return got[i].Package < got[j].Package })
	want := []*builderv0.UpgradeChange{
		{Package: "github.com/foo/bar", From: "v1.2.3", To: "v1.2.5"},
		{Package: "github.com/new/dep", From: "", To: "v0.0.1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diffMods mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestInOnly(t *testing.T) {
	if !inOnly("foo", nil) {
		t.Error("empty only should pass everything")
	}
	if !inOnly("foo", []string{"bar", "foo"}) {
		t.Error("foo should be allowed when in list")
	}
	if inOnly("baz", []string{"bar", "foo"}) {
		t.Error("baz should be blocked when not in list")
	}
}

func TestMajorOf(t *testing.T) {
	cases := map[string]string{
		"5.4.5":     "5",
		"1.0.0a1":   "1",
		"":          "",
		"abc":       "",
		"16":        "16",
		"latest":    "",
	}
	for in, want := range cases {
		if got := majorOf(in); got != want {
			t.Errorf("majorOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitOnSpecifier(t *testing.T) {
	cases := map[string]string{
		"django==4.2.0":         "django",
		"requests>=2.28":        "requests",
		"uvicorn[standard]":     "uvicorn",
		"flask~=3.0":            "flask",
		"  spaces==1.0  ":       "spaces",
		"plain-name":            "plain-name",
	}
	for in, want := range cases {
		if got := splitOnSpecifier(in); got != want {
			t.Errorf("splitOnSpecifier(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRewriteRequirements(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "requirements.txt")
	src := `# top comment
django==4.2.0
requests>=2.28
uvicorn[standard]
unchanged==1.0
-r dev-requirements.txt
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	changes := []*builderv0.UpgradeChange{
		{Package: "django", To: "5.0.1"},
		{Package: "requests", To: "2.31.0"},
		{Package: "Uvicorn", To: "0.30.0"}, // case-insensitive match
	}
	if err := rewriteRequirements(dir, changes); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := `# top comment
django==5.0.1
requests==2.31.0
uvicorn==0.30.0
unchanged==1.0
-r dev-requirements.txt
`
	if string(got) != want {
		t.Fatalf("rewrite mismatch:\nGOT:\n%s\nWANT:\n%s", got, want)
	}
}

func TestRewriteRequirements_missingFile(t *testing.T) {
	// No requirements.txt exists; should be a no-op, not an error.
	if err := rewriteRequirements(t.TempDir(), []*builderv0.UpgradeChange{{Package: "x", To: "1"}}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestPickBumpTag(t *testing.T) {
	tags := []string{"16.4.0", "16.3.5", "16.3.4", "15.8.0", "17.0.0", "alpine", "16-bookworm"}
	// patch+minor only: stay within major 16.
	if got := pickBumpTag("16.2.1", tags, false); got != "16.4.0" {
		t.Errorf("patch+minor pick: got %q want 16.4.0", got)
	}
	// include_major: pick highest semver overall.
	if got := pickBumpTag("16.2.1", tags, true); got != "17.0.0" {
		t.Errorf("major pick: got %q want 17.0.0", got)
	}
	// non-semver tags excluded.
	if got := pickBumpTag("alpine", tags, true); got != "17.0.0" {
		// majorOf("alpine") = "" so currentMajor restriction is dropped
		t.Errorf("non-semver current: got %q want 17.0.0", got)
	}
}

func TestSplitImage(t *testing.T) {
	cases := []struct{ in, repo, tag string }{
		{"postgres:16", "library/postgres", "16"},
		{"redis", "library/redis", "latest"},
		{"ghcr.io/codefly-dev/proto:0.0.9", "ghcr.io/codefly-dev/proto", "0.0.9"},
	}
	for _, c := range cases {
		repo, tag := splitImage(c.in)
		if repo != c.repo || tag != c.tag {
			t.Errorf("splitImage(%q) = (%q, %q), want (%q, %q)", c.in, repo, tag, c.repo, c.tag)
		}
	}
}
