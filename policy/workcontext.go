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
	wc := verified.Context()
	chain := wc.GetActorChain()

	p := &Principal{
		ID:      wc.GetOwnerPrincipalId(),
		Kind:    wc.GetOwnerPrincipalKind(),
		AgentID: wc.GetOwnerAgentId(),
		// The organization, never the tenant: a tenant may hold several
		// organizations and authorization is scoped per organization, so
		// substituting one for the other either denies everything or
		// matches an organization nobody granted access to.
		OrgID:     wc.GetOrganizationId(),
		Token:     verified.Encoded(),
		ExpiresAt: time.Unix(wc.GetExpiresAtUnix(), 0),
	}
	if actor := verified.Actor(); actor != nil {
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
