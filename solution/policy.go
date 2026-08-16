// Package solution holds the host-side enforcement for the Solution executor
// contract. The Solution service binds a solution_method_policy to every RPC
// declaring its maximum network reach and state effect; the host reads that
// policy from the generated descriptors and refuses to invoke any method whose
// ceiling exceeds the ceiling admitted for the current operation.
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
type Ceiling struct {
	Network solutionv0.SolutionNetworkMode
	Effect  solutionv0.SolutionEffect
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
	if ceiling.Network == solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_UNSPECIFIED {
		return fmt.Errorf("ceiling network mode is unspecified")
	}
	if ceiling.Effect == solutionv0.SolutionEffect_SOLUTION_EFFECT_UNSPECIFIED {
		return fmt.Errorf("ceiling effect is unspecified")
	}
	if network > ceiling.Network {
		return fmt.Errorf("network mode %s exceeds ceiling %s", network, ceiling.Network)
	}
	if effect > ceiling.Effect {
		return fmt.Errorf("effect %s exceeds ceiling %s", effect, ceiling.Effect)
	}
	return nil
}

// EnforcingClientInterceptor returns the host-side enforcement point: a unary
// client interceptor that reads each outgoing Solution RPC's declared policy and
// refuses to send a call whose ceiling exceeds the admitted ceiling. Calls to
// services other than Solution pass through untouched.
func EnforcingClientInterceptor(ceiling Ceiling) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		policy, isSolution := PolicyFor(method)
		if !isSolution {
			return invoker(ctx, method, req, reply, cc, opts...)
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
