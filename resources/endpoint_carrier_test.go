package resources_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// The carrier format — CODEFLY__ENDPOINT__<MODULE>__<SERVICE>__<NAME>__<API> —
// is core's contract, and a lookup compares the key it builds against the
// entries it is handed, exactly. So a carrier whose IDENTITY is wrong — the
// API of another declaration, a near-miss spelling — sits under no key a
// lookup builds: it read as absent, and a consumer entitled to fall back on
// absence fell back on it with no error (sdk-go#57 r3). The consumer judges
// what it was handed before it reads any of it, and the judgement is core's:
// every carrier is the canonical key of a declared endpoint, or it is refused
// by name. No typo is enumerated: the key is parsed and compared to the
// canonical spelling.

func declaredCarrierEndpoints() []*resources.Endpoint {
	return []*resources.Endpoint{
		{Module: "producer", Service: "records", Name: "rest", API: "rest", Visibility: "public"},
		{Module: "producer", Service: "records", Name: "grpc", API: "grpc", Visibility: "public"},
		{Module: "platform-example", Service: "back-end", Name: "rest", API: "rest", Visibility: "public"},
	}
}

// Acceptance is BECAUSE of the declarations, not a pass-through: the same
// carriers are refused the moment the declaration they match is withdrawn,
// naming exactly that carrier and nothing else — so an environment of valid
// carriers is accepted by a judgement, not by a validator that judges nothing.
func TestEndpointCarriersOfDeclaredEndpointsAreAccepted(t *testing.T) {
	carriers := []string{
		"PATH=/usr/bin",
		"CODEFLY__ENVIRONMENT=local",
		"CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST=localhost:8080",
		"CODEFLY__ENDPOINT__PRODUCER__RECORDS__GRPC__GRPC=localhost:9090",
		"CODEFLY__ENDPOINT__PLATFORM_EXAMPLE__BACK_END__REST__REST=http://back-end.platform-example.svc.cluster.local:8080",
		// The advertised-address carrier is another contract, read by its own
		// key; it is not an endpoint carrier and is not judged as one.
		"CODEFLY__SELF_ENDPOINT__PRODUCER__RECORDS__REST__REST=http://records.producer.svc.cluster.local:8080",
	}
	require.NoError(t, resources.ValidateEndpointCarriers(carriers, declaredCarrierEndpoints()))

	// Withdraw each declaration in turn: exactly its carrier is refused, the
	// rest still accepted — every acceptance above rests on one declaration.
	for withdrawn, endpoint := range declaredCarrierEndpoints() {
		remaining := make([]*resources.Endpoint, 0, 2)
		for i, declared := range declaredCarrierEndpoints() {
			if i != withdrawn {
				remaining = append(remaining, declared)
			}
		}
		err := resources.ValidateEndpointCarriers(carriers, remaining)
		require.ErrorIs(t, err, resources.ErrInvalidEndpointCarrier, "withdrawn %s", endpoint.Name)
		key := resources.EndpointAsEnvironmentVariableKey(&resources.EndpointInformation{Module: endpoint.Module, Service: endpoint.Service, Name: endpoint.Name, API: endpoint.API})
		require.Contains(t, err.Error(), key+" names no declared endpoint", "withdrawn %s", endpoint.Name)
		require.Equal(t, 1, strings.Count(err.Error(), "names no declared endpoint"), "only the withdrawn declaration's carrier is refused")
	}

	// The self carrier is accepted for what it is, not because it was
	// declared: spelled as an endpoint carrier it would be refused.
	err := resources.ValidateEndpointCarriers([]string{
		"CODEFLY__ENDPOINT__PRODUCER__RECORDS__ADMIN__REST=http://records.producer.svc.cluster.local:8080",
	}, declaredCarrierEndpoints())
	require.ErrorIs(t, err, resources.ErrInvalidEndpointCarrier)

	// An environment carrying no endpoint carrier has nothing to refuse.
	require.NoError(t, resources.ValidateEndpointCarriers([]string{"PATH=/usr/bin"}, declaredCarrierEndpoints()))
	require.NoError(t, resources.ValidateEndpointCarriers(nil, nil))
}

// API `grpc` injected for a declaration whose API is `rest`: module, service
// and name are a declared endpoint's, the API is not, so the key is nobody's
// canonical carrier. Refused, naming the declaration it almost names.
func TestAnEndpointCarrierWithAnotherAPIThanItsDeclarationIsRefused(t *testing.T) {
	err := resources.ValidateEndpointCarriers([]string{
		"CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__GRPC=localhost:8080",
	}, declaredCarrierEndpoints())
	require.ErrorIs(t, err, resources.ErrInvalidEndpointCarrier)
	require.Contains(t, err.Error(), "CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__GRPC ")
	require.Contains(t, err.Error(), "producer/records/rest with API GRPC")
	require.Contains(t, err.Error(), "its declaration serves rest")
	require.Contains(t, err.Error(), "CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST,")
}

// A near miss — the canonical key with a trailing underscore — is not the
// canonical key. It is refused for what it is, not matched to what it almost
// is.
func TestAnEndpointCarrierWithANearMissKeyIsRefused(t *testing.T) {
	err := resources.ValidateEndpointCarriers([]string{
		"CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST_=localhost:8080",
	}, declaredCarrierEndpoints())
	require.ErrorIs(t, err, resources.ErrInvalidEndpointCarrier)
	require.Contains(t, err.Error(), "CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST_ ")
	require.Contains(t, err.Error(), "producer/records/rest with API REST_")

	// A key the canonical one is merely a prefix of, and a key short of a
	// segment, are no closer.
	for _, entry := range []string{
		"CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST__EXTRA=localhost:8080",
		"CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST=localhost:8080",
	} {
		err = resources.ValidateEndpointCarriers([]string{entry}, declaredCarrierEndpoints())
		require.ErrorIs(t, err, resources.ErrInvalidEndpointCarrier, entry)
	}
}

// A carrier naming an endpoint nothing declared is refused, and the refusal
// says what IS declared so the consumer can see what it was handed instead.
func TestAnEndpointCarrierOfAnUndeclaredEndpointIsRefused(t *testing.T) {
	err := resources.ValidateEndpointCarriers([]string{
		"CODEFLY__ENDPOINT__PRODUCER__RECORDS__ADMIN__REST=localhost:8080",
	}, declaredCarrierEndpoints())
	require.ErrorIs(t, err, resources.ErrInvalidEndpointCarrier)
	require.Contains(t, err.Error(), "CODEFLY__ENDPOINT__PRODUCER__RECORDS__ADMIN__REST ")
	require.Contains(t, err.Error(), "names no declared endpoint")
	require.Contains(t, err.Error(), "CODEFLY__ENDPOINT__PLATFORM_EXAMPLE__BACK_END__REST__REST, CODEFLY__ENDPOINT__PRODUCER__RECORDS__GRPC__GRPC, CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST")

	// With nothing declared, every endpoint carrier is undeclared.
	err = resources.ValidateEndpointCarriers([]string{"CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST=localhost:8080"}, nil)
	require.ErrorIs(t, err, resources.ErrInvalidEndpointCarrier)
	require.Contains(t, err.Error(), "no endpoint is declared")
}

// One canonical key delivered twice is unjudgeable: a lookup takes the first
// by position. An entry with no value is not an entry. Every refusal is
// reported, in key order, so one fault does not hide another.
func TestEveryInvalidEndpointCarrierIsRefusedByName(t *testing.T) {
	err := resources.ValidateEndpointCarriers([]string{
		"CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST=localhost:8080",
		"CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST=localhost:8081",
		"CODEFLY__ENDPOINT__PRODUCER__RECORDS__GRPC__GRPC",
		"CODEFLY__ENDPOINT__PRODUCER__RECORDS__ADMIN__REST=localhost:1",
	}, declaredCarrierEndpoints())
	require.ErrorIs(t, err, resources.ErrInvalidEndpointCarrier)
	message := err.Error()
	require.Contains(t, message, "CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST is delivered more than once")
	require.Contains(t, message, "CODEFLY__ENDPOINT__PRODUCER__RECORDS__GRPC__GRPC carries no value")
	require.Contains(t, message, "CODEFLY__ENDPOINT__PRODUCER__RECORDS__ADMIN__REST names no declared endpoint")
	require.Less(t, indexOf(message, "ADMIN__REST names"), indexOf(message, "GRPC__GRPC carries"))
	require.Less(t, indexOf(message, "GRPC__GRPC carries"), indexOf(message, "REST__REST is delivered"))
}

// A declaration that cannot spell a carrier — a missing module, service, name
// or API — is a defect of the input, refused rather than matched against
// nothing; so are two declarations whose carriers spell the same.
func TestEndpointCarrierValidationRefusesADeclarationItCannotSpell(t *testing.T) {
	err := resources.ValidateEndpointCarriers(nil, []*resources.Endpoint{
		{Module: "producer", Service: "records", Name: "rest"},
	})
	require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration)
	require.Contains(t, err.Error(), "producer/records/rest")

	err = resources.ValidateEndpointCarriers(nil, []*resources.Endpoint{
		{Module: "producer", Service: "back-end", Name: "rest", API: "rest"},
		{Module: "producer", Service: "back_end", Name: "rest", API: "rest"},
	})
	require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration)
	require.Contains(t, err.Error(), "CODEFLY__ENDPOINT__PRODUCER__BACK_END__REST__REST")
}

// The lookup tells absence from a carrier that is present and does not hold
// an address: the first is typed, so a consumer entitled to fall back on it
// does so without matching error text, and the second is never mistaken for
// it. The self lookup makes the same distinction.
func TestTheEndpointLookupTellsAbsenceFromAParseFailure(t *testing.T) {
	ctx := context.Background()
	info := &resources.EndpointInformation{Module: "producer", Service: "records", Name: "rest", API: "rest"}

	instance, err := resources.FindNetworkInstanceInEnvironmentVariables(ctx, info, []string{"PATH=/usr/bin"})
	require.ErrorIs(t, err, resources.ErrEndpointCarrierAbsent)
	require.Contains(t, err.Error(), "CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST")
	require.Nil(t, instance)

	for _, value := range []string{"not-an-address", "localhost:port", ""} {
		instance, err = resources.FindNetworkInstanceInEnvironmentVariables(ctx, info,
			[]string{"CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST=" + value})
		require.Error(t, err, value)
		require.False(t, errors.Is(err, resources.ErrEndpointCarrierAbsent), "a carrier that is present and does not parse is not absence: %q", value)
		require.Contains(t, err.Error(), "CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST")
		require.Nil(t, instance, "no instance travels with a refusal: %q", value)
	}

	instance, err = resources.FindNetworkInstanceInEnvironmentVariables(ctx, info,
		[]string{"CODEFLY__ENDPOINT__PRODUCER__RECORDS__REST__REST=localhost:8080"})
	require.NoError(t, err)
	require.Equal(t, uint16(8080), instance.Port)

	self, err := resources.FindSelfNetworkInstanceInEnvironmentVariables(ctx, info, []string{"PATH=/usr/bin"})
	require.ErrorIs(t, err, resources.ErrEndpointCarrierAbsent)
	require.Contains(t, err.Error(), "CODEFLY__SELF_ENDPOINT__PRODUCER__RECORDS__REST__REST")
	require.Nil(t, self)
	self, err = resources.FindSelfNetworkInstanceInEnvironmentVariables(ctx, info,
		[]string{"CODEFLY__SELF_ENDPOINT__PRODUCER__RECORDS__REST__REST=not-an-address"})
	require.Error(t, err)
	require.False(t, errors.Is(err, resources.ErrEndpointCarrierAbsent))
	require.Nil(t, self)
}

func indexOf(message, needle string) int {
	for i := 0; i+len(needle) <= len(message); i++ {
		if message[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
