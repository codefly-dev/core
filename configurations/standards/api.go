package standards

import (
	"fmt"
	"slices"
)

var (
	GRPC = "grpc"
	REST = "rest"
	TCP  = "tcp"
)

var supportedAPI []string

func init() {
	supportedAPI = []string{GRPC, REST, TCP}
}

func SupportedAPI(kind string) error {
	if slices.Contains(supportedAPI, kind) {
		return nil
	}
	return fmt.Errorf("unsupported api: %s", kind)
}
