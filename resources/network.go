package resources

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/codefly-dev/core/standards"
)

func ParseAddress(address string) (*NetworkInstance, error) {
	u, err := url.Parse(address)
	if err == nil {
		port := u.Port()
		if port != "" {
			v, err := strconv.Atoi(port)
			if err != nil {
				return &NetworkInstance{
					Address:  address,
					Hostname: u.Hostname(),
					Host:     u.Host,
					Port:     standards.Port(standards.Unknown),
				}, err
			}
			return &NetworkInstance{
				Address:  address,
				Hostname: u.Hostname(),
				Host:     u.Host,
				Port:     uint16(v),
			}, nil
		}
	}
	tokens := strings.Split(address, ":")
	if len(tokens) == 2 {
		v, err := strconv.Atoi(tokens[1])
		if err != nil {
			return &NetworkInstance{
				Address:  address,
				Hostname: tokens[0],
				Host:     fmt.Sprintf("%s:%d", tokens[0], standards.Port(standards.Unknown)),
				Port:     standards.Port(standards.Unknown),
			}, err
		}
		return &NetworkInstance{
			Address:  address,
			Hostname: tokens[0],
			Host:     fmt.Sprintf("%s:%d", tokens[0], v),
			Port:     uint16(v),
		}, nil
	}
	return nil, fmt.Errorf("address can't be parsed")
}

func PortFromAddress(address string) (uint16, error) {
	instance, err := ParseAddress(address)
	if err != nil {
		return standards.Port(standards.Unknown), fmt.Errorf("info instance address does not have a port")
	}
	return instance.Port, nil
}
