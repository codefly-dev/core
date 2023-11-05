package configurations

type HttpMethod string

const (
	HttpMethodGet     HttpMethod = "GET"
	HttpMethodPut     HttpMethod = "PUT"
	HttpMethodPost    HttpMethod = "POST"
	HttpMethodDelete  HttpMethod = "DELETE"
	HttpMethodPatch   HttpMethod = "PATCH"
	HttpMethodOptions HttpMethod = "OPTIONS"
	HttpMethodHead    HttpMethod = "HEAD"
)

type RestRoute struct {
	Path    string
	Methods []HttpMethod
}

type ApplicationRestRoutes struct {
	ServiceRestRoutes []*ServiceRestRoutes
	Application       *Application
}

type ServiceRestRoutes struct {
	Routes  []*RestRoute
	Service *Service
}
