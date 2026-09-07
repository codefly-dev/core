package resources

import (
	"context"
	"regexp"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/wool"
	"google.golang.org/protobuf/proto"
)

// endpointInterpolationPattern matches ${endpoint:<module>/<service>/<endpoint>}
// references embedded in a configuration value.
var endpointInterpolationPattern = regexp.MustCompile(`\$\{endpoint:([^{}]+)\}`)

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
		reference := value[match[2]:match[3]]
		instance, err := resolveEndpointReference(ctx, mappings, reference, access)
		if err != nil {
			return "", err
		}
		b.WriteString(value[last:match[0]])
		b.WriteString(instance.Address)
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
// This is the strict, fail-fast variant: any reference that does not resolve for
// access is a hard error. Use it when the caller requested this configuration —
// by name or in full — and every reference is expected to resolve for the
// consumer. For a configuration injected run-wide into services that never
// declared the referenced endpoint, use InterpolateRunWideConfigurationEndpoints.
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
			resolved, err := InterpolateEndpoints(ctx, value.Value, mappings, access)
			if err != nil {
				// In the run-wide path, a value the consumer cannot satisfy is
				// simply not for it: drop the value (and, below, an information
				// left with no values) rather than fail the service. The strict
				// path propagates the error.
				if dropUnresolved {
					continue
				}
				return nil, w.Wrapf(err, "cannot interpolate configuration %s/%s", info.Name, value.Key)
			}
			value.Value = resolved
			values = append(values, value)
		}
		if len(values) == 0 {
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
	return nil, w.NewError("endpoint reference ${endpoint:%s} not found (access=%s); available endpoints: %v", reference, accessKind(access), available)
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
