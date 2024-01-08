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

func StandardPort(api string) int {
	switch api {
	case GRPC:
		return 9090
	case REST:
		return 8080
	}
	return 80
}

func PortAddress(endpoint string) string {
	if strings.HasSuffix(endpoint, GRPC) {
		return ":" + strconv.Itoa(StandardPort(GRPC))
	}
	if strings.HasSuffix(endpoint, REST) {
		return ":" + strconv.Itoa(StandardPort(REST))
	}
	return ":80"
}
