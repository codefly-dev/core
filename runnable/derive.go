package runnable

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
)

// AuthorityPolicyExtension is the method option an owner declares what a call
// must hold with: authority.hop.v1.policy, published by the authority's hop
// library. Every method a module serves behind the hop carries it, so it is
// the one declaration an operation's effect and authority are derived from.
// Core reads it by name from the descriptors the contract ships, never through
// generated Go types, so a module's contract is the whole of the input.
const AuthorityPolicyExtension protoreflect.FullName = "authority.hop.v1.policy"

// ErrNotDerivable answers a method that declares an authority policy but that
// no default derivation turns into an operation: a stream, a policy that
// requires nothing, a payload outside the bounded profile. It is a notice, not
// a failure — the method stays what it was, a call its own clients make — and
// it is distinct from ErrNotAnOperation, which says the method asked nothing.
var ErrNotDerivable = errors.New("method cannot be derived as a Codefly runnable operation by default")

// Default execution policy of a derived operation. A read is safe to re-run,
// so it retries on an unavailable owner. A mutation is attempted once: its
// receipt is what makes a retry exactly-once, and an owner that serves no
// receipt guard would apply a retried effect twice, so the default never
// assumes one. An uncertain mutation ends unknown and is resolved by its
// receipt, never by a second attempt.
const (
	DefaultAttemptTimeout   = 30 * time.Second
	DefaultTotalTimeout     = 90 * time.Second
	DefaultBackoff          = time.Second
	DefaultReadAttempts     = 3
	DefaultMutationAttempts = 1
)

// DefaultReadRetryableCodes are the outcomes a derived read is retried on.
var DefaultReadRetryableCodes = []string{"UNAVAILABLE"}

// Authority is the authority policy one method declares: the actions on each
// resource kind a call must hold, or that any checked caller may call it.
type Authority struct {
	Requires         []*basev0.WorkScopeV1
	AnyCheckedCaller bool
}

// ReadOnly reports a policy whose every requirement is the one read-only
// action. That is what makes an operation a read: the owner's hop lets a call
// through holding nothing but read, so the call cannot change what it reads.
// A policy requiring nothing says nothing about effect and is not read-only.
func (a *Authority) ReadOnly() bool {
	if a == nil || len(a.Requires) == 0 {
		return false
	}
	for _, requirement := range a.Requires {
		if len(requirement.GetActions()) != 1 || requirement.GetActions()[0] != ReadOnlyScopeAction {
			return false
		}
	}
	return true
}

// AuthorityFromMethod reads the authority policy a method declares, resolving
// the option against the descriptors the method was published with. A method
// declaring none answers nil.
func AuthorityFromMethod(files *protoregistry.Files, method protoreflect.MethodDescriptor) (*Authority, error) {
	options, _ := method.Options().(*descriptorpb.MethodOptions)
	if options == nil {
		return nil, nil
	}
	policy, err := extensionValue(files, options, AuthorityPolicyExtension)
	if err != nil || policy == nil {
		return nil, err
	}
	fields := policy.Descriptor().Fields()
	requiresField, anyField := fields.ByName("requires"), fields.ByName("any_checked_caller")
	if requiresField == nil || anyField == nil || !requiresField.IsList() || requiresField.Message() == nil {
		return nil, fmt.Errorf("%w: %s's %s is not the authority policy core reads (requires, any_checked_caller)", ErrInvalid, FullMethodName(method), AuthorityPolicyExtension)
	}
	authority := &Authority{AnyCheckedCaller: policy.Get(anyField).Bool()}
	requirements := policy.Get(requiresField).List()
	kindField, actionsField := requiresField.Message().Fields().ByName("resource_kind"), requiresField.Message().Fields().ByName("actions")
	if kindField == nil || actionsField == nil || !actionsField.IsList() {
		return nil, fmt.Errorf("%w: %s's %s requirement is not (resource_kind, actions)", ErrInvalid, FullMethodName(method), AuthorityPolicyExtension)
	}
	for i := 0; i < requirements.Len(); i++ {
		requirement := requirements.Get(i).Message()
		scope := &basev0.WorkScopeV1{ResourceKind: requirement.Get(kindField).String()}
		actions := requirement.Get(actionsField).List()
		for j := 0; j < actions.Len(); j++ {
			scope.Actions = append(scope.Actions, actions.Get(j).String())
		}
		authority.Requires = append(authority.Requires, scope)
	}
	// The hop refuses every call to a method declaring both or neither, so a
	// derivation from either would install an operation no call reaches.
	if authority.AnyCheckedCaller == (len(authority.Requires) > 0) {
		return nil, fmt.Errorf("%w: %s's %s declares both or neither of requires and any_checked_caller", ErrInvalid, FullMethodName(method), AuthorityPolicyExtension)
	}
	return authority, nil
}

// excluded reports the codefly.runnable.v0.exclude option.
func excluded(options *descriptorpb.MethodOptions) bool {
	if options == nil || !proto.HasExtension(options, runnablev0.E_Exclude) {
		return false
	}
	exclude, isBool := proto.GetExtension(options, runnablev0.E_Exclude).(bool)
	return isBool && exclude
}

// extensionValue finds an extension on options by its full name. A program
// that links the extension's Go type sees it parsed; one that does not (the
// usual case for a contract read off disk) sees unknown bytes, which are
// re-read against the published descriptors.
func extensionValue(files *protoregistry.Files, options *descriptorpb.MethodOptions, name protoreflect.FullName) (protoreflect.Message, error) {
	var found protoreflect.Message
	find := func(message protoreflect.Message) {
		message.Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
			if fd.IsExtension() && fd.FullName() == name {
				found = value.Message()
				return false
			}
			return true
		})
	}
	find(options.ProtoReflect())
	if found != nil || len(options.ProtoReflect().GetUnknown()) == 0 || files == nil {
		return found, nil
	}
	if _, err := files.FindDescriptorByName(name); err != nil {
		// The contract does not ship the extension's declaration, so the bytes
		// are some other option.
		return nil, nil //nolint:nilerr // absence of the declaration is absence of the policy
	}
	raw, err := proto.Marshal(options)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot re-read method options: %v", ErrInvalid, err)
	}
	reread := &descriptorpb.MethodOptions{}
	if err = (proto.UnmarshalOptions{Resolver: dynamicpb.NewTypes(files)}).Unmarshal(raw, reread); err != nil {
		return nil, fmt.Errorf("%w: cannot read %s: %v", ErrInvalid, name, err)
	}
	find(reread.ProtoReflect())
	return found, nil
}

// DefaultOperation is the operation a method declaring an authority policy
// derives with no runnable option: the policy says what the call must hold
// and, through the actions it requires, whether the call only reads.
//
// A read recomputes: no receipt, no lookup, invoked under exactly the scopes
// the policy requires. Anything else is a mutation resolved by its receipt,
// invoked under exactly the scopes the policy requires too: it states
// RECOVERY_RECEIPT and declares no lookup scopes, so its receipt is read under
// those same invoke scopes — the caller reads the receipt of its own effect —
// through the SDK's generic receipt lookup. Nothing is added to what the hop
// requires, so a module that contributes no read permission on a kind it
// writes can still mint the mutation.
func DefaultOperation(method protoreflect.MethodDescriptor, authority *Authority, audience string) (*OperationSpec, error) {
	full := FullMethodName(method)
	if method.IsStreamingClient() || method.IsStreamingServer() {
		return nil, fmt.Errorf("%w: %s streams, and an operation is one finite call", ErrNotDerivable, full)
	}
	if authority == nil || len(authority.Requires) == 0 {
		return nil, fmt.Errorf("%w: %s requires no authority, so there is nothing to mint the call's capability from", ErrNotDerivable, full)
	}
	spec := &OperationSpec{
		Method:         full,
		AttemptTimeout: DefaultAttemptTimeout,
		TotalTimeout:   DefaultTotalTimeout,
		Backoff:        DefaultBackoff,
		Audience:       audience,
		Completion:     basev0.RunnableExecution_COMPLETION_CALL,
	}
	if authority.ReadOnly() {
		spec.Recovery = basev0.RunnableExecution_RECOVERY_RECOMPUTE
		spec.MaxAttempts = DefaultReadAttempts
		spec.RetryableCodes = slices.Clone(DefaultReadRetryableCodes)
		spec.InvokeScopes = clonedScopes(authority.Requires)
	} else {
		spec.Recovery = basev0.RunnableExecution_RECOVERY_RECEIPT
		spec.MaxAttempts = DefaultMutationAttempts
		spec.InvokeScopes = clonedScopes(authority.Requires)
	}
	spec.Tool = DefaultTool(method, spec.Recovery, authority)
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return spec, nil
}

// DefaultTool presents an operation as a tool from what the method already
// says: its name, its leading doc comment and its effect. A recomputed
// operation is read-only; otherwise the authority policy decides, and an
// operation whose policy is not read-only — or that declares none — is a
// mutation, because omission is never read-only.
func DefaultTool(method protoreflect.MethodDescriptor, recovery basev0.RunnableExecution_Recovery, authority *Authority) *runnablev0.ToolExposure {
	effect := runnablev0.ToolExposure_EFFECT_MUTATION
	if recovery == basev0.RunnableExecution_RECOVERY_RECOMPUTE || authority.ReadOnly() {
		effect = runnablev0.ToolExposure_EFFECT_READ_ONLY
	}
	description := toolDescription(method.ParentFile().SourceLocations().ByDescriptor(method).LeadingComments)
	if description == "" {
		description = fmt.Sprintf("Calls %s on %s.", method.Name(), method.Parent().FullName())
	}
	return &runnablev0.ToolExposure{Name: toolName(string(method.Name())), Description: description, Effect: effect}
}

// toolDescription normalises a doc comment into the bounded description a
// tool carries: its first paragraph only — what the method does; the
// paragraphs after it are for the owner's maintainers (wiring, mesh and
// authority detail a model has no use for) — each line trimmed and joined, cut
// at a rune boundary within the bound.
func toolDescription(comment string) string {
	lines := strings.Split(strings.ReplaceAll(comment, "\x00", ""), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(kept) > 0 {
				break
			}
			continue
		}
		kept = append(kept, line)
	}
	text := strings.TrimSpace(strings.Join(kept, " "))
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "")
	}
	const bound = 4096
	for len(text) > bound {
		_, size := utf8.DecodeLastRuneInString(text)
		text = text[:len(text)-size]
	}
	return strings.TrimSpace(text)
}

// toolName keeps the characters a tool name admits and starts it with a
// letter: a method name already is one, a route needs spelling.
func toolName(raw string) string {
	var name strings.Builder
	for _, c := range []byte(raw) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			name.WriteByte(c)
		case name.Len() > 0 && (c >= '0' && c <= '9' || c == '_' || c == '-'):
			name.WriteByte(c)
		case name.Len() > 0:
			name.WriteByte('_')
		}
	}
	out := strings.Trim(name.String(), "_")
	if out == "" {
		return "operation"
	}
	return BoundedName(out, maxToolName, "_")
}

// maxToolName is the longest tool name ValidateToolExposure admits.
const maxToolName = 64

// BoundedName shortens a derived name to at most limit bytes, deterministically
// and without collapsing two long names into one: a stable prefix, the
// separator, and the first eight hex digits of the full name's SHA-256. A name
// within the limit is returned unchanged, so a short name never moves.
func BoundedName(name string, limit int, separator string) string {
	if len(name) <= limit {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	suffix := separator + hex.EncodeToString(sum[:])[:8]
	prefix := strings.TrimRight(name[:limit-len(suffix)], separator+"-_")
	return prefix + suffix
}

// PreparedTool presents a delivered binding whose policy names no tool, as
// one delivered before every operation carried its presentation: named after
// its operation, read-only exactly when it recomputes.
func preparedTool(binding *runnablev0.PreparedBinding) *runnablev0.ToolExposure {
	spelling := binding.GetOperation().GetSpelling()
	name := spelling
	if cut := strings.LastIndex(spelling, "/"); cut >= 0 && strings.HasPrefix(spelling, "/") {
		name = spelling[cut+1:]
	}
	effect := runnablev0.ToolExposure_EFFECT_MUTATION
	if binding.GetPolicy().GetRecovery() == basev0.RunnableExecution_RECOVERY_RECOMPUTE {
		effect = runnablev0.ToolExposure_EFFECT_READ_ONLY
	}
	return &runnablev0.ToolExposure{Name: toolName(name), Description: "Calls " + spelling + ".", Effect: effect}
}
