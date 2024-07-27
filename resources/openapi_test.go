package resources_test

import "testing"

func TestOpenAPICombine(t *testing.T) {
	//ctx := context.Background()
	//
	//endpoint := &resources.Endpoint{Service: "org", Module: "management", Name: "rest"}
	//rest, err := resources.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/api/swagger/one/org.swagger.json")
	//require.NoError(t, err)
	//
	//gateway := &resources.Endpoint{Service: "api", Module: "public", Name: "rest"}
	//gateway.WithDefault()
	//
	//combinator, err := resources.NewOpenAPICombinator(ctx, gateway, rest)
	//require.NoError(t, err)
	//
	//tmpDir := t.TempDir()
	//defer os.RemoveAll(tmpDir)
	//out := fmt.Sprintf("%s/openapi.json", tmpDir)
	//combinator.WithDestination(out)
	//combined, err := combinator.Combine(ctx)
	//require.NoError(t, err)
	//
	//content, _ := os.ReadFile(out)
	//
	//// parse again
	//api, err := resources.ParseOpenAPI(content)
	//require.NoError(t, err)
	//require.NotNil(t, api)
	//
	//// Parse back and do some check
	//result := resources.EndpointRestAPI(combined)
	//require.NotNil(t, result)
	//require.NotNil(t, result.Openapi)
	//require.Equal(t, 2, len(result.Groups))
	//
	//expected := []*resources.RestRoute{
	//	{
	//		Path:   "/management/org/version",
	//		Method: resources.HTTPMethodGet,
	//	},
	//	{
	//		Path:   "/management/org/organization",
	//		Method: resources.HTTPMethodPost,
	//	},
	//}
	//var got []*resources.RestRoute
	//for _, group := range result.Groups {
	//	for _, r := range group.Routes {
	//		got = append(got, resources.RestRouteFromProto(r))
	//	}
	//}
	//require.NoError(t, Exhaust(expected, got))

}
