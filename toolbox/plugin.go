// Package toolbox contains the small process-level conventions shared by
// standalone Codefly toolbox plugins.
package toolbox

import (
	"os"
	"strings"
)

const VersionEnvironment = "CODEFLY_TOOLBOX_VERSION"

// Version returns the version injected by the host, or the development value
// used when a toolbox binary is launched directly.
func Version() string {
	return Environment(VersionEnvironment, "0.0.0-dev")
}

// Environment returns an environment value or a fallback when it is unset.
func Environment(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// EnvironmentList parses a comma-separated environment variable, trimming
// whitespace and omitting empty entries.
func EnvironmentList(key string) []string {
	parts := strings.Split(os.Getenv(key), ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}
