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
// operation constructors below. That gives the ceiling a provenance: a caller
// declares the operation it is performing (inspect/scaffold/publish) rather than
// hand-assembling bounds, so it cannot silently widen its own privilege with a
// struct literal, and the interceptor can never receive an incoherent ceiling
// (e.g. registry network with only read-only effect). The intent→ceiling mapping
// lives here as the single audited chokepoint.
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

// CeilingScaffold admits offline local-filesystem RPCs — Create, Update, and
// Render — but not Package's registry push.
func CeilingScaffold() Ceiling {
	return Ceiling{
		network: solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_OFFLINE,
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

type ceilingContextKey struct{}

// WithCeiling stamps the ceiling admitted for the current operation onto a
// context. Pass a ceiling from one of the operation constructors
// (CeilingInspect/CeilingScaffold/CeilingPublish). The host sets it per call
// because one solution-agent connection is long-lived and reused across
// operations (see agents/manager.loader: AgentConn.GRPCConn), so the ceiling
// belongs to the call, not the dial. A Solution RPC issued without a ceiling in
// its context is refused by EnforcingClientInterceptor.
func WithCeiling(ctx context.Context, ceiling Ceiling) context.Context {
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

// PolicyFor returns the declared method policy for a full gRPC method name
// (e.g. "/codefly.services.solution.v0.Solution/Package"). The second result
// reports whether the method belongs to the Solution service; a Solution method
// with no annotation returns (nil, true) so callers fail closed.
func PolicyFor(fullMethod string) (*solutionv0.SolutionMethodPolicy, bool) {
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

// Admits reports whether a method policy is within the ceiling. It fails closed:
// a nil policy, an unspecified policy field, or an unspecified ceiling field is
// never admitted.
func Admits(policy *solutionv0.SolutionMethodPolicy, ceiling Ceiling) error {
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
// stamped on the call context (see WithCeiling) and refuses to dispatch a call
// whose declared ceiling exceeds the admitted ceiling, or that carries no
// ceiling at all. Calls to services other than Solution pass through untouched,
// so installing it universally does not affect non-solution agents.
//
// It covers unary RPCs only, which is complete because the Solution contract is
// unary-only — an invariant TestSolutionContractIsUnaryOnly guards. A streaming
// Solution RPC must not be added without a matching stream gate, or it would
// dispatch unchecked.
func EnforcingClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		policy, isSolution := PolicyFor(method)
		if !isSolution {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		ceiling, ok := ceilingFrom(ctx)
		if !ok {
			return status.Errorf(codes.PermissionDenied,
				"solution method %s denied: caller stamped no operation ceiling on the context; "+
					"the host must declare one with solution.WithCeiling (e.g. CeilingInspect) before dispatch",
				method)
		}
		if err := Admits(policy, ceiling); err != nil {
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
