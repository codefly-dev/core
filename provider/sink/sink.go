// Package sink defines the managed writable secret sink contract. The host
// coordinator uses it to capture provider-returned secrets durably without ever
// exposing plaintext to provider code, and to recover captured secrets on the
// host side for its own use. A managed cloud backend implements the same
// contract behind an adapter; the in-process Memory implementation here is the
// conformance reference that host and adapter behavior are tested against.
//
// The contract has four operations:
//
//   - Prepare returns an opaque capture target for one scoped secret slot.
//   - PutDurable makes a captured value durable before it reports success.
//   - Lookup recovers a durable value for authorized host use only.
//   - AbortUnused removes a reservation only while it is still unfilled.
//
// A value that PutDurable has made durable is never rolled back implicitly: a
// later action failing does not undo the capture, and AbortUnused refuses to
// touch a filled reservation. Removing a durable secret is the explicit,
// separately audited Revoke lifecycle.
package sink

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/configuration"
)

// Address scopes one secret slot by consumer, environment, binding, provider,
// and credential purpose. Name distinguishes multiple slots that otherwise
// share the same scope (for example two webhook secrets on one binding).
type Address struct {
	Consumer    string
	Environment string
	Binding     string
	Provider    string
	Purpose     providerv0.CredentialPurpose
	Name        string
}

// Reservation is an opaque capture target bound to one Address. Provider code
// never sees it; the host hands PutDurable the Target once a value is captured.
type Reservation struct {
	address Address
	target  string
	oneTime bool
}

// Target is the opaque capture target. It reveals nothing about the value and
// is safe to carry through host coordination.
func (r Reservation) Target() string { return r.target }

// Address returns the scope the reservation was prepared for.
func (r Reservation) Address() Address { return r.address }

// OneTime reports whether the reserved slot holds a one-time secret that Lookup
// consumes on first authorized recovery.
func (r Reservation) OneTime() bool { return r.oneTime }

// Sink is the managed writable secret sink contract.
type Sink interface {
	// Prepare returns a stable opaque capture target for addr. Preparing the
	// same address again returns the same target so retried preparation does
	// not fork a second reservation.
	Prepare(addr Address, oneTime bool) (Reservation, error)
	// PutDurable makes value durable for the reservation identified by target
	// and returns an opaque reference to it. Re-putting the value already stored
	// for the target is idempotent and returns the same reference; putting a
	// different value rotates to a new version.
	PutDurable(target string, value []byte) (*providerv0.OpaqueReference, error)
	// Lookup recovers the durable value for host use. It never returns a value
	// to provider code. A one-time secret is consumed on the first Lookup.
	Lookup(ref *providerv0.OpaqueReference) ([]byte, error)
	// AbortUnused removes a still-unfilled reservation. It refuses to remove a
	// reservation whose value has already been made durable.
	AbortUnused(target string) error
}

// AuditOp names a sink operation in the value-free audit log.
type AuditOp string

const (
	OpPrepare     AuditOp = "prepare"
	OpPutDurable  AuditOp = "put_durable"
	OpLookup      AuditOp = "lookup"
	OpAbortUnused AuditOp = "abort_unused"
	OpRevoke      AuditOp = "revoke"
)

// AuditEvent records one sink operation. It carries scope, the opaque reference,
// the version, and a non-reversible fingerprint, but never a secret value.
type AuditEvent struct {
	Seq         uint64
	Op          AuditOp
	Consumer    string
	Environment string
	Binding     string
	Provider    string
	Purpose     providerv0.CredentialPurpose
	Name        string
	Reference   string
	Version     uint64
	Fingerprint string
}

// Memory is the in-process reference implementation of Sink. It enforces every
// contract property so host and backend adapters can be tested against it.
type Memory struct {
	mu             sync.Mutex
	fingerprintKey []byte
	reservations   map[string]*reservation
	secrets        map[string]*secret
	versions       map[string]uint64
	events         []AuditEvent
	seq            uint64
}

var _ Sink = (*Memory)(nil)

type reservation struct {
	address Address
	oneTime bool
	filled  bool
}

type secret struct {
	address     Address
	version     uint64
	value       []byte
	fingerprint string
	oneTime     bool
	consumed    bool
	revoked     bool
}

// NewMemory returns an empty reference sink with a fresh fingerprint key.
func NewMemory() (*Memory, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate sink fingerprint key: %w", err)
	}
	return &Memory{
		fingerprintKey: key,
		reservations:   make(map[string]*reservation),
		secrets:        make(map[string]*secret),
		versions:       make(map[string]uint64),
	}, nil
}

func (m *Memory) Prepare(addr Address, oneTime bool) (Reservation, error) {
	if err := addr.validate(); err != nil {
		return Reservation{}, err
	}
	target := addr.target()
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.reservations[target]; ok {
		if existing.oneTime != oneTime {
			return Reservation{}, fmt.Errorf("reservation already prepared with a different one-time setting")
		}
		return Reservation{address: existing.address, target: target, oneTime: existing.oneTime}, nil
	}
	m.reservations[target] = &reservation{address: addr, oneTime: oneTime}
	m.record(OpPrepare, addr, "", 0, "")
	return Reservation{address: addr, target: target, oneTime: oneTime}, nil
}

func (m *Memory) PutDurable(target string, value []byte) (*providerv0.OpaqueReference, error) {
	if len(value) == 0 {
		return nil, fmt.Errorf("durable secret value is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	res, ok := m.reservations[target]
	if !ok {
		return nil, fmt.Errorf("no reservation for capture target")
	}
	addrHash := res.address.hash()
	fingerprint := m.fingerprint(value)
	// A duplicate capture of the latest value is identified by its fingerprint,
	// which survives one-time consumption and revocation (both zero the stored
	// bytes). Matching there returns the existing reference rather than forking a
	// new version, so a retried delivery of an already-consumed or revoked secret
	// is a deterministic no-op instead of resurrecting live material.
	if current := m.versions[addrHash]; current > 0 {
		latest := m.secrets[reference(addrHash, current)]
		if latest != nil && latest.fingerprint == fingerprint {
			res.filled = true
			m.record(OpPutDurable, res.address, latest.reference(addrHash), current, fingerprint)
			return latest.opaque(addrHash), nil
		}
	}
	version := m.versions[addrHash] + 1
	ref := reference(addrHash, version)
	stored := &secret{
		address:     res.address,
		version:     version,
		value:       append([]byte(nil), value...),
		fingerprint: fingerprint,
		oneTime:     res.oneTime,
	}
	m.secrets[ref] = stored
	m.versions[addrHash] = version
	res.filled = true
	m.record(OpPutDurable, res.address, ref, version, fingerprint)
	return stored.opaque(addrHash), nil
}

func (m *Memory) Lookup(ref *providerv0.OpaqueReference) ([]byte, error) {
	if ref == nil || ref.GetReference() == "" {
		return nil, fmt.Errorf("opaque reference is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.secrets[ref.GetReference()]
	if !ok {
		return nil, fmt.Errorf("no durable secret for reference")
	}
	if ref.GetPurpose() != stored.address.Purpose {
		return nil, fmt.Errorf("reference purpose does not match the captured secret purpose")
	}
	if stored.revoked {
		return nil, fmt.Errorf("durable secret has been revoked")
	}
	if stored.oneTime && stored.consumed {
		return nil, fmt.Errorf("one-time secret has already been consumed")
	}
	out := append([]byte(nil), stored.value...)
	m.record(OpLookup, stored.address, ref.GetReference(), stored.version, stored.fingerprint)
	if stored.oneTime {
		stored.consumed = true
		for i := range stored.value {
			stored.value[i] = 0
		}
	}
	return out, nil
}

func (m *Memory) AbortUnused(target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	res, ok := m.reservations[target]
	if !ok {
		return nil
	}
	if res.filled {
		return fmt.Errorf("cannot abort a reservation whose secret is already durable")
	}
	delete(m.reservations, target)
	m.record(OpAbortUnused, res.address, "", 0, "")
	return nil
}

// Revoke removes a durable secret. It is the only path that deletes durable
// material and is audited distinctly from an unused-reservation abort.
func (m *Memory) Revoke(ref *providerv0.OpaqueReference) error {
	if ref == nil || ref.GetReference() == "" {
		return fmt.Errorf("opaque reference is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.secrets[ref.GetReference()]
	if !ok {
		return fmt.Errorf("no durable secret for reference")
	}
	if stored.revoked {
		return nil
	}
	stored.revoked = true
	for i := range stored.value {
		stored.value[i] = 0
	}
	m.record(OpRevoke, stored.address, ref.GetReference(), stored.version, stored.fingerprint)
	return nil
}

// Audit returns a copy of the value-free audit log in operation order.
func (m *Memory) Audit() []AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AuditEvent(nil), m.events...)
}

func (m *Memory) record(op AuditOp, addr Address, ref string, version uint64, fingerprint string) {
	m.seq++
	m.events = append(m.events, AuditEvent{
		Seq:         m.seq,
		Op:          op,
		Consumer:    addr.Consumer,
		Environment: addr.Environment,
		Binding:     addr.Binding,
		Provider:    addr.Provider,
		Purpose:     addr.Purpose,
		Name:        addr.Name,
		Reference:   ref,
		Version:     version,
		Fingerprint: fingerprint,
	})
}

func (s *secret) opaque(addrHash string) *providerv0.OpaqueReference {
	return &providerv0.OpaqueReference{
		Reference:       s.reference(addrHash),
		Purpose:         s.address.Purpose,
		SafeFingerprint: s.fingerprint,
	}
}

func (s *secret) reference(addrHash string) string {
	return reference(addrHash, s.version)
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)

func (a Address) validate() error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"consumer", a.Consumer},
		{"environment", a.Environment},
		{"binding", a.Binding},
		{"provider", a.Provider},
		{"name", a.Name},
	} {
		if !identifierPattern.MatchString(field.value) || configuration.LooksSecret(field.value) {
			return fmt.Errorf("sink address %s is not a valid scope identifier", field.name)
		}
	}
	switch a.Purpose {
	case providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT,
		providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
		providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_BUILD,
		providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_WEBHOOK_VERIFICATION:
		return nil
	default:
		return fmt.Errorf("sink address purpose is required")
	}
}

func (a Address) hash() string {
	key := tupleKey(a.Consumer, a.Environment, a.Binding, a.Provider, strconv.Itoa(int(a.Purpose)), a.Name)
	sum := sha256.Sum256([]byte("codefly.sink.address\x00" + key))
	return hex.EncodeToString(sum[:])
}

func (a Address) target() string {
	return "capture://" + a.hash()
}

func reference(addrHash string, version uint64) string {
	return "secret://" + addrHash + "/v" + strconv.FormatUint(version, 10)
}

// fingerprint is a keyed, non-reversible digest of a secret value. Keying it
// with the sink's instance secret keeps it non-reversible even for low-entropy
// values, while remaining stable within the sink so it is safe for drift
// comparison and duplicate-capture detection.
func (m *Memory) fingerprint(value []byte) string {
	mac := hmac.New(sha256.New, m.fingerprintKey)
	mac.Write(value)
	return "sha256:" + hex.EncodeToString(mac.Sum(nil))
}

func tupleKey(parts ...string) string {
	var key strings.Builder
	for _, part := range parts {
		key.WriteString(strconv.Itoa(len(part)))
		key.WriteByte(':')
		key.WriteString(part)
	}
	return key.String()
}
