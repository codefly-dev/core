package configurations_test

//
//func TestOpenAPICombineForward(t *testing.T) {
//	ctx := context.Background()
//
//	endpoint := &configurations.Endpoint{Service: "org", Application: "management", Name: "rest"}
//	endpoint.WithDefault()
//	rest, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/api/swagger/one/org.swagger.json")
//	assert.NoError(t, err)
//
//	gateway := &configurations.Endpoint{Service: "api", Application: "public", Name: "rest"}
//	gateway.WithDefault()
//
//	combinator, err := configurations.NewOpenAPICombinator(ctx, gateway, rest)
//	assert.NoError(t, err)
//
//	tmpDir := t.TempDir()
//	defer os.RemoveAll(tmpDir)
//	out := fmt.Sprintf("%s/openapi.json", tmpDir)
//	combinator.WithDestination(out)
//	combined, err := combinator.Combine(ctx)
//	assert.NoError(t, err)
//
//	content, _ := os.ReadFile(out)
//
//	// parse again
//	api, err := configurations.ParseOpenAPI(content)
//	assert.NoError(t, err)
//	assert.NotNil(t, api)
//
//	// Parse back and do some check
//	result := configurations.EndpointRestAPI(combined)
//	assert.NotNil(t, result)
//	assert.NotNil(t, result.Openapi)
//	assert.Equal(t, 2, len(result.Groups))
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
//	assert.NoError(t, Exhaust(expected, got))
//
//}
//
//func TestOpenAPICombineSample(t *testing.T) {
//	ctx := context.Background()
//	endpoint := &configurations.Endpoint{Service: "svc", Application: "app", Name: "rest"}
//	endpoint.WithDefault()
//	rest, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/api/swagger/sample/server.swagger.json")
//	assert.NoError(t, err)
//
//	otherEndpoint := &configurations.Endpoint{Service: "org", Application: "management", Name: "rest"}
//	otherEndpoint.WithDefault()
//	otherRest, err := configurations.NewRestAPIFromOpenAPI(ctx, otherEndpoint, "testdata/api/swagger/sample/org.swagger.json")
//	assert.NoError(t, err)
//
//	gateway := &configurations.Endpoint{Service: "api", Application: "public", Name: "rest"}
//	gateway.WithDefault()
//	combinator, err := configurations.NewOpenAPICombinator(ctx, gateway, rest, otherRest)
//	assert.NoError(t, err)
//
//	tmpDir := t.TempDir()
//	defer os.RemoveAll(tmpDir)
//	out := fmt.Sprintf("%s/openapi.json", tmpDir)
//	combinator.WithDestination(out)
//	combined, err := combinator.Combine(ctx)
//	assert.NoError(t, err)
//
//	// Parse back and do some check
//	result := configurations.EndpointRestAPI(combined)
//	assert.NotNil(t, result)
//	assert.NotNil(t, result.Openapi)
//	assert.Equal(t, 3, len(result.Groups))
//
//}
//
//func TestOpenAPICombineWithFilter(t *testing.T) {
//	ctx := context.Background()
//	endpoint := &configurations.Endpoint{Service: "svc", Application: "app", Name: "rest"}
//	endpoint.WithDefault()
//	rest, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/api/swagger/sample/server.swagger.json")
//	assert.NoError(t, err)
//
//	otherEndpoint := &configurations.Endpoint{Service: "org", Application: "management", Name: "rest"}
//	otherEndpoint.WithDefault()
//	otherRest, err := configurations.NewRestAPIFromOpenAPI(ctx, otherEndpoint, "testdata/api/swagger/sample/org.swagger.json")
//	assert.NoError(t, err)
//
//	gateway := &configurations.Endpoint{Service: "api", Application: "public", Name: "rest"}
//	gateway.WithDefault()
//	combinator, err := configurations.NewOpenAPICombinator(ctx, gateway, rest, otherRest)
//	assert.NoError(t, err)
//	combinator.Only(otherEndpoint.ServiceUnique(), "/organization")
//
//	tmpDir := t.TempDir()
//	defer os.RemoveAll(tmpDir)
//	out := fmt.Sprintf("%s/openapi.json", tmpDir)
//	combinator.WithDestination(out)
//	combined, err := combinator.Combine(ctx)
//	assert.NoError(t, err)
//
//	// Parse back and do some check
//	result := configurations.EndpointRestAPI(combined)
//	assert.NotNil(t, result)
//	assert.NotNil(t, result.Openapi)
//	assert.Equal(t, 2, len(result.Groups)) // /version + /organization (GET+POST)
//
//}
