package wool

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// A realistic signed Work Context: base64url segments carrying '.', '-', '_'
// and '=' padding. A placeholder like "wc-token" survives any accidental
// normalization on the way through; this value would not.
const testWorkContext = "eyJ0eXAiOiJjb2RlZmx5LndvcmstY29udGV4dC92MSJ9.dGFzay1pZA==.c2ln-bmF0_dXJl"

func newRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://module/v1/documents/collection", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// The generated REST ingress installs MetadataFromRequest as its grpc-gateway
// annotator: whatever it does not carry, the gRPC handler never sees.
func TestMetadataFromRequestCarriesWorkContextVerbatim(t *testing.T) {
	req := newRequest(t)
	req.Header.Set(WorkContextHeader, testWorkContext)
	req.Header.Set("X-User-Id", "u-1")

	md := MetadataFromRequest(context.Background(), req)

	// The sdk reads the Work Context under the header's own (lowercased) name —
	// not a rewritten context key — so that is the key it must land under.
	if got := md.Get("x-codefly-work-context"); len(got) != 1 || got[0] != testWorkContext {
		t.Fatalf("work context metadata: got %v, want [%s]", got, testWorkContext)
	}
	// The identity mappings keep their context keys.
	if got := md.Get(string(UserIDKey)); len(got) != 1 || got[0] != "u-1" {
		t.Fatalf("user id metadata: got %v, want [u-1]", got)
	}
}

// The metadata key must stay *derived* from WorkContextHeader. Restating the
// lowercased name as its own constant is what lets a rename drop the header
// again exactly the way #435 did: silently, with no compile error. This fails
// if the two ever stop agreeing.
func TestWorkContextMetadataKeyIsDerivedFromHeaderName(t *testing.T) {
	req := newRequest(t)
	req.Header.Set(WorkContextHeader, testWorkContext)

	md := MetadataFromRequest(context.Background(), req)

	key := strings.ToLower(WorkContextHeader)
	if got := md.Get(key); len(got) != 1 || got[0] != testWorkContext {
		t.Fatalf("metadata key %q is not derived from header %q: md=%v", key, WorkContextHeader, md)
	}
}

func TestMetadataFromRequestWithoutWorkContextSetsNoKey(t *testing.T) {
	req := newRequest(t)

	md := MetadataFromRequest(context.Background(), req)

	if got := md.Get("x-codefly-work-context"); len(got) != 0 {
		t.Fatalf("expected no work context metadata, got %v", got)
	}
}

// A Work Context is one capability bound to one audience. A repeated header is
// ambiguous — which capability authorizes the call would depend on the index
// the verifier happens to read — so it must fail closed rather than resolve to
// whichever value the caller managed to place first.
func TestMetadataFromRequestDropsAmbiguousWorkContext(t *testing.T) {
	req := newRequest(t)
	req.Header.Add(WorkContextHeader, "caller-supplied")
	req.Header.Add(WorkContextHeader, testWorkContext)

	md := MetadataFromRequest(context.Background(), req)

	if got := md.Get("x-codefly-work-context"); len(got) != 0 {
		t.Fatalf("ambiguous work context must not be forwarded, got %v", got)
	}
}

// An empty header value is not a credential. Forwarding it would hand the
// verifier a "present but unverifiable" capability instead of no capability.
func TestMetadataFromRequestDropsEmptyWorkContext(t *testing.T) {
	req := newRequest(t)
	req.Header.Set(WorkContextHeader, "")

	md := MetadataFromRequest(context.Background(), req)

	if got := md.Get("x-codefly-work-context"); len(got) != 0 {
		t.Fatalf("empty work context must not be forwarded, got %v", got)
	}
}

// These are opaque carriers, not inferred authority: the bridge preserves
// spelling/bytes, while the receiving SDK checks them against the signed seal.
func TestMetadataFromRequestCarriesInstallationVerbatim(t *testing.T) {
	req := newRequest(t)
	req.Header.Set(WorkContextHeader, testWorkContext)
	req.Header.Set("X-Codefly-Installation-Id", "installation-A")
	req.Header.Set("X-Codefly-Installation-Revision", "0007")
	md := MetadataFromRequest(context.Background(), req)
	for key, want := range map[string]string{
		"x-codefly-installation-id":       "installation-A",
		"x-codefly-installation-revision": "0007",
	} {
		if got := md.Get(key); len(got) != 1 || got[0] != want {
			t.Fatalf("%s = %v, want [%s]", key, got, want)
		}
	}
}

func TestMetadataFromRequestDoesNotRepairInstallationCarriers(t *testing.T) {
	for _, header := range []string{"X-Codefly-Installation-Id", "X-Codefly-Installation-Revision"} {
		for _, values := range [][]string{nil, {""}, {"one", "two"}, {"same", "same"}} {
			t.Run(header+strings.Join(values, "/"), func(t *testing.T) {
				req := newRequest(t)
				req.Header.Set(WorkContextHeader, testWorkContext)
				for _, value := range values {
					req.Header.Add(header, value)
				}
				req.Header.Set("Grpc-Metadata-"+header, "alternative")
				md := MetadataFromRequest(context.Background(), req)
				if got := md.Get(strings.ToLower(header)); !slices.Equal(got, values) {
					t.Fatalf("carrier repaired: got %v, want %v", got, values)
				}
			})
		}
	}
}
