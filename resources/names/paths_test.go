package names

import "testing"

func TestIsPathPrefixHoldsOneSpelling(t *testing.T) {
	for _, accepted := range []string{"web", "web/src/clients", "Clients/Go", "a-b_c.d/e", "with space/dir", "..hidden/x", "a/..b", "données/clients", "日本語/src", "a<b>&c", "web/client\u00e9"} {
		if !IsPathPrefix(accepted) {
			t.Errorf("%q is a canonical path prefix and was refused", accepted)
		}
	}
	for _, refused := range []string{
		"", ".", "..", "/web", "./web", "web/", "web//src", "web/./src", "web/../src", "../web", "web\\src",
		"web/src\n", "web\x00", "web/src/.", "web/..",
		// Not UTF-8: the signing encoding would rewrite it, so the signed
		// document would name a path the counter never excluded.
		"vendor/\xff", "\xc3", "web/\xed\xa0\x80",
		// Decomposed (NFD): a second Unicode spelling of "clienté", which a
		// byte comparison against the composed one would never match.
		"web/clie\u006e\u0074\u0065\u0301",
	} {
		if IsPathPrefix(refused) {
			t.Errorf("%q is not a canonical path prefix and was accepted", refused)
		}
	}
}

func TestPathWithinComparesWholeSegments(t *testing.T) {
	for _, within := range [][2]string{{"web/src", "web/src"}, {"web/src", "web/src/index.ts"}, {"web", "web/src/deep/file.go"}} {
		if !PathWithin(within[0], within[1]) {
			t.Errorf("%q should cover %q", within[0], within[1])
		}
	}
	for _, outside := range [][2]string{{"web/src", "web/srcs/index.ts"}, {"web/src", "web"}, {"web/src", "other/web/src/x.ts"}, {"web/src/index.ts", "web/src"}} {
		if PathWithin(outside[0], outside[1]) {
			t.Errorf("%q should not cover %q", outside[0], outside[1])
		}
	}
}
