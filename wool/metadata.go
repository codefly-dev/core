package wool

import (
	"fmt"
	"strings"
)

// HeaderKey sanitizes the header name to be used in metadata
// Append wool:
// Lower case
// Suppress X-
func HeaderKey(header string) string {
	header = strings.ToLower(header)
	header = strings.TrimPrefix(header, "x-")
	return fmt.Sprintf("wool:%s", header)
}
