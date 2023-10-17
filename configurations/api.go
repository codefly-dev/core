package configurations

type ApiFramework string

const (
	GrpcApiFramework    ApiFramework = "grpc"
	RestApiFramework    ApiFramework = "rest"
	GraphQLApiFramework ApiFramework = "graphql"
)

type EndpointEntry struct {
	//Endpoint *Endpoint
	//Api *Api
}
