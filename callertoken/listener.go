package callertoken

import "fmt"

// Listener describes a server's caller-token configuration for CheckListener:
// the environment variables an operator sets, and what an unauthenticated
// caller could do if the token were absent.
type Listener struct {
	// TokenVar names the variable holding the token, e.g. "SOS_AUTH_TOKEN".
	TokenVar string
	// AnonymousVar names the variable that opts into an unauthenticated
	// listener, e.g. "SOS_ALLOW_ANONYMOUS".
	AnonymousVar string
	// Exposure completes "every caller that can reach the listener ...": what
	// an unauthenticated caller could do, e.g. "can run SQL against the bound
	// database".
	Exposure string
}

// CheckListener refuses to resolve a configuration that would serve its
// capabilities to anyone who can reach the listen port. A listener inside a
// container binds every interface, because port publishing cannot forward to a
// loopback-bound process, so its address says nothing about who can reach it
// and the token is the only thing the server itself can enforce.
//
// token is the configured token and allowAnonymous is whether the operator
// opted into an unauthenticated listener; the caller reads both from its own
// environment, so this package fixes no variable names and no parsing of
// booleans.
//
// A token with the opt-out set is a contradiction, not a preference: "here is a
// credential" and "accept callers with no credential" cannot both be the
// intent, and silently picking one leaves the operator believing the other. It
// is also the shape a half-finished migration takes, a token added to a secret
// while the manifest still carries the opt-out.
func CheckListener(token string, allowAnonymous bool, l Listener) error {
	switch {
	case token != "" && allowAnonymous:
		return fmt.Errorf("%s and %s=true are mutually exclusive: "+
			"unset %[2]s to enforce the token, or unset %[1]s to accept anonymous callers",
			l.TokenVar, l.AnonymousVar)
	case token != "", allowAnonymous:
		return nil
	}
	return fmt.Errorf("%[1]s is required: without it every caller that can reach the listener %[2]s. "+
		"Set %[1]s to a shared secret and have clients send it as the %[3]q gRPC metadata header, "+
		"or set %[4]s=true if the listener is confined to a private boundary enforced elsewhere (cluster NetworkPolicy / service-mesh mTLS)",
		l.TokenVar, l.Exposure, MetadataKey, l.AnonymousVar)
}
