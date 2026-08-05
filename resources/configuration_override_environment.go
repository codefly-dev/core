package resources

import "strings"

// WithoutInvocationConfigurationOverrides removes the private SDK-to-CLI
// carriers from an inherited environment after Codefly has loaded them into
// typed configurations.
//
// ARCHITECTURE: the CLI is the sole carrier consumer. Agent and service
// processes receive only the configuration values selected for their typed
// runtime contract; they must never inherit the aggregate carrier containing
// credentials for other services.
func WithoutInvocationConfigurationOverrides(environment []string) []string {
	blocked := map[string]struct{}{
		WorkspaceConfigurationOverridesEnvironment: {},
		ServiceConfigurationOverridesEnvironment:   {},
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found {
			if _, private := blocked[name]; private {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
