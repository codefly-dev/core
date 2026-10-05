package modulecontract

import (
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
