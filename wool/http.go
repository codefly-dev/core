package wool

import (
	"fmt"
	"net/http"
	"strings"
)

var HTTPMappings = map[string]ContextKey{
	"X-Codefly-User-Auth-Id":    UserAuthIDKey,
	"X-Codefly-User-Email":      UserEmailKey,
	"X-Codefly-User-Name":       UserNameKey,
	"X-Codefly-User-Given-Name": UserGivenNameKey,
}

// HeaderKey sanitizes the header name to be used in metadata
// Append wool:
// Lower case
// Suppress X-Codefly
func HeaderKey(header string) string {
	header = strings.ToLower(header)
	header = strings.ReplaceAll(header, "-", ".")
	if codeflyHeader, ok := strings.CutPrefix(header, "x.codefly."); ok {
		return fmt.Sprintf("codefly.%s", codeflyHeader)
	}
	return header
}

type HTTP struct {
	w *Wool
}

func Header(key ContextKey) string {
	for header, k := range HTTPMappings {
		if k == key {
			return header
		}
	}
	return ""
}

func (http *HTTP) Headers() http.Header {
	out := make(map[string][]string)
	for header, key := range HTTPMappings {
		value, ok := http.w.lookup(key)
		if !ok {
			continue
		}
		out[header] = []string{value}
	}
	return out
}
