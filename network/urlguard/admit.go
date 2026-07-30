package urlguard

import (
	"fmt"
	"slices"
	"strings"
)

// Ceiling is the manifest-declared origin ceiling. A concrete origin is
// admitted only if it stays inside every dimension of the ceiling. Host
// patterns are bounded: either an exact host or a single "*." wildcard that
// matches proper subdomains only.
type Ceiling struct {
	Schemes        []string
	HostPatterns   []string
	Ports          []uint32
	AllowedClasses []NetworkClass
}

// Admit normalizes raw and confirms it stays within the ceiling. It returns
// the normalized origin on success. Admission is purely structural: it does
// not resolve DNS. The caller pins a peer with Resolve and dials with Client.
func (c Ceiling) Admit(raw string) (Origin, error) {
	origin, err := NormalizeOrigin(raw)
	if err != nil {
		return Origin{}, err
	}
	return origin, c.AdmitOrigin(origin)
}

// AdmitOrigin confirms an already-normalized origin stays within the ceiling.
func (c Ceiling) AdmitOrigin(origin Origin) error {
	if !slices.Contains(c.Schemes, origin.Scheme) {
		return fmt.Errorf("origin scheme %q is outside the ceiling", origin.Scheme)
	}
	if !slices.Contains(c.Ports, origin.Port) {
		return fmt.Errorf("origin port %d is outside the ceiling", origin.Port)
	}
	if !hostMatchesAny(origin.Host, c.HostPatterns) {
		return fmt.Errorf("origin host %q is outside the ceiling", origin.Host)
	}
	return nil
}

// hostMatchesAny reports whether host is an exact match for a pattern or a
// proper subdomain of a "*." pattern. The apex of a wildcard pattern is not
// matched by the wildcard itself.
func hostMatchesAny(host string, patterns []string) bool {
	for _, pattern := range patterns {
		if host == pattern {
			return true
		}
		if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		}
	}
	return false
}
