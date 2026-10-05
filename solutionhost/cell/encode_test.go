package cell

import (
	"errors"
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
