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
// from the consumer's mappings: the consumer does not depend on it.
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
// This is the variant for a configuration the consumer selected by name. One
// group commonly serves several consumers that each read some of its keys, so a
// value referencing an endpoint the consumer does not depend on is omitted for
// it, and the consumer reports the missing key itself if it reads it. Every
// other failure is a hard error: a malformed reference, or an endpoint the
// consumer depends on with no instance for its access. For a configuration
// injected run-wide into services that never declared it, use
// InterpolateRunWideConfigurationEndpoints.
func InterpolateConfigurationEndpoints(ctx context.Context, conf *basev0.Configuration, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess) (*basev0.Configuration, error) {
	return interpolateConfigurationEndpoints(ctx, conf, mappings, access, false)
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

func interpolateConfigurationEndpoints(ctx context.Context, conf *basev0.Configuration, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess, dropUnresolved bool) (*basev0.Configuration, error) {
	if conf == nil || !configurationHasEndpointReference(conf) {
		return conf, nil
	}
	w := wool.Get(ctx).In("resources.InterpolateConfigurationEndpoints")
	cloned := proto.Clone(conf).(*basev0.Configuration)
	infos := cloned.Infos[:0]
	for _, info := range cloned.Infos {
		values := info.ConfigurationValues[:0]
		for _, value := range info.ConfigurationValues {
			resolved, err := interpolateConfigurationValue(ctx, value, mappings, access)
			if err != nil {
				// In the run-wide path, a value the consumer cannot satisfy is
				// simply not for it: drop the value (and, below, an information
				// left with no values) rather than fail the service. The strict
				// path propagates the error.
				if !dropUnresolved && errors.Is(err, errEndpointNotAvailable) {
					w.Debug("omitting configuration value: its endpoint is not a dependency of this consumer",
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
func interpolateConfigurationValue(ctx context.Context, value *basev0.ConfigurationValue, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess) (string, error) {
	for _, segment := range value.GetTemplate().GetSegments() {
		literal, isLiteral := segment.GetContent().(*basev0.ConfigurationValueTemplateSegment_Literal)
		if !isLiteral {
			continue
		}
		resolved, err := InterpolateEndpoints(ctx, literal.Literal, mappings, access)
		if err != nil {
			return "", err
		}
		literal.Literal = resolved
	}
	return InterpolateEndpoints(ctx, value.Value, mappings, access)
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
	return nil, fmt.Errorf("endpoint reference ${endpoint:%s} not found (access=%s); available endpoints: %v: %w", reference, accessKind(access), available, errEndpointNotAvailable)
}

func accessKind(access *basev0.NetworkAccess) string {
	if access == nil {
		return "none"
	}
	return access.Kind
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
