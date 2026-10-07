package module

import (
	"errors"
	"strings"
	"testing"

	"github.com/codefly-dev/core/internal/conditions"
)

// TestEveryRefusalConditionIsReachedByAFixture holds the kit to the SOURCE
// rather than to a list: every fmt.Errorf in this package that refuses a
// document must be the refusal some shipped fixture actually receives.
//
// This is the check eight rounds of review were doing by hand. A condition
// added without a counterexample fails here, at its own file and line, and a
// condition whose counterexample stops reaching it fails here too.
func TestEveryRefusalConditionIsReachedByAFixture(t *testing.T) {
	sites, err := conditions.Sites(".", "ErrInvalid")
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) == 0 {
		t.Fatal("no refusal sites found: the enumeration itself is broken")
	}

	all, err := Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	var refusals []string
	for _, fixture := range all {
		if _, err := Parse(fixture.Document); err != nil && errors.Is(err, ErrInvalid) {
			refusals = append(refusals, err.Error())
		}
	}

	// Conditions a DOCUMENT cannot reach, each with the reason and the path
	// that does reach it. A guard that trips only when another part of this
	// package is already wrong has no counterexample document — but it is
	// still a condition, so it is named here with its reason rather than
	// left as an unexplained gap, and the list is asserted to be exactly the
	// unreached set: one that starts being reached, or stops being, fails.
	defensive := map[string]string{
		"name one another more deeply than this reader follows; an anchor chain that long, or a cycle, has no meaning to resolve": "the bound exists so a cycle or a long chain terminates instead of overflowing the stack; an anchor chain deep enough to reach it is representable in yaml.v3's node graph, and I did NOT succeed in writing one as a document fixture — the attempt was a malformed document, not a refusal, so this is unfixtured rather than unreachable",
		"the contract cannot be encoded:":                                               "yaml.Marshal fails on a model Validate accepted; forced in TestTheWriterRefusesWhatItCannotWrite",
		"the encoded contract is refused by its own reader, so it is not written:":      "the writer emits a document its own reader refuses; forced in TestTheWriterRefusesWhatItCannotWrite",
		"the re-read contract cannot be encoded:":                                       "the re-read model fails to marshal; unreachable while Marshal is deterministic over a parsed model",
		"the encoded contract reads back as a different contract, so it is not written": "THE OPEN ONE: no model Validate accepts has been found whose encoding changes on re-marshal, so no fixture can reach it; TestTheEncoderIsDeterministic asserts the property this guard rests on, and the reviewer's scratch input would make it a fixture",
		"contract is required":                                                          "Validate(nil); forced in TestANilModelIsRefused",
		"a scope ceiling mixes bare actions with {resource_kind, actions} entries; a scope ceiling is written in one spelling": "the WRITER's refusal of the mixed union, reached through Marshal rather than Parse; the mixed-ceiling document fixture reaches Validate's own refusal, and the direct writer test forces this one",
	}

	var unreached []conditions.Site
	for _, site := range sites {
		if site.Literal == "" {
			t.Errorf("%s:%d carries no literal text to match on; give the refusal words of its own", site.File, site.Line)
			continue
		}
		reached := false
		for _, refusal := range refusals {
			if strings.Contains(refusal, site.Literal) {
				reached = true
				break
			}
		}
		if !reached {
			unreached = append(unreached, site)
		}
	}
	for _, site := range unreached {
		if _, declared := defensive[site.Literal]; !declared {
			t.Errorf("%s:%d is reached by no fixture and is not declared defensive: %q\n\tadd a fixture that reaches it, or declare it here with the reason it has no document",
				site.File, site.Line, site.Literal)
		}
	}
	reached := map[string]bool{}
	for _, site := range sites {
		for _, refusal := range refusals {
			if strings.Contains(refusal, site.Literal) {
				reached[site.Literal] = true
			}
		}
	}
	for literal := range defensive {
		if reached[literal] {
			t.Errorf("%q is declared defensive but a fixture now reaches it; the declaration is stale and should be deleted", literal)
		}
		found := false
		for _, site := range sites {
			if site.Literal == literal {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is declared defensive but is no longer a refusal in this package; the declaration is stale", literal)
		}
	}
	t.Logf("%d module contract refusal conditions: %d reached by a fixture, %d declared defensive",
		len(sites), len(sites)-len(unreached), len(defensive))
}
