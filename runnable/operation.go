package runnable

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
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
	"github.com/codefly-dev/core/workcontext"
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
	// MaxAudienceLength bounds the trust boundary name.
	MaxAudienceLength = 512
	// MaxScopes, MaxScopeActions and MaxScopeResourceIds bound one scope set.
	MaxScopes           = 64
	MaxScopeActions     = 256
	MaxScopeResourceIds = 256
	// MaxScopeKindLength, MaxScopeActionLength and MaxScopeResourceIdLength
	// bound the individual values of a scope.
	MaxScopeKindLength       = 128
	MaxScopeActionLength     = 128
	MaxScopeResourceIdLength = 512
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

// CodeVocabulary is how an operation names the outcomes an attempt may be
// retried on. A gRPC method names them by google.rpc.Code and a REST operation
// by HTTP status, so the same policy field is read against the vocabulary of
// the transport it was declared on rather than against one of them twice.
type CodeVocabulary int

const (
	// GRPCStatusNames names a retryable outcome by its google.rpc.Code name,
	// such as "UNAVAILABLE". It is the zero value because the method option is
	// where an operation is declared unless a marker says otherwise.
	GRPCStatusNames CodeVocabulary = iota
	// HTTPStatusCodes names a retryable outcome by its HTTP status, spelled as
	// the decimal number in a string, such as "503".
	HTTPStatusCodes
)

// names reports whether the vocabulary defines a code by this spelling. Each
// set is as closed as its own protocol makes it: google.rpc.Code is an
// enumeration, so a name outside it names nothing, while HTTP defines a status
// as any three-digit code in a class it defines and servers and proxies do
// return unregistered ones. Holding the second to a registry would refuse a
// policy that retries on a status a real deployment answers with.
func (v CodeVocabulary) names(code string) bool {
	if v == HTTPStatusCodes {
		status, err := strconv.Atoi(code)
		return err == nil && code == strconv.Itoa(status) && status >= 100 && status <= 599
	}
	_, defined := grpcCodeNames[code]
	return defined
}

func (v CodeVocabulary) String() string {
	if v == HTTPStatusCodes {
		return "HTTP status"
	}
	return "gRPC status code name"
}

// OperationSpec is the execution policy and authority one marked method
// declares. It is deliberately not part of the package the method derives: the
// package is the contract, and policy and authority are installed with the
// binding, so two installations of one contract may differ in both.
type OperationSpec struct {
	// Method is the operation the policy was read from, as the transport
	// spells it: "/pkg.Service/Method" for a gRPC method, "POST /path" for a
	// REST one.
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
	// RetryableCodes are the outcomes an attempt may be retried on, spelled in
	// the vocabulary Codes names.
	RetryableCodes []string
	// Codes is the vocabulary RetryableCodes are spelled in.
	Codes CodeVocabulary
	// Audience is the trust boundary the runtime mints authority for.
	Audience string
	// InvokeScopes are the scopes bound for the call.
	InvokeScopes []*basev0.WorkScopeV1
	// LookupScopes are the scopes bound to read an effect receipt.
	LookupScopes []*basev0.WorkScopeV1
	// LookupMethod is the paired receipt method on the same service, empty when
	// the SDK's generic receipt lookup answers instead.
	LookupMethod string
	// MaxInputBytes and MaxOutputBytes bound the derived package's inline
	// payloads; zero keeps resources.DefaultRunnablePayloadBytes.
	MaxInputBytes  uint64
	MaxOutputBytes uint64
	// Completion is how the operation's answer arrives.
	Completion basev0.RunnableExecution_Completion
	// Tool is how the operation is presented as a tool, never a grant. Every
	// operation is a tool; a derivation fills this from the method when the
	// owner wrote none (DefaultTool).
	Tool *runnablev0.ToolExposure
	// Recovery is how an uncertain outcome is resolved, as declared:
	// RECOVERY_UNKNOWN reads as RECOVERY_RECEIPT (EffectiveRecovery).
	Recovery basev0.RunnableExecution_Recovery
	// Declared says the owner wrote the codefly.runnable.v0.operation option;
	// false is an operation derived by default from its authority policy. It
	// is the derivation's account of itself, for a generator to report, and is
	// never delivered: a policy is the same policy whoever wrote it.
	Declared bool
	// RequiredScopeSlots are the owner's holes in its authority: resource kinds
	// and actions whose exact resource ids only the installing composition
	// knows. A spec carrying one is not yet a policy a binding may deliver;
	// ResolveScopeSlots is the one way to a concrete one.
	RequiredScopeSlots []*runnablev0.ScopeSlot
}

// ServiceOwner is the published service a derived operation is reached on: the
// facts the method descriptor does not carry. The release's own workspace, name
// and version come from the identity the caller passes.
type ServiceOwner struct {
	// Module is the owner service's module, named as an Endpoint's module is.
	// It is stated rather than taken from the release identity: a runnable's
	// module is where its declaration lives, which need not be where the
	// service publishing the method lives, and a binding resolves this one
	// against the Endpoint.
	Module string
	// Service is the owner service, named as an Endpoint's service is.
	Service string
	// Endpoint is the endpoint the method is published on.
	Endpoint string
	// Agent is the owner's pinned service agent: a method an owner already
	// publishes is built by the owner's own builder, never by a runnable agent.
	Agent *basev0.Agent
	// Audience is the trust boundary a derived operation's authority is
	// minted for when the method carries no option naming one. Empty is the
	// owner's module, which is the audience a module's hop admits.
	Audience string
}

// audience is the trust boundary a default derivation mints for.
func (o ServiceOwner) audience() string {
	if o.Audience != "" {
		return o.Audience
	}
	return o.Module
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
	// A policy field this core has no name for is a requirement it cannot
	// honour, not one it may drop: a reader that derived past it would emit a
	// package and a binding with that requirement erased, and nothing
	// downstream could tell them from complete ones. The marker path refuses
	// the same way through protojson; this is the descriptor path's refusal.
	if unknown := declared.ProtoReflect().GetUnknown(); len(unknown) > 0 {
		return nil, fmt.Errorf("%w: %s declares %d bytes of operation policy this core does not know; derive it with the core that defines every field it uses", ErrInvalid, full, len(unknown))
	}
	if method.IsStreamingClient() || method.IsStreamingServer() {
		return nil, fmt.Errorf("%w: %s streams, and a Runnable operation is one finite call with one input and one output", ErrInvalid, full)
	}
	// The option's presence is the marking, but every policy field is required:
	// an attempt budget and an authority nobody chose are not defaults core may
	// invent on an owner's behalf.
	if proto.Equal(declared, &runnablev0.Operation{}) {
		return nil, fmt.Errorf("%w: %s carries the operation option but declares no execution policy; the option marks the method and its fields state how it runs", ErrInvalid, full)
	}
	// AsDuration saturates a Duration outside its valid range instead of
	// reporting it, so an unreadable value would silently become 292 years.
	for _, declaredDuration := range []struct {
		field string
		value *durationpb.Duration
	}{
		{"attempt_timeout", declared.GetAttemptTimeout()},
		{"total_timeout", declared.GetTotalTimeout()},
		{"backoff", declared.GetBackoff()},
	} {
		if declaredDuration.value == nil {
			continue
		}
		if err := declaredDuration.value.CheckValid(); err != nil {
			return nil, fmt.Errorf("%w: %s %s is not a valid duration: %v", ErrInvalid, full, declaredDuration.field, err)
		}
	}
	if err := validateLookupMethod(method, full, declared.GetLookupMethod()); err != nil {
		return nil, err
	}
	spec := &OperationSpec{
		Method:             full,
		AttemptTimeout:     declared.GetAttemptTimeout().AsDuration(),
		TotalTimeout:       declared.GetTotalTimeout().AsDuration(),
		MaxAttempts:        declared.GetMaxAttempts(),
		Backoff:            declared.GetBackoff().AsDuration(),
		RetryableCodes:     slices.Clone(declared.GetRetryableCodes()),
		Audience:           declared.GetAudience(),
		InvokeScopes:       clonedScopes(declared.GetInvokeScopes()),
		LookupScopes:       clonedScopes(declared.GetLookupScopes()),
		LookupMethod:       declared.GetLookupMethod(),
		MaxInputBytes:      declared.GetMaxInputBytes(),
		MaxOutputBytes:     declared.GetMaxOutputBytes(),
		Completion:         declared.GetCompletion(),
		Tool:               proto.CloneOf(declared.GetTool()),
		RequiredScopeSlots: clonedSlots(declared.GetRequiredScopeSlots()),
		Recovery:           declared.GetRecovery(),
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return spec, nil
}

// OperationForMethod is the operation a method derives, and whether the owner
// declared it with the operation option. Every method that declares its
// authority policy is an operation, and so a tool, with no option at all: the
// option overrides the default policy, and exclude keeps the method from every
// caller but its own clients. A method declaring neither an option nor a
// policy is ErrNotAnOperation; one whose default derivation is impossible is
// ErrNotDerivable.
func OperationForMethod(files *protoregistry.Files, method protoreflect.MethodDescriptor, audience string) (*OperationSpec, bool, error) {
	if method == nil {
		return nil, false, fmt.Errorf("%w: method descriptor is required", ErrInvalid)
	}
	options, _ := method.Options().(*descriptorpb.MethodOptions)
	explicit := options != nil && proto.HasExtension(options, runnablev0.E_Operation)
	if excluded(options) {
		if explicit {
			return nil, true, fmt.Errorf("%w: %s both declares an operation and excludes itself from derivation", ErrInvalid, method.FullName())
		}
		return nil, false, fmt.Errorf("%w: %s excludes itself", ErrNotAnOperation, method.FullName())
	}
	authority, err := AuthorityFromMethod(files, method)
	if err != nil {
		return nil, explicit, err
	}
	if !explicit {
		if authority == nil {
			return nil, false, fmt.Errorf("%w: %s declares no authority policy", ErrNotAnOperation, method.FullName())
		}
		if lookup := receiptLookupOf(method); lookup != "" {
			return nil, false, fmt.Errorf("%w: %s is the receipt lookup of %s, which recovery calls and no person or agent does", ErrNotAnOperation, method.FullName(), lookup)
		}
		return defaultSpec(method, authority, audience)
	}
	spec, declaredErr := OperationFromMethod(method)
	if declaredErr != nil {
		return nil, true, declaredErr
	}
	spec.Declared = true
	if spec.Tool == nil {
		spec.Tool = DefaultTool(method, spec.EffectiveRecovery(), authority)
		if err = spec.Validate(); err != nil {
			return nil, true, err
		}
	}
	return spec, true, nil
}

// defaultSpec is a default derivation, reporting it as not declared.
func defaultSpec(method protoreflect.MethodDescriptor, authority *Authority, audience string) (*OperationSpec, bool, error) {
	spec, err := DefaultOperation(method, authority, audience)
	return spec, false, err
}

// receiptLookupOf names the operation a method is the receipt lookup of, or
// empty: a method another operation of its service names as its
// lookup_method, or one spelled Lookup<Op> beside a method <Op> of the same
// service. A receipt lookup is structurally recovery's, so it is never derived
// as an operation of its own — the one exclusion derivation makes without the
// owner saying so. Who may reach every other operation is a matter of grants.
func receiptLookupOf(method protoreflect.MethodDescriptor) string {
	service, ok := method.Parent().(protoreflect.ServiceDescriptor)
	if !ok {
		return ""
	}
	full := FullMethodName(method)
	methods := service.Methods()
	for i := 0; i < methods.Len(); i++ {
		sibling := methods.Get(i)
		options, _ := sibling.Options().(*descriptorpb.MethodOptions)
		if options == nil || !proto.HasExtension(options, runnablev0.E_Operation) {
			continue
		}
		if declared, isOperation := proto.GetExtension(options, runnablev0.E_Operation).(*runnablev0.Operation); isOperation && declared.GetLookupMethod() == full {
			return FullMethodName(sibling)
		}
	}
	if operation, found := strings.CutPrefix(string(method.Name()), "Lookup"); found && operation != "" {
		if sibling := methods.ByName(protoreflect.Name(operation)); sibling != nil {
			return FullMethodName(sibling)
		}
	}
	return ""
}

// validateLookupMethod ties the declared receipt lookup to a method that
// actually exists beside the operation. Whether it takes the effect id is not
// a descriptor fact — document-store carries it in call metadata — so what is
// checkable is checked: the same service publishes it, it is unary, and it is
// not the operation itself. That last one is the point: a package declaring
// RECOVERY_RECEIPT says an uncertain outcome is resolved by reading the
// receipt and never by re-running, and naming the operation as its own lookup
// would make recovery repeat the effect it exists to avoid repeating.
func validateLookupMethod(method protoreflect.MethodDescriptor, full, lookup string) error {
	if lookup == "" {
		return nil
	}
	if lookup == full {
		return fmt.Errorf("%w: %s names itself as its lookup method, so recovering an uncertain outcome would re-run the effect", ErrInvalid, full)
	}
	service, published := method.Parent().(protoreflect.ServiceDescriptor)
	prefix := "/" + string(method.Parent().FullName()) + "/"
	if !published || !strings.HasPrefix(lookup, prefix) {
		return fmt.Errorf("%w: %s declares lookup method %q, which %s does not publish; a receipt lookup is a method on the same service", ErrInvalid, full, lookup, method.Parent().FullName())
	}
	paired := service.Methods().ByName(protoreflect.Name(strings.TrimPrefix(lookup, prefix)))
	if paired == nil {
		return fmt.Errorf("%w: %s declares lookup method %q, which %s does not publish", ErrInvalid, full, lookup, service.FullName())
	}
	if paired.IsStreamingClient() || paired.IsStreamingServer() {
		return fmt.Errorf("%w: %s declares lookup method %q, which streams; a receipt is one answer", ErrInvalid, full, lookup)
	}
	return nil
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

// Validate checks a declared policy and authority a recovery could not widen.
// Required slots may supply all scopes; delivery still requires their resolution.
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
	for _, bound := range []struct {
		field string
		value uint64
	}{{"max_input_bytes", s.MaxInputBytes}, {"max_output_bytes", s.MaxOutputBytes}} {
		if bound.value > resources.DefaultRunnablePayloadBytes {
			return fmt.Errorf("%w: %s %s %d exceeds the inline payload bound %d; larger data travels as references", ErrInvalid, s.Method, bound.field, bound.value, resources.DefaultRunnablePayloadBytes)
		}
	}
	if s.Backoff < MinBackoff || s.Backoff > MaxBackoff {
		return fmt.Errorf("%w: %s backoff %s is outside %s..%s", ErrInvalid, s.Method, s.Backoff, MinBackoff, MaxBackoff)
	}
	if len(s.RetryableCodes) > MaxRetryableCodes {
		return fmt.Errorf("%w: %s names %d retryable codes; at most %d may be declared", ErrInvalid, s.Method, len(s.RetryableCodes), MaxRetryableCodes)
	}
	seen := make(map[string]struct{}, len(s.RetryableCodes))
	for _, name := range s.RetryableCodes {
		if !s.Codes.names(name) {
			return fmt.Errorf("%w: %s names retryable code %q, which is not a %s", ErrInvalid, s.Method, name, s.Codes)
		}
		if _, repeated := seen[name]; repeated {
			return fmt.Errorf("%w: %s names retryable code %q twice", ErrInvalid, s.Method, name)
		}
		seen[name] = struct{}{}
	}
	// Whether the mode is admissible against the operation's recovery is a
	// relation between two fields of the derived package, and is checked where
	// both are in view (validateExecution). Here the only question is whether
	// the owner named one at all.
	if s.Completion != basev0.RunnableExecution_COMPLETION_CALL && s.Completion != basev0.RunnableExecution_COMPLETION_SUBMIT {
		return fmt.Errorf("%w: %s declares no completion mode; %s and %s are the two, and an operation that did not say is not a synchronous one",
			ErrInvalid, s.Method, basev0.RunnableExecution_COMPLETION_CALL, basev0.RunnableExecution_COMPLETION_SUBMIT)
	}
	if err := ValidateToolExposure(s.Tool); err != nil {
		return err
	}
	if err := s.validateRecovery(); err != nil {
		return err
	}
	return s.authority().validate()
}

// EffectiveRecovery is the recovery the operation runs under. Unset is
// RECOVERY_RECEIPT: every operation declared before recovery could be stated
// resolved an uncertain outcome by its receipt, and reading an unstated one
// any other way could re-run an effect.
func (s *OperationSpec) EffectiveRecovery() basev0.RunnableExecution_Recovery {
	if s.Recovery == basev0.RunnableExecution_RECOVERY_UNKNOWN {
		return basev0.RunnableExecution_RECOVERY_RECEIPT
	}
	return s.Recovery
}

// validateRecovery holds a recomputed operation to what makes re-running it
// safe. A read has no effect to recover, so it has no receipt to read: lookup
// scopes or a lookup method on one would be authority and a route nothing
// uses. It answers in the call, because a submitted invocation's answer can be
// lost and only a receipt recovers it. And its tool says it only reads: a
// mutation presented as recomputable would be re-run on an uncertain outcome.
func (s *OperationSpec) validateRecovery() error {
	switch s.Recovery {
	case basev0.RunnableExecution_RECOVERY_UNKNOWN, basev0.RunnableExecution_RECOVERY_RECEIPT:
		return nil
	case basev0.RunnableExecution_RECOVERY_RECOMPUTE:
	default:
		return fmt.Errorf("%w: %s declares recovery %s, which is not one core defines", ErrInvalid, s.Method, s.Recovery)
	}
	switch {
	case len(s.LookupScopes) > 0 || s.LookupMethod != "":
		return fmt.Errorf("%w: %s is recomputed and declares a receipt lookup; a read has no effect receipt to look up", ErrInvalid, s.Method)
	case s.Completion != basev0.RunnableExecution_COMPLETION_CALL:
		return fmt.Errorf("%w: %s is recomputed and does not answer in the call; %s requires %s", ErrInvalid, s.Method, basev0.RunnableExecution_COMPLETION_SUBMIT, basev0.RunnableExecution_RECOVERY_RECEIPT)
	case s.Tool != nil && s.Tool.GetEffect() != runnablev0.ToolExposure_EFFECT_READ_ONLY:
		return fmt.Errorf("%w: %s is recomputed and its tool declares %s; only a read is safe to re-run", ErrInvalid, s.Method, s.Tool.GetEffect())
	}
	return nil
}

// authority is the audience-and-scopes half of the policy, as the same rule
// a binding's own authority is held to. One spelling on purpose: the relation
// protovalidate cannot express — that lookup scopes are a read-only subset of
// invoke scopes — is the one that matters most, and a second copy of it is a
// second place for it to be almost right.
func (s *OperationSpec) authority() authoritySpec {
	return authoritySpec{
		subject:            s.Method,
		origin:             "declaration",
		audience:           s.Audience,
		invokeScopes:       s.InvokeScopes,
		lookupScopes:       s.LookupScopes,
		requiredScopeSlots: s.RequiredScopeSlots,
		submitted:          s.Completion == basev0.RunnableExecution_COMPLETION_SUBMIT,
	}
}

// authoritySpec is one Work Context authority: the audience a child capability
// is minted for and the scopes bound for invoking and for reading a receipt.
// Fixed scopes are validated identically wherever they appear. A declaration
// with required slots may defer nonempty scope lists until resolution. Delivered
// policies refuse unresolved slots, and an installed binding's own authority
// has no slots, so concrete authority always requires invoke scopes.
//
// Lookup scopes are optional, under one rule wherever they appear — a
// declaration, a prepared policy, an installed binding — whatever the
// operation's recovery says or leaves unsaid. Empty, a receipt is read under
// the invoke scopes and only by the identity that made the effect: a receipt
// records its Work Context owner and acting principal, and AdmitReceiptLookup
// admits a lookup for that same identity alone. That binding, not the
// spelling of the declaration, is what makes an empty list safe, which is why
// the rule cannot depend on whether recovery was stated: the installed package
// carries only the recovery in effect. Declared, lookup scopes keep every rule
// below — read-only, and inside the invoke scopes.
type authoritySpec struct {
	// subject names what the authority belongs to, for the error message: an
	// operation spelling, or a binding's facility.
	subject string
	// origin says where the scopes were written — the owner's declaration or
	// an installation's binding — so a refusal names the record to correct.
	origin       string
	audience     string
	invokeScopes []*basev0.WorkScopeV1
	lookupScopes []*basev0.WorkScopeV1
	// requiredScopeSlots exists only while an owner declaration awaits selection.
	requiredScopeSlots []*runnablev0.ScopeSlot
	// submitted is an operation answering COMPLETION_SUBMIT, whose answer is
	// learnable only through its receipt. It keeps lookup scopes required until
	// a runtime mints a receipt lookup under the invoke scopes: installed with
	// none today, it would generate and install and never recover.
	submitted bool
}

func (a authoritySpec) validate() error {
	if err := validateScopeSlots(a.subject, a.requiredScopeSlots); err != nil {
		return err
	}
	if !boundedScopeValue(a.audience, MaxAudienceLength) {
		return fmt.Errorf("%w: %s audience %q is not a trust boundary the runtime can mint authority for", ErrInvalid, a.subject, a.audience)
	}
	if err := a.validateScopes("invoke_scopes", a.invokeScopes); err != nil {
		return err
	}
	if len(a.lookupScopes) == 0 && !a.submitted {
		// Identity-bound under the invoke scopes (see authoritySpec).
		return nil
	}
	if err := a.validateScopes("lookup_scopes", a.lookupScopes); err != nil {
		return err
	}
	for _, scope := range a.lookupScopes {
		if len(scope.GetActions()) != 1 || scope.GetActions()[0] != ReadOnlyScopeAction {
			return fmt.Errorf("%w: %s lookup scope %q is not read-only: recovering an outcome may only %q it", ErrInvalid, a.subject, scope.GetResourceKind(), ReadOnlyScopeAction)
		}
		if !workcontext.ScopeContained(scope, a.invokeScopes) {
			return fmt.Errorf("%w: %s lookup scope %q is not covered by its invoke scopes", ErrInvalid, a.subject, scope.GetResourceKind())
		}
	}
	return nil
}

// validateScopes applies the bounds the runtime applies when it installs the
// authority. An unauthorized operation is not a lenient one: with no audience
// and no scopes there is nothing to mint a child capability from, and the
// runtime refuses the installation rather than calling without authority.
func (a authoritySpec) validateScopes(at string, scopes []*basev0.WorkScopeV1) error {
	if len(scopes) == 0 && len(a.requiredScopeSlots) > 0 {
		// The resolver validates again after filling and clearing every slot.
		// This defers only absence; supplied fixed scopes keep all their checks.
		return nil
	}
	if len(scopes) == 0 {
		return fmt.Errorf("%w: %s declares no %s and no required_scope_slots; the owner must declare authority", ErrInvalid, a.subject, at)
	}
	if len(scopes) > MaxScopes {
		return fmt.Errorf("%w: %s declares %d %s; at most %d are allowed", ErrInvalid, a.subject, len(scopes), at, MaxScopes)
	}
	kinds := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		kind := scope.GetResourceKind()
		if !boundedScopeValue(kind, MaxScopeKindLength) {
			return fmt.Errorf("%w: %s declares a %s entry whose resource kind %q is not a usable name", ErrInvalid, a.subject, at, kind)
		}
		if _, exists := kinds[kind]; exists {
			return fmt.Errorf("%w: %s declares %s scope %q twice", ErrInvalid, a.subject, at, kind)
		}
		kinds[kind] = struct{}{}
		if len(scope.GetActions()) == 0 || len(scope.GetActions()) > MaxScopeActions {
			return fmt.Errorf("%w: %s %s scope %q names %d actions; between 1 and %d are required", ErrInvalid, a.subject, at, kind, len(scope.GetActions()), MaxScopeActions)
		}
		if owner := scope.GetResourceOwnerId(); owner != "" {
			return fmt.Errorf("%w: %s %s scope %q selects resource owner %q in its %s; %s", ErrInvalid, a.subject, at, kind, owner, a.origin, ownerScopedParent)
		}
		if len(scope.GetResourceIds()) > MaxScopeResourceIds {
			return fmt.Errorf("%w: %s %s scope %q names %d resource ids; at most %d may be declared", ErrInvalid, a.subject, at, kind, len(scope.GetResourceIds()), MaxScopeResourceIds)
		}
		if err := a.validateScopeValues(at, kind, "action", scope.GetActions(), MaxScopeActionLength); err != nil {
			return err
		}
		if err := a.validateScopeValues(at, kind, "resource id", scope.GetResourceIds(), MaxScopeResourceIdLength); err != nil {
			return err
		}
	}
	return nil
}

// ownerScopedParent is why no operation authority names a resource owner. An
// owner comes from the capability the operation is invoked under; the runtime
// carries it onto the operation child with workcontext.InheritResourceOwner,
// so a declaration, a binding or a slot selection naming one would pin every
// invocation to one owner whatever capability it ran under.
const ownerScopedParent = "owner-scoped parent: a resource owner is inherited from the capability the operation is invoked under " +
	"(workcontext.InheritResourceOwner, when the runtime mints the operation child), never written into an operation's authority"

func (a authoritySpec) validateScopeValues(at, kind, what string, values []string, bound int) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !boundedScopeValue(value, bound) {
			return fmt.Errorf("%w: %s %s scope %q names %s %q, which is not a usable value", ErrInvalid, a.subject, at, kind, what, value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%w: %s %s scope %q names %s %q twice", ErrInvalid, a.subject, at, kind, what, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

// boundedScopeValue mirrors the runtime's own admission of a scope string: a
// value it would refuse is a value this must not generate.
func boundedScopeValue(value string, bound int) bool {
	return value != "" && len(value) <= bound && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n\t")
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
func PackageFromMethod(files *protoregistry.Files, location *resources.RunnableLocation, owner ServiceOwner, fullMethod string) (pkg *basev0.RunnablePackage, spec *OperationSpec, err error) {
	var output *basev0.RunnableSchema
	if location == nil || location.Identity == nil {
		return nil, nil, fmt.Errorf("%w: the release identity a derived package carries is required", ErrInvalid)
	}
	method, err := methodByFullName(files, fullMethod)
	if err != nil {
		return nil, nil, err
	}
	spec, explicit, err := OperationForMethod(files, method, owner.audience())
	if err != nil {
		return nil, nil, err
	}
	input, err := ProjectMessage(method.Input())
	if err == nil {
		var projected *basev0.RunnableSchema
		if projected, err = ProjectMessage(method.Output()); err == nil {
			output = projected
		}
	}
	if err != nil {
		if !explicit {
			// An owner that asked for nothing is not refused for a payload it
			// never offered as an operation's: the method stays a call.
			return nil, nil, fmt.Errorf("%w: %w", ErrNotDerivable, err)
		}
		return nil, nil, err
	}
	identity, err := location.Identity.Proto()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: identity: %v", ErrInvalid, err)
	}
	pkg, err = PreparePackage(&basev0.RunnablePackage{
		Schema:   PackageSchemaV1,
		Identity: identity,
		Agent:    owner.Agent,
		Contract: &basev0.RunnableContract{Protocol: resources.RunnableServiceProtocolV1, Input: input, Output: output},
		Execution: &basev0.RunnableExecution{
			Facilities:     []*basev0.RunnableFacility{{Kind: basev0.RunnableFacility_SERVICE}},
			Timeout:        durationpb.New(spec.TotalTimeout),
			Cancellation:   basev0.RunnableExecution_CANCELLATION_NONE,
			Recovery:       spec.EffectiveRecovery(),
			Completion:     spec.Completion,
			MaxInputBytes:  payloadBound(spec.MaxInputBytes),
			MaxOutputBytes: payloadBound(spec.MaxOutputBytes),
		},
		ServiceOperations: []*basev0.RunnableServiceOperation{{
			Module:        owner.Module,
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

// payloadBound is a declared inline payload bound, or the default when the
// operation declares none.
func payloadBound(declared uint64) uint64 {
	if declared == 0 {
		return resources.DefaultRunnablePayloadBytes
	}
	return declared
}
