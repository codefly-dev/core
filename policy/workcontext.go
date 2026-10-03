package policy

import (
	"fmt"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/workcontext"
)

// PrincipalFromWorkContext derives the Principal a verified Work Context
// describes. It takes a *workcontext.Verified rather than the claims message
// so the derivation cannot be run on anything that has not passed signature,
// window, audience, attenuation, grant and replay checks.
//
// The identity is the current actor — the last hop of the actor chain, or the
// owner when the owner acts directly. DelegationChain is derived here and
// never assembled by hand: it lists the lenders oldest-first, which is the
// owner, then each preceding actor, and one link for an approval grant. That
// is what makes the audit record read U → A → (grant G, approved by V) → X.
//
// A capability minted before a field this derivation needs existed is a valid
// capability that no identity can be derived from. That is an error here,
// naming the field, rather than a schema rule that would retroactively
// invalidate every archived capability and every receipt embedding one.
func PrincipalFromWorkContext(verified *workcontext.Verified) (*Principal, error) {
	if verified == nil {
		return nil, fmt.Errorf("%w: no verified work context", ErrPrincipalInvalid)
	}
	return principalFrom(verified.Context(), verified.Encoded(), verified.Actor(), EstablishedByVerification)
}

// PrincipalFromAuthenticatedWorkContext derives the Principal an AUTHENTICATED
// Work Context describes — one checked against caller-supplied live state
// rather than against the issuer's own records, and whose approval hop, if it
// had one, was refused rather than held against a grant record.
//
// It exists because leaving it out forced the failure this model exists to
// prevent. A verify-only gateway needs an identity to authorize with; with no
// way to derive one it writes its own, re-implementing the delegation-chain
// derivation below — the grant link, the quorum-as-one-hop rule, the actor
// override — which is a second implementation of something subtle.
//
// The two entrypoints share one derivation, and the DIFFERENCE IS RECORDED on
// the Principal: EstablishedBy says which question was answered, so a policy
// that must not act on the weaker answer can refuse it, and an audit record
// says which it was. Without that field this function would have erased the
// distinction *Authenticated exists to carry, which is why it is not simply an
// overload.
func PrincipalFromAuthenticatedWorkContext(authenticated *workcontext.Authenticated) (*Principal, error) {
	if authenticated == nil {
		return nil, fmt.Errorf("%w: no authenticated work context", ErrPrincipalInvalid)
	}
	return principalFrom(authenticated.Context(), authenticated.Encoded(), authenticated.Actor(), EstablishedByAuthentication)
}

func principalFrom(wc *basev0.WorkContextV1, encoded string, actor *basev0.WorkActorV1, established Establishment) (*Principal, error) {
	chain := wc.GetActorChain()

	p := &Principal{
		ID:      wc.GetOwnerPrincipalId(),
		Kind:    wc.GetOwnerPrincipalKind(),
		AgentID: wc.GetOwnerAgentId(),
		// The organization, never the tenant: a tenant may hold several
		// organizations and authorization is scoped per organization, so
		// substituting one for the other either denies everything or
		// matches an organization nobody granted access to.
		OrgID:         wc.GetOrganizationId(),
		Token:         encoded,
		EstablishedBy: established,
		ExpiresAt:     time.Unix(wc.GetExpiresAtUnix(), 0),
	}
	if actor != nil {
		p.ID = actor.GetPrincipalId()
		p.Kind = actor.GetPrincipalKind()
		p.AgentID = actor.GetAgentId()
		if organization := actor.GetOrganizationId(); organization != "" {
			p.OrgID = organization
		}
	}
	if p.Kind == "" {
		return nil, fmt.Errorf("%w: work context names no principal kind for %q; it was minted before the canonical identity model carried one",
			ErrPrincipalInvalid, p.ID)
	}

	grant := wc.GetGrantHop()
	for index, hop := range chain {
		if grant != nil && index == len(chain)-1 {
			p.DelegationChain = append(p.DelegationChain, grantLink(grant))
			continue
		}
		lender := DelegationLink{
			PrincipalID: wc.GetOwnerPrincipalId(),
			Kind:        wc.GetOwnerPrincipalKind(),
			GrantID:     hop.GetDelegationId(),
		}
		if index > 0 {
			lender.PrincipalID = chain[index-1].GetPrincipalId()
			lender.Kind = chain[index-1].GetPrincipalKind()
		}
		p.DelegationChain = append(p.DelegationChain, lender)
	}

	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

// grantLink is the single link an approval contributes. One hop of
// delegation produces one link however large the quorum was; the quorum
// itself rides inside it.
func grantLink(grant *basev0.WorkGrantHopV1) DelegationLink {
	link := DelegationLink{GrantID: grant.GetGrantId()}
	for _, approver := range grant.GetApprovers() {
		link.Approvers = append(link.Approvers, Approver{
			PrincipalID: approver.GetPrincipalId(),
			Kind:        approver.GetPrincipalKind(),
		})
	}
	if len(link.Approvers) == 1 {
		link.PrincipalID = link.Approvers[0].PrincipalID
		link.Kind = link.Approvers[0].Kind
	}
	return link
}
