package sessionledger

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// SchemaV1 is the only session-ledger record schema this package reads or
// writes. It is stored in every record and in the on-disk directory name, so a
// future contract can be introduced beside this one without either release
// parsing the other's records.
const SchemaV1 = "codefly.session-ledger/v1"

// ErrInvalid is returned for a malformed or internally inconsistent record.
var ErrInvalid = errors.New("invalid Codefly session-ledger record")

// Ownership says whether this invocation brought a resource into existence.
type Ownership string

const (
	// Created means this invocation created the resource and may dispose of it.
	Created Ownership = "created"
	// Borrowed means the resource was already there — an outer session's
	// stack, a developer's own container. It is never stopped or deleted.
	Borrowed Ownership = "borrowed"
)

// Disposition is the last durably recorded state of one resource.
type Disposition string

const (
	// Declared is written before the resource is created. A record left in
	// this state after a crash names something that may or may not exist; the
	// backend decides.
	Declared Disposition = "declared"
	// Running means the resource was created and confirmed live.
	Running Disposition = "running"
	// Stopped means execution ended; any data the resource holds survives.
	Stopped Disposition = "stopped"
	// Retained means the resource was deliberately left as it was.
	Retained Disposition = "retained"
	// Deleted means the resource no longer exists.
	Deleted Disposition = "deleted"
)

// Mode says whether a session's resources outlive its process.
type Mode string

const (
	// ModeDisposable sessions are cleaned up when they end or when their owner
	// is found dead. They are never reattached to.
	ModeDisposable Mode = "disposable"
	// ModeReusable sessions stay warm after release and are reattached to by a
	// later invocation presenting the same fingerprint.
	ModeReusable Mode = "reusable"
)

var (
	keyPattern         = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	invocationPattern  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	kindPattern        = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?:\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*)+$`)
	backendPattern     = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	resourceIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,511}$`)
	digestPattern      = regexp.MustCompile(`^[0-9a-f]{1,128}$`)
	executablePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
	bootIDPattern      = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
)

// Witness is bounded, validated evidence that a backing resource is still the
// same instance the ledger recorded. It is what makes a stale record safe: a
// recycled PID has a different start id, a recreated container a different
// creation instant, so neither is mistaken for the recorded one.
//
// Every field is a machine identity. None of them carries user text, so a
// witness cannot smuggle a connection string or a token into the ledger.
type Witness struct {
	// PID is the process id, meaningful only together with BootID and StartID.
	PID int `json:"pid,omitempty"`
	// BootID identifies the boot the PID belongs to.
	BootID string `json:"boot_id,omitempty"`
	// StartID is the kernel-reported process start instant.
	StartID uint64 `json:"start_id,omitempty"`
	// Executable is the leaf name of the running binary, never a full path.
	Executable string `json:"executable,omitempty"`
	// CreatedAt is a backend-reported creation instant, such as a container's.
	CreatedAt *time.Time `json:"created_at,omitempty"`
	// Digest is an adapter-chosen hex witness for backends whose identity is
	// neither a process nor a creation instant.
	Digest string `json:"digest,omitempty"`
}

// Zero reports whether the witness carries no evidence at all.
func (witness Witness) Zero() bool {
	return witness == Witness{} || (witness.PID == 0 && witness.BootID == "" && witness.StartID == 0 &&
		witness.Executable == "" && witness.CreatedAt == nil && witness.Digest == "")
}

// Matches reports whether observed is evidence of the same instance as this
// witness. Only fields this witness actually recorded are compared: a backend
// that observes more than it recorded does not thereby fail the match.
func (witness Witness) Matches(observed Witness) bool {
	if witness.Zero() {
		return false
	}
	if witness.PID != 0 && witness.PID != observed.PID {
		return false
	}
	if witness.BootID != "" && witness.BootID != observed.BootID {
		return false
	}
	if witness.StartID != 0 && witness.StartID != observed.StartID {
		return false
	}
	if witness.Executable != "" && witness.Executable != observed.Executable {
		return false
	}
	if witness.CreatedAt != nil &&
		(observed.CreatedAt == nil || !witness.CreatedAt.Equal(*observed.CreatedAt)) {
		return false
	}
	if witness.Digest != "" && witness.Digest != observed.Digest {
		return false
	}
	return true
}

func (witness Witness) validate() error {
	if witness.BootID != "" && !bootIDPattern.MatchString(witness.BootID) {
		return fmt.Errorf("%w: witness boot id is not a bounded identity", ErrInvalid)
	}
	if witness.Executable != "" && !executablePattern.MatchString(witness.Executable) {
		return fmt.Errorf("%w: witness executable must be a bare binary name", ErrInvalid)
	}
	if witness.Digest != "" && !digestPattern.MatchString(witness.Digest) {
		return fmt.Errorf("%w: witness digest must be lowercase hex", ErrInvalid)
	}
	if witness.PID < 0 {
		return fmt.Errorf("%w: witness pid is negative", ErrInvalid)
	}
	return nil
}

// Result is what a cleanup attempt actually achieved.
type Result string

const (
	// Succeeded means the backend performed the action.
	Succeeded Result = "succeeded"
	// NotFound means the resource was already gone; nothing was done.
	NotFound Result = "not-found"
	// Preserved means the resource exists but was deliberately left alone —
	// borrowed, retained by policy, or not provably owned by this invocation.
	Preserved Result = "preserved"
	// Refused means no adapter could speak for the resource's backend, so
	// authorization to touch it could not be established.
	Refused Result = "refused"
	// Failed means the backend was asked and returned an error.
	Failed Result = "failed"
)

// Outcome is the idempotent receipt of one cleanup attempt.
type Outcome struct {
	// Action is what the lifecycle policy asked for.
	Action Action `json:"action"`
	// Result is what happened.
	Result Result `json:"result"`
	// Reason is a bounded stable reason class, never an error message.
	Reason string `json:"reason,omitempty"`
	// At is when the attempt completed.
	At time.Time `json:"at"`
}

// Resource is one backend object a session touched.
type Resource struct {
	// Kind is a namespaced resource kind such as "process.group".
	Kind string `json:"kind"`
	// Backend names the adapter that can speak for this resource.
	Backend string `json:"backend"`
	// ID is the backend's own opaque identifier — a pgid, a container name.
	// The pattern it must match is deliberately narrower than any URL or
	// connection string, so an identifier cannot smuggle a credential.
	ID string `json:"id"`
	// Ownership decides whether this session may dispose of the resource.
	Ownership Ownership `json:"ownership"`
	// Disposition is the last durably recorded state.
	Disposition Disposition `json:"disposition"`
	// Data marks a resource whose contents must survive a stop.
	Data bool `json:"data,omitempty"`
	// Witness proves the backing resource is still the recorded instance.
	Witness Witness `json:"witness,omitzero"`
	// Outcome is the receipt of the last cleanup attempt.
	Outcome *Outcome `json:"outcome,omitempty"`
}

// Ref identifies a resource within its session.
type Ref struct {
	Backend string
	ID      string
}

// Ref returns this resource's identity within its session.
func (resource Resource) Ref() Ref {
	return Ref{Backend: resource.Backend, ID: resource.ID}
}

// Terminal reports whether no cleanup could change this resource any further.
// Only deletion is that final: retained data is still there and an explicit
// reset can still remove it, and a stopped resource can still be deleted.
func (resource Resource) Terminal() bool {
	return resource.Disposition == Deleted
}

// Forgettable reports whether dropping the ledger's memory of this resource
// loses track of nothing that is still on the machine. Retained data is
// deliberately still there, so a record naming it is kept however old it is —
// forgetting it is exactly how state becomes unattributable garbage.
func (resource Resource) Forgettable() bool {
	if resource.Ownership == Borrowed {
		return true
	}
	if resource.Disposition == Deleted {
		return true
	}
	return resource.Disposition == Stopped && !resource.Data
}

func (resource Resource) validate() error {
	if !kindPattern.MatchString(resource.Kind) {
		return fmt.Errorf("%w: resource kind must be a canonical namespaced value", ErrInvalid)
	}
	if !backendPattern.MatchString(resource.Backend) {
		return fmt.Errorf("%w: resource backend must be a canonical slug", ErrInvalid)
	}
	if !resourceIDPattern.MatchString(resource.ID) {
		return fmt.Errorf("%w: resource id must be a bounded backend identifier", ErrInvalid)
	}
	switch resource.Ownership {
	case Created, Borrowed:
	default:
		return fmt.Errorf("%w: resource ownership must be created or borrowed", ErrInvalid)
	}
	switch resource.Disposition {
	case Declared, Running, Stopped, Retained, Deleted:
	default:
		return fmt.Errorf("%w: resource disposition %q is not defined", ErrInvalid, resource.Disposition)
	}
	return resource.Witness.validate()
}

// Lease records which live process is currently attached to a session. A nil
// lease means the session is released: warm and reattachable when reusable,
// abandoned and recoverable when not.
type Lease struct {
	// Holder is the attached process.
	Holder Witness `json:"holder"`
	// AcquiredAt is when it attached.
	AcquiredAt time.Time `json:"acquired_at"`
}

// Session is the durable record of one invocation's resource ownership.
type Session struct {
	// Schema is fixed to SchemaV1.
	Schema string `json:"schema"`
	// InvocationID is this invocation's unforgeable identity. Backends bind
	// resources to it so ownership can be proven from the backend's own state.
	InvocationID string `json:"invocation_id"`
	// Key is the reuse scope: sessions sharing a key compete for the same
	// warm state.
	Key string `json:"key"`
	// Mode says whether the session's resources outlive its process.
	Mode Mode `json:"mode"`
	// Fingerprint is the semantic plan fingerprint that warm reuse must match.
	// It deliberately excludes invocation identity, so two runs of the same
	// plan can reattach to each other's state.
	Fingerprint string `json:"fingerprint"`
	// Owner is the process that created the session.
	Owner Witness `json:"owner"`
	// Lease is the currently attached process, if any.
	Lease *Lease `json:"lease,omitempty"`
	// CreatedAt is when the session was first written.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the record last changed.
	UpdatedAt time.Time `json:"updated_at"`
	// Resources is every backend object this session touched.
	Resources []Resource `json:"resources"`
}

func (session *Session) validate() error {
	if session == nil {
		return fmt.Errorf("%w: session is required", ErrInvalid)
	}
	if session.Schema != SchemaV1 {
		return fmt.Errorf("%w: schema must be %s", ErrInvalid, SchemaV1)
	}
	if !invocationPattern.MatchString(session.InvocationID) {
		return fmt.Errorf("%w: invocation id must be 32 lowercase hex characters", ErrInvalid)
	}
	if !keyPattern.MatchString(session.Key) {
		return fmt.Errorf("%w: key must be a bounded lowercase slug", ErrInvalid)
	}
	switch session.Mode {
	case ModeDisposable, ModeReusable:
	default:
		return fmt.Errorf("%w: mode must be disposable or reusable", ErrInvalid)
	}
	if !fingerprintPattern.MatchString(session.Fingerprint) {
		return fmt.Errorf("%w: fingerprint must be a sha256 hex digest", ErrInvalid)
	}
	if err := session.Owner.validate(); err != nil {
		return err
	}
	if session.Owner.Zero() {
		return fmt.Errorf("%w: owner identity is required", ErrInvalid)
	}
	if session.Lease != nil {
		if err := session.Lease.Holder.validate(); err != nil {
			return err
		}
		if session.Lease.Holder.Zero() {
			return fmt.Errorf("%w: lease holder identity is required", ErrInvalid)
		}
	}
	if session.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", ErrInvalid)
	}
	if session.UpdatedAt.Before(session.CreatedAt) {
		return fmt.Errorf("%w: updated_at precedes created_at", ErrInvalid)
	}
	seen := make(map[Ref]struct{}, len(session.Resources))
	for _, resource := range session.Resources {
		if err := resource.validate(); err != nil {
			return err
		}
		if _, duplicate := seen[resource.Ref()]; duplicate {
			return fmt.Errorf("%w: duplicate resource %s/%s", ErrInvalid, resource.Backend, resource.ID)
		}
		seen[resource.Ref()] = struct{}{}
	}
	return nil
}

// Resource returns the recorded resource for ref.
func (session *Session) Resource(ref Ref) (Resource, bool) {
	for _, resource := range session.Resources {
		if resource.Ref() == ref {
			return resource, true
		}
	}
	return Resource{}, false
}
