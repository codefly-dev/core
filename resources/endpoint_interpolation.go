package resources

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/standards"
	"github.com/codefly-dev/core/wool"
	"google.golang.org/protobuf/proto"
)

// endpointInterpolationPattern matches ${endpoint:<module>/<service>/<endpoint>}
// references embedded in a configuration value.
var endpointInterpolationPattern = regexp.MustCompile(`\$\{endpoint:([^{}]+)\}`)

// authorityProjection is the suffix that asks for the authority (host:port) of
// the resolved address instead of the address itself:
// ${endpoint:<module>/<service>/<endpoint>|authority}. An HTTP-based endpoint's
// address is a URL; a client that dials the same listener in authority form —
// gRPC over h2c on an HTTP listener — needs host:port, and the reference keeps
// the composition from typing a port the run derives.
const authorityProjection = "|authority"

// splitEndpointProjection separates a reference from its projection suffix.
func splitEndpointProjection(reference string) (string, bool) {
	if trimmed, ok := strings.CutSuffix(reference, authorityProjection); ok {
		return trimmed, true
	}
	return reference, false
}

// addressAuthority projects an address onto its authority: the host:port of a
// URL, or the address itself when it already is one.
func addressAuthority(address string) (string, error) {
	if !strings.Contains(address, "://") {
		return address, nil
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("address %q has no authority", address)
	}
	return parsed.Host, nil
}

// ErrMalformedEndpointReference is returned when a value carries the reserved
// `${endpoint:` prefix in a form the reference grammar does not match — an
// empty marker, an unterminated one. Such a value must never reach a workload
// unchanged, and must never be omitted as though the marker were a reference
// this consumer merely cannot resolve: it is a composition fault wherever it is
// met.
var ErrMalformedEndpointReference = errors.New("malformed endpoint reference: the reserved ${endpoint: prefix is not a well-formed reference")

// malformedEndpointMarker reports whether value carries the reserved prefix
// outside any well-formed reference. The spans between well-formed references
// are judged each in place: joining them would let the text before one
// reference and the text after it spell a prefix that the value never carried.
func malformedEndpointMarker(value string) bool {
	const marker = "${endpoint:"
	at := 0
	for _, span := range endpointInterpolationPattern.FindAllStringIndex(value, -1) {
		if strings.Contains(value[at:span[0]], marker) {
			return true
		}
		at = span[1]
	}
	return strings.Contains(value[at:], marker)
}

// referenceCount says how many well-formed markers a value carries, for a
// diagnostic that must carry nothing from the value: a marker body is input
// until it has validated as coordinates, and a value may be a secret.
func referenceCount(value string) string {
	n := len(endpointInterpolationPattern.FindAllStringIndex(value, -1))
	if n == 1 {
		return "1 reference"
	}
	return fmt.Sprintf("%d references", n)
}

// referenceTokenPattern is what each coordinate of a reference may be: a
// module, service or endpoint name as the schema spells one.
var referenceTokenPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// ParseEndpointReference parses a marker body into coordinates and validates
// each one syntactically: every token matches the name pattern, and the API
// is one the model knows. A body that does not validate is
// ErrMalformedEndpointReference. Syntactic validity is all this establishes:
// a token that validates may still be text the author meant as a secret, so
// nothing of the body — valid or not — is carried in any diagnostic; a
// reference is named by its position and by the configuration and key. The
// plan-time checker and the resolution share this parser, so the two never
// disagree on what a reference is.
func ParseEndpointReference(reference string) (*EndpointInformation, error) {
	return parseEndpointReference(reference)
}

func parseEndpointReference(reference string) (*EndpointInformation, error) {
	info, err := ParseEndpoint(reference)
	if err != nil {
		return nil, ErrMalformedEndpointReference
	}
	if strings.Contains(reference, "::") && info.API == "" {
		return nil, fmt.Errorf("%w: an API qualifier is present but empty", ErrMalformedEndpointReference)
	}
	if info.Module == "" || info.Service == "" || (info.Name == "" && info.API == "") {
		return nil, fmt.Errorf("%w: a reference names <module>/<service>/<endpoint>", ErrMalformedEndpointReference)
	}
	for _, token := range []string{info.Module, info.Service, info.Name} {
		if token != "" && (!referenceTokenPattern.MatchString(token) || strings.Contains(token, "--")) {
			return nil, ErrMalformedEndpointReference
		}
	}
	if info.API != "" && standards.IsSupportedAPI(info.API) != nil {
		return nil, fmt.Errorf("%w: the API qualifier is not one the model knows", ErrMalformedEndpointReference)
	}
	return info, nil
}

// errEndpointNotAvailable marks a well-formed reference to a declared endpoint
// the consumer was handed no mapping for. With ErrEndpointNotReachable (a
// denial by a valid export policy) it is one of the two omission classes — the
// two facts about ONE consumer's view: the run-wide path drops a value on
// either, and the strict path only on this one, and only when the producer it
// names is not part of this run (see WithRunProducers). Every other failure —
// a malformed reference, an ambiguous one, an invalid declaration, an unknown
// producer, an endpoint matched with no instance for the consumer's access — is
// a fault of the composition or of a declaration, refused on both paths, and
// must not carry either sentinel.
var errEndpointNotAvailable = errors.New("endpoint not available to this consumer")

// EndpointReferences returns the <module>/<service>/<endpoint> references value
// carries, in order of appearance. A composition root uses it to learn which
// producers' addresses a consumer's configuration names before resolving it.
func EndpointReferences(value string) []string {
	var references []string
	for _, match := range endpointInterpolationPattern.FindAllStringSubmatch(value, -1) {
		reference, _ := splitEndpointProjection(match[1])
		references = append(references, reference)
	}
	return references
}

// InterpolateEndpointsFor replaces every ${endpoint:<module>/<service>/<endpoint>}
// reference in value with the endpoint's runtime address, resolved from mappings
// for access — the same address published as
// CODEFLY__ENDPOINT__<MODULE>__<SERVICE>__<ENDPOINT> — for the consumer the
// selection names, against the producers' declared endpoints it reads. A value
// with no reference is returned unchanged. An unresolvable reference is an
// error, never a broken URL. A selection that does not identify its consumer or
// cannot read the manifests is refused before any reference is read: there is no
// consumer-less resolution, because an endpoint reference is an edge into the
// consumer's module and the export boundary is a statement about that module.
func InterpolateEndpointsFor(ctx context.Context, value string, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess, selection EndpointSelectionContext) (string, error) {
	n := len(endpointInterpolationPattern.FindAllStringIndex(value, -1))
	return interpolateEndpointsAt(ctx, value, mappings, access, selection, 0, n)
}

// interpolateEndpointsAt is InterpolateEndpointsFor for one part of a
// configuration value whose references are numbered as a whole: the part's
// first reference is number base+1 of total. A diagnostic never carries the
// value — a configuration value may be a secret, and an error travels further
// than the value was meant to — so a reference is named by that number, and
// the number is the same one the plan-time check reports for it.
func interpolateEndpointsAt(ctx context.Context, value string, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess, selection EndpointSelectionContext, base, total int) (string, error) {
	if malformedEndpointMarker(value) {
		return "", fmt.Errorf("%w (beside %s)", ErrMalformedEndpointReference, referenceCount(value))
	}
	matches := endpointInterpolationPattern.FindAllStringSubmatchIndex(value, -1)
	if matches == nil {
		return value, nil
	}
	if err := selection.Complete(); err != nil {
		return "", fmt.Errorf("cannot resolve the value's %s: %w", referenceCount(value), err)
	}
	// Every reference is classified before any failure is reported, so that the
	// failure reported is the one that matters: a composition fault anywhere in
	// the value (an ambiguous reference, a malformed one, an invalid
	// declaration, …) dominates an omission the run-wide path would otherwise
	// be entitled to make on a reference that merely came first. Deciding on
	// the first failure let the order of two references in one value decide
	// whether a fault was reported or the key silently dropped.
	var b strings.Builder
	var omission error
	last := 0
	for i, match := range matches {
		reference, authority := splitEndpointProjection(value[match[2]:match[3]])
		// The marker body is parsed before it is resolved, and a diagnostic
		// names it only by its position, whether or not it validated: its
		// tokens are text from the value.
		position := fmt.Sprintf("reference %d of %d", base+i+1, total)
		info, err := parseEndpointReference(reference)
		if err != nil {
			return "", fmt.Errorf("%s: %w", position, err)
		}
		instance, err := resolveEndpointReference(ctx, mappings, info, position, access, selection)
		if err != nil {
			if !ReferenceFailureIsAnOmission(err) {
				return "", err
			}
			// Keep the WORSE omission, not the first: a denial outranks an
			// unavailability, because the strict path may drop the latter and
			// never the former, and the order of two references must not
			// decide which policy the value meets.
			omission = WorseReferenceFailure(omission, err)
			continue
		}
		address := instance.Address
		if authority {
			if address, err = addressAuthority(address); err != nil {
				return "", fmt.Errorf("%s (with %s): %w", position, strings.TrimPrefix(authorityProjection, "|"), err)
			}
		}
		b.WriteString(value[last:match[0]])
		b.WriteString(address)
		last = match[1]
	}
	if omission != nil {
		return "", omission
	}
	b.WriteString(value[last:])
	return b.String(), nil
}

// ReferenceFailureIsAnOmission reports whether a reference failure is one of the
// two facts about ONE consumer's view — the endpoint it was handed no mapping
// for, or may not reach — that a run-wide interpolation is entitled to omit the
// value on. Every other failure is a fault of the composition or of a
// declaration, and is refused wherever it is met.
func ReferenceFailureIsAnOmission(err error) bool {
	return errors.Is(err, errEndpointNotAvailable) || errors.Is(err, ErrEndpointNotReachable)
}

// InterpolateConfigurationEndpoints resolves ${endpoint:…} references in every
// value of conf, using the same resolution as InterpolateEndpointsFor. The endpoint
// address depends on the consuming service's network access, so it must not be
// baked into the shared configuration: when a value carries a reference, a
// resolved clone is returned and conf is left untouched; otherwise conf itself is
// returned unchanged.
//
// This is the variant for a configuration the consumer selected by name, and it
// is strict about what this run can resolve: every reference to a producer the
// run contains must resolve, and one that does not is an error naming the
// configuration, the key, the reference and its producer. Such a value is never
// silently omitted: a consumer that declared the group reads its keys, and a
// missing key surfaces only later, far from its cause, as "not configured". The
// caller hands mappings covering every producer the group references, not only
// the consumer's declared dependencies (a composition root binds producers the
// consuming module cannot name).
//
// A reference to a producer the run does NOT contain is a different fact, and
// not an error: a run that excludes optional infrastructure
// (architecture.ExcludeServices), or that starts one service rather than the
// whole workspace, cannot resolve it and no composition change would make it
// resolvable for that run. One group commonly serves several consumers, so such
// a value is dropped for this consumer — with a WARN naming it, because for a
// group the consumer declared this is worth seeing — and the consumer reports
// the missing key itself if it reads it.
//
// Telling the two apart needs WithRunProducers, and a caller that does not pass
// it has said nothing about the run: that is not evidence that the producer is
// outside it, so an unresolvable reference is an ERROR there rather than a drop.
// What the run contains is the caller's knowledge, not something mappings can be
// read for — an absent mapping is exactly the symptom of the bug this path exists
// to catch, and a render that binds none would otherwise have every address it
// declares dropped and report nothing but a WARN. A deployed render losing one
// address that way is invisible until a client dials it, which is the cost this
// asymmetry buys back.
//
// For a configuration injected run-wide into services that never declared it,
// use InterpolateRunWideConfigurationEndpoints.
func InterpolateConfigurationEndpoints(ctx context.Context, conf *basev0.Configuration, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess, opts ...ConfigurationInterpolationOption) (*basev0.Configuration, error) {
	return interpolateConfigurationEndpoints(ctx, conf, mappings, access, false, opts...)
}

// ConfigurationInterpolationOption supplies what the strict path cannot read off
// the configuration or the mappings.
type ConfigurationInterpolationOption func(*configurationInterpolation)

type configurationInterpolation struct {
	// producerInRun reports whether a <module>/<service> is part of this run.
	// Nil means the caller did not say, so nothing is provably in the run.
	producerInRun func(unique string) bool
	// selection is what this resolution knows about its consumer. Both halves
	// are required once a value carries a reference; see WithConsumer.
	selection EndpointSelectionContext
}

// WithConsumer tells the resolution which module is receiving these addresses,
// and how to read a producer's declared endpoints. Both are required: a
// configuration carrying an endpoint reference is refused when either is
// missing, because the export boundary is a statement about the consumer's
// module and the manifest is the only authority on which endpoint a token names
// (published mappings establish neither the complete declared set nor a
// declaration that was never published).
func WithConsumer(consumerModule string, declared DeclaredEndpoints) ConfigurationInterpolationOption {
	return func(opt *configurationInterpolation) {
		opt.selection = EndpointSelectionContext{ConsumerModule: consumerModule, Declared: declared}
	}
}

// WithRunProducers tells the strict path which producers this run contains, by
// <module>/<service>. A reference naming one of them must resolve — it is in the
// run, so its endpoint is missing because it was not ordered, not started, or
// not handed to this consumer, which is a fault to report rather than a value to
// drop. A reference naming anything else is not for this run and is dropped for
// this consumer.
//
// It is what makes a drop legitimate. Passing no run set, or one that reports
// nothing (a nil func), leaves the strict path with no basis to drop, so it fails
// instead: silently omitting a declared address because the caller never said
// what it was rendering is the failure this option exists to make impossible.
func WithRunProducers(inRun func(unique string) bool) ConfigurationInterpolationOption {
	return func(opt *configurationInterpolation) {
		opt.producerInRun = inRun
	}
}

// InterpolateRunWideConfigurationEndpoints is InterpolateConfigurationEndpoints
// for a configuration the composition root injects run-wide into every service.
// Such a configuration reaches leaf services that never declared the referenced
// endpoint and so have it absent from their per-consumer mapping set. A value
// whose ${endpoint:…} names an endpoint this consumer was handed no mapping
// for, or may not reach under a valid export policy, is not for it: the value
// is dropped — along with an information left with no values — rather than
// failing the service. Those are the only two omissions; every other failure
// (an ambiguous or malformed reference, an invalid declaration, an unknown
// producer, no instance for the access) is refused here as on the strict path.
// The decision is per endpoint reference, not per service: a value referencing
// one endpoint of a service the consumer depends on for a *different* endpoint
// is still dropped, because this consumer has no instance for the referenced
// one. Contrast the strict variant, which drops only an unavailable endpoint of
// a producer outside the run.
//
// A per-consumer drop is a judgement about one consumer of a render, so it needs
// a render that said what it was rendering. The rule is the strict path's, for
// the same reason: a caller that states no run set has said nothing, and dropping
// on nothing is how a render that never bound its network context looks exactly
// like a leaf service that legitimately sees no endpoint. This is the path the
// composition root's own groups take — the run-wide authority address every
// service reads — so left silent it is precisely how that address goes missing
// from a deployed workload with nothing but a DEBUG line behind it.
//
// With the run set stated, an empty mapping set is still a drop: that is the leaf
// consumer, and it is the case this path exists for.
func InterpolateRunWideConfigurationEndpoints(ctx context.Context, conf *basev0.Configuration, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess, opts ...ConfigurationInterpolationOption) (*basev0.Configuration, error) {
	return interpolateConfigurationEndpoints(ctx, conf, mappings, access, true, opts...)
}

func interpolateConfigurationEndpoints(ctx context.Context, conf *basev0.Configuration, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess, dropUnresolved bool, opts ...ConfigurationInterpolationOption) (*basev0.Configuration, error) {
	if conf == nil || !configurationHasEndpointReference(conf) {
		return conf, nil
	}
	options := &configurationInterpolation{}
	for _, opt := range opts {
		opt(options)
	}
	w := wool.Get(ctx).In("resources.InterpolateConfigurationEndpoints")
	cloned, ok := proto.Clone(conf).(*basev0.Configuration)
	if !ok {
		return nil, w.NewError("cloning a configuration did not produce a configuration")
	}
	infos := cloned.Infos[:0]
	for _, info := range cloned.Infos {
		values := info.ConfigurationValues[:0]
		for _, value := range info.ConfigurationValues {
			resolved, err := interpolateConfigurationValue(ctx, value, mappings, access, options.selection)
			if err != nil {
				// In the run-wide path, a value the consumer cannot satisfy is
				// simply not for it: drop the value (and, below, an information
				// left with no values) rather than fail the service. The strict
				// path propagates the error, naming the key — unless the only
				// thing wrong is that the value names a producer this run
				// provably does not contain, which no composition change would
				// fix for this run. "Provably" is the caller's run set: with none,
				// nothing is out of the run and nothing may be dropped.
				if !dropUnresolved && errors.Is(err, errEndpointNotAvailable) && options.producerInRun != nil &&
					!unresolvedNamesRunProducer(ctx, value, mappings, access, options.producerInRun, options.selection) {
					// WARN, not DEBUG: the consumer selected this group by name,
					// so a key of it going missing is worth seeing even though
					// the run is right to continue. The message carries the
					// producer, which is what says whether the run was meant to
					// contain it.
					w.Warn("dropping a configuration value the consumer selected: its endpoint reference names a producer this run does not contain",
						wool.Field("configuration", info.Name),
						wool.Field("key", value.Key),
						wool.Field("reason", err.Error()))
					continue
				}
				if dropUnresolved && options.producerInRun == nil {
					return nil, w.Wrapf(err, "cannot interpolate run-wide configuration %s/%s: this render stated no run producers, so a value this consumer cannot see cannot be told from a render that never bound its network context; state what this run contains (configurations.Manager.WithRunProducers)", info.Name, value.Key)
				}
				if dropUnresolved && !ReferenceFailureIsAnOmission(err) {
					// Not every failure is "not for this consumer". An ambiguous
					// reference, an API qualifier the named endpoint does not
					// serve, a producer the workspace does not declare, an
					// invalid declaration, an endpoint with no instance for this
					// access, a malformed reference: each is a fault of the
					// composition or of a declaration, and dropping the value
					// would deliver a configuration with a key silently missing
					// where a refusal was owed. Only the two facts about this
					// consumer's own view — an endpoint it was handed no mapping
					// for, or may not reach under a valid export policy — are
					// omissions.
					return nil, w.Wrapf(err, "cannot interpolate run-wide configuration %s/%s", info.Name, value.Key)
				}
				if dropUnresolved {
					// The drop is expected in the common case — a run-wide value
					// is interpolated for every service and only those depending
					// on the endpoint resolve it — so this is DEBUG, not WARN: a
					// WARN would fire on every boot for every non-consumer and
					// train operators to ignore it. But it must not vanish
					// silently: if a mapping that should have propagated did not,
					// this breadcrumb (run with --debug) is what turns an
					// otherwise silent runtime misconfiguration into a
					// diagnosable one.
					w.Debug("omitting run-wide configuration value: endpoint reference does not resolve for this consumer",
						wool.Field("configuration", info.Name),
						wool.Field("key", value.Key),
						wool.Field("reason", err.Error()))
					continue
				}
				if !dropUnresolved && options.producerInRun == nil && errors.Is(err, errEndpointNotAvailable) {
					// Naming the remedy here rather than leaving a bare "not
					// found": the reference may be perfectly good and the render
					// simply never bound the run's mappings, which is the one
					// cause the diagnostic cannot be derived from.
					return nil, w.Wrapf(err, "cannot interpolate configuration %s/%s: this render stated no run producers, so an endpoint it cannot resolve cannot be told from mappings it never bound; bind the run's network mappings and state its producers (configurations.Manager.WithNetworkMappings / WithRunProducers)", info.Name, value.Key)
				}
				return nil, w.Wrapf(err, "cannot interpolate configuration %s/%s", info.Name, value.Key)
			}
			value.Value = resolved
			values = append(values, value)
		}
		// Drop an information only when run-wide dropping emptied it: it had
		// values and every one was unsatisfiable for this consumer, so injecting
		// an empty block would be noise. An information that started with no
		// values is not a drop victim — it is preserved unchanged, so the strict
		// path (which drops nothing) returns exactly the structure it was given.
		if dropUnresolved && len(values) == 0 && len(info.ConfigurationValues) > 0 {
			continue
		}
		info.ConfigurationValues = values
		infos = append(infos, info)
	}
	cloned.Infos = infos
	return cloned, nil
}

func configurationHasEndpointReference(conf *basev0.Configuration) bool {
	for _, info := range conf.Infos {
		for _, value := range info.ConfigurationValues {
			if endpointInterpolationPattern.MatchString(value.Value) || malformedEndpointMarker(value.Value) {
				return true
			}
			// A value carrying a template holds its text in the template's
			// literals, so an ${endpoint:…} a producer wrote there is here and
			// nowhere else. Reading only value.Value would report no reference
			// and leave the whole pass skipped, which leaves a producer no way
			// to write a template except by typing the address the network model
			// is there to resolve.
			for _, segment := range value.GetTemplate().GetSegments() {
				if endpointInterpolationPattern.MatchString(segment.GetLiteral()) || malformedEndpointMarker(segment.GetLiteral()) {
					return true
				}
			}
		}
	}
	return false
}

// interpolateConfigurationValue resolves the endpoint references of one
// configuration value, in its value and in the literals of the template it
// carries, and returns what value.Value becomes. The template's literals are
// interpolated in place on the clone the caller already made; a reference that
// does not resolve fails the whole value, so the caller's drop-or-fail decision
// applies to a templated value exactly as it does to a plain one — a value
// assembled from a half-interpolated literal would otherwise reach a workload
// with "${endpoint:…}" in the middle of a connection string.
func interpolateConfigurationValue(ctx context.Context, value *basev0.ConfigurationValue, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess, selection EndpointSelectionContext) (string, error) {
	// The same dominance rule as within one value, across the value's parts: a
	// composition fault in any literal or in the value itself is reported over
	// an omission in another, whichever came first.
	// References are numbered across the whole value, in the order
	// ConfigurationValueEndpointReferences lists them — the value's own, then
	// each literal's — so a render and the plan-time check name the same
	// reference by the same number.
	var omission error
	total := len(ConfigurationValueEndpointReferences(value))
	base := len(EndpointReferences(value.GetValue()))
	for _, segment := range value.GetTemplate().GetSegments() {
		literal, isLiteral := segment.GetContent().(*basev0.ConfigurationValueTemplateSegment_Literal)
		if !isLiteral {
			continue
		}
		count := len(EndpointReferences(literal.Literal))
		resolved, err := interpolateEndpointsAt(ctx, literal.Literal, mappings, access, selection, base, total)
		base += count
		if err != nil {
			if !ReferenceFailureIsAnOmission(err) {
				return "", err
			}
			omission = WorseReferenceFailure(omission, err)
			continue
		}
		literal.Literal = resolved
	}
	resolved, err := interpolateEndpointsAt(ctx, value.Value, mappings, access, selection, 0, total)
	if err != nil {
		if !ReferenceFailureIsAnOmission(err) {
			return "", err
		}
		omission = WorseReferenceFailure(omission, err)
	}
	if omission != nil {
		return "", omission
	}
	return resolved, nil
}

// resolveEndpointReference resolves reference against mappings for access, by
// first SELECTING the one endpoint the reference names and then binding only
// that endpoint's address.
//
// It used to scan the mappings and take the first that matched the reference and
// had an instance for the access. That made three things decided by accident —
// which of several matching endpoints answered, whether the consumer was allowed
// to reach it, and what happened when the endpoint the reference named had no
// address for this access while a sibling sharing its API did. The answer was
// publication order, no judgement, and a silent fall-through to the sibling.
// Consumers of this package were left reimplementing selection beside it, where
// they could only model this scan rather than ask it.
//
// Now SelectEndpointForReference answers the reference once, and the mappings
// are searched for THAT endpoint by name. Order stops mattering, a sibling can
// never answer, a producer publishing several mappings for one endpoint is
// simply several places to find it, and an endpoint with no address for this
// access is reported as what it is rather than replaced.
//
// A malformed reference, an endpoint absent from mappings, or a missing instance
// for access all return an error — the caller (InterpolateConfigurationEndpoints
// vs InterpolateRunWideConfigurationEndpoints) decides whether an unresolved
// reference fails the service or is dropped for a consumer that does not depend
// on it.
// resolveEndpointReference resolves one validated reference. position is how
// a diagnostic names it ("reference 2 of 3"): a reference is text from a
// value, a value may be a secret, and a diagnostic that printed the reference
// — even one whose every token validated — would print the value. What a
// diagnostic may name is what the manifest and the run say: declared endpoint
// names, published ones, the access.
func resolveEndpointReference(ctx context.Context, mappings []*basev0.NetworkMapping, info *EndpointInformation, position string, access *basev0.NetworkAccess, selection EndpointSelectionContext) (*basev0.NetworkInstance, error) {
	w := wool.Get(ctx).In("resources.resolveEndpointReference")
	reference := position

	available := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		if mapping == nil || mapping.Endpoint == nil {
			continue
		}
		available = append(available, EndpointFromProto(mapping.Endpoint).Unique())
	}

	// The producer's manifest, and nothing else: it declares endpoints this
	// producer published no mapping for, which is the difference between "the
	// endpoint you named is not running" and "the endpoint you named does not
	// exist", and it is the only source that carries who may reach what. A
	// producer the workspace does not declare is a composition fault, not an
	// availability fact, so it does not carry errEndpointNotAvailable.
	candidates, declared := selection.Declared(info.Module + "/" + info.Service)
	if !declared {
		return nil, fmt.Errorf("%s: %w", reference, ErrUnknownProducer)
	}
	selected, err := SelectEndpointForReference(selection.ConsumerModule, info, candidates)
	if err != nil {
		return nil, w.Wrapf(err, "%s cannot be resolved for this consumer", reference)
	}

	// Only the endpoint that was selected, by name. A producer may publish more
	// than one mapping for it; they are searched in order and the first with an
	// instance for this access answers.
	// Every mapping of the selected endpoint is judged BEFORE any is bound: a
	// mapping published under the selected name that STATES another API is
	// conflicting metadata whichever order it was published in, and binding
	// an earlier mapping's address would hide it. A mapping that states no API
	// is not in conflict; the declaration is the authority.
	var named []*basev0.NetworkMapping
	for _, mapping := range mappings {
		if mapping == nil || mapping.Endpoint == nil {
			continue
		}
		endpoint := mapping.Endpoint
		if endpoint.Module != info.Module || endpoint.Service != info.Service || endpoint.Name != selected.Endpoint.Name {
			continue
		}
		if endpoint.Api != "" && selected.Endpoint.API != "" && endpoint.Api != selected.Endpoint.API {
			return nil, w.NewError("%s names endpoint %q declared with api %q, but a mapping for it carries api %q — conflicting mapping metadata",
				reference, selected.Endpoint.Name, selected.Endpoint.API, endpoint.Api)
		}
		named = append(named, mapping)
	}
	matchedButNoAccess := false
	for _, mapping := range named {
		matchedButNoAccess = true
		for _, instance := range mapping.Instances {
			if !accessKindMatches(instance, access) {
				continue
			}
			if instance.Address == "" {
				return nil, w.NewError("%s resolved to an empty address for access=%s", reference, accessKind(access))
			}
			return instance, nil
		}
	}
	if matchedButNoAccess {
		return nil, w.NewError("%s names endpoint %q, which has no instance for access=%s; available: %v",
			reference, selected.Endpoint.Name, accessKind(access), available)
	}
	return nil, fmt.Errorf("%s names endpoint %q, which published no network mapping for this consumer (access=%s): its producer is not part of the run or did not publish it; available endpoints: %v: %w",
		reference, selected.Endpoint.Name, accessKind(access), available, errEndpointNotAvailable)
}

// unresolvedNamesRunProducer reports whether any ${endpoint:…} reference the
// value carries names a producer this run contains AND fails to resolve for this
// consumer. That is the fault the strict path must report: the producer is in
// the run, so its address exists somewhere and this consumer was not given it.
//
// It is evaluated per reference rather than on the first failure, so a value
// mixing an in-run producer with an out-of-run one is judged by the in-run one
// whichever order they appear in.
//
// References come from the whole value — ConfigurationValueEndpointReferences,
// the representation the plan-time check and the manager already use — and not
// from value.Value. A value whose producer declared an assembly holds its text in
// the template's literals and its Value is empty, so reading Value alone found no
// reference, judged every such value out of the run, and DELETED a credential
// assembled around the address of a producer the caller had just said was in it.
func unresolvedNamesRunProducer(ctx context.Context, value *basev0.ConfigurationValue, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess, inRun func(string) bool, selection EndpointSelectionContext) bool {
	if inRun == nil {
		return false
	}
	for _, reference := range ConfigurationValueEndpointReferences(value) {
		info, err := parseEndpointReference(reference)
		if err != nil {
			// A malformed reference is refused by the resolution itself,
			// whichever producer it meant; it does not decide the run set.
			continue
		}
		if !inRun(info.Module + "/" + info.Service) {
			continue
		}
		if _, err := resolveEndpointReference(ctx, mappings, info, "a reference", access, selection); err != nil {
			return true
		}
	}
	return false
}

// accessKindNone is the access kind of a caller that stated no access. It is
// not an access the platform has; it names the absence in a message.
const accessKindNone = "none"

func accessKind(access *basev0.NetworkAccess) string {
	if access == nil {
		return accessKindNone
	}
	return access.Kind
}

// endpointMatchesReferenceInfo is the one matching predicate behind selection:
// an endpoint satisfies a parsed ${endpoint:…} reference when the reference's
// token is its name or its API, and any API qualifier agrees. Module and service
// are not compared here; SelectEndpointForReference scopes the candidates
// first. It is not exported: matching is a step of selection, and a caller that
// matched for itself would be a second selector.
func endpointMatchesReferenceInfo(endpoint *Endpoint, info *EndpointInformation) bool {
	if endpoint == nil || info == nil {
		return false
	}
	if info.API != "" && endpoint.API != info.API {
		return false
	}
	if info.Name == "" {
		return endpoint.API == info.API
	}
	return endpoint.Name == info.Name || endpoint.API == info.Name
}
