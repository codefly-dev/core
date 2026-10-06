package runnable

import (
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
)

// Policy is the one conversion from a derived spec to the policy a prepared
// binding delivers. A writer that copied the fields by hand had to learn of
// every new one, and learned of a missed one only when a host looked for what
// it carried: tool exposure arrived as exactly such a field, one a writer
// predating it would drop with nothing failing anywhere. So the spec writes
// its own policy, preparedPolicy reads it back, and TestPolicyRoundTripsEveryField
// holds both to the schema's field list — a field added to Operation without a
// line here is unset in every policy this writes, which that test refuses
// before any writer ships without it.
//
// The result is detached from the spec and carries no transport vocabulary:
// Codes is implied by the route the binding names, and Method is the binding's
// own operation spelling. Policy does not validate; EncodePrepared validates
// what is delivered, so an invalid spec yields a policy an installer refuses
// rather than one quietly repaired. A spec still carrying required scope slots
// yields a policy that carries them too, which EncodePrepared refuses: a writer
// resolves with ResolveScopeSlots first and delivers the concrete policy.
func (s *OperationSpec) Policy() *runnablev0.Operation {
	return &runnablev0.Operation{
		AttemptTimeout:     durationpb.New(s.AttemptTimeout),
		TotalTimeout:       durationpb.New(s.TotalTimeout),
		MaxAttempts:        s.MaxAttempts,
		Backoff:            durationpb.New(s.Backoff),
		RetryableCodes:     slices.Clone(s.RetryableCodes),
		Audience:           s.Audience,
		InvokeScopes:       clonedScopes(s.InvokeScopes),
		LookupScopes:       clonedScopes(s.LookupScopes),
		LookupMethod:       s.LookupMethod,
		MaxInputBytes:      s.MaxInputBytes,
		MaxOutputBytes:     s.MaxOutputBytes,
		Completion:         s.Completion,
		Tool:               proto.CloneOf(s.Tool),
		RequiredScopeSlots: clonedSlots(s.RequiredScopeSlots),
	}
}
