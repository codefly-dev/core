package resources_test

//
//func TestOpenAPICombineForward(t *testing.T) {
//	ctx := context.Background()
//
//	endpoint := &configurations.Endpoint{Service: "org", Module: "management", Name: "rest"}
//	endpoint.WithDefault()
//	rest, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/api/swagger/one/org.swagger.json")
//require.NoError(t, err)
//
//	gateway := &configurations.Endpoint{Service: "api", Module: "public", Name: "rest"}
//	gateway.WithDefault()
//
//	combinator, err := configurations.NewOpenAPICombinator(ctx, gateway, rest)
//require.NoError(t, err)
//
//	tmpDir := t.TempDir()
//	defer os.RemoveAll(tmpDir)
//	out := fmt.Sprintf("%s/openapi.json", tmpDir)
//	combinator.WithDestination(out)
//	combined, err := combinator.Combine(ctx)
//require.NoError(t, err)
//
//	content, _ := os.ReadFile(out)
//
//	// parse again
//	api, err := configurations.ParseOpenAPI(content)
//require.NoError(t, err)
//	require.NotNil(t, api)
//
//	// Parse back and do some check
//	result := configurations.EndpointRestAPI(combined)
//	require.NotNil(t, result)
//	require.NotNil(t, result.Openapi)
//	require.Equal(t, 2, len(result.Groups))
//
//	expected := []*configurations.RestRoute{
//		{
//			Path:   "/management/org/version",
//			Method: configurations.HTTPMethodGet,
//		},
//		{
//			Path:   "/management/org/organization",
//			Method: configurations.HTTPMethodPost,
//		},
//	}
//	var got []*configurations.RestRoute
//	for _, group := range result.Groups {
//		for _, r := range group.Routes {
//			got = append(got, configurations.RestRouteFromProto(r))
//		}
//	}
//require.NoError(t, Exhaust(expected, got))
//
//}
//
//func TestOpenAPICombineSample(t *testing.T) {
//	ctx := context.Background()
//	endpoint := &configurations.Endpoint{Service: "svc", Module: "app", Name: "rest"}
//	endpoint.WithDefault()
//	rest, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/api/swagger/sample/server.swagger.json")
//require.NoError(t, err)
//
//	otherEndpoint := &configurations.Endpoint{Service: "org", Module: "management", Name: "rest"}
//	otherEndpoint.WithDefault()
//	otherRest, err := configurations.NewRestAPIFromOpenAPI(ctx, otherEndpoint, "testdata/api/swagger/sample/org.swagger.json")
//require.NoError(t, err)
//
//	gateway := &configurations.Endpoint{Service: "api", Module: "public", Name: "rest"}
//	gateway.WithDefault()
//	combinator, err := configurations.NewOpenAPICombinator(ctx, gateway, rest, otherRest)
//require.NoError(t, err)
//
//	tmpDir := t.TempDir()
//	defer os.RemoveAll(tmpDir)
//	out := fmt.Sprintf("%s/openapi.json", tmpDir)
//	combinator.WithDestination(out)
//	combined, err := combinator.Combine(ctx)
//require.NoError(t, err)
//
//	// Parse back and do some check
//	result := configurations.EndpointRestAPI(combined)
//	require.NotNil(t, result)
//	require.NotNil(t, result.Openapi)
//	require.Equal(t, 3, len(result.Groups))
//
//}
//
//func TestOpenAPICombineWithFilter(t *testing.T) {
//	ctx := context.Background()
//	endpoint := &configurations.Endpoint{Service: "svc", Module: "app", Name: "rest"}
//	endpoint.WithDefault()
//	rest, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/api/swagger/sample/server.swagger.json")
//require.NoError(t, err)
//
//	otherEndpoint := &configurations.Endpoint{Service: "org", Module: "management", Name: "rest"}
//	otherEndpoint.WithDefault()
//	otherRest, err := configurations.NewRestAPIFromOpenAPI(ctx, otherEndpoint, "testdata/api/swagger/sample/org.swagger.json")
//require.NoError(t, err)
//
//	gateway := &configurations.Endpoint{Service: "api", Module: "public", Name: "rest"}
//	gateway.WithDefault()
//	combinator, err := configurations.NewOpenAPICombinator(ctx, gateway, rest, otherRest)
//require.NoError(t, err)
//	combinator.Only(otherEndpoint.ServiceUnique(), "/organization")
//
//	tmpDir := t.TempDir()
//	defer os.RemoveAll(tmpDir)
//	out := fmt.Sprintf("%s/openapi.json", tmpDir)
//	combinator.WithDestination(out)
//	combined, err := combinator.Combine(ctx)
//require.NoError(t, err)
//
//	// Parse back and do some check
//	result := configurations.EndpointRestAPI(combined)
//	require.NotNil(t, result)
//	require.NotNil(t, result.Openapi)
//	require.Equal(t, 2, len(result.Groups)) // /version + /organization (GET+POST)
//
//}
