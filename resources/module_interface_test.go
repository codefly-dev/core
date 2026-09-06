package resources

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeInterfaceFixture(t *testing.T, moduleYAML string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ModuleConfigurationName), []byte(moduleYAML), 0o644))
	serviceDir := filepath.Join(dir, "services", "accounts")
	require.NoError(t, os.MkdirAll(serviceDir, 0o755))
	service := `kind: service
name: accounts
version: 0.0.0
agent:
    kind: runtime::service
    name: go-grpc
    version: 0.0.16
    publisher: codefly.ai
endpoints:
    - name: grpc
      visibility: module
      api: grpc
`
	require.NoError(t, os.WriteFile(filepath.Join(serviceDir, "service.codefly.yaml"), []byte(service), 0o644))
	return dir
}

func TestLoadModuleFromDirRejectsInvalidInterface(t *testing.T) {
	dir := writeInterfaceFixture(t, `kind: module
name: billing
services:
    - name: accounts
interface:
    endpoints:
        - service: ghost
          endpoint: grpc
          visibility: module
`)
	_, err := LoadModuleFromDir(context.Background(), dir)
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid interface")
}

func TestLoadModuleFromDirAcceptsValidInterface(t *testing.T) {
	dir := writeInterfaceFixture(t, `kind: module
name: billing
services:
    - name: accounts
interface:
    endpoints:
        - service: accounts
          endpoint: grpc
          visibility: module
`)
	mod, err := LoadModuleFromDir(context.Background(), dir)
	require.NoError(t, err)
	require.True(t, mod.HasInterface())
}
