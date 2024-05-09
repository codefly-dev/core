package resources

import (
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

func MakeDnsSummary(dns *basev0.DNS) string {
	return fmt.Sprintf("%s::%s::%s", dns.Module, dns.Service, dns.Endpoint)
}

func MakeManyDnsSummary(dns []*basev0.DNS) string {
	out := make([]string, len(dns))
	for i, d := range dns {
		out[i] = MakeDnsSummary(d)
	}
	return strings.Join(out, ",")
}
