package configurations

/*
A Endpoint provides exposition to a Unique
For example, write and read are different roles for a storage service
*/

//	func DefaultEndpoint() Endpoint {
//		return Endpoint{
//			Name:        "default",
//			Description: "default endpoint to access the service",
//		}
//	}
type Api struct {
	Protocol  string `yaml:"protocol"`
	Framework string `yaml:"framework,omitempty"`
}

type Endpoint struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	// FailOver indicates that this endpoint should fail over to another endpoint
	FailOver *Endpoint `yaml:"fail-over,omitempty"`
}

//
//func (r Endpoint) String() string {
//	if r.FailOver != nil {
//		return fmt.Sprintf("%s (failover: %s)", r.Name, r.FailOver.Name)
//	}
//	return r.Name
//}
//
//func (r Endpoint) Proto() *factoryv1.Endpoint {
//	return &factoryv1.Endpoint{
//		Name:        r.Name,
//		Description: r.Description,
//	}
//}
//
//func EndpointFromProto(proto *runtimev1.Endpoint) *Endpoint {
//	return &Endpoint{
//		Name:        proto.Name,
//		Description: proto.Description,
//	}
//}
//
//func AsEnvironmentVariable(reference string) string {
//	return strings.ToUpper(fmt.Sprintf("codefly-network_%s", reference))
//}
//
//func Extract(env *runtimev1.Environment, reference string) []string {
//	for _, e := range env.Variables {
//		if e.Name == AsEnvironmentVariable(reference) {
//			return strings.Split(e.Value, " ")
//		}
//	}
//	return nil
//}
//
//func ExtractPorts(env *runtimev1.Environment, reference string) []string {
//	var ports []string
//	for _, e := range env.Variables {
//		if e.Name == AsEnvironmentVariable(reference) {
//			values := strings.Split(e.Value, " ")
//			for _, v := range values {
//				ports = append(ports, strings.Split(v, "_")[1])
//			}
//		}
//	}
//	return ports
//}
