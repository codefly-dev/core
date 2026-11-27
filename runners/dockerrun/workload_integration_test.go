//go:build !skip_infra

package dockerrun

import (
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

const workloadReadyPath = "/tmp/codefly-test-workload-ready"

// gracefulWorkload holds a real container open for lifecycle tests. It installs
// the handler before announcing readiness, so a test can stop the container
// without spending Docker's kill grace on a workload that has no data to flush.
func gracefulWorkload(setup string) []string {
	script := "trap 'exit 0' TERM\n"
	if setup != "" {
		script += setup + " || exit 1\n"
	}
	script += "touch " + workloadReadyPath + " || exit 1\nwhile :; do sleep 1 & wait $!; done"
	return []string{"sh", "-c", script}
}

func waitForWorkload(t *testing.T, daemon client.APIClient, id string) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := daemon.ContainerStatPath(t.Context(), id, client.ContainerStatPathOptions{Path: workloadReadyPath})
		return err == nil
	}, 5*time.Second, 20*time.Millisecond, "container workload did not become ready")
}
