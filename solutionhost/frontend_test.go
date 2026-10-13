package solutionhost_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The canonical encoding of the frontend fixture, pinned for the reason
// regression_test.go pins the others: the block travels inside the signed
// bytes, so its encoding is a compatibility surface from the day it ships.
const frontendFixtureDigest = "sha256:c988cb392cc063ec4a8e20793c2db8aad9c62a0d2afb5aaf10ce5708a4cd018d"

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
		{ID: "summary", Client: "slack", Title: "Summary", Module: "/surfaces/slack/summary.js", Contract: 1, Applies: "always"},
		{
			ID: "footnote", Client: "word", Title: "Footnote", Description: "Insert a footnote from the catalogue.", Module: "/surfaces/word/footnote.js",
			Contract: 2, Applies: map[string]any{"tagged": []any{"contract", "brief"}}, Events: []string{"example.item", "example.citation"},
		},
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
	require.Contains(t, string(canonical), `"frontend":{"dashboard":{"dashboards":[{"id":"overview","layout":"grid","title":"Overview","widgets":[{"enabled":true,"id":"items-per-day","limit":100,"metric":"items","title":"Items per day","version":"1.0","visualization":"line"}]}],"events":[{"description":"An item was created.","name":"item-created","type":"example.item.created"}],"metrics":[{"aggregation":"count","bucket":"day","event":"item-created","groupBy":"time","id":"items"}]},"exposed_module":"./Page","host_contract":1,"order":10,"surfaces":[{"applies":"always","client":"slack","contract":1,"id":"summary","module":"/surfaces/slack/summary.js","title":"Summary"},{"applies":{"tagged":["contract","brief"]},"client":"word","contract":2,"description":"Insert a footnote from the catalogue.","events":["example.citation","example.item"],"id":"footnote","module":"/surfaces/word/footnote.js","title":"Footnote"}],"title":"Alpha"}`)

	before, err := document.Digest()
	require.NoError(t, err)
	document.Frontend.Title = "Alpha, renamed"
	after, err := document.Digest()
	require.NoError(t, err)
	require.NotEqual(t, before, after, "a changed field must change what was signed")
}

// The surfaces, and each surface's events, are sets to the digest: a producer
// emitting either from a Go map must not move the digest per process. The
// dashboard's lists, and the tags a surface applies to, keep their order, as
// JSON lists do, so reordering one IS a different declaration.
func TestFrontendCanonicalOrderIsIndependentOfDeliveryOrder(t *testing.T) {
	document := parse(t, "frontend")
	expected, err := document.Digest()
	require.NoError(t, err)

	shuffled := parse(t, "frontend")
	shuffled.Frontend.Surfaces[0], shuffled.Frontend.Surfaces[1] = shuffled.Frontend.Surfaces[1], shuffled.Frontend.Surfaces[0]
	names := shuffled.Frontend.Surfaces[0].Events
	names[0], names[1] = names[1], names[0]
	actual, err := shuffled.Digest()
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	require.Equal(t, "slack", shuffled.Frontend.Surfaces[1].Client, "the document itself is not reordered")
	require.Equal(t, []string{"example.citation", "example.item"}, shuffled.Frontend.Surfaces[0].Events)

	retagged := parse(t, "frontend")
	tags := retagged.Frontend.Surfaces[1].Applies.(map[string]any)["tagged"].([]any)
	tags[0], tags[1] = tags[1], tags[0]
	moved, err := retagged.Digest()
	require.NoError(t, err)
	require.NotEqual(t, expected, moved, "the tags are read in order, so their order is signed")

	reordered := parse(t, "frontend")
	events := reordered.Frontend.Dashboard.(map[string]any)["events"].([]any)
	events = append(events, map[string]any{"name": "item-deleted", "type": "example.item.deleted"})
	events[0], events[1] = events[1], events[0]
	reordered.Frontend.Dashboard.(map[string]any)["events"] = events
	moved, err = reordered.Digest()
	require.NoError(t, err)
	require.NotEqual(t, expected, moved, "a dashboard's lists are read in order, so their order is signed")

	// And the canonical bytes are a document the reader accepts, which is
	// what makes them signable: the dashboard comes back as it went in.
	canonical, err := reordered.CanonicalBytes()
	require.NoError(t, err)
	verified, err := solutionhost.PresenceFromVerified(canonical)
	require.NoError(t, err)
	require.Equal(t, reordered.Frontend.Dashboard, verified.Frontend.Dashboard)
}

// Both component kinds use one frontend contract, including the signed reader
// and writer. A shared module's page does not change its kind or grant authority.
func TestModulesAndSolutionsDeclareFrontends(t *testing.T) {
	for _, fixture := range []string{"frontend", "module-with-frontend"} {
		t.Run(fixture, func(t *testing.T) {
			document := parse(t, fixture)
			require.NotNil(t, document.Frontend)
			require.NoError(t, document.Validate())
			written, err := solutionhost.Marshal(document)
			require.NoError(t, err)
			again, err := solutionhost.Parse(written)
			require.NoError(t, err)
			require.Equal(t, document.Kind, again.Kind)
			require.Equal(t, document.Frontend, again.Frontend)
			canonical, err := document.CanonicalBytes()
			require.NoError(t, err)
			verified, err := solutionhost.PresenceFromVerified(canonical)
			require.NoError(t, err)
			require.Equal(t, document.Kind, verified.Kind)
			require.Equal(t, document.Frontend.Title, verified.Frontend.Title)
		})
	}
	module := parse(t, "module-presence")
	module.Frontend = parse(t, "frontend").Frontend
	require.NoError(t, module.Validate())
	module.Frontend.HostContract = 0
	require.ErrorIs(t, module.Validate(), solutionhost.ErrInvalid)
}

func TestTombstonesDeclareNoFrontendForEitherKind(t *testing.T) {
	_, err := solutionhost.Parse(presence(t, "tombstone-with-frontend"))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "a removed generation declares no frontend")
	for _, kind := range []solutionhost.Kind{solutionhost.KindModule, solutionhost.KindSolution} {
		tombstone := parse(t, "tombstone")
		tombstone.Kind = kind
		tombstone.Frontend = parse(t, "frontend").Frontend
		require.ErrorIs(t, tombstone.Validate(), solutionhost.ErrInvalid)
	}
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

// A surface carries what the client reads — its contract major, what it
// applies to, the namespaces it reconciles on — held to exactly the shapes
// the client has a branch for, from a document built in Go as from one read.
func TestASurfaceCarriesWhatTheClientReads(t *testing.T) {
	for name, applies := range map[string]any{
		"a third word":           "sometimes",
		"an empty tag list":      map[string]any{"tagged": []any{}},
		"a second key":           map[string]any{"tagged": []any{"brief"}, "also": true},
		"a tag that is not text": map[string]any{"tagged": []any{"brief", 7}},
		"an empty tag":           map[string]any{"tagged": []any{""}},
		"nothing":                nil,
	} {
		t.Run(name, func(t *testing.T) {
			document := parse(t, "frontend")
			document.Frontend.Surfaces[0].Applies = applies
			err := document.Validate()
			require.ErrorIs(t, err, solutionhost.ErrInvalid)
			require.Contains(t, err.Error(), `frontend.surfaces[0].applies is neither "always" nor {tagged: [one or more tags]}`)
		})
	}
	document := parse(t, "frontend")
	document.Frontend.Surfaces[0].Events = []string{"example.item", "acme-widgets.v2"}
	require.NoError(t, document.Validate())
	document.Frontend.Surfaces[0].Events = []string{""}
	require.ErrorIs(t, document.Validate(), solutionhost.ErrInvalid)

	// An omitted contract or applies is refused on the node, before the typed
	// decoder reads a zero or a nil into it.
	for field, without := range map[string]string{
		"contract": "      applies: always\n",
		"applies":  "      contract: 1\n",
	} {
		written := strings.Replace(string(presence(t, "frontend")), "      contract: 1\n      applies: always\n", without, 1)
		_, err := solutionhost.Parse([]byte(written))
		require.ErrorIs(t, err, solutionhost.ErrInvalid)
		require.Contains(t, err.Error(), "frontend.surfaces[0]."+field+" must be declared")
	}
}

// Limits are identical for both kinds, and byte limits do not count runes.
func TestFrontendLimitsForBothKinds(t *testing.T) {
	for _, fixture := range []string{"frontend", "module-with-frontend"} {
		t.Run(fixture, func(t *testing.T) {
			d := parse(t, fixture)
			d.Frontend.Title = strings.Repeat("é", solutionhost.FrontendTextMaxBytes/2)
			require.NoError(t, d.Validate())
			d.Frontend.Title += "x"
			require.ErrorContains(t, d.Validate(), "frontend text exceeds")
		})
	}
}

func TestFrontendCollectionLimits(t *testing.T) {
	for _, collection := range []string{"surfaces", "events", "tags"} {
		t.Run(collection, func(t *testing.T) {
			d := parse(t, "frontend")
			surface := d.Frontend.Surfaces[0]
			set := func(n int) {
				names := make([]string, n)
				tags := make([]any, n)
				surfaces := make([]solutionhost.FrontendSurface, n)
				for i := range n {
					names[i] = fmt.Sprintf("example.item-%d", i)
					tags[i] = names[i]
					surfaces[i] = surface
					surfaces[i].ID = fmt.Sprintf("page-%d", i)
				}
				switch collection {
				case "surfaces":
					d.Frontend.Surfaces = surfaces
				case "events":
					d.Frontend.Surfaces[0].Events = names
				case "tags":
					d.Frontend.Surfaces[0].Applies = map[string]any{"tagged": tags}
				}
			}
			set(solutionhost.FrontendCollectionMaxItems)
			require.NoError(t, d.Validate())
			set(solutionhost.FrontendCollectionMaxItems + 1)
			require.ErrorContains(t, d.Validate(), "frontend collection exceeds")
		})
	}
}

// Every section fixture drives both readers. The full-presence tombstone rule
// remains the document's responsibility; the standalone section has no kind or
// removal flag with which to decide admission.
func TestStandaloneFrontendUsesPresenceRules(t *testing.T) {
	count := 0
	for _, fixture := range solutionhost.FixturesOf(solutionhost.DocumentTypePresence) {
		if !strings.Contains(fixture.Name, "frontend") || strings.Contains(fixture.Name, "tombstone") {
			continue
		}
		var tree yaml.Node
		require.NoError(t, yaml.Unmarshal(fixture.Document, &tree))
		var section *yaml.Node
		for i := 0; i < len(tree.Content[0].Content); i += 2 {
			if tree.Content[0].Content[i].Value == "frontend" {
				section = tree.Content[0].Content[i+1]
				break
			}
		}
		if section == nil {
			continue
		}
		count++
		t.Run(fixture.Name, func(t *testing.T) {
			raw, err := yaml.Marshal(section)
			require.NoError(t, err)
			parsed, err := solutionhost.ParseFrontend(raw)
			if fixture.Outcome == solutionhost.OutcomeRejected {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			document, err := solutionhost.Parse(fixture.Document)
			require.NoError(t, err)
			require.Equal(t, document.Frontend, parsed)
		})
	}
	require.Equal(t, 22, count, "the complete shipped frontend section fixture family must run")
	for _, raw := range []string{"", "[]", "null", "title: Example\norder: 0\nexposed_module: ./Page\nhost_contract: 1\nunknown: true", "title: Example\norder: 0\nexposed_module: ./Page\nhost_contract: 1\n---\n{}"} {
		_, err := solutionhost.ParseFrontend([]byte(raw))
		require.Error(t, err)
	}
}
