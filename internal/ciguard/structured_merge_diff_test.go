package ciguard

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Core is a library, so every version in its module graph is a floor its
// consumers' builds are raised to. A consumer that also compiles client-go
// builds only while that graph stays on sigs.k8s.io/structured-merge-diff/v6:
// every stable k8s.io/apimachinery (v0.35 through v0.37.1) is built on v6, and
// a v7 schema reaching it fails inside apimachinery's managedfields, not here.
//
// v0.19.2 shipped exactly that. A routine kustomize bump (v0.21.1 -> v0.21.3,
// #779) pulled a kube-openapi built on v7; core itself still built and every
// check here passed, and module-runtime's tasks module could not build against
// v0.19.2 or v0.19.3 at all. So the gate reads the GRAPH, which is what a
// consumer inherits, rather than core's own build, which never saw it.
//
// Lift it in the pull request that lifts the matching dependabot holds, once a
// stable apimachinery is built on structured-merge-diff/v7.
func TestCoreModuleGraphStaysOnStructuredMergeDiffV6(t *testing.T) {
	command := exec.Command("go", "mod", "graph")
	command.Dir = repoRoot(t)
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	out, err := command.Output()
	require.NoError(t, err, "go mod graph")

	for _, edge := range strings.Split(string(out), "\n") {
		parts := strings.Fields(edge)
		if len(parts) != 2 {
			continue
		}
		require.Falsef(t, strings.HasPrefix(parts[1], "sigs.k8s.io/structured-merge-diff/v7@"),
			"%s requires %s: a consumer that also compiles client-go on a stable apimachinery (built on structured-merge-diff/v6) cannot build against this core — hold the module that moved, as .github/dependabot.yml holds kustomize",
			parts[0], parts[1])
	}
}
