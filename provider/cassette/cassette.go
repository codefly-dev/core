// Package cassette records and replays broker responses deterministically.
//
// Recording is explicit opt-in; replay is the default and never falls back to
// the live network. Both paths run the identical broker admission, credential,
// and checkpoint checks — the cassette only substitutes for the network round
// trip, returning the already-filtered safe response that recording captured.
// A cassette never persists raw request or response bytes: it stores only the
// filtered safe response, keyed by a match digest that normalizes the
// host-injected idempotency key, request id, and timestamps without weakening
// the action, resource, origin, or response-policy binding. Recording fails
// closed if any secret-shaped value survives into what would be stored.
package cassette

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/configuration"
	"google.golang.org/protobuf/proto"
)

// Mode selects record or replay. The zero value is replay, so a misconfigured
// cassette never accidentally reaches the network.
type Mode int

const (
	// ModeReplay returns recorded responses and never dials the network.
	ModeReplay Mode = iota
	// ModeRecord stores live filtered responses.
	ModeRecord
)

// Entry is one recorded exchange. It holds only safe, filtered material.
type Entry struct {
	Sequence        int               `json:"sequence"`
	Method          string            `json:"method"`
	MatchDigest     string            `json:"match_digest"`
	PolicyDigest    string            `json:"policy_digest"`
	ProviderVersion string            `json:"provider_version"`
	Status          uint32            `json:"status"`
	SafeHeaders     map[string]string `json:"safe_headers,omitempty"`
	Response        json.RawMessage   `json:"response"`
}

// Cassette is a deterministic record/replay store.
type Cassette struct {
	mode            Mode
	providerVersion string
	mu              sync.Mutex
	entries         []*Entry
	replayCursor    map[string]int
}

// New builds a cassette in the given mode bound to a concrete provider version.
func New(mode Mode, providerVersion string) *Cassette {
	return &Cassette{mode: mode, providerVersion: providerVersion, replayCursor: map[string]int{}}
}

// Mode reports the cassette mode.
func (c *Cassette) Mode() Mode { return c.mode }

// Key identifies one logical request independent of host-injected volatile
// fields. Method, descriptor, path, query, body, origin, and response policy
// are bound; the idempotency key, request id, and timestamps are not.
type Key struct {
	planned *providerv0.PlannedRequest
	origin  *providerv0.AdmittedOrigin
}

// NewKey builds a match key from the admitted planned request and origin.
func NewKey(planned *providerv0.PlannedRequest, origin *providerv0.AdmittedOrigin) Key {
	return Key{planned: planned, origin: origin}
}

func (k Key) digests() (match string, policy string, method string, err error) {
	if k.planned == nil || k.origin == nil {
		return "", "", "", fmt.Errorf("cassette key requires a planned request and origin")
	}
	normalized := proto.Clone(k.planned).(*providerv0.PlannedRequest)
	// Normalize host-injected volatile fields; keep action/resource/origin. The
	// response policy is compared separately so a policy drift is a distinct,
	// explicit mismatch rather than a silent non-match.
	normalized.IdempotencyKey = ""
	normalized.RequestDigest = ""
	normalized.ResponsePolicyDigest = ""
	origin := proto.Clone(k.origin).(*providerv0.AdmittedOrigin)
	origin.AdmissionDigest = ""

	plannedBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(normalized)
	if err != nil {
		return "", "", "", err
	}
	originBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(origin)
	if err != nil {
		return "", "", "", err
	}
	sum := sha256.Sum256(append(plannedBytes, originBytes...))
	return "sha256:" + hex.EncodeToString(sum[:]), k.planned.GetResponsePolicyDigest(), k.planned.GetMethod().String(), nil
}

// Record stores a live filtered response. It fails closed if any secret-shaped
// value survived filtering, so a cassette can never become an exfiltration
// surface.
func (c *Cassette) Record(key Key, status uint32, safeHeaders map[string]string, response *providerv0.ExecuteRequestResponse) error {
	if c.mode != ModeRecord {
		return fmt.Errorf("cassette is not in record mode")
	}
	match, policy, method, err := key.digests()
	if err != nil {
		return err
	}
	if err := assertSafe(response); err != nil {
		return err
	}
	for name, value := range safeHeaders {
		if configuration.LooksSecret(name) || configuration.LooksSecret(value) {
			return fmt.Errorf("cassette header %q is secret-shaped", name)
		}
	}
	encoded, err := marshalResponse(response)
	if err != nil {
		return err
	}
	if configuration.LooksSecret(string(encoded)) {
		return fmt.Errorf("recorded response carries a secret-shaped literal")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, &Entry{
		Sequence:        len(c.entries),
		Method:          method,
		MatchDigest:     match,
		PolicyDigest:    policy,
		ProviderVersion: c.providerVersion,
		Status:          status,
		SafeHeaders:     safeHeaders,
		Response:        encoded,
	})
	return nil
}

// Replay returns the next recorded response for the key. It never falls back to
// the network: an unknown key or a response-policy mismatch is a hard error.
func (c *Cassette) Replay(key Key) (uint32, *providerv0.ExecuteRequestResponse, error) {
	if c.mode != ModeReplay {
		return 0, nil, fmt.Errorf("cassette is not in replay mode")
	}
	match, policy, _, err := key.digests()
	if err != nil {
		return 0, nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cursor := c.replayCursor[match]
	var found *Entry
	seen := 0
	for _, entry := range c.entries {
		if entry.MatchDigest != match {
			continue
		}
		if seen == cursor {
			found = entry
			break
		}
		seen++
	}
	if found == nil {
		return 0, nil, fmt.Errorf("no recorded response for request; replay does not fall back to live")
	}
	if found.PolicyDigest != policy {
		return 0, nil, fmt.Errorf("recorded response policy digest does not match current policy")
	}
	if found.ProviderVersion != c.providerVersion {
		return 0, nil, fmt.Errorf("recorded provider version does not match current provider")
	}
	c.replayCursor[match] = cursor + 1
	response, err := unmarshalResponse(found.Response)
	if err != nil {
		return 0, nil, err
	}
	return found.Status, response, nil
}

// Marshal serializes the cassette deterministically for a byte-stable on-disk
// form and reviewable diffs.
func (c *Cassette) Marshal() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := append([]*Entry(nil), c.entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Sequence < entries[j].Sequence })
	return json.MarshalIndent(entries, "", "  ")
}

// Load restores recorded entries for replay.
func Load(data []byte, providerVersion string) (*Cassette, error) {
	var entries []*Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("load cassette: %w", err)
	}
	return &Cassette{mode: ModeReplay, providerVersion: providerVersion, entries: entries, replayCursor: map[string]int{}}, nil
}

func marshalResponse(response *providerv0.ExecuteRequestResponse) (json.RawMessage, error) {
	clone := proto.Clone(response).(*providerv0.ExecuteRequestResponse)
	// Volatile host-injected fields are excluded from the stored form so a
	// re-record is byte-identical.
	clone.RequestId = ""
	clone.ResponseReceivedAt = nil
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(clone)
	if err != nil {
		return nil, err
	}
	return json.Marshal(data)
}

func unmarshalResponse(raw json.RawMessage) (*providerv0.ExecuteRequestResponse, error) {
	var data []byte
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	response := &providerv0.ExecuteRequestResponse{}
	if err := proto.Unmarshal(data, response); err != nil {
		return nil, err
	}
	return response, nil
}
