package standards

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const ProtoPath = "proto/api.proto"
const OpenAPIPath = "openapi/api.swagger.json"

var (
	Unknown = "unknown"
	GRPC    = "grpc"
	REST    = "rest"
	HTTP    = "http"
	TCP     = "tcp"
)

var supportedAPI []string

func init() {
	supportedAPI = []string{GRPC, REST, TCP, HTTP}
}

func APIS() []string {
	return supportedAPI
}

func IsSupportedAPI(kind string) error {
	if slices.Contains(supportedAPI, kind) {
		return nil
	}
	return fmt.Errorf("unsupported api: %s", kind)
}

func PortAddress(api string) string {
	return fmt.Sprintf(":%d", Port(api))
}

func LocalhostAddress(api string) string {
	return fmt.Sprintf("localhost:%d", Port(api))
}

func Port(api string) uint16 {
	switch api {
	case GRPC:
		return 9090
	case REST:
		return 8080
	case HTTP:
		return 8080
	case TCP:
		return 80
	}
	return 80
}

func PortAddressForEndpoint(endpoint string) string {
	if strings.HasSuffix(endpoint, GRPC) {
		return ":" + strconv.Itoa(int(Port(GRPC)))
	}
	if strings.HasSuffix(endpoint, REST) {
		return ":" + strconv.Itoa(int(Port(REST)))
	}
	return ":80"
}
