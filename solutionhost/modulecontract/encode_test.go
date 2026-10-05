package modulecontract

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestEncodeRefusesWhatItsOwnReaderWouldRefuse: the guarded writer. Marshaling
// the model directly let a publisher emit a document this reader refuses, with
// the failure surfacing at whoever read the file; Encode validates, marshals
// and reads back, and returns bytes only when all three agree.
func TestEncodeRefusesWhatItsOwnReaderWouldRefuse(t *testing.T) {
	contract, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	document, err := contract.Encode()
	if err != nil {
		t.Fatalf("the valid contract must encode: %v", err)
	}
	if _, err := Parse(document); err != nil {
		t.Fatalf("Encode returned bytes its own reader refuses: %v", err)
	}
	for name, break_ := range map[string]func(*Contract){
		"a principal that is not a name": func(c *Contract) { c.Principal = "INVALID PRINCIPAL" },
		"an action that is not an action": func(c *Contract) {
			c.ScopeCeilings[0].Actions = []string{"NOT AN ACTION"}
		},
		"a ceiling holding both spellings": func(c *Contract) {
			c.Bindings[0].ScopeCeiling["invoke"] = Ceiling{
				Actions: []string{"read"},
				Scopes:  []CeilingScope{{ResourceKind: "k", Actions: []string{"write"}}},
			}
		},
		"a queue list that is absent rather than empty": func(c *Contract) { c.Queues = nil },
	} {
		t.Run(name, func(t *testing.T) {
			broken, err := Parse(fixture(t))
			if err != nil {
				t.Fatal(err)
			}
			break_(broken)
			document, err := broken.Encode()
			if err == nil {
				t.Fatalf("Encode wrote a contract its own reader refuses:\n%s", document)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("the refusal does not carry ErrInvalid: %v", err)
			}
			if document != nil {
				t.Fatal("Encode returned bytes beside its error")
			}
		})
	}
	// A reader is not enough on its own: a document that parses to something
	// ELSE is refused too, because no reader would notice it.
	if !strings.Contains(mustFailEncode(t, func(c *Contract) { c.Schema = "" }), "validated before it is written") {
		t.Fatal("an invalid schema must be refused before marshaling")
	}
}

func mustFailEncode(t *testing.T, break_ func(*Contract)) string {
	t.Helper()
	contract, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	break_(contract)
	if _, err := contract.Encode(); err != nil {
		return err.Error()
	}
	t.Fatal("Encode accepted a contract its reader refuses")
	return ""
}

// TestEncodeAcceptsEquivalentEmptyRepresentations: nil and an empty slice are
// the same document — `omitempty` writes neither — so a writer comparing Go
// values refused valid models and made the prescribed API unusable for a
// programmatically built one. Encode compares WIRE MEANING.
func TestEncodeAcceptsEquivalentEmptyRepresentations(t *testing.T) {
	for name, empty := range map[string]func(*Contract){
		"an empty destinations slice": func(c *Contract) { c.Destinations = []Destination{} },
		"an empty namespaces slice":   func(c *Contract) { c.Namespaces = []string{} },
		"an empty scope-ceiling slice": func(c *Contract) {
			c.ScopeCeilings = []ScopeCeiling{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			contract, err := Parse(fixture(t))
			if err != nil {
				t.Fatal(err)
			}
			empty(contract)
			if _, err := contract.Encode(); err != nil {
				t.Fatalf("a valid model was refused by its own writer: %v", err)
			}
		})
	}
	// And a minimal contract built in Go: the required lists declared empty,
	// every other list nil, which the Go-value comparison refused outright.
	minimal := &Contract{Schema: SchemaV1, Principal: "assistant", Namespaces: []string{}, Queues: []string{}}
	if _, err := minimal.Encode(); err != nil {
		t.Fatalf("a minimal constructed contract was refused by its own writer: %v", err)
	}
}

// TestTheGuardedWriterReadsBackEveryAcceptedFixture drives the readback stages
// over every document the kit accepts: parse, encode, parse again, encode
// again, and require the bytes to match.
//
// For a model Validate accepts, all three of Encode's stages agree at this
// head — so no valid model trips the readback, and this is what makes that
// claim checkable rather than asserted. The stages exist to fail if the
// encoder and the decoder ever drift apart, which is a defect no reader would
// otherwise notice: a document that parses to something ELSE is refused by
// nobody.
func TestTheGuardedWriterReadsBackEveryAcceptedFixture(t *testing.T) {
	all, err := Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	accepted := 0
	for _, entry := range all {
		if entry.Outcome != OutcomeAccepted {
			continue
		}
		accepted++
		contract, err := Parse(entry.Document)
		if err != nil {
			t.Errorf("fixture %s is accepted by the kit and refused by Parse: %v", entry.Name, err)
			continue
		}
		first, err := contract.Encode()
		if err != nil {
			t.Errorf("fixture %s does not survive its own writer: %v", entry.Name, err)
			continue
		}
		again, err := Parse(first)
		if err != nil {
			t.Errorf("fixture %s encodes to bytes its own reader refuses: %v", entry.Name, err)
			continue
		}
		second, err := again.Encode()
		if err != nil {
			t.Errorf("fixture %s does not survive a second pass: %v", entry.Name, err)
			continue
		}
		if !bytes.Equal(first, second) {
			t.Errorf("fixture %s encodes to different bytes on the second pass", entry.Name)
		}
	}
	if accepted == 0 {
		t.Fatal("no accepted fixture was exercised")
	}
}
