package sdk

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// WithDependencies starts an agent per declared dependency. Only dependencies
// that constrain the run stage are agents to start: booting a build/codegen
// producer wastes a process the caller never asked for, and an external
// capability names no agent in the workspace at all.
func TestLoadStartsOnlyRunStageDependencies(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "service.codefly.yaml"), []byte(`kind: service
name: worker
version: 0.0.0
service-dependencies:
    - name: database
      kind: runtime
    - name: migration
      kind: completion
    - name: api
      kind: build
    - name: contract
      kind: schema
    - name: stripe
      module: vendor
      kind: external
    - name: legacy
`), 0o600))

	env, err := New().Load(dir)
	require.NoError(t, err)
	require.Equal(t, []string{"database", "migration", "legacy"}, env.agents)
}
