package runnable

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	codepb "google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/durationpb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/resources"
)

// Bounds on the execution policy a method may declare. They are the ones the
// orchestration runtime enforces when it installs the binding, restated here
// so a descriptor that generates is a descriptor that installs: a policy first
// refused at installation time would have been written, reviewed and released
// before anyone learned it was out of range.
const (
	// MinAttemptTimeout and MaxAttemptTimeout bound one attempt.
	MinAttemptTimeout = time.Second
	MaxAttemptTimeout = time.Minute
	// MinBackoff and MaxBackoff bound the delay before a second attempt.
	MinBackoff = 100 * time.Millisecond
	MaxBackoff = time.Minute
	// MaxOperationAttempts is how often one invocation may be attempted.
	MaxOperationAttempts = 5
	// MaxRetryableCodes bounds the declared retryable status codes.
	MaxRetryableCodes = 16
	// ReadOnlyScopeAction is the one action a lookup scope may carry: reading
	// an effect receipt is the whole of what recovery is allowed to do.
	ReadOnlyScopeAction = "read"
)

// ErrNotAnOperation is returned for a method that does not carry the
// codefly.runnable.v0.operation option. It is an answer, not a failure: a
// generator walks every method of a service and derives the marked ones.
var ErrNotAnOperation = errors.New("method is not a Codefly runnable operation")

// grpcCodeNames are the status code names a retry policy may name, read from
// google.rpc.Code itself so the accepted spelling cannot drift from the
// canonical one a runtime compares against.
var grpcCodeNames = func() map[string]struct{} {
	names := make(map[string]struct{}, len(codepb.Code_name))
	for _, name := range codepb.Code_name {
		names[name] = struct{}{}
	}
	return names
}()

// OperationSpec is the execution policy and authority one marked method
// declares. It is deliberately not part of the package the method derives: the
// package is the contract, and policy and authority are installed with the
// binding, so two installations of one contract may differ in both.
type OperationSpec struct {
	// Method is the method the option was read from, as "/pkg.Service/Method".
	Method string
	// AttemptTimeout bounds one attempt.
	AttemptTimeout time.Duration
	// TotalTimeout bounds every attempt together, and is the derived package's
	// execution timeout.
	TotalTimeout time.Duration
	// MaxAttempts is how often the operation may be attempted.
	MaxAttempts uint32
	// Backoff is the delay before the second attempt.
	Backoff time.Duration
	// RetryableCodes are the gRPC status code names an attempt may be retried on.
	RetryableCodes []string
	// Audience is the trust boundary the runtime mints authority for.
	Audience string
	// InvokeScopes are the scopes bound for the call.
	InvokeScopes []*basev0.WorkScopeV1
	// LookupScopes are the scopes bound to read an effect receipt.
	LookupScopes []*basev0.WorkScopeV1
	// LookupMethod is the paired receipt method on the same service, empty when
	// the SDK's generic receipt lookup answers instead.
	LookupMethod string
}

// ServiceOwner is the published service a derived operation is reached on: the
// facts the method descriptor does not carry. The module, workspace, name and
// version come from the release identity the caller passes.
type ServiceOwner struct {
	// Service is the owner service, named as an Endpoint's service is.
	Service string
	// Endpoint is the endpoint the method is published on.
	Endpoint string
	// Agent is the owner's pinned service agent: a method an owner already
	// publishes is built by the owner's own builder, never by a runnable agent.
	Agent *basev0.Agent
}

// OperationFromMethod reads and validates the operation option a method
// carries. A method without the option is ErrNotAnOperation.
func OperationFromMethod(method protoreflect.MethodDescriptor) (*OperationSpec, error) {
	if method == nil {
		return nil, fmt.Errorf("%w: method descriptor is required", ErrInvalid)
	}
	options, _ := method.Options().(*descriptorpb.MethodOptions)
	if !proto.HasExtension(options, runnablev0.E_Operation) {
		return nil, fmt.Errorf("%w: %s", ErrNotAnOperation, method.FullName())
	}
	declared, _ := proto.GetExtension(options, runnablev0.E_Operation).(*runnablev0.Operation)
	full := FullMethodName(method)
	if method.IsStreamingClient() || method.IsStreamingServer() {
		return nil, fmt.Errorf("%w: %s streams, and a Runnable operation is one finite call with one input and one output", ErrInvalid, full)
	}
	spec := &OperationSpec{
		Method:         full,
		AttemptTimeout: declared.GetAttemptTimeout().AsDuration(),
		TotalTimeout:   declared.GetTotalTimeout().AsDuration(),
		MaxAttempts:    declared.GetMaxAttempts(),
		Backoff:        declared.GetBackoff().AsDuration(),
		RetryableCodes: slices.Clone(declared.GetRetryableCodes()),
		Audience:       declared.GetAudience(),
		InvokeScopes:   clonedScopes(declared.GetInvokeScopes()),
		LookupScopes:   clonedScopes(declared.GetLookupScopes()),
		LookupMethod:   declared.GetLookupMethod(),
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return spec, nil
}

// clonedScopes copies the option's scopes out of the descriptor: a spec is
// handed to a generator that owns what it holds, and a descriptor's options
// are shared and must not be written through.
func clonedScopes(scopes []*basev0.WorkScopeV1) []*basev0.WorkScopeV1 {
	if scopes == nil {
		return nil
	}
	cloned := make([]*basev0.WorkScopeV1, 0, len(scopes))
	for _, scope := range scopes {
		cloned = append(cloned, proto.CloneOf(scope))
	}
	return cloned
}

// Validate rejects a policy the runtime would refuse to install and authority
// a recovery could widen with.
func (s *OperationSpec) Validate() error {
	if s.AttemptTimeout < MinAttemptTimeout || s.AttemptTimeout > MaxAttemptTimeout {
		return fmt.Errorf("%w: %s attempt_timeout %s is outside %s..%s", ErrInvalid, s.Method, s.AttemptTimeout, MinAttemptTimeout, MaxAttemptTimeout)
	}
	if s.TotalTimeout < s.AttemptTimeout {
		return fmt.Errorf("%w: %s total_timeout %s is shorter than one attempt (%s)", ErrInvalid, s.Method, s.TotalTimeout, s.AttemptTimeout)
	}
	if s.MaxAttempts < 1 || s.MaxAttempts > MaxOperationAttempts {
		return fmt.Errorf("%w: %s max_attempts %d is outside 1..%d", ErrInvalid, s.Method, s.MaxAttempts, MaxOperationAttempts)
	}
	if s.Backoff < MinBackoff || s.Backoff > MaxBackoff {
		return fmt.Errorf("%w: %s backoff %s is outside %s..%s", ErrInvalid, s.Method, s.Backoff, MinBackoff, MaxBackoff)
	}
	if len(s.RetryableCodes) > MaxRetryableCodes {
		return fmt.Errorf("%w: %s names %d retryable codes; at most %d may be declared", ErrInvalid, s.Method, len(s.RetryableCodes), MaxRetryableCodes)
	}
	seen := make(map[string]struct{}, len(s.RetryableCodes))
	for _, name := range s.RetryableCodes {
		if _, known := grpcCodeNames[name]; !known {
			return fmt.Errorf("%w: %s names retryable code %q, which is not a gRPC status code name", ErrInvalid, s.Method, name)
		}
		if _, repeated := seen[name]; repeated {
			return fmt.Errorf("%w: %s names retryable code %q twice", ErrInvalid, s.Method, name)
		}
		seen[name] = struct{}{}
	}
	if err := s.validateScopes("invoke_scopes", s.InvokeScopes); err != nil {
		return err
	}
	if err := s.validateScopes("lookup_scopes", s.LookupScopes); err != nil {
		return err
	}
	for _, scope := range s.LookupScopes {
		if len(scope.GetActions()) != 1 || scope.GetActions()[0] != ReadOnlyScopeAction {
			return fmt.Errorf("%w: %s lookup scope %q is not read-only: recovering an outcome may only %q it", ErrInvalid, s.Method, scope.GetResourceKind(), ReadOnlyScopeAction)
		}
		if !scopeContained(scope, s.InvokeScopes) {
			return fmt.Errorf("%w: %s lookup scope %q is not covered by its invoke scopes", ErrInvalid, s.Method, scope.GetResourceKind())
		}
	}
	return nil
}

func (s *OperationSpec) validateScopes(at string, scopes []*basev0.WorkScopeV1) error {
	kinds := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		kind := scope.GetResourceKind()
		if kind == "" {
			return fmt.Errorf("%w: %s declares a %s entry with no resource kind", ErrInvalid, s.Method, at)
		}
		if len(scope.GetActions()) == 0 {
			return fmt.Errorf("%w: %s %s scope %q names no action", ErrInvalid, s.Method, at, kind)
		}
		if _, exists := kinds[kind]; exists {
			return fmt.Errorf("%w: %s declares %s scope %q twice", ErrInvalid, s.Method, at, kind)
		}
		kinds[kind] = struct{}{}
	}
	return nil
}

// scopeContained applies the Work Context attenuation rule: a child may narrow
// a wildcard parent to explicit ids but may never widen an explicit parent set,
// and may never name an action the parent does not hold.
func scopeContained(child *basev0.WorkScopeV1, parents []*basev0.WorkScopeV1) bool {
	for _, parent := range parents {
		if parent.GetResourceKind() != child.GetResourceKind() {
			continue
		}
		for _, action := range child.GetActions() {
			if !slices.Contains(parent.GetActions(), action) {
				return false
			}
		}
		if len(parent.GetResourceIds()) == 0 {
			return true
		}
		if len(child.GetResourceIds()) == 0 {
			return false
		}
		for _, id := range child.GetResourceIds() {
			if !slices.Contains(parent.GetResourceIds(), id) {
				return false
			}
		}
		return true
	}
	return false
}

// FullMethodName spells a method the way gRPC dispatches it, so the operation
// a package names is the string a client and a server already agree on.
func FullMethodName(method protoreflect.MethodDescriptor) string {
	return "/" + string(method.Parent().FullName()) + "/" + string(method.Name())
}

// methodByFullName resolves "/pkg.Service/Method" against a descriptor set.
func methodByFullName(files *protoregistry.Files, fullMethod string) (protoreflect.MethodDescriptor, error) {
	service, name, found := strings.Cut(strings.TrimPrefix(fullMethod, "/"), "/")
	if !found || !strings.HasPrefix(fullMethod, "/") || service == "" || name == "" {
		return nil, fmt.Errorf("%w: %q is not a gRPC method name of the form /package.Service/Method", ErrInvalid, fullMethod)
	}
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	published, isService := descriptor.(protoreflect.ServiceDescriptor)
	if !isService {
		return nil, fmt.Errorf("%w: %s is not a service", ErrInvalid, service)
	}
	method := published.Methods().ByName(protoreflect.Name(name))
	if method == nil {
		return nil, fmt.Errorf("%w: service %s publishes no method %s", ErrInvalid, service, name)
	}
	return method, nil
}

// PackageFromMethod derives the immutable package of the Runnable operation a
// method declares, and the execution policy and authority that are installed
// beside it rather than digested into it.
//
// Nothing here is authored twice: the contract is the projection of the
// method's own messages, the implementation is the method itself reached on the
// owner's endpoint, and the identity is the release the caller is generating.
func PackageFromMethod(files *protoregistry.Files, location *resources.RunnableLocation, owner ServiceOwner, fullMethod string) (*basev0.RunnablePackage, *OperationSpec, error) {
	if location == nil || location.Identity == nil {
		return nil, nil, fmt.Errorf("%w: the release identity a derived package carries is required", ErrInvalid)
	}
	method, err := methodByFullName(files, fullMethod)
	if err != nil {
		return nil, nil, err
	}
	spec, err := OperationFromMethod(method)
	if err != nil {
		return nil, nil, err
	}
	input, err := ProjectMessage(method.Input())
	if err != nil {
		return nil, nil, err
	}
	output, err := ProjectMessage(method.Output())
	if err != nil {
		return nil, nil, err
	}
	identity, err := location.Identity.Proto()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: identity: %v", ErrInvalid, err)
	}
	pkg, err := PreparePackage(&basev0.RunnablePackage{
		Schema:   PackageSchemaV1,
		Identity: identity,
		Agent:    owner.Agent,
		Contract: &basev0.RunnableContract{Protocol: resources.RunnableServiceProtocolV1, Input: input, Output: output},
		Execution: &basev0.RunnableExecution{
			Facilities:     []*basev0.RunnableFacility{{Kind: basev0.RunnableFacility_SERVICE}},
			Timeout:        durationpb.New(spec.TotalTimeout),
			Cancellation:   basev0.RunnableExecution_CANCELLATION_NONE,
			Recovery:       basev0.RunnableExecution_RECOVERY_RECEIPT,
			MaxInputBytes:  resources.DefaultRunnablePayloadBytes,
			MaxOutputBytes: resources.DefaultRunnablePayloadBytes,
		},
		ServiceOperations: []*basev0.RunnableServiceOperation{{
			Module:        identity.GetModule(),
			Name:          owner.Service,
			Endpoint:      owner.Endpoint,
			Operation:     spec.Method,
			InputMessage:  string(method.Input().FullName()),
			OutputMessage: string(method.Output().FullName()),
			Adaptation:    basev0.RunnableServiceOperation_ADAPTATION_BOUNDED_JSON_V1,
		}},
	})
	if err != nil {
		return nil, nil, err
	}
	return pkg, spec, nil
}
