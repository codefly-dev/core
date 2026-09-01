package resources

import "testing"

func TestNormalizeRepo(t *testing.T) {
	cases := map[string]string{
		"git@github.com:codefly-dev/core.git":       "codefly-dev/core",
		"git@github.com:codefly-dev/core":           "codefly-dev/core",
		"https://github.com/codefly-dev/core.git":   "codefly-dev/core",
		"https://github.com/codefly-dev/core":       "codefly-dev/core",
		"ssh://git@github.com/codefly-dev/core.git": "codefly-dev/core",
		"obin-ai/module-saas-starter":               "obin-ai/module-saas-starter",
		"git@github.com:Obin-AI/Module.git":         "obin-ai/module",
	}
	for in, want := range cases {
		if got := normalizeRepo(in); got != want {
			t.Errorf("normalizeRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseWorktreeCoordinate(t *testing.T) {
	repo, ref, err := parseWorktreeCoordinate("obin-ai/module-document-store@main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo != "obin-ai/module-document-store" || ref != "main" {
		t.Fatalf("got (%q, %q)", repo, ref)
	}
	for _, bad := range []string{"", "no-at-sign", "@main", "repo@"} {
		if _, _, err := parseWorktreeCoordinate(bad); err == nil {
			t.Errorf("parseWorktreeCoordinate(%q) expected error", bad)
		}
	}
}
