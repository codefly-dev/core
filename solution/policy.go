// Package solution holds the host-side dispatch gate for the Solution executor
// contract. The Solution service binds a solution_method_policy to every RPC
// declaring its maximum network reach and state effect; the host reads that
// policy from the generated descriptors and refuses to dispatch any method
// whose declared ceiling exceeds the ceiling admitted for the current operation.
//
// The gate constrains what the host chooses to invoke — it does not, and under
// this contract cannot, constrain what a solution executor actually does inside
// a handler. Unlike provider.proto (where ProviderHost brokers the provider's
// side effects and the host enforces the policy at the point of effect), the
// Solution contract has no host-brokered callback path: a plugin's real
// filesystem and registry writes are unmediated. Enforcing declared effects
// against plugin behavior would require a broker this contract does not define.
//
// Two properties bound what the gate guarantees. First, it enforces
// caller-asserted intent, not authority: the constructors below stop a host from
// hand-widening its own bounds, but the ceiling is not bound to an authorized
// principal/operation/environment at a trusted chokepoint, so the gate prevents
// an honest host's accidental over-reach, not a hostile one's. Second, the tier
// vocabulary (Inspect/Scaffold/Render/Publish) is shaped by this contract alone:
// its fit to a host's real operations, and whether Render's reach stays
// REGISTRY_READ or becomes OFFLINE (which follows from how the host resolves the
// artifact — see solution.proto), are the consuming host's to establish.
package solution

import (
	"context"
	"fmt"
	"strings"

	solutionv0 "github.com/codefly-dev/core/generated/go/codefly/services/solution/v0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

const solutionServiceName protoreflect.FullName = "codefly.services.solution.v0.Solution"

// Ceiling is the maximum network reach and state effect a host admits for one
// operation. A method is admitted only when both of its declared policy fields
// are at or below the ceiling.
//
// The fields are unexported and a ceiling is obtained only through the named
// operation constructors below. That gives the ceiling an intent provenance: a
// caller declares the operation it is performing (inspect/scaffold/render/
// publish) rather than hand-assembling bounds, so it cannot silently widen its
// own privilege with a struct literal, and the interceptor can never receive an
// incoherent ceiling (e.g. registry network with only read-only effect). The
// intent→ceiling mapping lives here as the single audited chokepoint. Binding
// that intent to an authorized principal is a separate concern the constructors
// do not address (see the package doc): the constructor proves which operation a
// caller named, not that the caller was entitled to it.
type Ceiling struct {
	network solutionv0.SolutionNetworkMode
	effect  solutionv0.SolutionEffect
}

// CeilingInspect admits only offline, read-only RPCs — GetSolutionInformation.
// It is the ceiling for an operation that reads a solution executor's
// advertisement without invoking any lifecycle mutation.
func CeilingInspect() Ceiling {
	return Ceiling{
		network: solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_OFFLINE,
		effect:  solutionv0.SolutionEffect_SOLUTION_EFFECT_READ_ONLY,
	}
}

// CeilingScaffold admits offline local-filesystem RPCs — Create and Update —
// but not Render (which pulls the packaged artifact) or Package's registry push.
func CeilingScaffold() Ceiling {
	return Ceiling{
		network: solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_OFFLINE,
		effect:  solutionv0.SolutionEffect_SOLUTION_EFFECT_LOCAL_WRITE,
	}
}

// CeilingRender admits Render — a registry pull of the packaged artifact plus a
// local-filesystem write of its manifests — and every lower RPC, but not
// Package's registry push.
func CeilingRender() Ceiling {
	return Ceiling{
		network: solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_REGISTRY_READ,
		effect:  solutionv0.SolutionEffect_SOLUTION_EFFECT_LOCAL_WRITE,
	}
}

// CeilingPublish admits every Solution RPC, including Package's registry push.
func CeilingPublish() Ceiling {
	return Ceiling{
		network: solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_REGISTRY_WRITE,
		effect:  solutionv0.SolutionEffect_SOLUTION_EFFECT_REGISTRY_WRITE,
	}
}

// Client is the canonical host-side Solution client and the only path that can
// dispatch an effectful Solution RPC. It makes the operation ceiling a required
// argument of every call, so host code cannot dispatch a Solution RPC without
// declaring the operation it performs — the obligation is type-level, not a
// convention a caller can forget.
//
// Each method checks the method's declared policy against the ceiling before it
// dispatches, so the guarantee is intrinsic to Client and does not depend on the
// connection having been dialed with EnforcingClientInterceptor: a Client built
// over a plain connection still refuses an over-ceiling call before the wire. It
// also stamps the ceiling onto the context so that interceptor — installed on
// every agent connection (agents/manager.Load) as defense in depth and to gate
// callers that bypass Client — admits the same call rather than defaulting it to
// least privilege.
//
// The raw generated solutionv0.SolutionClient is deliberately not a second entry
// point for effectful calls: the context stamp is unexported (withCeiling), so a
// caller holding the raw client can only ever reach the least-privilege default
// ceiling — enough for the read-only advertisement, which stays reachable that way
// by design — while every mutating RPC fails closed. That leaves this type as the
// single path that can dispatch anything beyond that read.
type Client struct {
	inner solutionv0.SolutionClient
}

// NewClient wraps a connection with the typed, ceiling-enforcing Solution client.
// The connection need not carry EnforcingClientInterceptor — Client enforces the
// ceiling itself — though every agent connection from agents/manager.Load installs
// it anyway to gate any caller that reaches for the raw generated client.
func NewClient(conn grpc.ClientConnInterface) *Client {
	return &Client{inner: solutionv0.NewSolutionClient(conn)}
}

// GetSolutionInformation reads a solution executor's advertisement.
func (c *Client) GetSolutionInformation(ctx context.Context, ceiling Ceiling, in *solutionv0.GetSolutionInformationRequest, opts ...grpc.CallOption) (*solutionv0.GetSolutionInformationResponse, error) {
	if err := enforce(solutionv0.Solution_GetSolutionInformation_FullMethodName, ceiling); err != nil {
		return nil, err
	}
	return c.inner.GetSolutionInformation(withCeiling(ctx, ceiling), in, opts...)
}

// Create scaffolds a new solution into a destination directory.
func (c *Client) Create(ctx context.Context, ceiling Ceiling, in *solutionv0.CreateRequest, opts ...grpc.CallOption) (*solutionv0.CreateResponse, error) {
	if err := enforce(solutionv0.Solution_Create_FullMethodName, ceiling); err != nil {
		return nil, err
	}
	return c.inner.Create(withCeiling(ctx, ceiling), in, opts...)
}

// Update reconciles an existing solution source with the executor's template.
func (c *Client) Update(ctx context.Context, ceiling Ceiling, in *solutionv0.UpdateRequest, opts ...grpc.CallOption) (*solutionv0.UpdateResponse, error) {
	if err := enforce(solutionv0.Solution_Update_FullMethodName, ceiling); err != nil {
		return nil, err
	}
	return c.inner.Update(withCeiling(ctx, ceiling), in, opts...)
}

// Package builds an OCI artifact from a solution source directory and pushes it.
func (c *Client) Package(ctx context.Context, ceiling Ceiling, in *solutionv0.PackageRequest, opts ...grpc.CallOption) (*solutionv0.PackageResponse, error) {
	if err := enforce(solutionv0.Solution_Package_FullMethodName, ceiling); err != nil {
		return nil, err
	}
	return c.inner.Package(withCeiling(ctx, ceiling), in, opts...)
}

// Render renders a packaged solution's manifests into a gitops destination.
func (c *Client) Render(ctx context.Context, ceiling Ceiling, in *solutionv0.RenderRequest, opts ...grpc.CallOption) (*solutionv0.RenderResponse, error) {
	if err := enforce(solutionv0.Solution_Render_FullMethodName, ceiling); err != nil {
		return nil, err
	}
	return c.inner.Render(withCeiling(ctx, ceiling), in, opts...)
}

// enforce refuses an over-ceiling dispatch from Client itself, so the ceiling
// guarantee holds even on a connection whose dial did not install
// EnforcingClientInterceptor. It mirrors the interceptor's over-ceiling denial
// (same PermissionDenied, same message shape); admits fails closed on a missing or
// unspecified policy, which cannot occur for Client's own annotated Solution
// methods but keeps the check total.
func enforce(fullMethod string, ceiling Ceiling) error {
	policy, _ := policyFor(fullMethod)
	if err := admits(policy, ceiling); err != nil {
		return status.Errorf(codes.PermissionDenied, "solution method %s denied: %v", fullMethod, err)
	}
	return nil
}

type ceilingContextKey struct{}

// withCeiling stamps the ceiling admitted for the current operation onto a
// context. It is unexported so Client is the only way to stamp one: the host sets
// it per call because one solution-agent connection is long-lived and reused
// across operations (see agents/manager.loader: AgentConn.GRPCConn), so the
// ceiling belongs to the call, not the dial — but that per-call stamp is Client's
// job, not something a caller assembles by hand. A Solution RPC issued without a
// ceiling (i.e. through the raw generated client, which cannot reach this) is
// gated against the least-privilege ceiling by EnforcingClientInterceptor, so
// only the read-only advertisement call succeeds unstamped.
func withCeiling(ctx context.Context, ceiling Ceiling) context.Context {
	return context.WithValue(ctx, ceilingContextKey{}, ceiling)
}

// ceilingFrom returns the ceiling stamped on the context, if any. It is
// unexported because only the interceptor consumes a ceiling; a caller cannot
// read a Ceiling's bounds (its fields are unexported), so there is nothing for
// external code to do with the value.
func ceilingFrom(ctx context.Context) (Ceiling, bool) {
	ceiling, ok := ctx.Value(ceilingContextKey{}).(Ceiling)
	return ceiling, ok
}

// policyFor returns the declared method policy for a full gRPC method name
// (e.g. "/codefly.services.solution.v0.Solution/Package"). The second result
// reports whether the method belongs to the Solution service; a Solution method
// with no annotation returns (nil, true) so callers fail closed. It is
// unexported: only the interceptor consults policies; a host uses the interceptor
// plus Client, never the policy lookup directly.
func policyFor(fullMethod string) (*solutionv0.SolutionMethodPolicy, bool) {
	method := methodDescriptor(fullMethod)
	if method == nil {
		return nil, false
	}
	options, ok := method.Options().(*descriptorpb.MethodOptions)
	if !ok || !proto.HasExtension(options, solutionv0.E_SolutionMethodPolicy) {
		return nil, true
	}
	return proto.GetExtension(options, solutionv0.E_SolutionMethodPolicy).(*solutionv0.SolutionMethodPolicy), true
}

// admits reports whether a method policy is within the ceiling. It fails closed:
// a nil policy, an unspecified policy field, or an unspecified ceiling field is
// never admitted. It is unexported for the same reason as policyFor — it is the
// interceptor's internal check, not host-facing API.
func admits(policy *solutionv0.SolutionMethodPolicy, ceiling Ceiling) error {
	if policy == nil {
		return fmt.Errorf("no method policy declared")
	}
	network, effect := policy.GetNetwork(), policy.GetEffect()
	if network == solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_UNSPECIFIED {
		return fmt.Errorf("network mode is unspecified")
	}
	if effect == solutionv0.SolutionEffect_SOLUTION_EFFECT_UNSPECIFIED {
		return fmt.Errorf("effect is unspecified")
	}
	if ceiling.network == solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_UNSPECIFIED {
		return fmt.Errorf("ceiling network mode is unspecified")
	}
	if ceiling.effect == solutionv0.SolutionEffect_SOLUTION_EFFECT_UNSPECIFIED {
		return fmt.Errorf("ceiling effect is unspecified")
	}
	if network > ceiling.network {
		return fmt.Errorf("network mode %s exceeds ceiling %s", network, ceiling.network)
	}
	if effect > ceiling.effect {
		return fmt.Errorf("effect %s exceeds ceiling %s", effect, ceiling.effect)
	}
	return nil
}

// EnforcingClientInterceptor is the host-side dispatch gate: a unary client
// interceptor installed on every agent connection (agents/manager.loader). For
// each outgoing Solution RPC it reads the declared policy and the ceiling
// stamped on the call context (see Client) and refuses to dispatch a call
// whose declared network or effect exceeds the admitted ceiling. Calls to
// services other than Solution pass through untouched, so installing it
// universally does not affect non-solution agents.
//
// A call with no ceiling on its context is admitted against the least-privilege
// ceiling (CeilingInspect): a caller that never declared its operation may still
// read a solution executor's advertisement, but every mutating RPC is refused
// until the host declares a higher ceiling through Client. Defaulting to the
// minimum — rather than denying even the harmless read — keeps inspection
// ergonomic while staying fail-closed for every effectful RPC.
//
// It covers unary RPCs only, which is complete because the Solution contract is
// unary-only — an invariant TestSolutionContractIsUnaryOnly guards. A streaming
// Solution RPC must not be added without a matching stream gate, or it would
// dispatch unchecked.
func EnforcingClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		policy, isSolution := policyFor(method)
		if !isSolution {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		ceiling, explicit := ceilingFrom(ctx)
		if !explicit {
			ceiling = CeilingInspect()
		}
		if err := admits(policy, ceiling); err != nil {
			if !explicit {
				return status.Errorf(codes.PermissionDenied,
					"solution method %s denied under the default least-privilege ceiling: %v; "+
						"dispatch it through solution.Client (solution.NewClient), which requires an operation ceiling",
					method, err)
			}
			return status.Errorf(codes.PermissionDenied, "solution method %s denied: %v", method, err)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// methodDescriptor resolves the Solution method descriptor for a full gRPC
// method name, or nil when the name does not target the Solution service.
func methodDescriptor(fullMethod string) protoreflect.MethodDescriptor {
	trimmed := strings.TrimPrefix(fullMethod, "/")
	slash := strings.LastIndex(trimmed, "/")
	if slash < 0 {
		return nil
	}
	if protoreflect.FullName(trimmed[:slash]) != solutionServiceName {
		return nil
	}
	service := solutionv0.File_codefly_services_solution_v0_solution_proto.Services().ByName("Solution")
	if service == nil {
		return nil
	}
	return service.Methods().ByName(protoreflect.Name(trimmed[slash+1:]))
}
