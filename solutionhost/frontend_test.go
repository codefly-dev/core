package solutionhost_test

import (
	"strings"
	"testing"

	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

// The canonical encoding of the frontend fixture, pinned for the reason
// regression_test.go pins the others: the block travels inside the signed
// bytes, so its encoding is a compatibility surface from the day it ships.
const frontendFixtureDigest = "sha256:5bb75404a754605c06d2756f6fe9a32add994b0401651e76234313e9ac7b6b51"

func TestTheFrontendFixtureCarriesEveryDeclaredField(t *testing.T) {
	document := parse(t, "frontend")
	require.Equal(t, uint64(5), document.Generation)
	frontend := document.Frontend
	require.NotNil(t, frontend, "the fixture carries the block")
	require.Equal(t, "Alpha", frontend.Title)
	require.Equal(t, int64(10), frontend.Order)
	require.Equal(t, "./Page", frontend.ExposedModule)
	require.Equal(t, uint64(1), frontend.HostContract)
	require.Equal(t, []solutionhost.FrontendSurface{
		{ID: "summary", Client: "slack", Title: "Summary", Module: "/surfaces/slack/summary.js"},
		{ID: "footnote", Client: "word", Title: "Footnote", Description: "Insert a footnote from the catalogue.", Module: "/surfaces/word/footnote.js"},
	}, frontend.Surfaces)

	// The dashboard is opaque: decoded as the JSON it is, nothing more.
	dashboard, ok := frontend.Dashboard.(map[string]any)
	require.True(t, ok, "a dashboard is an object")
	widgets := dashboard["dashboards"].([]any)[0].(map[string]any)["widgets"].([]any)
	widget := widgets[0].(map[string]any)
	require.Equal(t, "1.0", widget["version"], "a quoted number stays a string")
	require.Equal(t, true, widget["enabled"])
	require.Equal(t, 100, widget["limit"])

	digest, err := document.Digest()
	require.NoError(t, err)
	require.Equal(t, frontendFixtureDigest, digest, "the canonical encoding of the frontend block moved")
}

// A document without the block is an older producer's and is accepted: the
// valid fixture carries none, its digest is pinned unchanged in
// regression_test.go, and nothing this Core writes for it mentions the key.
func TestADocumentWithoutAFrontendIsAnOlderProducersAndIsAccepted(t *testing.T) {
	document := valid(t)
	require.Nil(t, document.Frontend)
	require.NoError(t, document.Validate())
	canonical, err := document.CanonicalBytes()
	require.NoError(t, err)
	require.NotContains(t, string(canonical), `"frontend":{`)
	written, err := solutionhost.Marshal(document)
	require.NoError(t, err)
	require.NotContains(t, string(written), "\nfrontend:")
}

// The block is signed with the rest of the generation: inside the canonical
// bytes, keyed in name order at every depth — the dashboard's own keys
// included — and moving one field moves the digest.
func TestTheFrontendIsInsideTheSignedBytes(t *testing.T) {
	document := parse(t, "frontend")
	canonical, err := document.CanonicalBytes()
	require.NoError(t, err)
	require.Contains(t, string(canonical), `"frontend":{"dashboard":{"dashboards":[{"id":"overview","layout":"grid","title":"Overview","widgets":[{"enabled":true,"id":"items-per-day","limit":100,"metric":"items","title":"Items per day","version":"1.0","visualization":"line"}]}],"events":[{"description":"An item was created.","name":"item-created","type":"example.item.created"}],"metrics":[{"aggregation":"count","bucket":"day","event":"item-created","groupBy":"time","id":"items"}]},"exposed_module":"./Page","host_contract":1,"order":10,"surfaces":[{"client":"slack","id":"summary","module":"/surfaces/slack/summary.js","title":"Summary"},{"client":"word","description":"Insert a footnote from the catalogue.","id":"footnote","module":"/surfaces/word/footnote.js","title":"Footnote"}],"title":"Alpha"}`)

	before, err := document.Digest()
	require.NoError(t, err)
	document.Frontend.Title = "Alpha, renamed"
	after, err := document.Digest()
	require.NoError(t, err)
	require.NotEqual(t, before, after, "a changed field must change what was signed")
}

// The surfaces are a set to the digest: a producer emitting them from a Go
// map must not move the digest per process. The dashboard's lists keep their
// order, as JSON lists do, so reordering one IS a different dashboard.
func TestFrontendCanonicalOrderIsIndependentOfDeliveryOrder(t *testing.T) {
	document := parse(t, "frontend")
	expected, err := document.Digest()
	require.NoError(t, err)

	shuffled := parse(t, "frontend")
	shuffled.Frontend.Surfaces[0], shuffled.Frontend.Surfaces[1] = shuffled.Frontend.Surfaces[1], shuffled.Frontend.Surfaces[0]
	actual, err := shuffled.Digest()
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	require.Equal(t, "slack", shuffled.Frontend.Surfaces[1].Client, "the document itself is not reordered")

	reordered := parse(t, "frontend")
	events := reordered.Frontend.Dashboard.(map[string]any)["events"].([]any)
	events = append(events, map[string]any{"name": "item-deleted", "type": "example.item.deleted"})
	events[0], events[1] = events[1], events[0]
	reordered.Frontend.Dashboard.(map[string]any)["events"] = events
	moved, err := reordered.Digest()
	require.NoError(t, err)
	require.NotEqual(t, expected, moved)

	// And the canonical bytes are a document the reader accepts, which is
	// what makes them signable: the dashboard comes back as it went in.
	canonical, err := reordered.CanonicalBytes()
	require.NoError(t, err)
	verified, err := solutionhost.PresenceFromVerified(canonical)
	require.NoError(t, err)
	require.Equal(t, reordered.Frontend.Dashboard, verified.Frontend.Dashboard)
}

// Only a solution declares a frontend: a module and a tombstone carrying one
// are refused by name, from the shipped fixtures and from a document built
// in Go.
func TestOnlyASolutionDeclaresAFrontend(t *testing.T) {
	_, err := solutionhost.Parse(presence(t, "module-with-frontend"))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "a module declares no frontend")

	module := parse(t, "module-presence")
	module.Frontend = parse(t, "frontend").Frontend
	require.ErrorIs(t, module.Validate(), solutionhost.ErrInvalid)

	_, err = solutionhost.Parse(presence(t, "tombstone-with-frontend"))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "a removed generation declares no frontend")

	tombstone := parse(t, "tombstone")
	tombstone.Frontend = parse(t, "frontend").Frontend
	require.ErrorIs(t, tombstone.Validate(), solutionhost.ErrInvalid)
}

// The writer reads a frontend back as it wrote it: the dashboard's strings
// that look like numbers or dates stay strings, and the absent slots stay
// absent rather than becoming null or empty.
func TestTheWriterReadsBackAFrontend(t *testing.T) {
	document := parse(t, "frontend")
	dashboard := document.Frontend.Dashboard.(map[string]any)
	dashboard["since"] = "2024-01-01"
	dashboard["ratio"] = "0.5"
	dashboard["empty"] = map[string]any{}
	dashboard["none"] = []any{}
	written, err := solutionhost.Marshal(document)
	require.NoError(t, err)
	require.Contains(t, string(written), `since: "2024-01-01"`)
	require.Contains(t, string(written), `ratio: "0.5"`)
	require.NotContains(t, string(written), "null")
	again, err := solutionhost.Parse(written)
	require.NoError(t, err)
	require.Equal(t, document.Frontend, again.Frontend)
	expected, err := document.Digest()
	require.NoError(t, err)
	actual, err := again.Digest()
	require.NoError(t, err)
	require.Equal(t, expected, actual)

	// A surface's empty description is omitted, not written empty.
	require.NotContains(t, string(written), `description: ""`)
}

// Every dashboard value the document accepts survives the signing encoding:
// the canonical bytes are JSON, and a value JSON would respell is refused
// where it is read (the node rule) — so what comes back from the verified
// payload is what went in, over every spelling JSON treats specially.
func TestEveryAcceptedDashboardValueSurvivesTheSigningEncoding(t *testing.T) {
	document := parse(t, "frontend")
	dashboard := document.Frontend.Dashboard.(map[string]any)
	dashboard["texts"] = []any{"données", "日本語", "a<b>&c", "line sep", "quote\"d", "back\\slash", "tab\tin", ""}
	dashboard["numbers"] = []any{0, -1, 9223372036854775807, uint64(18446744073709551615), -9223372036854775808}
	dashboard["nested"] = map[string]any{"z": map[string]any{"a": []any{map[string]any{"k": false}}}}
	payload, err := solutionhost.SignedPayload(document)
	require.NoError(t, err)
	verified, err := solutionhost.PresenceFromVerified(payload)
	require.NoError(t, err)
	got := verified.Frontend.Dashboard.(map[string]any)
	require.Equal(t, dashboard["texts"], got["texts"])
	require.Equal(t, dashboard["nested"], got["nested"])
	// Numbers come back as the integers the reader types them to: int when
	// the value fits one, uint64 past int64 — on both sides.
	require.Equal(t, dashboard["numbers"], got["numbers"])
}

// A dashboard a Go producer builds is held to the same bound: a value the
// signing encoding cannot carry — a fraction, which the reader refuses as not
// whole — fails loudly at the writer and at verification rather than being
// signed as something else, and a value that cannot be encoded at all is
// refused by name.
func TestADashboardBuiltInGoIsRefusedWhereItCannotBeCarried(t *testing.T) {
	document := parse(t, "frontend")
	document.Frontend.Dashboard.(map[string]any)["threshold"] = 0.95
	_, err := solutionhost.Marshal(document)
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "refused by its own reader")
	require.Contains(t, err.Error(), "not a whole number")
	payload, err := solutionhost.SignedPayload(document)
	require.NoError(t, err, "the canonical bytes exist; it is the reader that refuses them")
	_, err = solutionhost.PresenceFromVerified(payload)
	require.ErrorIs(t, err, solutionhost.ErrInvalid)

	document.Frontend.Dashboard = map[string]any{"handler": func() {}}
	err = document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "frontend.dashboard cannot be encoded")

	document.Frontend.Dashboard = map[string]any{"note": strings.Repeat("x", solutionhost.FrontendDashboardMaxBytes)}
	err = document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "more than the 65536 a dashboard may hold")
	document.Frontend.Dashboard = map[string]any{"note": strings.Repeat("x", solutionhost.FrontendDashboardMaxBytes-len(`{"note":""}`))}
	require.NoError(t, document.Validate(), "exactly the bound is within it")
}

// The block's texts are one line each, so a title cannot carry a newline into
// a navigation entry; a surface's description is the one optional text.
func TestFrontendTextsAreSingleLine(t *testing.T) {
	for field, set := range map[string]func(*solutionhost.Frontend, string){
		"frontend.title":                   func(f *solutionhost.Frontend, v string) { f.Title = v },
		"frontend.exposed_module":          func(f *solutionhost.Frontend, v string) { f.ExposedModule = v },
		"frontend.surfaces[1].title":       func(f *solutionhost.Frontend, v string) { f.Surfaces[1].Title = v },
		"frontend.surfaces[1].description": func(f *solutionhost.Frontend, v string) { f.Surfaces[1].Description = v },
	} {
		t.Run(field, func(t *testing.T) {
			document := parse(t, "frontend")
			set(document.Frontend, "two\nlines")
			err := document.Validate()
			require.ErrorIs(t, err, solutionhost.ErrInvalid)
			require.Contains(t, err.Error(), field+" must be a non-empty single-line text")
		})
	}
	document := parse(t, "frontend")
	document.Frontend.Surfaces[1].Description = ""
	require.NoError(t, document.Validate(), "a description may be absent")
	document.Frontend.Title = "Widgets · GO"
	require.NoError(t, document.Validate(), "a title may carry spaces and any printable rune")
}
