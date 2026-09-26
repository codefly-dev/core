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
	matches := endpointInterpolationPattern.FindAllStringSubmatchIndex(value, -1)
	if matches == nil {
		return value, nil
	}
	var b strings.Builder
	last := 0
	for _, match := range matches {
		reference, authority := splitEndpointProjection(value[match[2]:match[3]])
		instance, err := resolveEndpointReference(ctx, mappings, reference, access)
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
// the missing key itself if it reads it. Pass WithRunProducers so this path can
// tell the two apart; without it, no producer is provably part of the run and
// every unavailable endpoint is a drop. What the run contains is the caller's
// knowledge, not something mappings can be read for: an absent mapping is
// exactly the symptom of the bug the strict path exists to catch.
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
}

// WithRunProducers tells the strict path which producers this run contains, by
// <module>/<service>. A reference naming one of them must resolve — it is in the
// run, so its endpoint is missing because it was not ordered, not started, or
// not handed to this consumer, which is a fault to report rather than a value to
// drop. A reference naming anything else is not for this run.
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
func InterpolateRunWideConfigurationEndpoints(ctx context.Context, conf *basev0.Configuration, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess) (*basev0.Configuration, error) {
	return interpolateConfigurationEndpoints(ctx, conf, mappings, access, true)
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
	cloned := proto.Clone(conf).(*basev0.Configuration)
	infos := cloned.Infos[:0]
	for _, info := range cloned.Infos {
		values := info.ConfigurationValues[:0]
		for _, value := range info.ConfigurationValues {
			resolved, err := InterpolateEndpoints(ctx, value.Value, mappings, access)
			if err != nil {
				// In the run-wide path, a value the consumer cannot satisfy is
				// simply not for it: drop the value (and, below, an information
				// left with no values) rather than fail the service. The strict
				// path propagates the error, naming the key — unless the only
				// thing wrong is that the value names a producer this run does
				// not contain, which no composition change would fix for this
				// run.
				if !dropUnresolved && errors.Is(err, errEndpointNotAvailable) &&
					!unresolvedNamesRunProducer(ctx, value.Value, mappings, access, options.producerInRun) {
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
		}
	}
	return false
}

// resolveEndpointReference resolves reference against mappings for access. A
// malformed reference, an endpoint absent from mappings, or a missing instance
// for access all return an error — the caller (InterpolateConfigurationEndpoints
// vs InterpolateRunWideConfigurationEndpoints) decides whether an unresolved
// reference fails the service or is dropped for a consumer that does not depend on
// it.
func resolveEndpointReference(ctx context.Context, mappings []*basev0.NetworkMapping, reference string, access *basev0.NetworkAccess) (*basev0.NetworkInstance, error) {
	w := wool.Get(ctx).In("resources.resolveEndpointReference")
	info, err := ParseEndpoint(reference)
	if err != nil {
		return nil, w.Wrapf(err, "invalid endpoint reference ${endpoint:%s}", reference)
	}
	if info.Name == "" && info.API == "" {
		return nil, w.NewError("endpoint reference ${endpoint:%s} must name an endpoint (module/service/endpoint)", reference)
	}
	var matchedButNoAccess bool
	available := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		if mapping == nil || mapping.Endpoint == nil {
			continue
		}
		available = append(available, EndpointFromProto(mapping.Endpoint).Unique())
		if !endpointReferenceMatchesInfo(mapping.Endpoint, info) {
			continue
		}
		matchedButNoAccess = true
		for _, instance := range mapping.Instances {
			if accessKindMatches(instance, access) {
				if instance.Address == "" {
					return nil, w.NewError("endpoint reference ${endpoint:%s} resolved to an empty address for access=%s", reference, accessKind(access))
				}
				return instance, nil
			}
		}
	}
	if matchedButNoAccess {
		return nil, w.NewError("endpoint reference ${endpoint:%s} matched but has no instance for access=%s; available: %v", reference, accessKind(access), available)
	}
	return nil, fmt.Errorf("endpoint reference ${endpoint:%s} not found for this consumer (access=%s): producer %s/%s is not part of the run or does not publish that endpoint; available endpoints: %v: %w",
		reference, accessKind(access), info.Module, info.Service, available, errEndpointNotAvailable)
}

// unresolvedNamesRunProducer reports whether any ${endpoint:…} reference in
// value names a producer this run contains AND fails to resolve for this
// consumer. That is the fault the strict path must report: the producer is in
// the run, so its address exists somewhere and this consumer was not given it.
//
// It is evaluated per reference rather than on the first failure, so a value
// mixing an in-run producer with an out-of-run one is judged by the in-run one
// whichever order they appear in.
func unresolvedNamesRunProducer(ctx context.Context, value string, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess, inRun func(string) bool) bool {
	if inRun == nil {
		return false
	}
	for _, reference := range EndpointReferences(value) {
		info, err := ParseEndpoint(reference)
		if err != nil || info.Module == "" || info.Service == "" {
			continue
		}
		if !inRun(info.Module + "/" + info.Service) {
			continue
		}
		if _, err := resolveEndpointReference(ctx, mappings, reference, access); err != nil {
			return true
		}
	}
	return false
}

func accessKind(access *basev0.NetworkAccess) string {
	if access == nil {
		return "none"
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

// endpointReferenceMatchesInfo reports whether endpoint satisfies a parsed
// reference. The trailing token (info.Name) matches either the endpoint's name or
// its API, so ${endpoint:m/s/http} resolves whether the endpoint is named "http"
// or exposes the http API under another name.
func endpointReferenceMatchesInfo(endpoint *basev0.Endpoint, info *EndpointInformation) bool {
	if endpoint.Module != info.Module || endpoint.Service != info.Service {
		return false
	}
	if info.API != "" && endpoint.Api != info.API {
		return false
	}
	if info.Name == "" {
		return endpoint.Api == info.API
	}
	return endpoint.Name == info.Name || endpoint.Api == info.Name
}
