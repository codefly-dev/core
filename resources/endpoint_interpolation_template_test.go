package resources

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

func templatedConnectionConfiguration(literal string) *basev0.Configuration {
	return &basev0.Configuration{
		Origin: "mod/store",
		Infos: []*basev0.ConfigurationInformation{{
			Name: "postgres",
			ConfigurationValues: []*basev0.ConfigurationValue{
				{Key: "POSTGRES_PASSWORD", Value: "hunter2", Secret: true},
				{Key: "CONNECTION", Secret: true, Template: &basev0.ConfigurationValueTemplate{
					Segments: []*basev0.ConfigurationValueTemplateSegment{
						templateLiteral("postgresql://reader:"),
						templateReference("postgres", "POSTGRES_PASSWORD",
							basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_URL_USERINFO),
						templateLiteral(literal),
					},
				}},
			},
		}},
	}
}

// A producer writes ${endpoint:…} in a template's literals, because that is
// where a templated value's text lives. Reading only value.Value reported no
// reference at all, so the whole interpolation pass was skipped and the only
// template a producer could write was one with the address typed into it — the
// thing the network model exists to resolve.
func TestInterpolateConfigurationEndpointsResolvesTemplateLiterals(t *testing.T) {
	conf := templatedConnectionConfiguration("@${endpoint:mod/store/postgres}/app")
	if !configurationHasEndpointReference(conf) {
		t.Fatal("a reference in a template literal must be seen by the pass")
	}
	mappings := []*basev0.NetworkMapping{{
		Endpoint: &basev0.Endpoint{Module: "mod", Service: "store", Name: "postgres"},
		Instances: []*basev0.NetworkInstance{{
			Access:  NewContainerNetworkAccess(),
			Address: "store.svc:5432",
		}},
	}}
	interpolated, err := InterpolateConfigurationEndpoints(context.Background(), conf, mappings, NewContainerNetworkAccess())
	if err != nil {
		t.Fatal(err)
	}
	assembled, err := ConfigurationAsEnvironmentVariables(interpolated, "production", true)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, env := range assembled {
		if env.Key == "CODEFLY__SERVICE_SECRET_CONFIGURATION__MOD__STORE__POSTGRES__CONNECTION" {
			got = env.ValueAsString()
		}
	}
	if got != "postgresql://reader:hunter2@store.svc:5432/app" {
		t.Errorf("assembled = %q", got)
	}
	// The source configuration is a shared input: the literals are interpolated
	// on the clone the pass already makes, never in place.
	for _, segment := range conf.Infos[0].ConfigurationValues[1].GetTemplate().GetSegments() {
		if segment.GetLiteral() == "@store.svc:5432/app" {
			t.Error("interpolation mutated the source configuration's template literal")
		}
	}
}

// An unresolvable endpoint in a template literal must decide the whole value,
// exactly as one in value.Value does: never a literal left carrying
// "${endpoint:…}" in the middle of a connection string, and never a value
// assembled from a half-interpolated literal.
func TestInterpolateConfigurationEndpointsRefusesAnUnresolvableTemplateLiteral(t *testing.T) {
	// An endpoint this consumer does not depend on: both paths omit the value,
	// which is what they already do for the same reference in value.Value.
	conf := templatedConnectionConfiguration("@${endpoint:mod/absent/postgres}/app")
	for _, c := range []struct {
		name        string
		interpolate func() (*basev0.Configuration, error)
	}{
		{"strict", func() (*basev0.Configuration, error) {
			return InterpolateConfigurationEndpoints(context.Background(), conf, nil, NewContainerNetworkAccess())
		}},
		{"run-wide", func() (*basev0.Configuration, error) {
			return InterpolateRunWideConfigurationEndpoints(context.Background(), conf, nil, NewContainerNetworkAccess())
		}},
	} {
		out, err := c.interpolate()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for _, info := range out.GetInfos() {
			for _, value := range info.GetConfigurationValues() {
				if value.GetKey() == "CONNECTION" {
					t.Errorf("%s: must omit a value whose template literal does not resolve", c.name)
				}
			}
		}
	}

	// A malformed reference is a hard error on the strict path, and a literal is
	// no exception: assembling it would ship the unresolved marker.
	malformed := templatedConnectionConfiguration("@${endpoint:nonsense}/app")
	if out, err := InterpolateConfigurationEndpoints(context.Background(), malformed, nil, NewContainerNetworkAccess()); err == nil {
		t.Fatalf("a malformed reference in a template literal must fail, got %v", out)
	}
}
