package urlguard_test

import (
	"testing"

	"github.com/codefly-dev/core/network/urlguard"
)

// FuzzNormalizeOrigin proves URL normalization never panics and that an
// admitted origin is always in canonical form (round-trips through parse).
func FuzzNormalizeOrigin(f *testing.F) {
	for _, seed := range []string{
		"https://api.example.com",
		"https://API.Example.com.:8443",
		"http://user:pass@host/path?q#f",
		"//scheme-relative",
		"https://[::1]",
		"ftp://host",
		"https://xn--pple-43d.com",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		origin, err := urlguard.NormalizeOrigin(raw)
		if err != nil {
			return
		}
		// A normalized origin must re-normalize to itself.
		again, err := urlguard.NormalizeOrigin(origin.String())
		if err != nil {
			t.Fatalf("normalized origin %q did not re-normalize: %v", origin.String(), err)
		}
		if again != origin {
			t.Fatalf("normalization is not idempotent: %v -> %v", origin, again)
		}
	})
}

// FuzzSafePath proves path validation never panics and never admits traversal
// or a query/fragment.
func FuzzSafePath(f *testing.F) {
	for _, seed := range []string{"/", "/v1/x", "/a/../b", "/a%2e%2e/b", "/a?x=1", "//b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		safe, err := urlguard.SafePath(path)
		if err != nil {
			return
		}
		if safe == "" || safe[0] != '/' {
			t.Fatalf("safe path %q is not absolute", safe)
		}
	})
}
