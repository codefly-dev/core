package wool

import (
	"fmt"
	"net/http"
	"strings"
)

// HTTPMappings maps HTTP header names to context keys.
var HTTPMappings = map[string]ContextKey{
	"X-Codefly-User-Auth-Id":    UserAuthIDKey,
	"X-Codefly-User-Email":      UserEmailKey,
	"X-Codefly-User-Name":       UserNameKey,
	"X-Codefly-User-Given-Name": UserGivenNameKey,
}

// HeaderKey sanitizes a header name for use as a metadata key.
func HeaderKey(header string) string {
	header = strings.ToLower(header)
	header = strings.ReplaceAll(header, "-", ".")
	if codeflyHeader, ok := strings.CutPrefix(header, "x.codefly."); ok {
		return fmt.Sprintf("codefly.%s", codeflyHeader)
	}
	return header
}

// HTTP provides HTTP header propagation for user identity.
type HTTP struct {
	w *Wool
}

// Header returns the HTTP header name for a context key.
func Header(key ContextKey) string {
	for header, k := range HTTPMappings {
		if k == key {
			return header
		}
	}
	return ""
}

// Headers returns HTTP headers populated from the Wool context.
func (h *HTTP) Headers() http.Header {
	out := make(map[string][]string)
	for header, key := range HTTPMappings {
		value, ok := h.w.lookup(key)
		if !ok {
			continue
		}
		out[header] = []string{value}
	}
	return out
}

// Headers returns all known HTTP header names.
func Headers() []string {
	out := make([]string, 0, len(HTTPMappings))
	for header := range HTTPMappings {
		out = append(out, header)
	}
	return out
}
