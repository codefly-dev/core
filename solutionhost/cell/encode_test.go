package cell

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestEncodeRefusesWhatItsOwnReaderWouldRefuse: the guarded writer, for the
// reason the module contract has one — a publisher marshaling the model
// directly could write a cell the platform's loader rejects at admission
// rather than at publish.
func TestEncodeRefusesWhatItsOwnReaderWouldRefuse(t *testing.T) {
	file, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	document, err := file.Encode()
	if err != nil {
		t.Fatalf("the valid cell must encode: %v", err)
	}
	if _, err := Parse(document); err != nil {
		t.Fatalf("Encode returned bytes its own reader refuses: %v", err)
	}
	for name, break_ := range map[string]func(*File){
		"an image digest of the wrong length": func(f *File) {
			f.Namespaces[0].Workloads[0].Containers[0].Image.Digest = "sha256:abc"
		},
		"an endpoint with no visibility": func(f *File) {
			f.Namespaces[0].Workloads[0].Endpoints[0].Visibility = ""
		},
		"a selector label the cluster would refuse": func(f *File) {
			f.Namespaces[0].Workloads[0].Selector["app"] = "bad/value"
		},
		"an identity of another trust domain": func(f *File) {
			f.Namespaces[0].Workloads[0].SPIFFEID = "spiffe://other.example/ns/payments/sa/api"
		},
	} {
		t.Run(name, func(t *testing.T) {
			broken, err := Parse(fixture(t))
			if err != nil {
				t.Fatal(err)
			}
			break_(broken)
			document, err := broken.Encode()
			if err == nil {
				t.Fatalf("Encode wrote a cell its own reader refuses:\n%s", document)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("the refusal does not carry ErrInvalid: %v", err)
			}
			if document != nil {
				t.Fatal("Encode returned bytes beside its error")
			}
		})
	}
}

// TestEncodeAcceptsEquivalentEmptyRepresentations: nil and an empty slice are
// the same document — `omitempty` writes neither — so a writer comparing Go
// values refused valid models and made the prescribed API unusable for a
// programmatically built one. Encode compares WIRE MEANING.
func TestEncodeAcceptsEquivalentEmptyRepresentations(t *testing.T) {
	for name, empty := range map[string]func(*File){
		"an empty init-container slice": func(f *File) { f.Namespaces[0].Workloads[1].InitContainers = []Container{} },
		"an empty ingress slice":        func(f *File) { f.Namespaces[0].Workloads[1].Ingress = []Ingress{} },
		"an empty bindings slice":       func(f *File) { f.Namespaces[0].Workloads[1].Bindings = []string{} },
		"an empty egress slice":         func(f *File) { f.Namespaces[0].Egress = []Egress{} },
	} {
		t.Run(name, func(t *testing.T) {
			file, err := Parse(fixture(t))
			if err != nil {
				t.Fatal(err)
			}
			empty(file)
			if _, err := file.Encode(); err != nil {
				t.Fatalf("a valid model was refused by its own writer: %v", err)
			}
		})
	}
	// And an explicitly written empty list reads back the same way.
	document := strings.Replace(string(fixture(t)), "            bindings: [cache, store]\n", "            bindings: []\n", 1)
	file, err := Parse([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Encode(); err != nil {
		t.Fatalf("an explicitly empty list was refused by the writer: %v", err)
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
		file, err := Parse(entry.Document)
		if err != nil {
			t.Errorf("fixture %s is accepted by the kit and refused by Parse: %v", entry.Name, err)
			continue
		}
		first, err := file.Encode()
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
