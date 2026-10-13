package solutionhost_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

func endpointDelivery(t *testing.T) (*solutionhost.SolutionHostBinding, string, []byte) {
	t.Helper()
	document := valid(t)
	endpoint := document.Endpoints[0]
	key := resources.EndpointAsEnvironmentVariableKey(&resources.EndpointInformation{
		Module: endpoint.Module, Service: endpoint.Service, Name: endpoint.Name, API: endpoint.API,
	})
	raw, err := solutionhost.MarshalEndpointDelivery(document, map[string]string{key: "http://component.example.svc.cluster.local:8080"})
	require.NoError(t, err)
	return document, key, raw
}

func TestEndpointDeliveryJoinsTheWholeAppliedPresence(t *testing.T) {
	document, key, raw := endpointDelivery(t)
	projection, err := solutionhost.ParseEndpointDelivery(raw, document)
	require.NoError(t, err)
	require.NotEmpty(t, projection.Carriers[key])
	filename, err := solutionhost.EndpointDeliveryFilename(document)
	require.NoError(t, err)
	require.Regexp(t, `^[a-f0-9]{64}\.json$`, filename)
	for name, change := range map[string]func(*solutionhost.SolutionHostBinding){
		"generation":                      func(d *solutionhost.SolutionHostBinding) { d.Generation++ },
		"host":                            func(d *solutionhost.SolutionHostBinding) { d.Host.Coordinate = "another-host" },
		"component":                       func(d *solutionhost.SolutionHostBinding) { d.Host.Component = "another-component" },
		"binding":                         func(d *solutionhost.SolutionHostBinding) { d.Binding = "another-binding" },
		"same generation changed ceiling": func(d *solutionhost.SolutionHostBinding) { d.EnvelopeRevision++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := *document
			change(&changed)
			require.NoError(t, changed.Validate())
			_, err := solutionhost.ParseEndpointDelivery(raw, &changed)
			require.ErrorContains(t, err, "active applied presence")
		})
	}
	document.Generation++
	next, err := solutionhost.EndpointDeliveryFilename(document)
	require.NoError(t, err)
	require.Equal(t, filename, next, "a generation replaces the same projected file")
}

func TestEndpointDeliveryRefusesAmbiguousOrUndeclaredCarriers(t *testing.T) {
	document, key, raw := endpointDelivery(t)
	var projection solutionhost.EndpointDelivery
	require.NoError(t, json.Unmarshal(raw, &projection))
	for name, mutate := range map[string]func() []byte{
		"duplicate key": func() []byte {
			return []byte(strings.Replace(string(raw), `"schema":`, `"schema":"codefly/endpoint-delivery/v1","schema":`, 1))
		},
		"unknown":            func() []byte { return append([]byte(`{"extra":true,`), raw[1:]...) },
		"multiple documents": func() []byte { return append(append([]byte{}, raw...), []byte("\n---\n{}")...) },
		"null carriers": func() []byte {
			p := projection
			p.Carriers = nil
			b, err := json.Marshal(p)
			require.NoError(t, err)
			return b
		},
		"undeclared": func() []byte {
			p := projection
			p.Carriers = map[string]string{key + "_OTHER": "http://another.example:8080"}
			b, err := json.Marshal(p)
			require.NoError(t, err)
			return b
		},
		"oversized": func() []byte { return []byte(strings.Repeat(" ", solutionhost.EndpointDeliveryMaxBytes+1)) },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := solutionhost.ParseEndpointDelivery(mutate(), document)
			require.Error(t, err)
		})
	}
	raw, err := solutionhost.MarshalEndpointDelivery(document, nil)
	require.NoError(t, err)
	got, err := solutionhost.ParseEndpointDelivery(raw, document)
	require.NoError(t, err)
	require.Empty(t, got.Carriers, "an explicit empty subset grants no route")
}
