package policy

import (
	"fmt"
	"time"

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
// owner, then each preceding actor, and for a capability carrying an approval
// the grant's approvers in place of the actor that would otherwise have lent
// the hop its authority. That is what makes the audit record read
// U → A → (grant G, approved by V) → X.
func PrincipalFromWorkContext(verified *workcontext.Verified) (*Principal, error) {
	if verified == nil {
		return nil, fmt.Errorf("%w: no verified work context", ErrPrincipalInvalid)
	}
	wc := verified.Context
	chain := wc.GetActorChain()

	p := &Principal{
		ID:        wc.GetOwnerPrincipalId(),
		Kind:      wc.GetOwnerPrincipalKind(),
		AgentID:   wc.GetOwnerAgentId(),
		OrgID:     wc.GetTenantId(),
		Token:     verified.Encoded,
		ExpiresAt: time.Unix(wc.GetExpiresAtUnix(), 0),
	}
	if actor := verified.Actor(); actor != nil {
		p.ID = actor.GetPrincipalId()
		p.Kind = actor.GetPrincipalKind()
		p.AgentID = actor.GetAgentId()
	}

	grant := wc.GetGrantHop()
	for index, hop := range chain {
		if grant != nil && index == len(chain)-1 {
			for _, approver := range grant.GetApproverPrincipalIds() {
				p.DelegationChain = append(p.DelegationChain, DelegationLink{
					PrincipalID: approver,
					GrantID:     grant.GetGrantId(),
				})
			}
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
