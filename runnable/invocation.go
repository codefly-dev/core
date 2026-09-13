package runnable

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	// Result is the document found at the result path, nil when nothing is
	// there. The harness renames its document into place, so a document that
	// exists is a complete one.
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
	if err := (protojson.UnmarshalOptions{}).Unmarshal(document, result); err != nil {
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
	if observed.Ended == EndedOnCancel && pkg.GetExecution().GetCancellation() != basev0.RunnableExecution_CANCELLATION_SIGNAL {
		return nil, fmt.Errorf("%w: package declares cancellation %s, so a launcher must not interrupt it",
			ErrInvalid, pkg.GetExecution().GetCancellation())
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
	result, resultErr := parseObservedResult(observed.Result, prepared, pkg)
	switch {
	case result != nil:
		completion.Result = result
		if result.GetStatus() == basev0.RunnableResult_SUCCEEDED {
			completion.Outcome = basev0.RunnableCompletion_SUCCEEDED
		} else {
			completion.Outcome = basev0.RunnableCompletion_FAILED
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
	return completion, nil
}

// OutcomeIsCertain says whether the outcome proves what happened to the
// effect. SUCCEEDED and FAILED do: the harness observed its own completion.
// Every other outcome leaves the effect unproven, and the package's recovery
// policy says what it may be resolved with — recomputed, or looked up by the
// invocation's effect identity.
func OutcomeIsCertain(outcome basev0.RunnableCompletion_Outcome) bool {
	return outcome == basev0.RunnableCompletion_SUCCEEDED || outcome == basev0.RunnableCompletion_FAILED
}

func parseObservedResult(document []byte, inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage) (*basev0.RunnableResult, error) {
	if document == nil {
		return nil, nil
	}
	return ParseResult(document, inv, pkg)
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
	if !inv.GetDeadline().AsTime().After(inv.GetIssuedAt().AsTime()) {
		return fmt.Errorf("%w: invocation deadline is not after the instant it was issued", ErrInvalid)
	}
	needsEffect := pkg.GetExecution().GetRecovery() == basev0.RunnableExecution_RECOVERY_RECEIPT
	if needsEffect && inv.GetEffectId() == "" {
		return fmt.Errorf("%w: package declares %s, so the invocation must carry the effect identity an uncertain outcome is resolved with",
			ErrInvalid, basev0.RunnableExecution_RECOVERY_RECEIPT)
	}
	if !needsEffect && inv.GetEffectId() != "" {
		return fmt.Errorf("%w: package declares %s, which has no effect to look up, so the invocation must carry no effect identity",
			ErrInvalid, pkg.GetExecution().GetRecovery())
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
