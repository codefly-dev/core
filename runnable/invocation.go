package runnable

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// The environment a launcher adds to the bound artifact's command. Nothing
// else in the environment is part of the framing: configuration and
// credentials reach the process the way the facility resolves them.
const (
	// EnvProtocol names the framing, so a harness refuses a launcher it does
	// not implement rather than misreading its documents.
	EnvProtocol = "CODEFLY__RUNNABLE_PROTOCOL"
	// EnvInvocationPath is the absolute path of the invocation document the
	// launcher wrote before starting the process.
	EnvInvocationPath = "CODEFLY__RUNNABLE_INVOCATION"
	// EnvResultPath is the absolute path the harness writes its result to, by
	// renaming a temporary file in the same directory onto it.
	EnvResultPath = "CODEFLY__RUNNABLE_RESULT"
)

// EndReason says whether the launcher ended the process itself.
type EndReason int

const (
	// EndedOnItsOwn means the process ended without the launcher ending it.
	EndedOnItsOwn EndReason = iota
	// EndedOnDeadline means the launcher ended it because the deadline passed.
	EndedOnDeadline
	// EndedOnCancel means the launcher interrupted it at the caller's request.
	EndedOnCancel
)

// LogStream is what one log stream produced.
type LogStream struct {
	// Bytes is how much the launcher captured, at most the package's
	// max_log_bytes.
	Bytes uint64
	// Truncated means the stream produced more than the bound allowed.
	Truncated bool
}

// Observation is what a launcher saw of one invocation's process: raw process
// facts only, which Complete turns into the typed outcome.
type Observation struct {
	// StartedAt and EndedAt bracket the process.
	StartedAt time.Time
	EndedAt   time.Time
	// ResultPresent says a document was found at the result path. It is
	// separate from Result because Go routinely turns a nil slice into an empty
	// one, and "no result" and "an empty result" are different outcomes.
	ResultPresent bool
	// Result is that document. The harness renames it into place, so a document
	// that exists is a complete one.
	Result []byte
	// ExitCode is the process exit status, meaningful when Signal is empty.
	ExitCode int32
	// Signal names the signal the process died on.
	Signal string
	// Ended says whether the launcher ended the process itself.
	Ended EndReason
	// Stdout and Stderr record what the log streams produced.
	Stdout LogStream
	Stderr LogStream
}

// PrepareInvocation clones inv and validates it against the verified package
// it invokes. Unlike a package or a binding an invocation is not
// content-addressed: it names one run of an installed binding rather than an
// immutable installation fact, so there is no digest to populate.
func PrepareInvocation(inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage) (*basev0.RunnableInvocation, error) {
	if inv == nil {
		return nil, fmt.Errorf("%w: invocation is required", ErrInvalid)
	}
	if err := VerifyPackage(pkg); err != nil {
		return nil, err
	}
	prepared := &basev0.RunnableInvocation{}
	proto.Merge(prepared, inv)
	if err := validateInvocation(prepared, pkg); err != nil {
		return nil, err
	}
	return prepared, nil
}

// EncodeInvocation returns the document a launcher writes for inv: proto3 JSON
// spelled with the proto field names, so a harness generating bindings from
// the same sources reads it without a hand-written spelling of the framing.
func EncodeInvocation(inv *basev0.RunnableInvocation) ([]byte, error) {
	encoded, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(inv)
	if err != nil {
		return nil, fmt.Errorf("%w: encode invocation: %v", ErrInvalid, err)
	}
	return encoded, nil
}

// InvocationEnvironment is the framing environment for one invocation. The
// protocol comes from the invocation itself, so the environment can never name
// a framing the document is not written in.
func InvocationEnvironment(inv *basev0.RunnableInvocation, invocationPath, resultPath string) map[string]string {
	return map[string]string{
		EnvProtocol:       inv.GetProtocol(),
		EnvInvocationPath: invocationPath,
		EnvResultPath:     resultPath,
	}
}

// ParseResult decodes and validates the document a harness wrote for inv. Its
// error is what makes an outcome INVALID_OUTPUT rather than a completion, so
// every launcher draws that line in the same place.
func ParseResult(document []byte, inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage) (*basev0.RunnableResult, error) {
	result := &basev0.RunnableResult{}
	// A field this core does not know belongs to a harness generated from newer
	// sources. Rejecting it would turn every invocation of that runnable into an
	// uncertain outcome, recomputing pure work and re-reconciling effectful work
	// that in fact completed. Unknown fields are dropped here, unlike on the
	// installation facts, whose digest would silently stop covering them.
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(document, result); err != nil {
		return nil, fmt.Errorf("%w: result is not a %s document: %v", ErrInvalid, ProtocolV1, err)
	}
	if err := validateResult(result, inv, pkg); err != nil {
		return nil, err
	}
	return result, nil
}

// Complete classifies one observed process into the typed outcome. The rule
// lives here rather than in each launcher so that a timeout, a crash and a
// harness that never wrote its result mean the same thing everywhere.
//
// Precedence, highest first:
//
//  1. a valid result for this invocation, so an outcome the harness already
//     proved is never discarded because the launcher also ended the process;
//  2. the launcher ending the process, which explains it better than the exit
//     status the kill produced;
//  3. a result document that is not this invocation's valid result;
//  4. a non-zero exit or a signal;
//  5. nothing at all from a process that exited successfully.
func Complete(inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage, observed Observation) (*basev0.RunnableCompletion, error) {
	prepared, err := PrepareInvocation(inv, pkg)
	if err != nil {
		return nil, err
	}
	if err := validateObservation(observed); err != nil {
		return nil, err
	}
	completion := &basev0.RunnableCompletion{
		Protocol:     prepared.GetProtocol(),
		Runnable:     prepared.GetRunnable(),
		InvocationId: prepared.GetInvocationId(),
		ExitCode:     observed.ExitCode,
		Signal:       observed.Signal,
		StartedAt:    timestamppb.New(observed.StartedAt),
		EndedAt:      timestamppb.New(observed.EndedAt),
		Logs: &basev0.RunnableLogs{
			Stdout: &basev0.RunnableLogStream{Bytes: observed.Stdout.Bytes, Truncated: observed.Stdout.Truncated},
			Stderr: &basev0.RunnableLogStream{Bytes: observed.Stderr.Bytes, Truncated: observed.Stderr.Truncated},
		},
	}
	result, resultErr := parseObservedResult(observed, prepared, pkg)
	switch {
	case result != nil:
		completion.Result = result
		completion.Outcome = reportedOutcome[result.GetStatus()]
		if completion.Outcome == basev0.RunnableCompletion_CANCELED {
			completion.Message = "the harness reported that an interruption stopped it"
		}
	case observed.Ended == EndedOnDeadline:
		completion.Outcome = basev0.RunnableCompletion_TIMED_OUT
		completion.Message = fmt.Sprintf("deadline %s passed before the harness reported a result", prepared.GetDeadline().AsTime().Format(time.RFC3339))
	case observed.Ended == EndedOnCancel:
		completion.Outcome = basev0.RunnableCompletion_CANCELED
		completion.Message = "the launcher interrupted the invocation before the harness reported a result"
	case resultErr != nil:
		completion.Outcome = basev0.RunnableCompletion_INVALID_OUTPUT
		completion.Message = resultErr.Error()
	case observed.Signal != "" || observed.ExitCode != 0:
		completion.Outcome = basev0.RunnableCompletion_CRASHED
		completion.Message = crashMessage(observed)
	default:
		completion.Outcome = basev0.RunnableCompletion_MISSING_OUTPUT
		completion.Message = "the process exited successfully without writing a result document"
	}
	if observed.Ended == EndedOnCancel && pkg.GetExecution().GetCancellation() != basev0.RunnableExecution_CANCELLATION_SIGNAL {
		completion.Message = strings.TrimSpace(fmt.Sprintf("the launcher interrupted an invocation whose package declares cancellation %s. %s",
			pkg.GetExecution().GetCancellation(), completion.GetMessage()))
	}
	return completion, nil
}

// reportedOutcome maps what a harness reported to the outcome it means. An
// interruption the harness handled is the same uncertain end as one the
// launcher had to force.
var reportedOutcome = map[basev0.RunnableResult_Status]basev0.RunnableCompletion_Outcome{
	basev0.RunnableResult_SUCCEEDED:   basev0.RunnableCompletion_SUCCEEDED,
	basev0.RunnableResult_FAILED:      basev0.RunnableCompletion_FAILED,
	basev0.RunnableResult_INTERRUPTED: basev0.RunnableCompletion_CANCELED,
}

// OutcomeIsCertain says whether the outcome proves what happened to the
// effect. SUCCEEDED and FAILED do: the harness observed its own completion.
// Every other outcome leaves the effect unproven, and the package's recovery
// policy says what it may be resolved with — recomputed, or looked up by the
// invocation's effect identity.
func OutcomeIsCertain(outcome basev0.RunnableCompletion_Outcome) bool {
	return outcome == basev0.RunnableCompletion_SUCCEEDED || outcome == basev0.RunnableCompletion_FAILED
}

func parseObservedResult(observed Observation, inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage) (*basev0.RunnableResult, error) {
	if !observed.ResultPresent {
		return nil, nil
	}
	return ParseResult(observed.Result, inv, pkg)
}

// validateObservation rejects process facts a completion cannot be built from.
// A zero time satisfies the wire contract's "required" and would be persisted
// as year one, and an end before a start describes no process at all.
func validateObservation(observed Observation) error {
	if observed.StartedAt.IsZero() || observed.EndedAt.IsZero() {
		return fmt.Errorf("%w: observation must bracket the process with a start and an end", ErrInvalid)
	}
	if observed.EndedAt.Before(observed.StartedAt) {
		return fmt.Errorf("%w: observation ends at %s, before it starts at %s",
			ErrInvalid, observed.EndedAt.Format(time.RFC3339), observed.StartedAt.Format(time.RFC3339))
	}
	if !observed.ResultPresent && len(observed.Result) > 0 {
		return fmt.Errorf("%w: observation carries a result document but does not report one as present", ErrInvalid)
	}
	return nil
}

func crashMessage(observed Observation) string {
	if observed.Signal != "" {
		return fmt.Sprintf("the process died on %s without writing a result document", observed.Signal)
	}
	return fmt.Sprintf("the process exited %d without writing a result document", observed.ExitCode)
}

func validateInvocation(inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage) error {
	if err := validator.Validate(inv); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if inv.GetProtocol() != pkg.GetContract().GetProtocol() {
		return fmt.Errorf("%w: invocation protocol %q is not the package's %q", ErrInvalid, inv.GetProtocol(), pkg.GetContract().GetProtocol())
	}
	if !proto.Equal(inv.GetRunnable(), pkg.GetIdentity()) {
		return fmt.Errorf("%w: invocation names another release than the package it runs", ErrInvalid)
	}
	budget := inv.GetDeadline().AsTime().Sub(inv.GetIssuedAt().AsTime())
	if budget <= 0 {
		return fmt.Errorf("%w: invocation deadline is not after the instant it was issued", ErrInvalid)
	}
	// The declared timeout bounds one invocation's duration. A launcher
	// computing a deadline of its own may shorten that, never overrule it.
	if declared := pkg.GetExecution().GetTimeout().AsDuration(); budget > declared {
		return fmt.Errorf("%w: invocation allows %s but the package declares a timeout of %s", ErrInvalid, budget, declared)
	}
	if pkg.GetExecution().GetRecovery() == basev0.RunnableExecution_RECOVERY_RECEIPT && inv.GetEffectId() == "" {
		return fmt.Errorf("%w: package declares %s, so the invocation must carry the effect identity an uncertain outcome is resolved with",
			ErrInvalid, basev0.RunnableExecution_RECOVERY_RECEIPT)
	}
	return validatePayload("input", inv.GetInput(), pkg.GetExecution().GetMaxInputBytes())
}

func validateResult(result *basev0.RunnableResult, inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage) error {
	if err := validator.Validate(result); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if result.GetProtocol() != inv.GetProtocol() {
		return fmt.Errorf("%w: result protocol %q is not the invocation's %q", ErrInvalid, result.GetProtocol(), inv.GetProtocol())
	}
	if result.GetInvocationId() != inv.GetInvocationId() {
		return fmt.Errorf("%w: result belongs to invocation %q, not %q", ErrInvalid, result.GetInvocationId(), inv.GetInvocationId())
	}
	switch result.GetStatus() {
	case basev0.RunnableResult_SUCCEEDED:
		if result.GetError() != nil {
			return fmt.Errorf("%w: a succeeded result carries no error", ErrInvalid)
		}
		return validatePayload("output", result.GetOutput(), pkg.GetExecution().GetMaxOutputBytes())
	case basev0.RunnableResult_FAILED:
		if len(result.GetOutput()) > 0 {
			return fmt.Errorf("%w: a failed result carries no output", ErrInvalid)
		}
		if result.GetError().GetCode() == "" {
			return fmt.Errorf("%w: a failed result requires the handler's failure code", ErrInvalid)
		}
		return nil
	case basev0.RunnableResult_INTERRUPTED:
		if len(result.GetOutput()) > 0 {
			return fmt.Errorf("%w: an interrupted result carries no output", ErrInvalid)
		}
		return nil
	default:
		return fmt.Errorf("%w: result status %s is not a completion a harness can report", ErrInvalid, result.GetStatus())
	}
}

// validatePayload frames a payload without type-checking it: the launcher
// bounds the document and proves it is the object shape the profile promises,
// while the harness checks it against the typed bindings generated from the
// contract.
func validatePayload(kind string, payload []byte, bound uint64) error {
	if uint64(len(payload)) > bound {
		return fmt.Errorf("%w: %s is %d bytes, over the declared bound of %d", ErrInvalid, kind, len(payload), bound)
	}
	trimmed := bytes.TrimLeft(payload, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(payload) {
		return fmt.Errorf("%w: %s must be one JSON object", ErrInvalid, kind)
	}
	return nil
}
