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
	resolved, _, err := interpolateEndpointsValue(ctx, value, mappings, access)
	return resolved, err
}

// interpolateEndpointsValue resolves every reference in value. absent is true
// when a reference names a module/service missing from mappings entirely — the
// consumer does not depend on that endpoint — so a run-wide workspace-config
// value referencing it can be dropped for this consumer rather than failing it. A
// reference to a service the consumer does have but with a wrong endpoint or no
// instance for access is a genuine misconfiguration: err is set with absent
// false.
func interpolateEndpointsValue(ctx context.Context, value string, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess) (string, bool, error) {
	matches := endpointInterpolationPattern.FindAllStringSubmatchIndex(value, -1)
	if matches == nil {
		return value, false, nil
	}
	var b strings.Builder
	last := 0
	for _, match := range matches {
		reference := value[match[2]:match[3]]
		instance, absent, err := resolveEndpointReference(ctx, mappings, reference, access)
		if err != nil {
			return "", absent, err
		}
		b.WriteString(value[last:match[0]])
		b.WriteString(instance.Address)
		last = match[1]
	}
	b.WriteString(value[last:])
	return b.String(), false, nil
}

// InterpolateConfigurationEndpoints resolves ${endpoint:…} references in every
// value of conf, using the same resolution as InterpolateEndpoints. The endpoint
// address depends on the consuming service's network access, so it must not be
// baked into the shared configuration: when a value carries a reference, a
// resolved clone is returned and conf is left untouched; otherwise conf itself is
// returned unchanged.
//
// A run-wide workspace configuration is interpolated for every service, including
// leaf services that do not depend on the referenced endpoint and so have it
// absent from their mapping set. A value referencing such an endpoint is not for
// that consumer: it is dropped — along with an information left with no values —
// rather than failing the service. A reference to a service the consumer does
// depend on but with a wrong endpoint or no instance for access stays a hard
// error.
func InterpolateConfigurationEndpoints(ctx context.Context, conf *basev0.Configuration, mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess) (*basev0.Configuration, error) {
	if conf == nil || !configurationHasEndpointReference(conf) {
		return conf, nil
	}
	w := wool.Get(ctx).In("resources.InterpolateConfigurationEndpoints")
	cloned := proto.Clone(conf).(*basev0.Configuration)
	infos := cloned.Infos[:0]
	for _, info := range cloned.Infos {
		values := info.ConfigurationValues[:0]
		for _, value := range info.ConfigurationValues {
			resolved, absent, err := interpolateEndpointsValue(ctx, value.Value, mappings, access)
			if err != nil {
				// A dependency-less workspace configuration is interpolated for
				// every service. A reference to an endpoint the consumer does not
				// depend on (absent from its mapping set) is simply not for this
				// consumer: drop the value rather than fail the service. A wrong
				// endpoint on a service it does depend on stays a hard error.
				if absent {
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

// resolveEndpointReference resolves reference against mappings for access. The
// absent return is true only when no mapping names the reference's module/service
// at all — the consumer does not depend on that endpoint. A malformed reference,
// a wrong endpoint on a service the consumer does have, or a missing instance for
// access are misconfigurations: err is set with absent false.
func resolveEndpointReference(ctx context.Context, mappings []*basev0.NetworkMapping, reference string, access *basev0.NetworkAccess) (*basev0.NetworkInstance, bool, error) {
	w := wool.Get(ctx).In("resources.resolveEndpointReference")
	info, err := ParseEndpoint(reference)
	if err != nil {
		return nil, false, w.Wrapf(err, "invalid endpoint reference ${endpoint:%s}", reference)
	}
	if info.Name == "" && info.API == "" {
		return nil, false, w.NewError("endpoint reference ${endpoint:%s} must name an endpoint (module/service/endpoint)", reference)
	}
	var matchedButNoAccess bool
	var serviceSeen bool
	available := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		if mapping == nil || mapping.Endpoint == nil {
			continue
		}
		available = append(available, EndpointFromProto(mapping.Endpoint).Unique())
		if mapping.Endpoint.Module == info.Module && mapping.Endpoint.Service == info.Service {
			serviceSeen = true
		}
		if !endpointReferenceMatchesInfo(mapping.Endpoint, info) {
			continue
		}
		matchedButNoAccess = true
		for _, instance := range mapping.Instances {
			if accessKindMatches(instance, access) {
				if instance.Address == "" {
					return nil, false, w.NewError("endpoint reference ${endpoint:%s} resolved to an empty address for access=%s", reference, accessKind(access))
				}
				return instance, false, nil
			}
		}
	}
	if matchedButNoAccess {
		return nil, false, w.NewError("endpoint reference ${endpoint:%s} matched but has no instance for access=%s; available: %v", reference, accessKind(access), available)
	}
	return nil, !serviceSeen, w.NewError("endpoint reference ${endpoint:%s} not found (access=%s); available endpoints: %v", reference, accessKind(access), available)
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
