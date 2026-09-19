package proto

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A template's `out` is what decides where the formatting pass runs. These pin
// the reading of it: relative to the template, only where Go was generated,
// once per directory however many plugins share it.
func TestGoOutputRootsReadsTheTemplateLikeBufDoes(t *testing.T) {
	root := t.TempDir()
	protoDir := filepath.Join(root, "proto")
	goOut := filepath.Join(root, "code", "pkg", "gen")
	tsOut := filepath.Join(root, "frontend", "src", "gen")
	empty := filepath.Join(root, "generated", "openapi-raw")
	for _, dir := range []string{protoDir, filepath.Join(goOut, "saas", "v1"), tsOut, empty} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(goOut, "saas", "v1", "x.pb.go"), []byte("package v1\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tsOut, "x_pb.ts"), []byte("export {}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(protoDir, "buf.gen.yaml"), []byte(`version: v2
plugins:
  - local: protoc-gen-go
    out: ../code/pkg/gen
  - local: protoc-gen-go-grpc
    out: ../code/pkg/gen
  - local: protoc-gen-connect-go
    out: ../code/pkg/gen
  - local: protoc-gen-openapiv2
    out: ../generated/openapi-raw
  - local: protoc-gen-es
    out: ../frontend/src/gen
  - local: protoc-gen-python
    out: ../python/gen
`), 0o600))

	roots, err := GoOutputRoots(protoDir, "buf.gen.yaml")
	require.NoError(t, err)
	// Three Go plugins share one out: one root. The OpenAPI out exists but
	// holds no Go; the TypeScript out holds no Go; the Python out does not
	// exist. None is a root.
	require.Equal(t, []string{goOut}, roots)
}

func TestGoOutputRootsWithNoGoIsNothingToFormat(t *testing.T) {
	root := t.TempDir()
	protoDir := filepath.Join(root, "proto")
	require.NoError(t, os.MkdirAll(protoDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(protoDir, "buf.gen.yaml"), []byte(`version: v2
plugins:
  - local: protoc-gen-es
    out: ../ts
`), 0o600))
	roots, err := GoOutputRoots(protoDir, "buf.gen.yaml")
	require.NoError(t, err)
	require.Empty(t, roots)
}

func TestGoOutputRootsRequiresATemplate(t *testing.T) {
	_, err := GoOutputRoots(t.TempDir(), "buf.gen.yaml")
	require.Error(t, err)
}
