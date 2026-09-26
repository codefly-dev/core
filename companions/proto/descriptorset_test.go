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
	return plainDescriptorSetFor(t, ctx, restProto)
}

func plainDescriptorSetFor(t *testing.T, ctx context.Context, source string) []byte {
	t.Helper()
	dir := t.TempDir()
	protoPath := filepath.Join(dir, filepath.FromSlash(restProtoPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(protoPath), 0755))
	require.NoError(t, os.WriteFile(protoPath, []byte(source), 0600))
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

func TestRustClientRetainsImportedMessageFields(t *testing.T) {
	ctx := t.Context()
	testutil.RequireProtoImage(t, ctx)
	source := strings.Replace(restProto, `import "google/protobuf/timestamp.proto";`, `import "google/protobuf/timestamp.proto";
import "google/rpc/status.proto";`, 1)
	source = strings.Replace(source, "google.protobuf.Timestamp created_at = 1;", `google.protobuf.Timestamp created_at = 1;
  google.rpc.Status status = 2;
  buf.validate.Violations violations = 3;`, 1)
	for _, descriptors := range []bool{false, true} {
		name := "sources"
		if descriptors {
			name = "descriptor-set"
		}
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dest := filepath.Join(parent, "output")
			require.NoError(t, os.Mkdir(dest, 0o750))
			require.NoError(t, os.Chmod(parent, 0o550))
			t.Cleanup(func() { require.NoError(t, os.Chmod(parent, 0o750)) })
			request := proto.ClientRequest{Language: languages.RUST, Destination: dest, Module: "rest"}
			if descriptors {
				request.DescriptorSet = plainDescriptorSetFor(t, ctx, source)
			} else {
				request.Sources = []proto.Source{{Path: restProtoPath, Content: []byte(source)}}
			}
			require.NoError(t, proto.GenerateClient(ctx, request))
			bindings, err := os.ReadFile(filepath.Join(dest, "saas/rest/v1/saas.rest.v1.rs"))
			require.NoError(t, err)
			require.Contains(t, string(bindings), "pub status:")
			require.Contains(t, string(bindings), "pub violations:")
			require.Contains(t, string(bindings), "::prost_types::Timestamp")
			require.NoDirExists(t, filepath.Join(dest, "google/protobuf"))
			unrelated := filepath.Join(dest, "src", "user.rs")
			require.NoError(t, os.MkdirAll(filepath.Dir(unrelated), 0o750))
			require.NoError(t, os.WriteFile(unrelated, []byte("user source"), 0o600))
			broken := proto.ClientRequest{Language: languages.RUST, Destination: dest,
				Sources: []proto.Source{{Path: "api.proto", Content: []byte("invalid proto")}}}
			require.Error(t, proto.GenerateClient(ctx, broken))
			unchanged, err := os.ReadFile(filepath.Join(dest, "saas/rest/v1/saas.rest.v1.rs"))
			require.NoError(t, err)
			require.Equal(t, bindings, unchanged)
			require.NoError(t, proto.GenerateClient(ctx, request))
			unchanged, err = os.ReadFile(unrelated)
			require.NoError(t, err)
			require.Equal(t, "user source", string(unchanged))
			require.NoError(t, os.WriteFile(filepath.Join(dest, "Cargo.toml"), []byte(`[package]
name = "generated-imports-test"
version = "0.0.0"
edition = "2021"
[dependencies]
prost = "=0.14.1"
prost-types = "=0.14.1"
tonic = "=0.14.2"
tonic-prost = "=0.14.2"
[lib]
path = "lib.rs"
`), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dest, "lib.rs"), []byte(`pub mod google { pub mod rpc { include!("google/rpc/google.rpc.rs"); } }
pub mod buf { pub mod validate { include!("buf/validate/buf.validate.rs"); } }
pub mod saas { pub mod rest { pub mod v1 { include!("saas/rest/v1/saas.rest.v1.rs"); } } }
#[test]
fn imported_messages_round_trip() {
    use prost::Message;
    let value = saas::rest::v1::GetResponse {
        created_at: Some(prost_types::Timestamp { seconds: 42, nanos: 7 }),
        status: Some(google::rpc::Status { code: 9, message: "retained".into(), details: vec![] }),
        violations: Some(buf::validate::Violations { violations: vec![] }),
    };
    let decoded = saas::rest::v1::GetResponse::decode(value.encode_to_vec().as_slice()).unwrap();
    assert_eq!(decoded, value);
    assert_eq!(decoded.status.unwrap().message, "retained");
}
`), 0o600))
			command := exec.CommandContext(ctx, "cargo", "test", "--quiet")
			command.Dir = dest
			out, err := command.CombinedOutput()
			require.NoError(t, err, "%s", out)
		})
	}
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
// googleapis or protovalidate: it emits `../../google/api/annotations_pb.js`, a
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
	// A namespace an earlier contract imported and this one does not. The clean
	// step runs before buf, so what the run still owes is rewritten and what it
	// does not is reclaimed; skipping the clean for the namespaces TypeScript
	// keeps would leave this shipping in the library forever.
	stale := filepath.Join(dest, "google", "rpc", "status_pb.ts")
	require.NoError(t, os.MkdirAll(filepath.Dir(stale), 0750))
	require.NoError(t, os.WriteFile(stale, []byte("export {};\n"), 0600))

	require.NoError(t, proto.GenerateClient(ctx, proto.ClientRequest{
		Language:      languages.TYPESCRIPT,
		Destination:   dest,
		Module:        "rest",
		DescriptorSet: plainDescriptorSet(t, ctx),
	}))

	_, err := os.Stat(stale)
	require.True(t, os.IsNotExist(err), "a foreign namespace the contract no longer imports must be reclaimed")

	_, err = os.Stat(filepath.Join(dest, "google", "protobuf"))
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
