package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

type warningCapture struct {
	mu   sync.Mutex
	logs []*wool.Log
}

func (c *warningCapture) Process(log *wool.Log) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logs = append(c.logs, log)
}

func (c *warningCapture) count(substr string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, l := range c.logs {
		if strings.Contains(l.Message, substr) {
			n++
		}
	}
	return n
}

// Loading a service through its module must run postLoad exactly once: a
// deprecated visibility value is warned a single time, not once per internal
// load pass. Before the single-pass loader, the module path loaded the service
// module-less and re-ran postLoad, doubling every warning.
func TestModuleServiceLoadWarnsDeprecatedVisibilityOnce(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "module.codefly.yaml"),
		[]byte("kind: module\nname: vault\nservices:\n    - name: secrets\n"), 0o644))
	svcDir := filepath.Join(dir, "services", "secrets")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.codefly.yaml"),
		[]byte("kind: service\nname: secrets\nagent:\n  kind: runtime::service\n  name: go-grpc\n  version: 0.0.1\n  publisher: codefly.ai\nendpoints:\n  - name: http\n    api: http\n    visibility: module\n"), 0o644))

	capture := &warningCapture{}
	baseCtx := context.Background()
	ctx := wool.New(baseCtx, &wool.Resource{Kind: "test", Unique: "deprecation"}).WithLogger(capture).Inject(baseCtx)
	previous := wool.GlobalLogLevel()
	wool.SetGlobalLogLevel(wool.TRACE)
	t.Cleanup(func() { wool.SetGlobalLogLevel(previous) })

	mod, err := resources.LoadModuleFromDir(ctx, dir)
	require.NoError(t, err)
	svc, err := mod.LoadServiceFromName(ctx, "secrets")
	require.NoError(t, err)

	// The single load pass still resolves the producer module and preserves the
	// authored (deprecated) value.
	require.Len(t, svc.Endpoints, 1)
	require.Equal(t, "vault", svc.Endpoints[0].Module)
	require.Equal(t, resources.VisibilityModule, svc.Endpoints[0].Visibility)

	require.Equal(t, 1, capture.count("endpoint visibility 'module' is deprecated"),
		"the deprecated-visibility warning must be emitted exactly once per service load")
}
