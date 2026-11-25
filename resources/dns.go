package resources

import (
	"fmt"
	"strings"

	basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"
)

func MakeDNSSummary(dns *basev1.DNS) string {
	return fmt.Sprintf("%s::%s::%s", dns.Module, dns.Service, dns.Endpoint)
}

func MakeManyDNSSummary(dns []*basev1.DNS) string {
	out := make([]string, len(dns))
	for i, d := range dns {
		out[i] = MakeDNSSummary(d)
	}
	return strings.Join(out, ",")
}
