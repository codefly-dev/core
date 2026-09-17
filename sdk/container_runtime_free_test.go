package sdk_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The SDK is what a consumer's test binary links purely to boot its
// dependencies; it never drives a container runtime itself. Any package that
// does belongs behind the CLI, because the import graph is the whole contract
// here: reaching a container client from sdk puts it in the go.mod of every
// consumer, along with each of its advisories — including ones whose newest
// published release carries no fix, so the consumer cannot bump out of a red
// vulnerability gate and has to pin an older core instead.
var containerRuntimeModules = []string{
	"github.com/docker/docker",
	"github.com/containerd",
	"github.com/opencontainers",
}

// agents/helpers/docker, agents/services and agents/testing drive containers by
// design and are linked only by an agent implementation. The root agents package
// is named separately from the sdk graph that already contains it: it is the
// embeddable server every agent builds on, and it is where the edge was
// introduced, so a regression should be reported against it by name.
func TestSDKDoesNotLinkAContainerRuntimeClient(t *testing.T) {
	for _, pkg := range []string{"github.com/codefly-dev/core/sdk/...", "github.com/codefly-dev/core/agents"} {
		t.Run(pkg, func(t *testing.T) {
			// Deps of the packages themselves, not of their tests: a consumer
			// builds the former and never the latter.
			output, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
			require.NoError(t, err, "%s", output)
			for _, dep := range strings.Fields(string(output)) {
				for _, module := range containerRuntimeModules {
					require.False(t, strings.HasPrefix(dep, module+"/") || dep == module,
						"%s reaches %s; route the container runtime through runners/dockerrun, which no sdk consumer links", pkg, dep)
				}
			}
		})
	}
}
