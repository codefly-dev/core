package wool

import (
	"fmt"
	"strings"
)

// HeaderKey sanitizes the header name to be used in metadata
// Append wool:
// Lower case
// Suppress X-Codefly
func HeaderKey(header string) string {
	header = strings.ToLower(header)
	if codeflyHeader, ok := strings.CutPrefix(header, "x-codefly-"); ok {
		return fmt.Sprintf("wool:%s", codeflyHeader)
	}
	return header
}
