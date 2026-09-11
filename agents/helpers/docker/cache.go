package docker

import (
	"crypto/sha256"
	"fmt"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/distribution/reference"
)

const CacheContractVersion = "registry-v1"

// CacheArguments translates caller-owned cache policy for both recipe executors
// and in-agent builds. BuildKit, rather than this transport tag, keys individual
// layers by their platform, base image, instruction and consumed file contents.
func CacheArguments(cache *builderv0.BuildCacheOptions, platforms []string) ([]string, error) {
	if cache == nil {
		return nil, nil
	}
	if cache.Backend != "registry" {
		return nil, fmt.Errorf("unsupported build cache backend %q: supported backend is registry", cache.Backend)
	}
	if strings.TrimSpace(cache.Scope) == "" {
		return nil, fmt.Errorf("build cache scope is required")
	}
	mode := cache.Mode
	if mode == "" {
		mode = "min"
	}
	if mode != "min" && mode != "max" {
		return nil, fmt.Errorf("unsupported build cache mode %q", mode)
	}
	if len(platforms) == 0 {
		return nil, fmt.Errorf("build cache requires explicit target platforms")
	}
	var args []string
	for _, group := range []struct {
		repositories []string
		export       bool
	}{{cache.Imports, false}, {cache.Exports, true}} {
		for _, repository := range group.repositories {
			named, err := reference.ParseNamed(repository)
			if err != nil || !reference.IsNameOnly(named) || strings.ContainsAny(repository, ",@ \t\r\n") {
				return nil, fmt.Errorf("build cache requires a fully qualified registry repository without tag or digest")
			}
			for _, platform := range platforms {
				if len(strings.Split(platform, "/")) < 2 || strings.ContainsAny(platform, ", \t\r\n") {
					return nil, fmt.Errorf("invalid cache target platform %q", platform)
				}
				key := sha256.Sum256([]byte(CacheContractVersion + "\x00" + cache.Scope + "\x00" + platform))
				value := fmt.Sprintf("type=registry,ref=%s:codefly-%x", repository, key)
				if group.export {
					args = append(args, "--cache-to", value+",mode="+mode)
				} else {
					args = append(args, "--cache-from", value)
				}
			}
		}
	}
	// Mutable base tags must be resolved even when an imported cache is warm.
	return append(args, "--pull"), nil
}
