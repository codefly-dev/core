package configurations

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/codefly-dev/core/configurations/standards"
)

func PortAndPortAddressFromAddress(address string) (int, string, error) {
	port, err := PortFromAddress(address)
	if err != nil {
		return 0, "", err
	}
	portAddress := fmt.Sprintf(":%d", port)
	return port, portAddress, nil
}

func PortFromAddress(address string) (int, error) {
	u, err := url.Parse(address)
	if err == nil {
		port := u.Port()
		if port != "" {
			return strconv.Atoi(port)
		}
	}
	tokens := strings.Split(address, ":")
	if len(tokens) == 2 {
		return strconv.Atoi(tokens[1])
	}
	return standards.Port(standards.TCP), fmt.Errorf("info instance address does not have a port")
}
