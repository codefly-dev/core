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

// `go get -u=patch` refuses a module with no required version, so a package
// the module does not require yet, and one naming its own version, are
// fetched without the semver flag; the ones already required are bumped
// under it.
func TestGoGetCommandsAddWhatIsNotRequiredYet(t *testing.T) {
	required := map[string]string{
		"github.com/foo/bar":    "v1.2.3",
		"github.com/jackc/pgx":  "v3.0.0",
		"golang.org/x/net":      "v0.30.0",
		"github.com/foo/barbaz": "v0.1.0",
	}
	for name, tc := range map[string]struct {
		opts     Options
		commands [][]string
		added    []string
	}{
		"everything, patch": {
			opts:     Options{},
			commands: [][]string{{"get", "-u=patch", "./..."}},
		},
		"everything, major": {
			opts:     Options{IncludeMajor: true},
			commands: [][]string{{"get", "-u", "./..."}},
		},
		"one already required": {
			opts:     Options{Only: []string{"github.com/foo/bar"}},
			commands: [][]string{{"get", "-u=patch", "github.com/foo/bar"}},
		},
		"a package inside a required module": {
			opts:     Options{Only: []string{"golang.org/x/net/http2"}},
			commands: [][]string{{"get", "-u=patch", "golang.org/x/net/http2"}},
		},
		// github.com/jackc/pgx/v5 is another module than github.com/jackc/pgx:
		// a major version has its own path, and it is not required yet.
		"a new module": {
			opts:     Options{Only: []string{"github.com/jackc/pgx/v5"}},
			commands: [][]string{{"get", "github.com/jackc/pgx/v5"}},
			added:    []string{"github.com/jackc/pgx/v5"},
		},
		"a new module at a version": {
			opts:     Options{Only: []string{"github.com/new/dep@v1.4.0"}},
			commands: [][]string{{"get", "github.com/new/dep@v1.4.0"}},
			added:    []string{"github.com/new/dep"},
		},
		"a required module at a version is fetched, not added": {
			opts:     Options{Only: []string{"github.com/foo/bar@v1.2.9"}},
			commands: [][]string{{"get", "github.com/foo/bar@v1.2.9"}},
		},
		"a name that only shares a prefix is not required": {
			opts:     Options{Only: []string{"github.com/foo/barista"}},
			commands: [][]string{{"get", "github.com/foo/barista"}},
			added:    []string{"github.com/foo/barista"},
		},
		"mixed, major": {
			opts: Options{IncludeMajor: true, Only: []string{"github.com/foo/bar", "github.com/new/dep", "golang.org/x/net"}},
			commands: [][]string{
				{"get", "-u", "github.com/foo/bar", "golang.org/x/net"},
				{"get", "github.com/new/dep"},
			},
			added: []string{"github.com/new/dep"},
		},
	} {
		commands, added := goGetCommands(tc.opts, required)
		if !reflect.DeepEqual(commands, tc.commands) || !reflect.DeepEqual(added, tc.added) {
			t.Errorf("%s:\n commands %v (want %v)\n added %v (want %v)", name, commands, tc.commands, added, tc.added)
		}
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

func TestParseNpmOutdatedSupportsWorkspaceArrays(t *testing.T) {
	out := []byte(`{
		"react": [
			{"current":"19.2.4","wanted":"19.2.4","latest":"19.2.7","dependent":"app"},
			{"current":"19.2.4","wanted":"19.2.7","latest":"19.2.7","dependent":"ui"}
		],
		"next": {"current":"16.2.9","wanted":"16.2.10","latest":"16.2.10"}
	}`)
	entries, err := parseNpmOutdated(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := entries["react"][0]; got.Current != "19.2.4" || got.Wanted != "19.2.4" || got.Latest != "19.2.7" || got.Dependent != "app" {
		t.Fatalf("react = %+v", got)
	}
	if got := entries["next"][0]; got.Current != "16.2.9" || got.Wanted != "16.2.10" {
		t.Fatalf("next = %+v", got)
	}
}

func TestNpmLatestInstallGroupsAreAtomicAndWorkspaceAware(t *testing.T) {
	entries := map[string][]npmOutdatedEntry{
		"react-dom": {{Current: "19.2.4", Latest: "19.2.7", Dependent: "app"}},
		"react": {
			{Current: "19.2.4", Latest: "19.2.7", Dependent: "app"},
			{Current: "19.2.4", Latest: "19.2.7", Dependent: "ui"},
		},
		"typescript": {{Current: "5.9.3", Latest: "7.0.0", Dependent: "app"}},
	}
	got := npmLatestInstallGroups(entries, []string{"react", "react-dom"}, map[string]string{"ui": "@scope/ui"})
	want := []npmInstallGroup{
		{Workspace: "@scope/ui", Packages: []string{"react@latest"}},
		{Packages: []string{"react-dom@latest", "react@latest"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
}

func TestMergeUpgradeChangesKeepsWorkspaceOnlyChanges(t *testing.T) {
	planned := []*builderv0.UpgradeChange{
		{Package: "react", From: "19.2.7", To: "19.2.8"},
		{Package: "react-dom", From: "19.2.7", To: "19.2.8"},
	}
	observed := []*builderv0.UpgradeChange{
		{Package: "react-dom", From: "19.2.7", To: "19.2.9"},
	}
	want := []*builderv0.UpgradeChange{
		{Package: "react", From: "19.2.7", To: "19.2.8"},
		{Package: "react-dom", From: "19.2.7", To: "19.2.9"},
	}
	if got := mergeUpgradeChanges(observed, planned); !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
}

func TestMajorOf(t *testing.T) {
	cases := map[string]string{
		"5.4.5":   "5",
		"1.0.0a1": "1",
		"":        "",
		"abc":     "",
		"16":      "16",
		"latest":  "",
	}
	for in, want := range cases {
		if got := majorOf(in); got != want {
			t.Errorf("majorOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitOnSpecifier(t *testing.T) {
	cases := map[string]string{
		"django==4.2.0":     "django",
		"requests>=2.28":    "requests",
		"uvicorn[standard]": "uvicorn",
		"flask~=3.0":        "flask",
		"  spaces==1.0  ":   "spaces",
		"plain-name":        "plain-name",
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
