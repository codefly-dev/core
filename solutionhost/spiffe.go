package solutionhost

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// WorkloadAccount is the account a workload SPIFFE ID names: what an attested
// credential authenticates (workloadidentity.Attestation's Namespace and
// Account), under the trust domain that issued it.
type WorkloadAccount struct {
	TrustDomain string
	Namespace   string
	Account     string
}

// dns1123Label is a Kubernetes namespace or ServiceAccount name.
var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// ParseWorkloadSPIFFEID is the one reader of a workload SPIFFE ID:
// spiffe://<trust domain>/ns/<namespace>/sa/<account>, the trust domain
// lowercase, the namespace and the account each a DNS-1123 label. It parses the
// whole URI rather than scanning for an "ns" segment, so a trust domain named
// "ns" is never read as a namespace, and refuses a port, user info, a query, a
// fragment, an escape and any other path: a string that names no workload is
// not read leniently. The native and the cluster mechanism spell an identity
// the same way.
func ParseWorkloadSPIFFEID(id string) (WorkloadAccount, error) {
	refuse := func(reason string) (WorkloadAccount, error) {
		return WorkloadAccount{}, fmt.Errorf("SPIFFE ID %q %s", id, reason)
	}
	if id == "" {
		return refuse("is empty; a workload names the identity it runs as")
	}
	if strings.ContainsAny(id, "?#%\\") {
		return refuse("carries a query, a fragment, an escape or a backslash")
	}
	parsed, err := url.Parse(id)
	switch {
	case err != nil:
		return refuse("does not parse")
	case parsed.Scheme != "spiffe":
		return refuse("does not use the spiffe scheme")
	case parsed.User != nil:
		return refuse("carries user info")
	case parsed.Port() != "" || strings.HasSuffix(parsed.Host, ":"):
		return refuse("carries a port")
	case parsed.Host == "" || parsed.Host != strings.ToLower(parsed.Host):
		return refuse("does not name a lowercase trust domain")
	}
	segments := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if !strings.HasPrefix(parsed.Path, "/") || len(segments) != 4 || segments[0] != "ns" || segments[2] != "sa" {
		return refuse("is not spiffe://<trust domain>/ns/<namespace>/sa/<account>")
	}
	if !dns1123Label.MatchString(segments[1]) || !dns1123Label.MatchString(segments[3]) {
		return refuse("names a namespace or account that is not a DNS-1123 label")
	}
	return WorkloadAccount{TrustDomain: parsed.Host, Namespace: segments[1], Account: segments[3]}, nil
}
