// Package broker is the trusted host-mediated HTTP boundary for provider
// agents. Provider code never dials the network; it hands the host a structured
// request from an already-admitted plan action, and the broker re-derives the
// exact action, resource, origin, headers, and credentials from host-owned
// context before any bytes leave the machine. Provider descriptors are treated
// as adversarial: the action, resource, read-only intent, and origin the
// provider claims are all ignored in favor of what the host planned.
//
// The broker never retries. It reports one of NOT_SENT, SENT_OUTCOME_UNKNOWN,
// or RESPONSE_RECEIVED so the coordinator can recover honestly, and it filters
// every response through the manifest response policy so secret-bearing fields
// are captured or suppressed rather than forwarded.
package broker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/network/urlguard"
	"github.com/codefly-dev/core/provider/canonical"
	"github.com/codefly-dev/core/provider/cassette"
	"github.com/codefly-dev/core/provider/credentials"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/codefly-dev/core/provider/responsepolicy"
)

// Checkpointer reports the durable recovery checkpoint the coordinator persisted
// for an operation. The durable store itself is out of scope (F3); the broker
// depends only on this read-side to enforce checkpoint-before-send ordering.
type Checkpointer interface {
	Latest(ctx context.Context, operation *providerv0.OperationIdentity) (*providerv0.ActionCheckpoint, error)
}

// Config binds one broker session to exactly one admitted plan action. Every
// field is host-owned; the provider cannot override any of it.
type Config struct {
	Manifest *manifest.Manifest
	Action   *providerv0.PlanAction
	Binding  *providerv0.BindingAddress
	Budget   *providerv0.RequestBudget
	ReadOnly bool

	Vault       *credentials.Vault
	Sink        responsepolicy.Sink
	Checkpoints Checkpointer
	Resolver    *net.Resolver
	Deadlines   urlguard.Deadlines
	UserAgent   string

	// Cassette, when set, records or replays this session's responses. Replay
	// never touches the network; record stores only filtered safe responses.
	Cassette *cassette.Cassette

	// OnStreamEvent, when set, receives each filtered event of a streamed (SSE)
	// response in order, as it is delivered live or replayed, before Execute
	// returns. It is how a caller gets live tokens instead of only the fully
	// buffered ordered set. A non-nil error from it fails the delivery closed.
	OnStreamEvent func(*providerv0.FilteredEvent) error

	// MaxRequestDuration bounds one round trip — including reading a streamed
	// body — when the budget carries no absolute deadline. It exists so a slow or
	// stalled stream cannot hold the session open indefinitely. Zero selects
	// DefaultMaxRequestDuration.
	MaxRequestDuration time.Duration

	// ClientFor overrides guarded client construction. Production leaves it nil
	// to use the SSRF-hardened urlguard client; tests inject a client bound to a
	// local server.
	ClientFor func(urlguard.Origin, urlguard.Resolution) *http.Client
	// Now overrides the clock for deterministic tests.
	Now func() time.Time
}

// DefaultMaxRequestDuration bounds a round trip when no budget deadline is set.
// It is generous enough for a long model generation yet finite, so a stalled
// stream releases the session instead of blocking on it forever.
const DefaultMaxRequestDuration = 10 * time.Minute

// Session is the per-action broker context. A session serializes its requests:
// budget, capture-gate, and delivery are inherently sequential, so Execute holds
// a session lock for the duration of each call.
type Session struct {
	mu sync.Mutex

	action    *providerv0.PlanAction
	binding   *providerv0.BindingAddress
	readOnly  bool
	userAgent string

	requests        map[string]manifest.RequestDescriptor
	originRules     map[string]manifest.OriginRule
	responseSchemas map[string]manifest.ResponseSchema
	purposes        map[string]manifest.CredentialPurpose

	budget    *providerv0.RequestBudget
	limits    responsepolicy.Limits
	remaining uint32

	vault         *credentials.Vault
	sink          responsepolicy.Sink
	checkpoints   Checkpointer
	cassette      *cassette.Cassette
	onStreamEvent func(*providerv0.FilteredEvent) error
	maxDuration   time.Duration
	resolver      *net.Resolver
	clientFor     func(urlguard.Origin, urlguard.Resolution) *http.Client
	now           func() time.Time

	// captureGate holds the checkpoint id present when the last capture became
	// durable. A later external request is refused until a newer checkpoint
	// acknowledges the capture.
	captureGate string
}

// New builds a session and validates that the action belongs to the manifest.
func New(cfg Config) (*Session, error) {
	if cfg.Manifest == nil || cfg.Action == nil || cfg.Budget == nil {
		return nil, fmt.Errorf("broker requires a manifest, action, and budget")
	}
	if cfg.Vault == nil || cfg.Sink == nil || cfg.Checkpoints == nil {
		return nil, fmt.Errorf("broker requires a vault, sink, and checkpointer")
	}
	if err := canonical.ValidatePlanAction(cfg.Action); err != nil {
		return nil, err
	}
	session := &Session{
		action:          cfg.Action,
		binding:         cfg.Binding,
		readOnly:        cfg.ReadOnly,
		userAgent:       cfg.UserAgent,
		requests:        make(map[string]manifest.RequestDescriptor),
		originRules:     make(map[string]manifest.OriginRule),
		responseSchemas: make(map[string]manifest.ResponseSchema),
		purposes:        make(map[string]manifest.CredentialPurpose),
		budget:          cfg.Budget,
		remaining:       cfg.Budget.GetRequestCount(),
		vault:           cfg.Vault,
		sink:            cfg.Sink,
		checkpoints:     cfg.Checkpoints,
		cassette:        cfg.Cassette,
		onStreamEvent:   cfg.OnStreamEvent,
		maxDuration:     cfg.MaxRequestDuration,
		resolver:        cfg.Resolver,
		clientFor:       cfg.ClientFor,
		now:             cfg.Now,
	}
	if session.userAgent == "" {
		session.userAgent = "codefly-provider-broker"
	}
	if session.maxDuration <= 0 {
		session.maxDuration = DefaultMaxRequestDuration
	}
	if session.now == nil {
		session.now = time.Now
	}
	if session.clientFor == nil {
		session.clientFor = func(origin urlguard.Origin, pinned urlguard.Resolution) *http.Client {
			return urlguard.Client(origin, pinned, cfg.Deadlines)
		}
	}
	for _, descriptor := range cfg.Manifest.Requests {
		session.requests[descriptor.ID] = descriptor
	}
	for _, rule := range cfg.Manifest.OriginRules {
		session.originRules[rule.ID] = rule
	}
	for _, schema := range cfg.Manifest.ResponseSchemas {
		session.responseSchemas[schema.ID] = schema
	}
	for _, purpose := range cfg.Manifest.CredentialPurposes {
		session.purposes[purpose.ID] = purpose
	}
	session.limits = responsePolicyLimits(cfg.Budget)
	return session, nil
}

func responsePolicyLimits(budget *providerv0.RequestBudget) responsepolicy.Limits {
	limits := responsepolicy.DefaultLimits()
	if bytes := int64(budget.GetResponseBytes()); bytes > 0 {
		limits.MaxCompressedBytes = bytes
		limits.MaxDecompressedBytes = bytes
	}
	return limits
}

// Execute runs one admitted broker request. It returns a filtered response and,
// on any admission or filtering failure, a non-nil error the coordinator treats
// as a hard stop. Original response bytes are never forwarded.
func (s *Session) Execute(ctx context.Context, request *providerv0.ExecuteRequestRequest) (*providerv0.ExecuteRequestResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := canonical.ValidateExecuteRequest(s.action, request); err != nil {
		return nil, fmt.Errorf("admit request: %w", err)
	}
	planned := request.GetRequest()
	descriptor, err := s.admitDescriptor(planned)
	if err != nil {
		return nil, err
	}
	if s.readOnly && !descriptor.ReadOnly {
		return nil, fmt.Errorf("read-only context cannot execute a mutating request")
	}
	if err := s.checkBudget(); err != nil {
		return nil, err
	}
	origin, ceiling, err := s.admitOrigin(descriptor, request.GetOrigin())
	if err != nil {
		return nil, err
	}
	// SSRF, DNS-rebinding, and private-address checks must complete before any
	// credential is resolved. Replay is enforcement-identical but performs no
	// network I/O, so it resolves and pins nothing.
	replay := s.replaying()
	var pinned urlguard.Resolution
	if !replay {
		if pinned, err = s.resolveAndPin(ctx, origin, ceiling, request.GetOrigin()); err != nil {
			return nil, err
		}
	}
	remoteID, err := plannedRemoteID(s.action)
	if err != nil {
		return nil, err
	}
	httpRequest, bodySize, err := s.buildRequest(ctx, descriptor, planned, origin, remoteID)
	if err != nil {
		return nil, err
	}
	checkpointID, err := s.requireCheckpoint(ctx, request.GetContext().GetOperation(), planned)
	if err != nil {
		return nil, err
	}
	if err := s.injectCredentials(httpRequest, request, origin); err != nil {
		return nil, err
	}
	// The whole outbound message — request line, host-owned headers including the
	// resolved credential, and body — is bounded by the request byte budget.
	if limit := s.budget.GetRequestBytes(); limit != 0 && outboundSize(httpRequest, bodySize) > int64(limit) {
		return nil, fmt.Errorf("request exceeds byte budget")
	}

	// The callback is consumed once the request is fully admitted and about to be
	// delivered.
	s.remaining--

	delivery, response, err := s.deliver(ctx, descriptor, planned, request.GetOrigin(), origin, pinned, httpRequest)
	if err != nil {
		return nil, err
	}
	// Arm the capture gate: a durable capture must be checkpointed before the
	// next external request, whether it was produced live or replayed. A streamed
	// response carries its captures on the individual events, so both are counted.
	if responseHasCaptures(response) {
		s.captureGate = checkpointID
	}
	response.RequestId = request.GetRequestId()
	response.Delivery = delivery
	return response, nil
}

// replaying reports whether this session serves responses from a cassette.
func (s *Session) replaying() bool {
	return s.cassette != nil && s.cassette.Mode() == cassette.ModeReplay
}

// deliver performs the request either live or through a cassette. Replay never
// touches the network; record stores the filtered response. All admission
// checks have already run, so both paths share identical validation.
func (s *Session) deliver(
	ctx context.Context,
	descriptor manifest.RequestDescriptor,
	planned *providerv0.PlannedRequest,
	attested *providerv0.AdmittedOrigin,
	origin urlguard.Origin,
	pinned urlguard.Resolution,
	httpRequest *http.Request,
) (providerv0.DeliveryState, *providerv0.ExecuteRequestResponse, error) {
	if s.cassette != nil && s.cassette.Mode() == cassette.ModeReplay {
		status, response, err := s.cassette.Replay(cassette.NewKey(planned, attested))
		if err != nil {
			return 0, nil, err
		}
		response.StatusCode = status
		// Replay is delivery-identical: a streamed response's events are emitted in
		// order through the same live callback, so a replayed stream is
		// indistinguishable from a live one to the caller.
		if err := s.emitStreamEvents(response.GetEvents()); err != nil {
			return 0, nil, err
		}
		return providerv0.DeliveryState_DELIVERY_STATE_RESPONSE_RECEIVED, response, nil
	}
	client := s.clientFor(origin, pinned)
	delivery, response, err := s.roundTrip(ctx, client, descriptor, httpRequest)
	if err != nil {
		return 0, nil, err
	}
	if s.cassette != nil && s.cassette.Mode() == cassette.ModeRecord && delivery == providerv0.DeliveryState_DELIVERY_STATE_RESPONSE_RECEIVED {
		if err := s.cassette.Record(cassette.NewKey(planned, attested), response.GetStatusCode(), nil, response); err != nil {
			return 0, nil, fmt.Errorf("record cassette: %w", err)
		}
	}
	return delivery, response, nil
}

func (s *Session) admitDescriptor(planned *providerv0.PlannedRequest) (manifest.RequestDescriptor, error) {
	descriptor, ok := s.requests[planned.GetRequestDescriptorId()]
	if !ok {
		return manifest.RequestDescriptor{}, fmt.Errorf("request descriptor %q is not packaged", planned.GetRequestDescriptorId())
	}
	digest, err := manifest.RequestDescriptorDigest(descriptor)
	if err != nil {
		return manifest.RequestDescriptor{}, err
	}
	if digest != planned.GetRequestDescriptorDigest() {
		return manifest.RequestDescriptor{}, fmt.Errorf("request descriptor digest mismatch")
	}
	return descriptor, nil
}

func (s *Session) checkBudget() error {
	if s.remaining == 0 {
		return fmt.Errorf("request budget is exhausted")
	}
	if deadline := s.budget.GetDeadline(); deadline != nil && s.now().After(deadline.AsTime()) {
		return fmt.Errorf("request budget deadline has passed")
	}
	return nil
}

// admitOrigin re-admits the host-attested origin against the descriptor's
// ceiling, resolves and pins a single peer IP, and confirms the attested
// network class matches the resolved one.
// admitOrigin performs the structural, network-free admission: it binds the
// attested origin to the descriptor's rule and confirms it stays within the
// manifest ceiling. It never resolves DNS, so it runs identically for live and
// replay.
func (s *Session) admitOrigin(descriptor manifest.RequestDescriptor, attested *providerv0.AdmittedOrigin) (urlguard.Origin, urlguard.Ceiling, error) {
	if attested.GetOriginRuleId() != descriptor.OriginRule {
		return urlguard.Origin{}, urlguard.Ceiling{}, fmt.Errorf("origin rule does not match descriptor")
	}
	rule, ok := s.originRules[descriptor.OriginRule]
	if !ok {
		return urlguard.Origin{}, urlguard.Ceiling{}, fmt.Errorf("origin rule %q is not packaged", descriptor.OriginRule)
	}
	origin := urlguard.Origin{Scheme: attested.GetScheme(), Host: attested.GetHost(), Port: attested.GetPort()}
	ceiling := ceilingFor(rule)
	if err := ceiling.AdmitOrigin(origin); err != nil {
		return urlguard.Origin{}, urlguard.Ceiling{}, err
	}
	return origin, ceiling, nil
}

// resolveAndPin performs the single DNS resolution in the request path, pins one
// admitted peer IP, and confirms the resolved network class matches the
// host-attested class. It runs only for live delivery — never during replay —
// and always before any credential is resolved.
func (s *Session) resolveAndPin(ctx context.Context, origin urlguard.Origin, ceiling urlguard.Ceiling, attested *providerv0.AdmittedOrigin) (urlguard.Resolution, error) {
	pinned, err := urlguard.Resolve(ctx, s.resolver, origin, ceiling.AllowedClasses)
	if err != nil {
		return urlguard.Resolution{}, err
	}
	if classEnum(pinned.Class) != attested.GetPrivateNetworkClass() {
		return urlguard.Resolution{}, fmt.Errorf("resolved network class does not match attested origin")
	}
	return pinned, nil
}

// requireCheckpoint enforces that a durable pre-send checkpoint exists before
// any bytes leave, and that a prior durable capture has been acknowledged by a
// newer checkpoint before the next external request.
func (s *Session) requireCheckpoint(ctx context.Context, operation *providerv0.OperationIdentity, planned *providerv0.PlannedRequest) (string, error) {
	checkpoint, err := s.checkpoints.Latest(ctx, operation)
	if err != nil {
		return "", fmt.Errorf("read checkpoint: %w", err)
	}
	if checkpoint == nil {
		return "", fmt.Errorf("a durable checkpoint is required before sending")
	}
	if checkpoint.GetOperation().GetActionId() != s.action.GetActionId() {
		return "", fmt.Errorf("checkpoint does not bind this action")
	}
	if isMutating(planned.GetMethod()) && checkpoint.GetIdempotencyKey() != planned.GetIdempotencyKey() {
		return "", fmt.Errorf("checkpoint idempotency key does not match request")
	}
	if s.captureGate != "" && checkpoint.GetCheckpointId() == s.captureGate {
		return "", fmt.Errorf("a durable capture must be checkpointed before the next request")
	}
	return checkpoint.GetCheckpointId(), nil
}

func (s *Session) injectCredentials(httpRequest *http.Request, request *providerv0.ExecuteRequestRequest, origin urlguard.Origin) error {
	use := credentials.Use{
		Binding:       s.binding,
		ActionID:      s.action.GetActionId(),
		RequestDigest: request.GetRequest().GetRequestDigest(),
		Origin:        origin,
		Method:        request.GetRequest().GetMethod(),
	}
	for _, handle := range request.GetCredentialHandles() {
		use.Purpose = handle.GetPurpose()
		if err := s.vault.Inject(httpRequest, handle.GetHandle(), use); err != nil {
			return fmt.Errorf("resolve credential: %w", err)
		}
	}
	return nil
}

// roundTrip performs exactly one request with no retry. Request tracing tells
// whether request bytes crossed the boundary so a lost response is reported as
// SENT_OUTCOME_UNKNOWN rather than NOT_SENT.
func (s *Session) roundTrip(ctx context.Context, client *http.Client, descriptor manifest.RequestDescriptor, httpRequest *http.Request) (providerv0.DeliveryState, *providerv0.ExecuteRequestResponse, error) {
	// The absolute budget deadline bounds the whole round trip, not just the
	// pre-send admission checks. When the budget carries no deadline a default
	// bound still applies, so reading a slow or stalled streamed body cannot hold
	// the session open indefinitely. Because the body is read on this request's
	// context, the deadline interrupts a mid-stream read too.
	var cancel context.CancelFunc
	if deadline := s.budget.GetDeadline(); deadline != nil {
		ctx, cancel = context.WithDeadline(ctx, deadline.AsTime())
	} else {
		ctx, cancel = context.WithTimeout(ctx, s.maxDuration)
	}
	defer cancel()
	var wrote bool
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { wrote = true }}
	traced := httpRequest.WithContext(httptrace.WithClientTrace(ctx, trace))

	resp, err := client.Do(traced)
	if err != nil {
		if wrote {
			return providerv0.DeliveryState_DELIVERY_STATE_SENT_OUTCOME_UNKNOWN,
				&providerv0.ExecuteRequestResponse{Certainty: providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_UNCERTAIN}, nil
		}
		certainty := providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE
		if descriptor.ReadOnly {
			certainty = providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_UNCERTAIN
		}
		return providerv0.DeliveryState_DELIVERY_STATE_NOT_SENT,
			&providerv0.ExecuteRequestResponse{Certainty: certainty}, nil
	}
	defer resp.Body.Close()
	response, err := s.handleResponse(descriptor, resp)
	if err != nil {
		return providerv0.DeliveryState_DELIVERY_STATE_RESPONSE_RECEIVED, nil, err
	}
	return providerv0.DeliveryState_DELIVERY_STATE_RESPONSE_RECEIVED, response, nil
}
