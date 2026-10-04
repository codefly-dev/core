package resources

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
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

// errEndpointNotAvailable marks a well-formed reference to an endpoint absent
// from the consumer's mappings. It is the one unresolved reference that can be
// legitimate, so it is the one the callers branch on: the run-wide path drops
// such a value for the consumer, and the strict path drops it only when the
// producer it names is not part of this run (see WithRunProducers) and fails
// otherwise. Every other failure — a malformed reference, an endpoint matched
// with no instance for the consumer's access — is a hard error on both paths
// that reach it, and must not carry this sentinel.
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

// InterpolateEndpoints replaces every ${endpoint:<module>/<service>/<endpoint>}
// reference in value with the endpoint's runtime address, resolved from mappings
// for access — the same address published as
// CODEFLY__ENDPOINT__<MODULE>__<SERVICE>__<ENDPOINT>. A value with no reference is
// returned unchanged. An unresolvable reference is an error, never a broken URL.
func InterpolateEndpoints(ctx context.Context, value string, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess) (string, error) {
	return InterpolateEndpointsFor(ctx, value, mappings, access, EndpointSelectionContext{})
}

// InterpolateEndpointsFor is InterpolateEndpoints for a caller that knows which
// consumer it is resolving for, and can therefore have the producer's export
// boundary enforced on the reference and the producer's manifest decide which
// endpoint it names. A zero selection is InterpolateEndpoints.
func InterpolateEndpointsFor(ctx context.Context, value string, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess, selection EndpointSelectionContext) (string, error) {
	matches := endpointInterpolationPattern.FindAllStringSubmatchIndex(value, -1)
	if matches == nil {
		return value, nil
	}
	var b strings.Builder
	last := 0
	for _, match := range matches {
		reference, authority := splitEndpointProjection(value[match[2]:match[3]])
		instance, err := resolveEndpointReference(ctx, mappings, reference, access, selection)
		if err != nil {
			return "", err
		}
		address := instance.Address
		if authority {
			if address, err = addressAuthority(address); err != nil {
				return "", fmt.Errorf("endpoint reference ${endpoint:%s%s}: %w", reference, authorityProjection, err)
			}
		}
		b.WriteString(value[last:match[0]])
		b.WriteString(address)
		last = match[1]
	}
	b.WriteString(value[last:])
	return b.String(), nil
}

// InterpolateConfigurationEndpoints resolves ${endpoint:…} references in every
// value of conf, using the same resolution as InterpolateEndpoints. The endpoint
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
	// selection is what this resolution knows about its consumer. A zero value
	// selects from the endpoints the mappings carry and judges no visibility.
	selection EndpointSelectionContext
}

// WithConsumer tells the resolution which module is receiving these addresses,
// and how to read a producer's declared endpoints.
//
// Both are needed to answer a reference the way the plan-time check answers it.
// The consumer's module is the export boundary: a reference is an edge into that
// module like any declared dependency, so an endpoint the module may not reach
// must be refused rather than resolved. The manifest is what says which endpoint
// a token names — a published mapping carries no visibility and a producer may
// publish several mappings for one endpoint, so a scan of mappings alone cannot
// decide it.
//
// Passing neither leaves the resolution selecting from the mappings' own
// endpoints with no visibility judgement, which is all a caller that has not
// identified its consumer can honestly ask for. It is not a fallback to the old
// first-match behaviour: an exact name still wins and an ambiguous reference is
// still refused.
func WithConsumer(consumerModule string, declared func(unique string) []*Endpoint) ConfigurationInterpolationOption {
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
// whose ${endpoint:…} does not resolve for this consumer is not for it: the value
// is dropped — along with an information left with no values — rather than failing
// the service. The decision is per endpoint reference, not per service: a value
// referencing one endpoint of a service the consumer depends on for a *different*
// endpoint is still dropped, because this consumer has no instance for the
// referenced one. Contrast the strict variant, which errors on any unresolved
// reference.
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
			if endpointInterpolationPattern.MatchString(value.Value) {
				return true
			}
			// A value carrying a template holds its text in the template's
			// literals, so an ${endpoint:…} a producer wrote there is here and
			// nowhere else. Reading only value.Value would report no reference
			// and leave the whole pass skipped, which leaves a producer no way
			// to write a template except by typing the address the network model
			// is there to resolve.
			for _, segment := range value.GetTemplate().GetSegments() {
				if endpointInterpolationPattern.MatchString(segment.GetLiteral()) {
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
	for _, segment := range value.GetTemplate().GetSegments() {
		literal, isLiteral := segment.GetContent().(*basev0.ConfigurationValueTemplateSegment_Literal)
		if !isLiteral {
			continue
		}
		resolved, err := InterpolateEndpointsFor(ctx, literal.Literal, mappings, access, selection)
		if err != nil {
			return "", err
		}
		literal.Literal = resolved
	}
	return InterpolateEndpointsFor(ctx, value.Value, mappings, access, selection)
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
func resolveEndpointReference(ctx context.Context, mappings []*basev0.NetworkMapping, reference string, access *basev0.NetworkAccess, selection EndpointSelectionContext) (*basev0.NetworkInstance, error) {
	w := wool.Get(ctx).In("resources.resolveEndpointReference")
	info, err := ParseEndpoint(reference)
	if err != nil {
		return nil, w.Wrapf(err, "invalid endpoint reference ${endpoint:%s}", reference)
	}
	if info.Name == "" && info.API == "" {
		return nil, w.NewError("endpoint reference ${endpoint:%s} must name an endpoint (module/service/endpoint)", reference)
	}

	available := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		if mapping == nil || mapping.Endpoint == nil {
			continue
		}
		available = append(available, EndpointFromProto(mapping.Endpoint).Unique())
	}

	// The producer's manifest when the caller supplied one, and otherwise the
	// endpoints the mappings themselves carry. The manifest is the better
	// source: it declares endpoints this producer published no mapping for,
	// which is the difference between "the endpoint you named is not running"
	// and "the endpoint you named does not exist".
	candidates := selection.declaredFor(info.Module + "/" + info.Service)
	fromManifest := candidates != nil
	if candidates == nil {
		seen := make(map[string]bool, len(mappings))
		for _, mapping := range mappings {
			if mapping == nil || mapping.Endpoint == nil {
				continue
			}
			endpoint := mapping.Endpoint
			if endpoint.Module != info.Module || endpoint.Service != info.Service {
				continue
			}
			if seen[endpoint.Name] {
				continue
			}
			seen[endpoint.Name] = true
			candidates = append(candidates, EndpointFromProto(endpoint))
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("endpoint reference ${endpoint:%s} not found for this consumer (access=%s): producer %s/%s is not part of the run or does not publish that endpoint; available endpoints: %v: %w",
			reference, accessKind(access), info.Module, info.Service, available, errEndpointNotAvailable)
	}
	selected, err := SelectEndpointForReference(selection.ConsumerModule, info, candidates)
	if err != nil {
		// Without a manifest the candidates ARE the published mappings, so
		// "no such endpoint" means this consumer was handed no mapping for it —
		// which is availability, the fact errEndpointNotAvailable names and the
		// run-wide path reads to decide a drop. With a manifest it is a
		// composition fault and must not be mistaken for one.
		if !fromManifest && errors.Is(err, ErrNoSuchEndpoint) {
			return nil, fmt.Errorf("endpoint reference ${endpoint:%s} not found for this consumer (access=%s): producer %s/%s is not part of the run or does not publish that endpoint; available endpoints: %v: %w",
				reference, accessKind(access), info.Module, info.Service, available, errEndpointNotAvailable)
		}
		return nil, w.Wrapf(err, "endpoint reference ${endpoint:%s} cannot be resolved for this consumer", reference)
	}

	// Only the endpoint that was selected, by name. A producer may publish more
	// than one mapping for it; they are searched in order and the first with an
	// instance for this access answers.
	matchedButNoAccess := false
	for _, mapping := range mappings {
		if mapping == nil || mapping.Endpoint == nil {
			continue
		}
		endpoint := mapping.Endpoint
		if endpoint.Module != info.Module || endpoint.Service != info.Service || endpoint.Name != selected.Endpoint.Name {
			continue
		}
		matchedButNoAccess = true
		for _, instance := range mapping.Instances {
			if !accessKindMatches(instance, access) {
				continue
			}
			if instance.Address == "" {
				return nil, w.NewError("endpoint reference ${endpoint:%s} resolved to an empty address for access=%s", reference, accessKind(access))
			}
			return instance, nil
		}
	}
	if matchedButNoAccess {
		return nil, w.NewError("endpoint reference ${endpoint:%s} names endpoint %q, which has no instance for access=%s; available: %v",
			reference, selected.Endpoint.Name, accessKind(access), available)
	}
	return nil, fmt.Errorf("endpoint reference ${endpoint:%s} names endpoint %q, which published no network mapping for this consumer (access=%s): producer %s/%s is not part of the run or did not publish it; available endpoints: %v: %w",
		reference, selected.Endpoint.Name, accessKind(access), info.Module, info.Service, available, errEndpointNotAvailable)
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
		info, err := ParseEndpoint(reference)
		if err != nil || info.Module == "" || info.Service == "" {
			continue
		}
		if !inRun(info.Module + "/" + info.Service) {
			continue
		}
		if _, err := resolveEndpointReference(ctx, mappings, reference, access, selection); err != nil {
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

// EndpointMatchesReferenceInfo reports whether an endpoint a service DECLARES
// satisfies a parsed ${endpoint:…} reference, by the same rule
// endpointReferenceMatchesInfo applies to the mapping the reference resolves to.
// Module and service are not compared: a caller reaches this with the producer
// already identified.
//
// One body rather than a copy per caller: a plan-time check that judged a
// reference differently from the resolution would pass compositions that then
// fail, or refuse ones that would have worked.
func EndpointMatchesReferenceInfo(endpoint *Endpoint, info *EndpointInformation) bool {
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
