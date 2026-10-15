package native

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/workloadidentity"
)

func subject() Subject {
	return Subject{
		Workload:  workloadidentity.Workload{TrustDomain: "cluster.local", Module: "runtime", Service: "worker", Instance: "worker-1"},
		Container: "worker",
	}
}

func issuerAndAttester(t *testing.T) (*Issuer, *Attester) {
	t.Helper()
	issuer, err := NewIssuer()
	if err != nil {
		t.Fatal(err)
	}
	attester, err := NewAttester(issuer.Anchor())
	if err != nil {
		t.Fatal(err)
	}
	return issuer, attester
}

func TestAttestReportsWhatTheLauncherVouchedFor(t *testing.T) {
	issuer, attester := issuerAndAttester(t)
	token, err := issuer.Issue(subject(), "accounts")
	if err != nil {
		t.Fatal(err)
	}
	got, err := attester.Attest(context.Background(), token, "accounts")
	if err != nil {
		t.Fatal(err)
	}
	if got.Mechanism != workloadidentity.Native || got.Workload != subject().Workload || got.Credential.ID == "" ||
		got.Facts == nil || got.Facts.Phase != workloadidentity.PhaseRunning || got.Facts.InstanceUID != got.Credential.ID ||
		got.Facts.Namespace != "" || got.Facts.Account != "" {
		t.Fatalf("attestation = %+v", got)
	}
	if len(got.Facts.Containers) != 1 || got.Facts.Containers[0].Name != "worker" ||
		got.Facts.Containers[0].State != workloadidentity.ContainerRunning || got.Facts.Containers[0].Kind != workloadidentity.ContainerRegular {
		t.Fatalf("containers = %+v", got.Facts.Containers)
	}
	if lifetime := got.Credential.ExpiresAt.Sub(got.Credential.IssuedAt); lifetime != TokenLifetime {
		t.Fatalf("lifetime = %s", lifetime)
	}
}

func TestEachCredentialHasItsOwnID(t *testing.T) {
	issuer, attester := issuerAndAttester(t)
	first, _ := issuer.Issue(subject(), "accounts")
	second, _ := issuer.Issue(subject(), "accounts")
	a, _ := attester.Attest(context.Background(), first, "accounts")
	b, _ := attester.Attest(context.Background(), second, "accounts")
	if a.Credential.ID == b.Credential.ID {
		t.Fatal("two credentials share an id, so a host could not make them single-use")
	}
}

func rejected(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, workloadidentity.ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
}

func TestAnotherRunsCredentialIsRejected(t *testing.T) {
	other, _ := issuerAndAttester(t)
	_, attester := issuerAndAttester(t)
	token, _ := other.Issue(subject(), "accounts")
	_, err := attester.Attest(context.Background(), token, "accounts")
	rejected(t, err)
}

func TestAnotherAudienceIsRejected(t *testing.T) {
	issuer, attester := issuerAndAttester(t)
	token, _ := issuer.Issue(subject(), "delivery")
	_, err := attester.Attest(context.Background(), token, "accounts")
	rejected(t, err)
}

func TestExpiredIsRejected(t *testing.T) {
	issuer, attester := issuerAndAttester(t)
	issuer.now = func() time.Time { return time.Now().Add(-TokenLifetime - time.Second) }
	token, _ := issuer.Issue(subject(), "accounts")
	_, err := attester.Attest(context.Background(), token, "accounts")
	rejected(t, err)
}

func TestFutureIsRejected(t *testing.T) {
	issuer, attester := issuerAndAttester(t)
	issuer.now = func() time.Time { return time.Now().Add(time.Hour) }
	token, _ := issuer.Issue(subject(), "accounts")
	_, err := attester.Attest(context.Background(), token, "accounts")
	rejected(t, err)
}

func TestTamperedPayloadIsRejected(t *testing.T) {
	issuer, attester := issuerAndAttester(t)
	token, _ := issuer.Issue(subject(), "accounts")
	parts := strings.Split(token, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var body map[string]any
	_ = json.Unmarshal(payload, &body)
	body["codefly.dev/native"].(map[string]any)["account"] = "tasks"
	changed, _ := json.Marshal(body)
	parts[1] = base64.RawURLEncoding.EncodeToString(changed)
	_, err := attester.Attest(context.Background(), strings.Join(parts, "."), "accounts")
	rejected(t, err)
}

func TestAClusterTokenShapeIsNotANativeCredential(t *testing.T) {
	issuer, attester := issuerAndAttester(t)
	token, _ := issuer.Issue(subject(), "accounts")
	parts := strings.Split(token, ".")
	parts[0] = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`))
	_, err := attester.Attest(context.Background(), strings.Join(parts, "."), "accounts")
	rejected(t, err)
}

func TestAnIncompleteSubjectIsNeverIssued(t *testing.T) {
	issuer, _ := issuerAndAttester(t)
	incomplete := subject()
	incomplete.Workload.Instance = ""
	if _, err := issuer.Issue(incomplete, "accounts"); err == nil {
		t.Fatal("issued a credential naming no instance")
	}
}

func TestACarrierIdentityAttestsNoContainer(t *testing.T) {
	issuer, attester := issuerAndAttester(t)
	carrier := subject()
	carrier.Container = ""
	token, _ := issuer.Issue(carrier, "accounts")
	got, err := attester.Attest(context.Background(), token, "accounts")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Facts.Containers) != 0 {
		t.Fatalf("a carrier attested containers %+v", got.Facts.Containers)
	}
}

func TestAnchorRoundTripsThroughItsFile(t *testing.T) {
	issuer, _ := issuerAndAttester(t)
	path := filepath.Join(t.TempDir(), "anchor.json")
	if err := issuer.Anchor().WriteFile(path); err != nil {
		t.Fatal(err)
	}
	anchor, err := ReadAnchor(path)
	if err != nil {
		t.Fatal(err)
	}
	if anchor != issuer.Anchor() || len(anchor.Fingerprint()) != 64 {
		t.Fatalf("anchor = %+v", anchor)
	}
}

func TestLoadAttesterRefusesInAPod(t *testing.T) {
	issuer, _ := issuerAndAttester(t)
	path := filepath.Join(t.TempDir(), "anchor.json")
	_ = issuer.Anchor().WriteFile(path)
	t.Setenv(AnchorEnvironment, path)
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	if _, err := LoadAttester(); !errors.Is(err, ErrInPod) {
		t.Fatalf("want ErrInPod, got %v", err)
	}
}

func TestLoadAttesterNamesNothingWhenUnset(t *testing.T) {
	t.Setenv(AnchorEnvironment, "")
	attester, err := LoadAttester()
	if attester != nil || err != nil {
		t.Fatalf("got %v, %v", attester, err)
	}
}

func TestLoadAttesterRefusesARelativeAnchor(t *testing.T) {
	t.Setenv(AnchorEnvironment, "anchor.json")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if _, err := LoadAttester(); err == nil {
		t.Fatal("accepted a relative anchor path")
	}
}
