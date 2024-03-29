package configurations

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/codefly-dev/core/configurations/standards"
)

func PortAndPortAddressFromAddress(address string) (uint16, string, error) {
	port, err := PortFromAddress(address)
	if err != nil {
		return 0, "", err
	}
	portAddress := fmt.Sprintf(":%d", port)
	return port, portAddress, nil
}

func PortFromAddress(address string) (uint16, error) {
	u, err := url.Parse(address)
	if err == nil {
		port := u.Port()
		if port != "" {
			v, err := strconv.Atoi(port)
			if err != nil {
				return 0, err
			}
			return uint16(v), nil
		}
	}
	tokens := strings.Split(address, ":")
	if len(tokens) == 2 {
		v, err := strconv.Atoi(tokens[1])
		if err != nil {
			return 0, err
		}
		return uint16(v), nil
	}
	return standards.Port(standards.TCP), fmt.Errorf("info instance address does not have a port")
}
