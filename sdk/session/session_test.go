package session

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestNewMintsDistinctIdentityPerInvocation(t *testing.T) {
	seen := map[string]bool{}
	for range 64 {
		invocation, err := New()
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if seen[invocation.ID] || seen[invocation.Secret] {
			t.Fatal("session identity repeated across invocations")
		}
		seen[invocation.ID] = true
		seen[invocation.Secret] = true
		if scope := invocation.Scope(); len(scope) != 13 || strings.ContainsAny(scope, "_./ ") {
			t.Fatalf("scope = %q, want a short backend-safe label", scope)
		}
	}
}

func TestProofRejectsForeignSecretAndReplayedChallenge(t *testing.T) {
	owner, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	foreign, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	foreign.ID = owner.ID

	challenge, err := Challenge()
	if err != nil {
		t.Fatalf("Challenge() error = %v", err)
	}
	if !owner.VerifyProof(challenge, owner.Proof(challenge)) {
		t.Fatal("owner proof was rejected")
	}
	if owner.VerifyProof(challenge, foreign.Proof(challenge)) {
		t.Fatal("a server that does not hold the session secret produced an accepted proof")
	}

	other, err := Challenge()
	if err != nil {
		t.Fatalf("Challenge() error = %v", err)
	}
	if owner.VerifyProof(other, owner.Proof(challenge)) {
		t.Fatal("a proof for one challenge was accepted for another")
	}
}

func TestInjectOwnsControlEnvironmentAndDropsLegacyPort(t *testing.T) {
	invocation := &Session{ID: "abc", Secret: "shh"}
	got := invocation.Inject([]string{
		"PATH=/usr/bin",
		PortEnvironment + "=25870",
		IDEnvironment + "=stale",
		SecretEnvironment + "=stale",
		SocketEnvironment + "=/stale.sock",
	}, "/tmp/private/c.sock")

	want := []string{
		"PATH=/usr/bin",
		IDEnvironment + "=abc",
		SecretEnvironment + "=shh",
		SocketEnvironment + "=/tmp/private/c.sock",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("child environment = %v, want %v", got, want)
	}
	if FromEnvironment(got).ID != "abc" {
		t.Fatal("the child cannot recover the session it was started with")
	}
	if FromEnvironment([]string{"PATH=/usr/bin"}) != nil {
		t.Fatal("a process outside an isolated session reported one")
	}
}

func TestNewControlOwnsAPrivateDirectoryPerInvocation(t *testing.T) {
	first, err := NewControl()
	if err != nil {
		t.Fatalf("NewControl() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Remove() })
	second, err := NewControl()
	if err != nil {
		t.Fatalf("NewControl() error = %v", err)
	}
	t.Cleanup(func() { _ = second.Remove() })

	if first.Socket == second.Socket {
		t.Fatalf("two invocations selected the same control socket %s", first.Socket)
	}
	info, err := os.Stat(first.Directory)
	if err != nil {
		t.Fatalf("stat control directory: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("control directory mode = %o, want 700", perm)
	}
	if got := first.Target(); got != "unix:"+first.Socket {
		t.Fatalf("dial target = %q", got)
	}
	if err := first.Remove(); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Stat(first.Directory); !os.IsNotExist(err) {
		t.Fatalf("control directory survived Remove: %v", err)
	}
}

func TestOpenControlIsStablePerKeyAndCarriesAReceipt(t *testing.T) {
	control, err := OpenControl("fingerprintkey01")
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	t.Cleanup(func() { _ = control.Remove() })

	again, err := OpenControl("fingerprintkey01")
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	if again.Socket != control.Socket {
		t.Fatalf("reusable control socket drifted: %s vs %s", control.Socket, again.Socket)
	}
	other, err := OpenControl("fingerprintkey02")
	if err != nil {
		t.Fatalf("OpenControl() error = %v", err)
	}
	t.Cleanup(func() { _ = other.Remove() })
	if other.Socket == control.Socket {
		t.Fatal("different reuse fingerprints shared one control socket")
	}

	if receipt, err := control.ReadReceipt(); err != nil || receipt != nil {
		t.Fatalf("ReadReceipt() = %v, %v; want no receipt yet", receipt, err)
	}
	want := Receipt{ID: "id", Secret: "secret", Fingerprint: "fingerprint"}
	if err := control.WriteReceipt(want); err != nil {
		t.Fatalf("WriteReceipt() error = %v", err)
	}
	got, err := again.ReadReceipt()
	if err != nil {
		t.Fatalf("ReadReceipt() error = %v", err)
	}
	if got == nil || *got != want {
		t.Fatalf("receipt = %v, want %v", got, want)
	}
	info, err := os.Stat(filepath.Join(control.Directory, "receipt.json"))
	if err != nil {
		t.Fatalf("stat receipt: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("receipt mode = %o, want 600", perm)
	}
}
