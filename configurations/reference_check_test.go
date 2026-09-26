package configurations_test

import (
	"errors"
	"testing"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func referenceCheckService(module, name string, groups []string, endpoints ...*resources.Endpoint) *resources.Service {
	service := &resources.Service{Name: name, WorkspaceConfigurationDependencies: groups, Endpoints: endpoints}
	service.WithModule(module)
	return service
}

// Every unresolvable reference in the groups the plan's consumers declare is
// reported at once — consumer, group, key, reference and producer — while a
// resolvable one, an excluded group and a group no consumer declares are not.
func TestCheckEndpointReferencesListsEveryUnresolvedReference(t *testing.T) {
	host := referenceCheckService("host", "api", nil,
		&resources.Endpoint{Name: "http", API: "http"}, &resources.Endpoint{Name: "rpc", API: "grpc"})
	chat := referenceCheckService("assistant", "chat", []string{"assistant", "excluded"})
	worker := referenceCheckService("assistant", "worker", []string{"assistant"})
	plan := map[string]*resources.Service{"host/api": host, "assistant/chat": chat, "assistant/worker": worker}
	lookup := func(unique string) (*resources.Service, bool) { service, ok := plan[unique]; return service, ok }

	provided := []*basev0.ConfigurationInformation{
		{Name: "assistant", ConfigurationValues: []*basev0.ConfigurationValue{
			{Key: "host-endpoint", Value: "${endpoint:host/api/http}"},
			{Key: "host-rpc", Value: "${endpoint:host/api/grpc|authority}"},
			{Key: "documents-endpoint", Value: "${endpoint:documents/store/grpc}"},
			{Key: "host-missing", Value: "${endpoint:host/api/admin}"},
		}},
		{Name: "excluded", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "k", Value: "${endpoint:absent/service/http}"}}},
		{Name: "undeclared", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "k", Value: "${endpoint:absent/service/http}"}}},
	}
	err := configurations.CheckEndpointReferences(provided, []*resources.Service{worker, chat}, map[string]bool{"excluded": true}, lookup)
	var unresolved *configurations.UnresolvedReferencesError
	require.True(t, errors.As(err, &unresolved), "got %v", err)
	type found struct{ consumer, key, producer string }
	var got []found
	for _, reference := range unresolved.References {
		got = append(got, found{reference.Consumer, reference.Key, reference.Producer})
		require.Equal(t, "assistant", reference.Group)
	}
	require.Equal(t, []found{
		{"assistant/chat", "documents-endpoint", "documents/store"},
		{"assistant/chat", "host-missing", "host/api"},
		{"assistant/worker", "documents-endpoint", "documents/store"},
		{"assistant/worker", "host-missing", "host/api"},
	}, got)
	require.Contains(t, err.Error(), "assistant/chat: assistant/documents-endpoint = ${endpoint:documents/store/grpc} (producer documents/store)")

	require.NoError(t, configurations.CheckEndpointReferences(provided[:1], []*resources.Service{host}, nil, lookup),
		"a service that does not declare the group is not checked against it")
}

// A reference that cannot name an endpoint is reported as malformed.
func TestCheckEndpointReferencesReportsAMalformedReference(t *testing.T) {
	consumer := referenceCheckService("assistant", "chat", []string{"assistant"})
	err := configurations.CheckEndpointReferences([]*basev0.ConfigurationInformation{
		{Name: "assistant", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "bad", Value: "${endpoint:host}"}}},
	}, []*resources.Service{consumer}, nil, func(string) (*resources.Service, bool) { return nil, false })
	require.Error(t, err)
	require.Contains(t, err.Error(), "assistant/bad")
	require.Contains(t, err.Error(), "malformed reference")
}
