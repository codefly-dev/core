package solutionhost_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/codefly-dev/core/resources/names"
	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

// The canonical encoding of the build-size fixture, pinned for the reason
// regression_test.go pins the others: the section travels inside the signed
// bytes, so its encoding is a compatibility surface from the day it ships.
const buildSizeFixtureDigest = "sha256:afa149e7a55ae3349784e218662397b094c3c3827f9a0b2738fef0d2d631be04"

func TestTheBuildSizeFixtureCarriesEveryDeclaredField(t *testing.T) {
	document := parse(t, "build-size")
	require.Equal(t, uint64(5), document.Generation)
	size := document.BuildSize
	require.NotNil(t, size, "the fixture carries the section")
	require.NotNil(t, size.Languages)
	require.Equal(t, []solutionhost.LanguageSize{
		{Language: solutionhost.LanguageGo, Backend: 12416, Frontend: 0},
		{Language: solutionhost.LanguageTypeScript, Backend: 0, Frontend: 8102},
	}, *size.Languages)
	require.Equal(t, uint64(12416), size.Backend)
	require.Equal(t, uint64(8102), size.Frontend)
	require.Equal(t, uint64(20518), size.Total)
	require.NotNil(t, size.Vendored)
	require.Equal(t, []string{"web/src/clients"}, *size.Vendored)
	require.NoError(t, size.Validate())

	digest, err := document.Digest()
	require.NoError(t, err)
	require.Equal(t, buildSizeFixtureDigest, digest, "the canonical encoding of the build-size section moved")
}

// A document without the section is an older producer's and is accepted: the
// valid fixture carries none, its digest is pinned unchanged in
// regression_test.go, and nothing this Core writes for it mentions the key.
func TestADocumentWithoutABuildSizeIsAnOlderProducersAndIsAccepted(t *testing.T) {
	document := valid(t)
	require.Nil(t, document.BuildSize)
	require.NoError(t, document.Validate())
	canonical, err := document.CanonicalBytes()
	require.NoError(t, err)
	require.NotContains(t, string(canonical), "build_size")
	written, err := solutionhost.Marshal(document)
	require.NoError(t, err)
	require.NotContains(t, string(written), "build_size")
}

// The section is signed with the rest of the build facts: it is inside the
// canonical bytes, keyed in name order like everything else, and moving one
// count moves the digest.
func TestTheBuildSizeIsInsideTheSignedBytes(t *testing.T) {
	document := parse(t, "build-size")
	canonical, err := document.CanonicalBytes()
	require.NoError(t, err)
	require.Contains(t, string(canonical), `"build_size":{"backend":12416,"frontend":8102,"languages":[{"backend":12416,"frontend":0,"language":"go"},{"backend":0,"frontend":8102,"language":"typescript"}],"total":20518,"vendored":["web/src/clients"]}`)

	before, err := document.Digest()
	require.NoError(t, err)
	(*document.BuildSize.Languages)[0].Backend++
	document.BuildSize.Backend++
	document.BuildSize.Total++
	after, err := document.Digest()
	require.NoError(t, err)
	require.NotEqual(t, before, after, "a changed count must change what was signed")
}

// Both of the section's collections are sets to the digest: a producer
// emitting rows or excluded paths from a Go map must not move the digest per
// process, and an empty list stays an empty list rather than collapsing to
// null, which Validate would refuse on the way back.
func TestBuildSizeCanonicalOrderIsIndependentOfDeliveryOrder(t *testing.T) {
	document := parse(t, "build-size")
	expected, err := document.Digest()
	require.NoError(t, err)

	shuffled := parse(t, "build-size")
	rows := *shuffled.BuildSize.Languages
	rows[0], rows[1] = rows[1], rows[0]
	*shuffled.BuildSize.Vendored = append([]string{"zz/vendored"}, *shuffled.BuildSize.Vendored...)
	*document.BuildSize.Vendored = append(*document.BuildSize.Vendored, "zz/vendored")
	expected, err = document.Digest()
	require.NoError(t, err)
	actual, err := shuffled.Digest()
	require.NoError(t, err)
	require.Equal(t, expected, actual)

	empty := parse(t, "build-size")
	empty.BuildSize = &solutionhost.BuildSize{Languages: &[]solutionhost.LanguageSize{}, Vendored: &[]string{}}
	canonical, err := empty.CanonicalBytes()
	require.NoError(t, err)
	require.Contains(t, string(canonical), `"build_size":{"backend":0,"frontend":0,"languages":[],"total":0,"vendored":[]}`)
	// And the canonical bytes are a document the reader accepts, which is
	// what makes them signable.
	_, err = solutionhost.PresenceFromVerified(canonical)
	require.NoError(t, err)
}

// Every rejected presence fixture is refused naming its message, so a
// consumer can assert the reason and not only the outcome. A fixture a
// document's own rules refuse never reaches the host; the rest are refused
// by admission.
func TestEachRejectedPresenceFixtureIsRefusedNamingItsMessage(t *testing.T) {
	host := fixtureHost(t)
	for _, shipped := range solutionhost.FixturesOf(solutionhost.DocumentTypePresence) {
		if shipped.Outcome != solutionhost.OutcomeRejected {
			require.Empty(t, shipped.Message, "%s is accepted and carries a refusal message", shipped.Name)
			continue
		}
		t.Run(shipped.Name, func(t *testing.T) {
			require.NotEmpty(t, shipped.Message, "every rejected fixture says what its refusal carries")
			document, err := solutionhost.Parse(shipped.Document)
			if err == nil {
				_, err = admitOne(t, host, document)
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), shipped.Message)
		})
	}
}

// Marshal is the one way to write a presence document: it validates, writes,
// and reads back through Parse, returning bytes only when the re-read document
// writes to the same bytes. This holds the property over every accepted
// fixture; the guard's own witness — encoders whose output the reader refuses
// or that drifts between write and re-read — is
// TestTheWriterGuardRefusesAnEncodingThatDoesNotReadBack, driven over the
// injectable encoder, since no accepted model is known whose real encoding
// changes on re-read. Below, an invalid document is also refused before a byte
// is written, by its own defect.
func TestTheWriterReadsBackEveryAcceptedFixture(t *testing.T) {
	for _, shipped := range solutionhost.FixturesOf(solutionhost.DocumentTypePresence) {
		if shipped.Outcome != solutionhost.OutcomeAccepted {
			continue
		}
		t.Run(shipped.Name, func(t *testing.T) {
			document, err := solutionhost.Parse(shipped.Document)
			require.NoError(t, err)
			written, err := solutionhost.Marshal(document)
			require.NoError(t, err)
			again, err := solutionhost.Parse(written)
			require.NoError(t, err)
			expected, err := document.Digest()
			require.NoError(t, err)
			actual, err := again.Digest()
			require.NoError(t, err)
			require.Equal(t, expected, actual)
		})
	}

	// The declared-empty lists survive the trip as declared-empty lists.
	document := parse(t, "build-size")
	document.BuildSize.Vendored = &[]string{}
	written, err := solutionhost.Marshal(document)
	require.NoError(t, err)
	require.Contains(t, string(written), "vendored: []")
	again, err := solutionhost.Parse(written)
	require.NoError(t, err)
	require.NotNil(t, again.BuildSize.Vendored)
	require.Empty(t, *again.BuildSize.Vendored)

	// An invalid document is refused before marshaling, by its own defect.
	broken := parse(t, "build-size")
	broken.BuildSize.Total = 1
	written, err = solutionhost.Marshal(broken)
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "build_size.total is 1")
	require.Nil(t, written, "Marshal returned bytes beside its error")
}

// A producer can hold a section to the rules before building a document
// around it, and a nil section is refused rather than read as empty.
func TestABuildSizeIsValidatedOnItsOwn(t *testing.T) {
	var none *solutionhost.BuildSize
	require.ErrorIs(t, none.Validate(), solutionhost.ErrInvalid)

	size := parse(t, "build-size").BuildSize
	require.NoError(t, size.Validate())
	size.Frontend = 1
	err := size.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "build_size.frontend is 1")
	require.NotContains(t, err.Error(), "build_size.total", "the first refusal is returned, in rule order")

	// A sum that does not fit is refused rather than wrapped into agreement.
	wrapped := &solutionhost.BuildSize{
		Languages: &[]solutionhost.LanguageSize{
			{Language: solutionhost.LanguageGo, Backend: 1 << 63},
			{Language: solutionhost.LanguagePython, Backend: 1 << 63},
		},
		Backend:  0,
		Frontend: 0,
		Total:    0,
		Vendored: &[]string{},
	}
	err = wrapped.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "backend lines sum to more than a count can hold")
}

// Every exclusion the document accepts survives the signing encoding
// byte-for-byte: the canonical bytes are JSON, and encoding/json rewrites a
// byte that is not UTF-8 as U+FFFD, so a path the grammar admitted but the
// encoding changed would make the signed document name a path the counter
// never excluded. The grammar refuses such a path (names.IsPathPrefix requires
// valid UTF-8); this holds the consequence, over the accepted fixture and over
// every spelling the grammar admits that JSON treats specially.
func TestEveryAcceptedExclusionSurvivesTheSigningEncoding(t *testing.T) {
	document := parse(t, "build-size")
	*document.BuildSize.Vendored = append(*document.BuildSize.Vendored,
		"données/clients", "日本語/src", "a<b>&c", "line\u2028sep", "quote\"d/path", "back\\slash-free")
	// The last one is refused on sight: a backslash is not in the grammar.
	err := document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	*document.BuildSize.Vendored = (*document.BuildSize.Vendored)[:len(*document.BuildSize.Vendored)-1]
	require.NoError(t, document.Validate())

	payload, err := solutionhost.SignedPayload(document)
	require.NoError(t, err)
	verified, err := solutionhost.PresenceFromVerified(payload)
	require.NoError(t, err)
	// Byte-for-byte, after sorting — the canonical order is the document's.
	expected := append([]string(nil), *document.BuildSize.Vendored...)
	sort.Strings(expected)
	require.Equal(t, expected, *verified.BuildSize.Vendored)
	for _, entry := range *verified.BuildSize.Vendored {
		require.True(t, utf8.ValidString(entry))
		require.NotContains(t, entry, "\uFFFD")
	}

	// And the path the grammar refuses is exactly the one the encoding would
	// have changed: encoded by hand, it comes back different.
	rewritten, err := json.Marshal("vendor/\xff")
	require.NoError(t, err)
	var back string
	require.NoError(t, json.Unmarshal(rewritten, &back))
	require.NotEqual(t, "vendor/\xff", back, "json rewrites the byte, which is why the grammar refuses it")
	require.False(t, names.IsPathPrefix("vendor/\xff"))
	document.BuildSize.Vendored = &[]string{"vendor/\xff"}
	require.ErrorIs(t, document.Validate(), solutionhost.ErrInvalid)
}

// A tombstone declares no size: the refusal names the half-removal, before any
// collection is looked at.
func TestATombstoneDeclaresNoBuildSize(t *testing.T) {
	_, err := solutionhost.Parse(presence(t, "tombstone-with-build-size"))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "a removed generation declares no build_size")

	tombstone := parse(t, "tombstone")
	tombstone.BuildSize = parse(t, "build-size").BuildSize
	require.ErrorIs(t, tombstone.Validate(), solutionhost.ErrInvalid)
}

// LanguageOf is the one place a file's extension becomes a language.
func TestLanguageOfMatchesTheExtensionAsSpelled(t *testing.T) {
	for file, want := range map[string]solutionhost.Language{
		"main.go": solutionhost.LanguageGo, "web/src/App.tsx": solutionhost.LanguageTypeScript,
		"lib/index.mjs": solutionhost.LanguageJavaScript, "svc/app.py": solutionhost.LanguagePython,
		"core/src/lib.rs": solutionhost.LanguageRust, "types/index.d.ts": solutionhost.LanguageTypeScript,
	} {
		language, known := solutionhost.LanguageOf(file)
		require.Truef(t, known, "%s", file)
		require.Equal(t, want, language, file)
	}
	for _, file := range []string{"README.md", "Dockerfile", "api/v1/service.proto", "go.mod", "Main.GO", "schema.sql", "styles.css", "index.html", ".gitignore"} {
		_, known := solutionhost.LanguageOf(file)
		require.Falsef(t, known, "%s names no language the build's size counts", file)
	}
	require.True(t, strings.HasPrefix(string(solutionhost.LanguageGo), "go"))
}

// The authority reader shares the presence reader's node checks, so a merge
// key is refused there too, by the same rule, rather than by a second
// implementation that could drift.
func TestTheAuthorityReaderRefusesAMergeKeyThroughTheSharedCheck(t *testing.T) {
	_, err := solutionhost.ParseAuthority(authority(t, "merge-key"))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "uses the merge key")
	_, err = solutionhost.ParseAuthority(authority(t, "valid"))
	require.NoError(t, err)
}
