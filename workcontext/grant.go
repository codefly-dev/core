package workcontext

import (
	"context"
	"fmt"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// Grant is the issuer's record of one approval decision. Core carries it
// between the approvals engine and the capability model and checks a
// capability against it; deciding, storing and revoking grants is the
// engine's, and the product's, business.
type Grant struct {
	// ID is the grant the capability refers back to, and the handle a
	// revocation names.
	ID string

	// Approvers are every principal whose decision produced the grant,
	// including each member of an N-of-M quorum.
	Approvers []string

	// Scope is exactly what the approval adds: one action on one resource
	// of one kind.
	Scope *basev0.WorkScopeV1

	// Subject is what the grant is pinned to, in the engine's own
	// vocabulary.
	Subject string

	// RequestDigest is the exact call the approver saw.
	RequestDigest string

	// Audience is the tool the granted capability may be spent on.
	Audience string

	// NotAfter closes the grant window. It is distinct from the window the
	// approver had to decide in, and it bounds the capability's expiry.
	NotAfter time.Time

	// AuthorizationRevision is the issuer's revision at the moment of the
	// decision. Bumping the issuer's revision past it is what makes an
	// unused grant capability stop verifying.
	AuthorizationRevision uint64

	// Revoked marks a grant withdrawn before its window closed.
	Revoked bool
}

func (g *Grant) validate() error {
	switch {
	case g == nil:
		return fmt.Errorf("%w: no grant", ErrInvalid)
	case g.ID == "":
		return fmt.Errorf("%w: grant has no id", ErrInvalid)
	case len(g.Approvers) == 0:
		return fmt.Errorf("%w: grant %q names no approver", ErrInvalid, g.ID)
	case g.Subject == "":
		return fmt.Errorf("%w: grant %q is pinned to no subject", ErrInvalid, g.ID)
	case g.RequestDigest == "":
		return fmt.Errorf("%w: grant %q is pinned to no request", ErrInvalid, g.ID)
	case g.Audience == "":
		return fmt.Errorf("%w: grant %q names no audience", ErrInvalid, g.ID)
	case g.NotAfter.IsZero():
		return fmt.Errorf("%w: grant %q has no window", ErrInvalid, g.ID)
	case len(g.Scope.GetActions()) != 1 || len(g.Scope.GetResourceIds()) != 1 || g.Scope.GetResourceKind() == "":
		return fmt.Errorf("%w: grant %q must approve exactly one action on one resource", ErrInvalid, g.ID)
	}
	return nil
}

// GrantSource resolves the approval a grant capability claims. A verifier
// holds one so a signed capability cannot assert an approval the issuer never
// recorded, and so revoking a grant stops a capability already minted.
type GrantSource interface {
	Grant(ctx context.Context, grantID string) (*Grant, error)
}

// RevisionSource answers the issuer's current authorization revision for a
// tenant. Bumping it is the coarse revocation lever: every capability minted
// against an older revision stops verifying.
type RevisionSource interface {
	AuthorizationRevision(ctx context.Context, tenantID string) (uint64, error)
}

// FixedRevision is a RevisionSource that answers the same revision for every
// tenant. Hosts that do not model revocation this way, and tests, use it.
type FixedRevision uint64

// AuthorizationRevision implements RevisionSource.
func (r FixedRevision) AuthorizationRevision(context.Context, string) (uint64, error) {
	return uint64(r), nil
}
