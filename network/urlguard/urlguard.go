// Package urlguard provides host-owned URL normalization, origin admission,
// and SSRF-hardened transport construction for outbound requests that a
// codefly host makes on behalf of untrusted provider code.
//
// Provider code never dials the network directly. It hands the host a
// descriptor; the host normalizes and admits the origin here, resolves and
// pins a single peer IP, and dials only that IP. This closes the classic
// bypasses: URL userinfo, encoded path traversal, scheme-relative URLs,
// proxy environment influence, credentialed redirects, and DNS rebinding
// between validation and dial.
package urlguard

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

// NetworkClass classifies a concrete resolved address. It is independent of
// the provider proto so this package stays reusable; callers map it to their
// own vocabulary.
type NetworkClass string

const (
	ClassPublic    NetworkClass = "public"
	ClassLoopback  NetworkClass = "loopback"
	ClassLinkLocal NetworkClass = "link-local"
	ClassPrivate   NetworkClass = "private"
)

// Origin is a normalized, host-attested base origin. Host is a canonical
// lowercase ASCII hostname or IP literal with no trailing dot; Port is always
// explicit.
type Origin struct {
	Scheme string
	Host   string
	Port   uint32
}

// String renders the canonical scheme://host:port form. IPv6 hosts are
// bracketed.
func (o Origin) String() string {
	return o.Scheme + "://" + o.HostPort()
}

// HostPort renders host:port, bracketing IPv6 hosts, suitable for a URL
// authority.
func (o Origin) HostPort() string {
	host := o.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s:%d", host, o.Port)
}

// defaultPort returns the scheme default. Callers only reach it for the two
// admitted schemes.
func defaultPort(scheme string) uint32 {
	if scheme == "http" {
		return 80
	}
	return 443
}

// NormalizeOrigin parses and canonicalizes a base-origin string. It rejects
// anything that could smuggle a second interpretation past admission: URL
// userinfo, a path/query/fragment, opaque or scheme-relative forms, non-HTTP(S)
// schemes, and IDNA-ambiguous hosts.
func NormalizeOrigin(raw string) (Origin, error) {
	if raw == "" {
		return Origin{}, fmt.Errorf("origin is required")
	}
	if strings.HasPrefix(raw, "//") {
		return Origin{}, fmt.Errorf("origin must not be scheme-relative")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return Origin{}, fmt.Errorf("origin is not a valid URL: %w", err)
	}
	if parsed.Opaque != "" {
		return Origin{}, fmt.Errorf("origin must not be opaque")
	}
	if parsed.User != nil {
		return Origin{}, fmt.Errorf("origin must not carry userinfo")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
		return Origin{}, fmt.Errorf("base origin must not carry a path, query, or fragment")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return Origin{}, fmt.Errorf("origin scheme %q is not admitted", parsed.Scheme)
	}
	host, err := canonicalHost(parsed.Hostname())
	if err != nil {
		return Origin{}, err
	}
	port := defaultPort(scheme)
	if raw := parsed.Port(); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 16)
		if err != nil || n == 0 {
			return Origin{}, fmt.Errorf("origin port %q is invalid", raw)
		}
		port = uint32(n)
	}
	return Origin{Scheme: scheme, Host: host, Port: port}, nil
}

// canonicalHost lowercases, strips a single trailing dot, canonicalizes IDNA
// to ASCII, and canonicalizes IP literals. It rejects hosts that are not
// already in their canonical ASCII form so a provider cannot admit one origin
// and later dial a homograph.
func canonicalHost(host string) (string, error) {
	if host == "" {
		return "", fmt.Errorf("origin host is required")
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", fmt.Errorf("origin host is required")
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	lowered := strings.ToLower(host)
	ascii, err := idna.Lookup.ToASCII(lowered)
	if err != nil {
		return "", fmt.Errorf("origin host is not a valid domain: %w", err)
	}
	if ascii != lowered {
		return "", fmt.Errorf("origin host is not in canonical form")
	}
	if !validHostname(ascii) {
		return "", fmt.Errorf("origin host is not a valid domain")
	}
	return ascii, nil
}

func validHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 || strings.Contains(host, "..") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

// SafePath validates that a request path is already in canonical, fully
// unescaped-safe form. It rejects encoded traversal and any character that
// would let the path smuggle a query, fragment, or second path segment past
// admission. An empty path is treated as "/".
func SafePath(path string) (string, error) {
	if path == "" {
		return "/", nil
	}
	if path[0] != '/' {
		return "", fmt.Errorf("path must be absolute")
	}
	if strings.ContainsAny(path, "?#\\") {
		return "", fmt.Errorf("path must not contain a query, fragment, or backslash")
	}
	if strings.Contains(path, "..") {
		return "", fmt.Errorf("path must not contain traversal")
	}
	// %2e (.) and %2f (/) reintroduce traversal after the server decodes; any
	// percent-encoding of a path separator or dot is rejected outright.
	lowered := strings.ToLower(path)
	if strings.Contains(lowered, "%2e") || strings.Contains(lowered, "%2f") || strings.Contains(lowered, "%5c") {
		return "", fmt.Errorf("path must not contain encoded traversal")
	}
	if strings.Contains(path, "//") {
		return "", fmt.Errorf("path must not contain empty segments")
	}
	return path, nil
}

// Classify maps a concrete IP to its network class. Cloud metadata endpoints
// (169.254.169.254) fall in link-local and are blocked with the rest of that
// class unless a rule explicitly admits link-local.
func Classify(ip net.IP) NetworkClass {
	switch {
	case ip.IsLoopback():
		return ClassLoopback
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		return ClassLinkLocal
	case ip.IsUnspecified() || ip.IsPrivate() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() || isCGNAT(ip):
		return ClassPrivate
	default:
		return ClassPublic
	}
}

// isCGNAT reports whether ip is in the RFC 6598 shared-address space
// (100.64.0.0/10), which Go's IsPrivate does not cover.
func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 100 && v4[1]&0xc0 == 64
}

// admitClass reports whether class may be dialed. Public is always admitted;
// any private class requires explicit allowance.
func admitClass(class NetworkClass, allowed []NetworkClass) bool {
	return class == ClassPublic || slices.Contains(allowed, class)
}

// Resolution is the single pinned peer for one operation.
type Resolution struct {
	IP    net.IP
	Class NetworkClass
}

// Resolve looks up the origin host exactly once, classifies every returned
// address, and pins the first admitted one. If any returned address is a
// disallowed class the resolution fails closed — a host that returns one
// public and one private answer must not be dialable. An IP-literal host is
// classified without a lookup.
func Resolve(ctx context.Context, resolver *net.Resolver, origin Origin, allowed []NetworkClass) (Resolution, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if literal := net.ParseIP(origin.Host); literal != nil {
		class := Classify(literal)
		if !admitClass(class, allowed) {
			return Resolution{}, fmt.Errorf("origin address class %q is not admitted", class)
		}
		return Resolution{IP: literal, Class: class}, nil
	}
	addrs, err := resolver.LookupIPAddr(ctx, origin.Host)
	if err != nil {
		return Resolution{}, fmt.Errorf("resolve origin host: %w", err)
	}
	if len(addrs) == 0 {
		return Resolution{}, fmt.Errorf("origin host %q did not resolve", origin.Host)
	}
	for _, addr := range addrs {
		if class := Classify(addr.IP); !admitClass(class, allowed) {
			return Resolution{}, fmt.Errorf("origin host resolved to disallowed class %q", class)
		}
	}
	first := addrs[0]
	return Resolution{IP: first.IP, Class: Classify(first.IP)}, nil
}

// Client builds an SSRF-hardened HTTP client bound to one pinned resolution.
// The transport has no proxy, dials only the pinned IP regardless of the
// address the URL carries (defeating DNS rebinding), verifies TLS against the
// origin host, and the client refuses every redirect.
func Client(origin Origin, pinned Resolution, deadlines Deadlines) *http.Client {
	dialer := &net.Dialer{Timeout: deadlines.Connect}
	pinnedAddr := net.JoinHostPort(pinned.IP.String(), strconv.FormatUint(uint64(origin.Port), 10))
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, pinnedAddr)
		},
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{ServerName: origin.Host, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   deadlines.TLS,
		ResponseHeaderTimeout: deadlines.ResponseHeader,
		ExpectContinueTimeout: time.Second,
		MaxIdleConns:          1,
		IdleConnTimeout:       deadlines.Connect,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Deadlines bounds each phase of an outbound request.
type Deadlines struct {
	Connect        time.Duration
	TLS            time.Duration
	ResponseHeader time.Duration
}

// DefaultDeadlines are conservative bounds suitable for vendor management APIs.
func DefaultDeadlines() Deadlines {
	return Deadlines{Connect: 5 * time.Second, TLS: 5 * time.Second, ResponseHeader: 15 * time.Second}
}
