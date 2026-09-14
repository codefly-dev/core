package dependencies

import (
	"os/exec"
	"strings"
	"testing"
)

// The CLI session client must stay usable without linking the agent manager or
// Docker implementation. Check the complete compiled dependency graph, so an
// indirect import cannot silently reintroduce that coupling.
func TestCLISessionImportBoundary(t *testing.T) {
	command := exec.Command("go", "list", "-deps", ".")
	command.Dir = packageDir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect dependency graph: %v: %s", err, output)
	}
	for _, name := range strings.Fields(string(output)) {
		for _, forbidden := range []string{"github.com/codefly-dev/core/agents", "github.com/codefly-dev/core/runners/dockerrun", "github.com/docker/docker", "github.com/moby/moby"} {
			if name == forbidden || strings.HasPrefix(name, forbidden+"/") {
				t.Errorf("CLI session client imports agent implementation: %s", name)
			}
		}
	}
}
