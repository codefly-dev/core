// Package workloadidentity is how a host learns which declared workload is
// calling it, whatever proved it.
//
// A workload presents a credential: a token it reads from a file it was handed
// and re-reads before every use, so rotation needs nothing from it. A host
// hands the credential to an Attester, which answers with one Attestation:
// the account the credential authenticates, the instance it was issued to,
// its lifetime, and the containers that instance runs. Policy — which
// principal that account belongs to, how long a credential may live — stays
// with the host. This package only establishes facts.
//
// There is one Attester per mechanism, and the mechanism is the environment's,
// never the caller's:
//
//   - kubernetes: a projected ServiceAccount token, reviewed by the cluster's
//     API server (TokenReview), and the pod it names, read from that server.
//     Implemented where the cluster client lives.
//   - native: a token the CLI signs for a process it started on a laptop,
//     verified against the run's anchor (package native). Nothing imitates a
//     cluster: there is no API server, pod or ServiceAccount, only the facts
//     the process's launcher can vouch for.
//
// Both answer the same Attestation, so the host's checks run unchanged under
// either, and a gap in one mechanism cannot hide behind the other's shape.
package workloadidentity

import (
	"context"
	"errors"
	"time"
)

// Mechanism names how a credential is proven.
type Mechanism string

const (
	// Kubernetes is a projected ServiceAccount token reviewed by a cluster.
	Kubernetes Mechanism = "kubernetes"
	// Native is a token the CLI signed for a process it started.
	Native Mechanism = "native"
)

// ErrRejected reports that the credential was examined and refused: another
// audience, another issuer, expired, malformed, or not signed by the anchor.
// It is a verdict about the caller, so a caller hearing it must not retry with
// the same credential.
var ErrRejected = errors.New("workload credential rejected")

// ErrUnavailable reports that the attestation could not be established — the
// API server unreachable, the instance not yet running. Nothing has been
// decided about the caller.
var ErrUnavailable = errors.New("workload attestation unavailable")

// Attester establishes an Attestation from a presented credential.
type Attester interface {
	// Mechanism is how this attester proves a credential.
	Mechanism() Mechanism
	// Authenticate verifies credential for audience and reports the account
	// and instance it was issued to, without establishing what that instance
	// runs: Phase and Containers are left empty. It is for a caller that only
	// needs to know who signed, such as a delivery carrier.
	Authenticate(ctx context.Context, credential, audience string) (*Attestation, error)
	// Attest verifies credential for audience and reports what it proves,
	// including the instance's live phase and containers. A refusal wraps
	// ErrRejected; an inability to decide wraps ErrUnavailable.
	Attest(ctx context.Context, credential, audience string) (*Attestation, error)
}

// Attestation is what a credential proves about the workload presenting it.
type Attestation struct {
	Mechanism Mechanism

	// Namespace and Account are the workload's account: in a cell its
	// ServiceAccount, natively the account its declaration names. A module
	// manifest workload's SPIFFE ID names the same pair.
	Namespace string
	Account   string

	// Instance and InstanceUID name the one running instance the credential
	// was issued to: the pod and its UID in a cell, the process the CLI
	// started natively. A replacement at the same name has another UID.
	Instance    string
	InstanceUID string

	// CredentialID is the credential's own identifier (its JTI), which a host
	// records to make each credential single-use.
	CredentialID string

	IssuedAt  time.Time
	ExpiresAt time.Time

	// Phase is the instance's lifecycle phase; PhaseRunning is the one that
	// may mint. Terminating reports an instance being deleted.
	Phase       string
	Terminating bool

	// Containers are what the instance runs. A host selects the one container
	// the workload declares as authenticating and refuses any other it does
	// not cover. Natively there is exactly one: the process.
	Containers []Container
}

// PhaseRunning is the phase in which an instance may be issued authority.
const PhaseRunning = "Running"

// ContainerKind is how a container participates in the instance.
type ContainerKind string

const (
	ContainerRegular   ContainerKind = "regular"
	ContainerInit      ContainerKind = "init"
	ContainerEphemeral ContainerKind = "ephemeral"
)

// ContainerState is a container's lifecycle state.
type ContainerState string

const (
	ContainerRunning      ContainerState = "running"
	ContainerWaiting      ContainerState = "waiting"
	ContainerTerminated   ContainerState = "terminated"
	ContainerStateUnknown ContainerState = "unknown"
)

// Container is one container of an instance as its attester reports it.
type Container struct {
	Name  string
	Kind  ContainerKind
	State ContainerState
	// Restartable marks an init container that keeps running (a native sidecar).
	Restartable bool
}
