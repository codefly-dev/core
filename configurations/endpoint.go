package configurations

import (
	"fmt"
	"strings"
)

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

const NetworkPrefix = "CODEFLY-NETWORK_"

func SerializeAddresses(addresses []string) string {
	return strings.Join(addresses, " ")
}

func AsEnvironmentVariable(reference string, addresses []string) string {
	return fmt.Sprintf("%s%s=%s", NetworkPrefix, strings.ToUpper(reference), SerializeAddresses(addresses))
}

func ParseEnvironmentVariable(env string) (string, []string) {
	tokens := strings.Split(env, "=")
	reference := strings.ToLower(tokens[0])
	// Namespace break
	reference = strings.Replace(reference, "_", ".", 1)
	reference = strings.Replace(reference, "_", "::", 1)
	values := strings.Split(tokens[1], " ")
	return reference, values
}

//func LoadEnvironmentVariables() {
//	for _, env := range os.Environ() {
//		if p, ok := strings.CutPrefix(env, NetworkPrefix); ok {
//			tokens := strings.Split(p, "=")
//			key := strings.ToLower(tokens[0])
//			// Namespace break
//			key = strings.Replace(key, "_", ".", 1)
//			key = strings.Replace(key, "_", "::", 1)
//			value := tokens[1]
//			networks[key] = value
//		}
//	}
//}

//	func (r Endpoint) String() string {
//		if r.FailOver != nil {
//			return fmt.Sprintf("%s (failover: %s)", r.Name, r.FailOver.Name)
//		}
//		return r.Name
//	}
//
//	func (r Endpoint) Proto() *factoryv1.Endpoint {
//		return &factoryv1.Endpoint{
//			Name:        r.Name,
//			Description: r.Description,
//		}
//	}
//
//	func EndpointFromProto(proto *runtimev1.Endpoint) *Endpoint {
//		return &Endpoint{
//			Name:        proto.Name,
//			Description: proto.Description,
//		}
//	}

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
