// Package testharness provides in-process test scaffolding for the
// permission system. It exists so unit tests of guards, interceptors,
// and plugin code can assert PDP behavior WITHOUT spinning up a real
// saas-starter (which lives in a separate repo and needs Postgres).
//
// What's here:
//
//   - FakePDP: programmable decisions + a recorded call log.
//     Use this when a unit test needs "PDP says allow on action X,
//     deny on action Y" without caring how the decisions are made.
//
//   - PrincipalBuilder: ergonomic construction of test Principals
//     (humans, services, agents) for stamping into context.
//
//   - DelegationBuilder: build delegation chains for tests of
//     escalation / sub-agent flows.
//
// What's NOT here (yet):
//
//   - SaaSFixture: a Postgres + saas-starter API harness. Lives in
//     M0 phase 2 once the saas-starter Principal/Decide RPCs land.
//     For now, integration tests against saas-starter live alongside
//     the saas-starter codebase, not here.
package testharness

import (
	"context"
	"fmt"
	"sync"

	"github.com/codefly-dev/core/policy"
)

// FakePDP is a programmable PDP for unit tests. It records every
// call so tests can assert what was asked, AND it returns a decision
// determined by either:
//
//  1. An exact-match rule (Tool == request.Tool AND Toolbox == request.Toolbox)
//  2. A toolbox-match rule (Toolbox == request.Toolbox, Tool == "")
//  3. The default decision (set by NewFakePDP)
//
// First match in the rules slice wins, in the order rules were added.
//
// Concurrency: safe. Tests that fire goroutines at the PDP can read
// Calls() after the fact to assert what happened.
type FakePDP struct {
	mu       sync.Mutex
	rules    []fakeRule
	defaultD policy.PDPDecision
	calls    []FakeCall
}

type fakeRule struct {
	toolbox  string // "" matches any
	tool     string // "" matches any (with toolbox match)
	decision policy.PDPDecision
}

// FakeCall is one recorded Evaluate. The fields are immutable
// snapshots — callers can hold them safely after Calls() returns.
type FakeCall struct {
	Toolbox  string
	Tool     string
	Args     map[string]any
	Identity map[string]any
	Decision policy.PDPDecision // what the FakePDP returned
}

// NewFakePDP returns a FakePDP whose default (when no rule matches)
// is the supplied decision. Most tests use NewFakeAllow or
// NewFakeDeny rather than this directly.
func NewFakePDP(defaultDecision policy.PDPDecision) *FakePDP {
	return &FakePDP{defaultD: defaultDecision}
}

// NewFakeAllow returns a FakePDP whose default is allow. Use when
// the test cares about specific denies; everything else is permitted.
func NewFakeAllow() *FakePDP {
	return NewFakePDP(policy.PDPDecision{Allow: true})
}

// NewFakeDeny returns a FakePDP whose default is deny with a
// recognizable reason. Use when the test cares about specific
// allows; everything else is refused. Reason includes "fake-pdp"
// substring so failure messages clearly show the gate is the test
// fixture, not a misconfigured production PDP.
func NewFakeDeny() *FakePDP {
	return NewFakePDP(policy.PDPDecision{
		Allow:  false,
		Reason: "fake-pdp default-deny (no rule matched in test fixture)",
	})
}

// AllowToolbox installs a rule allowing all tools on the named
// toolbox. Returns the receiver for chaining.
func (f *FakePDP) AllowToolbox(toolbox string) *FakePDP {
	return f.addRule(toolbox, "", policy.PDPDecision{Allow: true})
}

// DenyToolbox installs a rule denying all tools on the named toolbox
// with the given reason. Empty reason gets a defaulted message.
func (f *FakePDP) DenyToolbox(toolbox, reason string) *FakePDP {
	if reason == "" {
		reason = fmt.Sprintf("fake-pdp deny: toolbox %q forbidden", toolbox)
	}
	return f.addRule(toolbox, "", policy.PDPDecision{Allow: false, Reason: reason})
}

// AllowTool installs a rule allowing a specific tool on the named
// toolbox. Both must match exactly. Wildcards are not supported in
// the fake — tests that need pattern matching should configure
// multiple explicit rules.
func (f *FakePDP) AllowTool(toolbox, tool string) *FakePDP {
	return f.addRule(toolbox, tool, policy.PDPDecision{Allow: true})
}

// DenyTool installs a rule denying a specific tool with a reason.
// Reason MUST be non-empty — silent denials are a debugging
// nightmare; force the test author to write what they're asserting.
func (f *FakePDP) DenyTool(toolbox, tool, reason string) *FakePDP {
	if reason == "" {
		panic("DenyTool: reason must be non-empty (silent denials are not debuggable)")
	}
	return f.addRule(toolbox, tool, policy.PDPDecision{Allow: false, Reason: reason})
}

func (f *FakePDP) addRule(toolbox, tool string, d policy.PDPDecision) *FakePDP {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, fakeRule{toolbox: toolbox, tool: tool, decision: d})
	return f
}

// Evaluate implements policy.PDP. Records the call, returns the
// matched decision (or default).
func (f *FakePDP) Evaluate(_ context.Context, req *policy.PDPRequest) policy.PDPDecision {
	f.mu.Lock()
	defer f.mu.Unlock()

	decision := f.defaultD
	for _, r := range f.rules {
		if r.toolbox != "" && r.toolbox != req.Toolbox {
			continue
		}
		if r.tool != "" && r.tool != req.Tool {
			continue
		}
		decision = r.decision
		break
	}

	f.calls = append(f.calls, FakeCall{
		Toolbox:  req.Toolbox,
		Tool:     req.Tool,
		Args:     copyMap(req.Args),
		Identity: copyMap(req.Identity),
		Decision: decision,
	})
	return decision
}

// Calls returns a snapshot of every Evaluate call observed so far.
// Returned slice is safe to retain — it's a copy.
func (f *FakePDP) Calls() []FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FakeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// CallCount returns the number of Evaluate calls observed. Cheaper
// than Calls() when the test only needs a count.
func (f *FakePDP) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// Reset clears the call log without removing rules. Useful when a
// single test case spans multiple "phases" and wants to assert calls
// per phase.
func (f *FakePDP) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = f.calls[:0]
}

// LastCall returns the most recent FakeCall, panicking if none have
// been recorded. Tests that assert on a single call use this for
// concise assertions: pdp.LastCall().Tool == "git.status".
func (f *FakePDP) LastCall() FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		panic("FakePDP.LastCall: no calls recorded yet")
	}
	return f.calls[len(f.calls)-1]
}

// copyMap shallow-copies a map. Used to snapshot Args/Identity at
// record time so subsequent mutations by callers don't retroactively
// alter what the test observed.
func copyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// --- Compile-time assertion ---------------------------------------

var _ policy.PDP = (*FakePDP)(nil)
