package resources

import "testing"

// The thesis: per-service resolution is by-service > by-agent > default >
// caller fallback — so a developer can run Go services native while postgres
// runs nix, with everything else falling back to the global runtime context.
func TestUserPreferences_RuntimeContextFor(t *testing.T) {
	p := &UserPreferences{Runtime: &RuntimePreferences{
		Default:   "native",
		ByAgent:   map[string]string{"postgres": "nix"},
		ByService: map[string]string{"special": "container"},
	}}

	cases := []struct {
		name, service, agent, fallback, want string
	}{
		{"by-service wins over agent+default", "special", "go-grpc", "free", "container"},
		{"by-agent: postgres → nix", "postgres", "postgres", "free", "nix"},
		{"default native for go services", "mind", "go-grpc", "free", "native"},
	}
	for _, c := range cases {
		if got := p.RuntimeContextFor(c.service, c.agent, c.fallback); got != c.want {
			t.Errorf("%s: RuntimeContextFor(%q,%q,%q) = %q, want %q", c.name, c.service, c.agent, c.fallback, got, c.want)
		}
	}

	// No default set → fall back to the caller's global runtime context.
	noDefault := &UserPreferences{Runtime: &RuntimePreferences{ByAgent: map[string]string{"postgres": "nix"}}}
	if got := noDefault.RuntimeContextFor("mind", "go-grpc", "native"); got != "native" {
		t.Errorf("no-default fallback = %q, want native", got)
	}

	// Nil/empty prefs → always the fallback (missing preferences file case).
	var nilPrefs *UserPreferences
	if got := nilPrefs.RuntimeContextFor("mind", "go-grpc", "nix"); got != "nix" {
		t.Errorf("nil prefs fallback = %q, want nix", got)
	}
	if got := (&UserPreferences{}).RuntimeContextFor("mind", "go-grpc", "nix"); got != "nix" {
		t.Errorf("empty prefs fallback = %q, want nix", got)
	}
}
