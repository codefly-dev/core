package names

import (
	"path"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// IsPathPrefix reports whether value is a canonical path prefix inside a
// module tree: valid UTF-8 in Unicode normalization form C, slash-separated,
// relative to the tree's root, and spelled exactly as path.Clean spells it. So
// no empty value, no ".", no leading "/" or "./", no ".." segment, no doubled
// or trailing slash, no backslash, no control character, no byte sequence that
// is not UTF-8 and no decomposed character.
//
// One prefix has one spelling. The module manifest declares the paths a build
// vendors, and the presence document records the ones a build excluded; both
// hold an entry to this grammar so "web/src/clients/" and "./web/src/clients"
// cannot read as two different exclusions of one directory, and a path
// written one way in the manifest cannot fail to match a file walked the other.
//
// Valid UTF-8 is part of the grammar because the presence document is signed
// over a JSON encoding, and encoding/json REPLACES a byte that is not UTF-8
// with U+FFFD. A YAML document can carry such bytes (`!!binary`), a counter
// comparing bytes would exclude the path as written, and the signed document
// would then name a different path than the one excluded — accepted at every
// step, with YAML's own read-back preserving the bytes as `!!binary`. So a
// path the signing encoding cannot carry byte-for-byte is not a path here.
//
// Normalization form C is part of the grammar for the same reason: "clienté"
// has two Unicode spellings, and a filesystem may store a directory under the
// decomposed one while the manifest declares the composed one, so a byte
// comparison would never match and the exclusion would silently cover nothing.
// One spelling here is NFC; a walked path in another form is refused where it
// is read rather than merely unmatched.
func IsPathPrefix(value string) bool {
	if value == "" || value == "." || value == ".." || !utf8.ValidString(value) || !norm.NFC.IsNormalString(value) {
		return false
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "../") || strings.Contains(value, "\\") {
		return false
	}
	if strings.ContainsFunc(value, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return false
	}
	return path.Clean(value) == value
}

// PathWithin reports whether value is prefix itself or lies under it, by
// whole segments: "web/src" covers "web/src/index.ts" and not
// "web/srcs/index.ts". Both arguments are expected in IsPathPrefix's
// spelling; the comparison is textual and does not clean either.
func PathWithin(prefix, value string) bool {
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}
