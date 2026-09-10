package wool

import (
	"context"
	"net/http"
	"testing"
)

// The generated REST ingress installs MetadataFromRequest as its grpc-gateway
// annotator: whatever it does not carry, the gRPC handler never sees.
func TestMetadataFromRequestCarriesWorkContextVerbatim(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://module/v1/documents/collection", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(WorkContextHeader, "wc-token")
	req.Header.Set("X-User-Id", "u-1")

	md := MetadataFromRequest(context.Background(), req)

	// The sdk reads the Work Context under the header's own (lowercased) name —
	// not a rewritten context key — so that is the key it must land under.
	if got := md.Get("x-codefly-work-context"); len(got) != 1 || got[0] != "wc-token" {
		t.Fatalf("work context metadata: got %v, want [wc-token]", got)
	}
	// The identity mappings keep their context keys.
	if got := md.Get(string(UserIDKey)); len(got) != 1 || got[0] != "u-1" {
		t.Fatalf("user id metadata: got %v, want [u-1]", got)
	}
}

func TestMetadataFromRequestWithoutWorkContextSetsNoKey(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://module/v1/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	md := MetadataFromRequest(context.Background(), req)
	if got := md.Get("x-codefly-work-context"); len(got) != 0 {
		t.Fatalf("expected no work context metadata, got %v", got)
	}
}
