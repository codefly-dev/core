package standards

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

var (
	GRPC = "grpc"
	REST = "rest"
	HTTP = "http"
	TCP  = "tcp"
)

var supportedAPI []string

func init() {
	supportedAPI = []string{GRPC, REST, TCP, HTTP}
}

func SupportedAPI(kind string) error {
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

func Port(api string) int {
	switch api {
	case GRPC:
		return 9090
	case REST:
		return 8080
	}
	return 80
}

func PortAddressForEndpoint(endpoint string) string {
	if strings.HasSuffix(endpoint, GRPC) {
		return ":" + strconv.Itoa(Port(GRPC))
	}
	if strings.HasSuffix(endpoint, REST) {
		return ":" + strconv.Itoa(Port(REST))
	}
	return ":80"
}
