package broker

import (
	"encoding/json"
	"net/url"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/stretchr/testify/require"
)

func TestRequestWireFieldCaseIsPreservedAndBoundExactly(t *testing.T) {
	value := &providerv0.PublicValue{Kind: &providerv0.PublicValue_StringValue{StringValue: "owned-resource"}}
	descriptor := manifest.RequestDescriptor{
		AllowedQueryFields:  []string{"clientID"},
		AllowedBodyFields:   []string{"tokenName"},
		OwnershipBodyFields: []string{"tokenName"},
	}
	planned := &providerv0.PlannedRequest{
		Query: map[string]*providerv0.PublicValue{"clientID": value},
		Body:  map[string]*providerv0.PublicValue{"tokenName": value},
	}
	query, err := bindQuery(descriptor, planned)
	require.NoError(t, err)
	decodedQuery, err := url.ParseQuery(query)
	require.NoError(t, err)
	require.Equal(t, url.Values{"clientID": {"owned-resource"}}, decodedQuery)
	body, _, err := bindBody(descriptor, planned, "owned-resource")
	require.NoError(t, err)
	var decodedBody map[string]string
	require.NoError(t, json.Unmarshal(body, &decodedBody))
	require.Equal(t, map[string]string{"tokenName": "owned-resource"}, decodedBody)

	_, _, err = bindBody(descriptor, planned, "different-resource")
	require.ErrorContains(t, err, "not the planned remote id")
	planned.Query = map[string]*providerv0.PublicValue{"clientid": value}
	_, err = bindQuery(descriptor, planned)
	require.ErrorContains(t, err, "not allowed")
	planned.Body = map[string]*providerv0.PublicValue{"tokenname": value}
	_, _, err = bindBody(descriptor, planned, "owned-resource")
	require.ErrorContains(t, err, "ownership body field")
	planned.Body["tokenName"] = value
	_, _, err = bindBody(descriptor, planned, "owned-resource")
	require.ErrorContains(t, err, "not allowed")
}
