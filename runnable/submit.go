package runnable

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/resources"
)

// Submit mode, from the caller's side.
//
// A call-mode invocation is answered by its reply and this file has nothing to
// do with it. A submit-mode invocation is answered four times over — an
// acceptance now, heartbeats while it runs, a terminal answer later, and a
// status read for the caller whose terminal answer never arrived — and the one
// thing every one of those shapes has in common is that none of them is
// evidence about the effect. Only the receipt is, which is why COMPLETION_SUBMIT
// requires RECOVERY_RECEIPT.
//
// Everything here is the caller's half. What an owner must serve is stated in
// docs/runnable.md and enforced where an owner is generated; core validates what
// arrives and draws the same proven/unproven line served mode draws, so a
// submitted invocation and a called one are recovered by one rule rather than
// two.

// The schemas the four submit shapes carry. A caller reads the acceptance's
// schema as a self-check — which shape a reply has follows from the operation's
// declared completion mode, not from the body — but on the callback address it
// is a discriminator, because the heartbeat and the completion arrive on one
// URL and nothing outside them says which is which.
const (
	// AcceptanceSchemaV1 is the reply to a submit-mode invocation.
	AcceptanceSchemaV1 = "codefly.runnable-acceptance/v1"
	// HeartbeatSchemaV1 is one liveness report from an owner holding accepted work.
	HeartbeatSchemaV1 = "codefly.runnable-heartbeat/v1"
	// CallbackSchemaV1 is the terminal answer delivered to a caller that is no
	// longer waiting.
	CallbackSchemaV1 = "codefly.runnable-callback/v1"
	// StatusSchemaV1 is what an owner can say about work it accepted.
	StatusSchemaV1 = "codefly.runnable-status/v1"
)

// The wire facts of submit mode that served mode does not already pin. The
// invoke route, the four per-call headers and the receipt route are the same
// ones a call-mode invocation uses: submit changes what the reply proves, not
// how the call is made.
const (
	// ServedStatusProcedure is the status route, beside the operation's own.
	// It takes a RunnableStatusRequest naming the handle and answers a
	// RunnableStatus. An owner serving submit mode serves it whether or not the
	// caller presented a callback address: a callback can always be lost, so
	// this is the recovery path rather than a second mechanism.
	ServedStatusProcedure = "/codefly.runnable.v0.Runnable/Status"
	// CallbackHeader carries the absolute URL an owner POSTs its reports to. Its
	// presence is the caller saying "push to me here"; its absence is the caller
	// undertaking to read the status itself. An owner never chooses.
	CallbackHeader = "Codefly-Runnable-Callback"
	// CallbackAudienceHeader carries the trust boundary the owner mints its own
	// capability for when it reports. The report is authenticated by the
	// reporting workload's identity and by nothing else — there is no shared
	// secret anywhere in this contract — and an audience is a trust boundary
	// rather than a route, so it is stated instead of derived from the URL host.
	CallbackAudienceHeader = "Codefly-Runnable-Callback-Audience"
)

// Accepted is what one submit call concluded. Exactly one field is set: an
// owner either took responsibility for the work, in which case nothing is known
// about the effect yet, or the call ended some other way, which is classified
// by the served taxonomy.
//
// There is deliberately no outcome meaning "submitted successfully". An
// acceptance that was read as a success would be a completion the owner never
// gave, and the caller would stop waiting for the answer that proves anything.
type Accepted struct {
	// Acceptance is the owner's acceptance, naming the handle everything the
	// caller says about this work from here on is keyed by.
	Acceptance *runnablev0.RunnableAcceptance
	// Completion is set when the call did not accept: the same outcomes a
	// call-mode invocation reaches, minus SERVED_SUCCEEDED, which a submit call
	// cannot reach because its reply is not the work's answer.
	Completion *basev0.RunnableServedCompletion
}

// ClassifySubmit turns what a caller saw of a submit call into either an
// acceptance or a served completion.
//
// It errors only on a call it cannot record at all, the line ClassifyServed
// draws: every way an owner can answer is an outcome, because a caller told
// "this was not a real call" may submit the same effect a second time.
func ClassifySubmit(inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage, observed Call) (Accepted, error) {
	if completion := pkg.GetExecution().GetCompletion(); completion != basev0.RunnableExecution_COMPLETION_SUBMIT {
		return Accepted{}, fmt.Errorf("%w: package declares %s, so its answer is the reply to the call and there is no acceptance to read",
			ErrInvalid, completion)
	}
	completion, err := ClassifyServed(inv, pkg, observed)
	if err != nil {
		return Accepted{}, err
	}
	// ClassifyServed reaches SERVED_SUCCEEDED and SERVED_INVOCATION_INTEGRITY by
	// reading the answer as the operation's own result. A submit reply is not
	// that document, so the parse it did says nothing here and the acceptance is
	// read instead; every other outcome is about how the call ended and holds
	// unchanged.
	switch completion.GetOutcome() {
	case basev0.RunnableServedOutcome_SERVED_SUCCEEDED, basev0.RunnableServedOutcome_SERVED_INVOCATION_INTEGRITY:
	default:
		return Accepted{Completion: completion}, nil
	}
	acceptance, err := ParseAcceptance(observed.Response, inv)
	if err != nil {
		completion.Outcome = basev0.RunnableServedOutcome_SERVED_INVOCATION_INTEGRITY
		completion.Result = nil
		completion.Message = appendServedMessage(messageOf(observed.Trouble), err.Error())
		return Accepted{Completion: completion}, nil
	}
	return Accepted{Acceptance: acceptance}, nil
}

// ParseAcceptance decodes and validates an owner's acceptance of inv.
func ParseAcceptance(document []byte, inv *basev0.RunnableInvocation) (*runnablev0.RunnableAcceptance, error) {
	acceptance := &runnablev0.RunnableAcceptance{}
	if err := unmarshalSubmit(document, acceptance, "acceptance"); err != nil {
		return nil, err
	}
	if acceptance.GetInvocationId() != inv.GetInvocationId() {
		return nil, fmt.Errorf("%w: acceptance names invocation %q, not %q", ErrInvalid, acceptance.GetInvocationId(), inv.GetInvocationId())
	}
	if acceptance.GetHeartbeatInterval().AsDuration() <= 0 {
		return nil, fmt.Errorf("%w: acceptance undertakes no heartbeat interval, so no silence could ever be measured against it", ErrInvalid)
	}
	return acceptance, nil
}

// Reported is one document an owner POSTed to the caller's callback address.
// Exactly one field is set, decided by the document's own schema: that address
// receives both shapes, and nothing outside the document says which arrived.
type Reported struct {
	// Heartbeat is a liveness report. It is never terminal: an operation that
	// reported progress and then died has an unproven effect, exactly as one
	// that reported nothing.
	Heartbeat *runnablev0.RunnableHeartbeat
	// Completion is the terminal answer, classified by the served taxonomy: the
	// answer a submit-mode operation gives is the answer a call-mode one would
	// have given, arriving by a different route.
	Completion *basev0.RunnableServedCompletion
}

// ClassifyReport reads one document posted to the callback address of inv,
// which the acceptance named the work of.
//
// A report that cannot be read is an error rather than an outcome, the opposite
// of a call's answer: a caller reaches this only because something posted to its
// own address, and a malformed post is not evidence that anything happened to
// the work. The caller goes on waiting, and resolves by status or receipt.
func ClassifyReport(document []byte, inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage, acceptance *runnablev0.RunnableAcceptance) (Reported, error) {
	if err := validateAccepted(inv, pkg, acceptance); err != nil {
		return Reported{}, err
	}
	schema, err := schemaOf(document)
	if err != nil {
		return Reported{}, err
	}
	switch schema {
	case HeartbeatSchemaV1:
		heartbeat := &runnablev0.RunnableHeartbeat{}
		if err := unmarshalSubmit(document, heartbeat, "heartbeat"); err != nil {
			return Reported{}, err
		}
		if err := belongsTo("heartbeat", heartbeat.GetHandle(), heartbeat.GetInvocationId(), inv, acceptance); err != nil {
			return Reported{}, err
		}
		return Reported{Heartbeat: heartbeat}, nil
	case CallbackSchemaV1:
		callback := &runnablev0.RunnableCompletionCallback{}
		if err := unmarshalSubmit(document, callback, "callback"); err != nil {
			return Reported{}, err
		}
		if err := belongsTo("callback", callback.GetHandle(), callback.GetInvocationId(), inv, acceptance); err != nil {
			return Reported{}, err
		}
		// The effect identity is repeated on the callback so a caller holding
		// only this document can read the receipt. One that names another
		// effect would send that caller to the wrong receipt, which is the one
		// mistake recovery cannot survive.
		if callback.GetEffectId() != inv.GetEffectId() {
			return Reported{}, fmt.Errorf("%w: callback names effect %q, not the invocation's %q",
				ErrInvalid, callback.GetEffectId(), inv.GetEffectId())
		}
		completion, err := completionOf(inv, pkg, callback.GetResult(), callback.GetCompletedAt())
		if err != nil {
			return Reported{}, err
		}
		return Reported{Completion: completion}, nil
	default:
		return Reported{}, fmt.Errorf("%w: %q is not a document this address receives; %s and %s are the two",
			ErrInvalid, schema, HeartbeatSchemaV1, CallbackSchemaV1)
	}
}

// ClassifyStatus reads an owner's answer to a status request.
//
// The completion is nil for work that has not ended: a caller reads that as
// "still live" and keeps waiting, which is the whole reason the read exists. It
// is not nil for STATE_LOST, which is an outcome — an owner that accepted a
// handle and cannot say what became of it has told the caller to read the
// receipt, and a caller told nothing could not tell that from an owner that
// never accepted the work.
func ClassifyStatus(document []byte, inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage, acceptance *runnablev0.RunnableAcceptance) (*runnablev0.RunnableStatus, *basev0.RunnableServedCompletion, error) {
	if err := validateAccepted(inv, pkg, acceptance); err != nil {
		return nil, nil, err
	}
	status := &runnablev0.RunnableStatus{}
	if err := unmarshalSubmit(document, status, "status"); err != nil {
		return nil, nil, err
	}
	if err := belongsTo("status", status.GetHandle(), status.GetInvocationId(), inv, acceptance); err != nil {
		return nil, nil, err
	}
	switch status.GetState() {
	case runnablev0.RunnableStatus_STATE_ACCEPTED, runnablev0.RunnableStatus_STATE_RUNNING:
		if status.GetResult() != nil {
			return nil, nil, fmt.Errorf("%w: status reports %s and carries a result, which would be an answer for work it says has not ended",
				ErrInvalid, status.GetState())
		}
		return status, nil, nil
	case runnablev0.RunnableStatus_STATE_COMPLETED:
		completion, err := completionOf(inv, pkg, status.GetResult(), status.GetCompletedAt())
		if err != nil {
			return nil, nil, err
		}
		return status, completion, nil
	case runnablev0.RunnableStatus_STATE_LOST:
		completion := submitCompletion(inv, pkg, nil)
		completion.Outcome = basev0.RunnableServedOutcome_SERVED_OWNER_UNAVAILABLE
		completion.Message = "the owner accepted this work and cannot say what became of it; the effect receipt is the only evidence"
		return status, completion, nil
	default:
		return nil, nil, fmt.Errorf("%w: status state %s says nothing about the work", ErrInvalid, status.GetState())
	}
}

// ValidateCallbackTarget holds a caller's own reporting address to the rules
// every address in this contract is held to. It is validated where the
// invocation is prepared rather than where a report arrives, because an owner
// that cannot reach the address reports nothing and the caller learns only from
// a silence it cannot tell from a dead worker.
func ValidateCallbackTarget(target *basev0.RunnableCallbackTarget) error {
	if err := validator.Validate(target); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	parsed, err := url.Parse(target.GetAddress())
	if err != nil {
		return fmt.Errorf("%w: callback address %q is not a URL: %v", ErrInvalid, target.GetAddress(), err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: callback address %q is not an HTTP address; a report is a POST", ErrInvalid, target.GetAddress())
	}
	if parsed.Host == "" {
		return fmt.Errorf("%w: callback address %q names no host, so nothing could dial it", ErrInvalid, target.GetAddress())
	}
	if parsed.User != nil {
		return fmt.Errorf("%w: callback address %q carries userinfo; the report is authenticated by the reporting workload's identity, never by a credential in a URL", ErrInvalid, target.GetAddress())
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%w: callback address %q carries a query or fragment, which an owner POSTing a document has no use for", ErrInvalid, target.GetAddress())
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return fmt.Errorf("%w: callback address %q is plaintext to %q, which is not a loopback host: a completion and the Work Context authenticating it would travel in clear",
			ErrInvalid, target.GetAddress(), parsed.Hostname())
	}
	return nil
}

// isLoopbackHost reports whether a host always resolves to this machine.
// "localhost" and its subdomains are loopback by RFC 6761, and a literal is
// classified by its address; anything else may resolve anywhere, so plaintext
// to it is refused.
func isLoopbackHost(host string) bool {
	lowered := strings.ToLower(strings.TrimSuffix(host, "."))
	if lowered == "localhost" || strings.HasSuffix(lowered, ".localhost") {
		return true
	}
	if ip := net.ParseIP(lowered); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// validateAccepted holds the three facts every later shape is read against: the
// invocation is a submit-mode one, the acceptance is this invocation's, and it
// named a handle.
func validateAccepted(inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage, acceptance *runnablev0.RunnableAcceptance) error {
	if err := validateInvocation(inv, pkg); err != nil {
		return err
	}
	if completion := pkg.GetExecution().GetCompletion(); completion != basev0.RunnableExecution_COMPLETION_SUBMIT {
		return fmt.Errorf("%w: package declares %s, which is answered by the reply to the call", ErrInvalid, completion)
	}
	if err := validator.Validate(acceptance); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if acceptance.GetInvocationId() != inv.GetInvocationId() {
		return fmt.Errorf("%w: acceptance names invocation %q, not %q", ErrInvalid, acceptance.GetInvocationId(), inv.GetInvocationId())
	}
	return nil
}

// belongsTo holds a later shape to the work it claims to be about. A document
// naming another handle or another invocation has found something that is not
// this work's, and reading it as this work's would resolve one invocation with
// another's answer.
func belongsTo(kind string, handle string, invocation string, inv *basev0.RunnableInvocation, acceptance *runnablev0.RunnableAcceptance) error {
	if handle != acceptance.GetHandle() {
		return fmt.Errorf("%w: %s names handle %q, not the accepted %q", ErrInvalid, kind, handle, acceptance.GetHandle())
	}
	if invocation != inv.GetInvocationId() {
		return fmt.Errorf("%w: %s names invocation %q, not %q", ErrInvalid, kind, invocation, inv.GetInvocationId())
	}
	return nil
}

// completionOf turns a terminal answer that arrived by callback or by status
// read into the completion a reply would have produced. The routes differ; what
// the answer proves does not, so the taxonomy is applied once.
func completionOf(inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage, result *basev0.RunnableResult, completed *timestamppb.Timestamp) (*basev0.RunnableServedCompletion, error) {
	if result == nil {
		return nil, fmt.Errorf("%w: a terminal answer carrying no result says the work ended and refuses to say how", ErrInvalid)
	}
	if err := validateResult(result, inv, pkg); err != nil {
		return nil, err
	}
	completion := submitCompletion(inv, pkg, completed)
	completion.Result = proto.CloneOf(result)
	switch result.GetStatus() {
	case basev0.RunnableResult_SUCCEEDED:
		completion.Outcome = basev0.RunnableServedOutcome_SERVED_SUCCEEDED
	case basev0.RunnableResult_FAILED:
		completion.Outcome = basev0.RunnableServedOutcome_SERVED_OWNER_FAILED
		completion.FailureCode = result.GetError().GetCode()
	default:
		completion.Outcome = basev0.RunnableServedOutcome_SERVED_EFFECT_OUTCOME_UNKNOWN
	}
	return completion, nil
}

// submitCompletion brackets a completion the caller did not observe as a call.
// called_at is the instant the work was submitted, and answered_at the instant
// the answer reached the caller — the owner's own completed_at when it stated
// one, because for a submitted invocation that is when the work ended, and the
// caller's clock only says when it heard.
func submitCompletion(inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage, completed *timestamppb.Timestamp) *basev0.RunnableServedCompletion {
	answered := completed
	if answered == nil {
		answered = inv.GetDeadline()
	}
	return &basev0.RunnableServedCompletion{
		Runnable:     proto.CloneOf(pkg.GetIdentity()),
		InvocationId: inv.GetInvocationId(),
		CalledAt:     inv.GetIssuedAt(),
		AnsweredAt:   answered,
	}
}

// schemaOf reads the schema of a document that has not been decoded yet, so a
// shape is chosen by what arrived rather than guessed and then checked.
func schemaOf(document []byte) (string, error) {
	var envelope struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(document, &envelope); err != nil {
		return "", fmt.Errorf("%w: report is not a JSON document: %v", ErrInvalid, err)
	}
	if envelope.Schema == "" {
		return "", fmt.Errorf("%w: report names no schema, and the callback address receives more than one shape", ErrInvalid)
	}
	return envelope.Schema, nil
}

// unmarshalSubmit decodes one submit shape and holds it to its own validation
// rules. Unknown fields are dropped for the same reason ParseResult drops them:
// a field this core does not know belongs to an owner generated from newer
// sources, and refusing it would turn work that in fact completed into an
// outcome nobody can resolve.
func unmarshalSubmit(document []byte, message proto.Message, kind string) error {
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(document, message); err != nil {
		return fmt.Errorf("%w: %s is not a %s document: %v", ErrInvalid, kind, resources.RunnableServedProtocolV1, err)
	}
	if err := validator.Validate(message); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

func messageOf(trouble error) string {
	if trouble == nil {
		return ""
	}
	return trouble.Error()
}
