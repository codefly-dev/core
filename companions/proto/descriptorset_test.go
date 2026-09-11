//go:build proto_companion_required

package proto_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/companions/proto"
	"github.com/codefly-dev/core/companions/testutil"
	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

// restProtoPath is a contract shaped like the real ones: it imports a well-known
// type, google/api/annotations.proto and buf/validate/validate.proto, the three
// namespaces whose bindings someone else publishes.
const restProtoPath = "saas/rest/v1/rest.proto"

const restProto = `syntax = "proto3";

package saas.rest.v1;

import "buf/validate/validate.proto";
import "google/api/annotations.proto";
import "google/protobuf/timestamp.proto";

message GetRequest {
  string id = 1 [(buf.validate.field).string.min_len = 1];
}

message GetResponse {
  google.protobuf.Timestamp created_at = 1;
}

service RestService {
  rpc Get(GetRequest) returns (GetResponse) {
    option (google.api.http) = {get: "/v1/rest/{id}"};
  }
}
`

// plainDescriptorSet builds the contract with `buf build --as-file-descriptor-set`.
//
// That is the shape a codefly contract actually travels in, and it is the whole
// premise of the marking: a plain FileDescriptorSet deliberately drops buf's
// image extensions, so buf treats every file it carries — imports included — as
// a generation target. buildImage's `buf build -o` produces a real buf image,
// which already carries is_import on every non-target file and therefore cannot
// reproduce any of this.
func plainDescriptorSet(t *testing.T, ctx context.Context) []byte {
	t.Helper()
	dir := t.TempDir()
	protoPath := filepath.Join(dir, filepath.FromSlash(restProtoPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(protoPath), 0755))
	require.NoError(t, os.WriteFile(protoPath, []byte(restProto), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "buf.yaml"),
		[]byte("version: v2\nmodules:\n  - path: .\ndeps:\n  - buf.build/googleapis/googleapis\n  - buf.build/bufbuild/protovalidate\n"), 0600))

	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", dir+":/work", "-w", "/work", imageRef(t, ctx),
		"sh", "-c", "buf dep update && buf build --as-file-descriptor-set -o /work/image.binpb")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("buf build --as-file-descriptor-set: %v\n%s", err, output)
	}
	image, err := os.ReadFile(filepath.Join(dir, "image.binpb"))
	require.NoError(t, err)
	return image
}

// TestGoClientFromPlainDescriptorSetOwnsOnlyItsOwnProtos is the test that fails
// without the marking, in both of the ways the marking has to fix.
//
// Unmarked, buf generates every file the descriptor set carries: one Go package
// per well-known type lands in a single google/protobuf directory and the tree
// does not compile at all ("found packages descriptorpb and durationpb").
//
// Marked but without the go_package overrides, google/api is no longer generated
// yet managed mode still rewrites its go_package — `except` matches by buf module
// identity, which a plain FileDescriptorSet does not carry — so the module's
// bindings import <prefix>/google/api, a package nothing generated.
func TestGoClientFromPlainDescriptorSetOwnsOnlyItsOwnProtos(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	testutil.RequireProtoImage(t, ctx)

	dest := t.TempDir()
	require.NoError(t, proto.GenerateClient(ctx, proto.ClientRequest{
		Language:      languages.GO,
		Destination:   dest,
		Module:        "rest",
		DescriptorSet: plainDescriptorSet(t, ctx),
	}))

	for _, namespace := range []string{"google", "buf"} {
		_, err := os.Stat(filepath.Join(dest, namespace))
		require.True(t, os.IsNotExist(err),
			"the generated library must not vendor %s/: a second registration of those proto file names panics the consumer's descriptor pool", namespace)
	}

	bindings, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash("saas/rest/v1/rest.pb.go")))
	require.NoError(t, err)
	require.Contains(t, string(bindings), "google.golang.org/genproto/googleapis/api/annotations",
		"the module's bindings must reference the canonical upstream package, not a local copy")

	mod := generatedModule(t, dest)
	run(t, ctx, mod, "go", "mod", "tidy")
	run(t, ctx, mod, "go", "build", "./...")
}

// TestTypeScriptClientFromPlainDescriptorSetKeepsWhatItImportsRelatively is the
// TypeScript half, where the answer is not the Go one. protoc-gen-es names the
// well-known types by package — @bufbuild/protobuf/wkt — for every file it is
// not asked to generate, so dropping those is right. It has no package for
// googleapis or protovalidate: it emits `../../google/api/annotations_pb`, a
// path that resolves only if the library carries the file, so dropping those
// leaves one dangling import per reference.
//
// Only tsc catches it. Generation succeeds either way and the bindings it
// writes are well-formed TypeScript; they just import files nothing wrote.
func TestTypeScriptClientFromPlainDescriptorSetKeepsWhatItImportsRelatively(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	testutil.RequireProtoImage(t, ctx)

	dest := t.TempDir()
	require.NoError(t, proto.GenerateClient(ctx, proto.ClientRequest{
		Language:      languages.TYPESCRIPT,
		Destination:   dest,
		Module:        "rest",
		DescriptorSet: plainDescriptorSet(t, ctx),
	}))

	_, err := os.Stat(filepath.Join(dest, "google", "protobuf"))
	require.True(t, os.IsNotExist(err),
		"the well-known types come from @bufbuild/protobuf/wkt; a local copy is one Timestamp the consumer's is not")

	for _, vendored := range []string{"google/api/annotations_pb.ts", "buf/validate/validate_pb.ts"} {
		_, err = os.Stat(filepath.Join(dest, filepath.FromSlash(vendored)))
		require.NoError(t, err, "the module's bindings import %s relatively, so the library has to own it", vendored)
	}

	bindings, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash("saas/rest/v1/rest_pb.ts")))
	require.NoError(t, err)
	require.Contains(t, string(bindings), "@bufbuild/protobuf/wkt")

	tsc(t, ctx, dest)
}

// restModulePath must match the go_package prefix CreateBufConfiguration derives
// from the Module name, or the generated tree cannot be compiled as itself.
const restModulePath = "github.com/codefly-dev/cli/pkg/builder/clients/rest"

// generatedModule copies the generated tree into a module of its own. It does not
// resolve dependencies: the non-facade Go template emits go-grpc stubs too, so
// the dependency set is whatever the generated code imports and each caller lets
// `go mod tidy` work it out.
func generatedModule(t *testing.T, dest string) string {
	t.Helper()
	mod := t.TempDir()
	require.NoError(t, copyTree(dest, mod))
	require.NoError(t, os.WriteFile(filepath.Join(mod, "go.mod"),
		[]byte("module "+restModulePath+"\n\ngo 1.27.0\n"), 0600))
	return mod
}

// TestGoClientFromPlainDescriptorSetLinksWithGoogleapis is the consumer-side half:
// a library that vendors google/api compiles perfectly well on its own and only
// fails when it meets the canonical package, at init, inside someone else's
// binary. Nothing short of linking the two together catches that.
func TestGoClientFromPlainDescriptorSetLinksWithGoogleapis(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	testutil.RequireProtoImage(t, ctx)

	dest := t.TempDir()
	require.NoError(t, proto.GenerateClient(ctx, proto.ClientRequest{
		Language:      languages.GO,
		Destination:   dest,
		Module:        "rest",
		DescriptorSet: plainDescriptorSet(t, ctx),
	}))

	mod := generatedModule(t, dest)
	require.NoError(t, os.MkdirAll(filepath.Join(mod, "cmd", "link"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(mod, "cmd", "link", "main.go"), []byte(
		"package main\n\nimport (\n\t_ \"google.golang.org/genproto/googleapis/api/annotations\"\n\n\t_ \""+
			restModulePath+"/saas/rest/v1\"\n)\n\nfunc main() {}\n"), 0600))

	run(t, ctx, mod, "go", "mod", "tidy")

	// `go run`, not `go build`: the duplicate registration is a panic in
	// protoregistry's init, so the program has to actually start.
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/link")
	cmd.Dir = mod
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "generated client cannot be linked with googleapis:\n%s", output)
	require.NotContains(t, string(output), "already registered")
	require.False(t, strings.Contains(string(output), "panic:"), "unexpected panic:\n%s", output)
}
