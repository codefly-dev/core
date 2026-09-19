// Package protoguard asserts that the Go bindings in generated/go are the ones
// proto/ currently describes.
//
// proto/ is the schema source of truth and generated/go is its output, but
// every other check reads one side alone: CI's breaking-change gate runs `buf
// breaking` over proto/, while `go build` and the rest of the suite read
// generated/go. Editing a .proto without regenerating therefore passes lint,
// passes the gate whenever the edit is additive, and passes the suite against
// the stale bindings — leaving the schema every downstream consumer resolves
// and the bindings every in-repo Go consumer imports describing different
// contracts, with nothing to say so.
//
// Regenerate-and-diff is the natural shape and is closed off: the bindings come
// from `codefly generate proto --local`, and the CLI owning that command is a
// private module CI here must not depend on. So the schema is compiled
// in-process with protocompile — the pure-Go compiler buf itself is built on —
// and compared against the descriptors the generated packages register.
//
// What that does and does not cover. The comparison is of descriptors: the
// rawDesc embedded in each .pb.go is the only generated artifact carrying the
// schema, so a _grpc.pb.go, .connect.go or .pb.gw.go left stale beside a
// current .pb.go — a plugin that failed midway through a generate, a merge
// resolved one-sided — is outside what this can see. Only running the
// generator closes that, which is the constraint above.
//
// The comparison also has a second input. Option bytes on our fields are
// parsed here against the protovalidate and googleapis descriptors that go.mod
// pins, while the committed bytes were serialized by buf against the BSR
// commits proto/buf.lock pins. internal/ciguard holds the two protovalidate
// pins on the same BSR commit, so that half cannot drift silently. googleapis
// reaches the two sides from different publishers of the same upstream and
// shares no identifier between them, so nothing can hold it that way; a file
// the two snapshot differently would land here as a diff confined to field
// options, which the failure message names alongside staleness.
package protoguard

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bufbuild/protocompile"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/descriptorpb"

	_ "github.com/codefly-dev/core/generated/go/codefly/actions/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/ci/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/cli/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/execution/v1"
	_ "github.com/codefly-dev/core/generated/go/codefly/mcp/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/observability/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/runnable/receipts/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/solution/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/update/v0"
	_ "github.com/codefly-dev/core/generated/go/mind/debug/v1"
	_ "github.com/codefly-dev/core/generated/go/mind/gateway/v1"
	_ "github.com/codefly-dev/core/generated/go/mind/v1"
)

const regenerate = "codefly generate proto --proto ./proto --output ./generated --local"

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dir, "go.mod"))
	return dir
}

// schemaPaths lists every .proto under proto/ as the import path it is
// addressed by, which is also the path its bindings register under.
func schemaPaths(t *testing.T, dir string) []string {
	t.Helper()
	var paths []string
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".proto" {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	}))
	require.NotEmpty(t, paths, "no .proto sources under %s", dir)
	return paths
}

// schemaResolver reads our own schema from proto/ and nothing else from
// anywhere else. Imports outside proto/ — the googleapis and protovalidate
// descriptors buf.lock names — come from the registry, because no copy of them
// is vendored here.
//
// The registry is also where the artifact under test lives, which is why this
// is not a protocompile.CompositeResolver: that falls through to the next
// resolver on ANY error from the one before, not just fs.ErrNotExist. Under it
// a source in proto/ that cannot be read — a mode change, EMFILE under the
// compiler's parallelism, a stalled mount — resolved from the bindings
// instead, and the comparison downstream came out green having compared the
// generated descriptor with itself. Here a path we are here to check never
// reaches the registry at all, so unreadable surfaces as the read error it is
// rather than as agreement.
func schemaResolver(dir string, own []string) protocompile.Resolver {
	ours := make(map[string]struct{}, len(own))
	for _, path := range own {
		ours[path] = struct{}{}
	}
	source := &protocompile.SourceResolver{ImportPaths: []string{dir}}
	return protocompile.ResolverFunc(func(path string) (protocompile.SearchResult, error) {
		result, err := source.FindFileByPath(path)
		if err == nil {
			return result, nil
		}
		if _, mine := ours[path]; mine {
			return protocompile.SearchResult{}, err
		}
		file, missing := protoregistry.GlobalFiles.FindFileByPath(path)
		if missing != nil {
			// The read failure is the root cause worth reporting; the registry
			// was only ever the second place to look.
			return protocompile.SearchResult{}, err
		}
		return protocompile.SearchResult{Desc: file}, nil
	})
}

// compileSchema builds descriptors straight from the .proto sources.
func compileSchema(t *testing.T, dir string, paths []string) []*descriptorpb.FileDescriptorProto {
	t.Helper()
	compiler := protocompile.Compiler{
		Resolver: schemaResolver(dir, paths),
		// protoc-gen-go strips comments and spans from the descriptor it
		// embeds, so carrying them here would diff every file.
		SourceInfoMode: protocompile.SourceInfoNone,
	}
	files, err := compiler.Compile(context.Background(), paths...)
	require.NoError(t, err, "proto/ does not compile")

	out := make([]*descriptorpb.FileDescriptorProto, len(files))
	for i, file := range files {
		out[i] = schema(file)
	}
	return out
}

// schema renders a descriptor down to what the schema actually says, dropping
// the per-language naming options buf's managed mode writes in as it generates
// (see generated/buf.gen.local.yaml). Those record how the bindings were
// produced rather than what they describe, and no .proto declares them.
func schema(file protoreflect.FileDescriptor) *descriptorpb.FileDescriptorProto {
	out := protodesc.ToFileDescriptorProto(file)
	options := out.GetOptions()
	if options == nil {
		return out
	}
	options.JavaPackage = nil
	options.JavaOuterClassname = nil
	options.JavaMultipleFiles = nil
	options.GoPackage = nil
	options.ObjcClassPrefix = nil
	options.CsharpNamespace = nil
	options.PhpNamespace = nil
	options.PhpMetadataNamespace = nil
	options.RubyPackage = nil
	// A file declaring no options of its own has to compare equal to one whose
	// options block managed mode created from nothing.
	if proto.Equal(options, &descriptorpb.FileOptions{}) {
		out.Options = nil
	}
	return out
}

func TestGeneratedBindingsMatchTheSchema(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "proto")
	paths := schemaPaths(t, dir)
	compiled := compileSchema(t, dir, paths)

	for i, path := range paths {
		generated, err := protoregistry.GlobalFiles.FindFileByPath(path)
		if err != nil {
			t.Errorf("%s has no bindings registered in this binary. Either generated/go is "+
				"missing them — regenerate with `%s` — or the package they live in is new "+
				"and needs its blank import added to this file, without which the guard "+
				"does not cover it.", path, regenerate)
			continue
		}
		if diff := cmp.Diff(compiled[i], schema(generated), protocmp.Transform()); diff != "" {
			t.Errorf("%s: the committed bindings no longer describe the schema.\n"+
				"Usually they are stale — regenerate with `%s` and commit the result "+
				"alongside the .proto change.\n"+
				"If this .proto was not touched, suspect the other input: field options "+
				"are parsed here against the googleapis descriptors go.mod takes from "+
				"genproto, which share no commit id with the BSR snapshot proto/buf.lock "+
				"names and that buf generated against, so a file those two publishers "+
				"snapshot differently shows up as a diff confined to options.\n"+
				"(-proto +generated/go)\n%s", path, regenerate, diff)
		}
	}
}

// schemaCandidates names the .proto a binding could have come from, nearest
// spelling first. protoc-gen-go and protoc-gen-go-grpc both write
// source_relative, one file and one _grpc file per .proto — but stripping
// _grpc unconditionally misreads a schema legitimately named that way:
// stream_grpc.proto generates stream_grpc.pb.go, which is its own file, not
// the stubs for a stream.proto that never existed. Nothing forbids the name
// either; proto/buf.yaml lints with BASIC and COMMENTS, neither of which
// constrains it. So the literal spelling is tried before the stripped one.
func schemaCandidates(rel string) []string {
	base := strings.TrimSuffix(rel, ".pb.go")
	candidates := []string{base + ".proto"}
	if stubs := strings.TrimSuffix(base, "_grpc"); stubs != base {
		candidates = append(candidates, stubs+".proto")
	}
	return candidates
}

// A .proto that is deleted or renamed leaves its bindings behind, and the
// comparison above only walks the sources that still exist — so the orphan
// keeps exporting types and registering a file path the schema no longer has.
//
// This also closes the one hole schemaResolver cannot: an import naming a
// deleted .proto still resolves, because the deleted file's bindings are still
// registered, so the importer compiles and compares clean. The two tests meet
// exactly — for a codefly path to resolve from the registry its .pb.go must
// exist, and a .pb.go with no .proto is what this reports — so weakening this
// test reopens that path silently rather than loudly.
func TestEveryBindingStillHasASchema(t *testing.T) {
	root := repoRoot(t)
	bindings := filepath.Join(root, "generated", "go")

	found := 0
	var orphans []string
	require.NoError(t, filepath.WalkDir(bindings, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".pb.go") {
			return err
		}
		rel, err := filepath.Rel(bindings, path)
		if err != nil {
			return err
		}
		found++
		candidates := schemaCandidates(filepath.ToSlash(rel))
		for _, source := range candidates {
			if _, err := os.Stat(filepath.Join(root, "proto", filepath.FromSlash(source))); err == nil {
				return nil
			}
		}
		orphans = append(orphans, filepath.ToSlash(rel)+" -> proto/"+strings.Join(candidates, " or proto/"))
		return nil
	}))
	require.NotZero(t, found, "no bindings under %s", bindings)
	require.Empty(t, orphans,
		"these bindings have no schema left. The .proto was deleted or renamed without "+
			"regenerating; rerun `%s` and delete what it no longer writes.", regenerate)
}

// An unreadable source must reach the caller as a compile error. Before the
// fallback learned to refuse our own paths it returned the registered
// descriptor instead, and TestGeneratedBindingsMatchTheSchema then compared
// the generated descriptor with itself and passed — drift present in the
// .proto and the guard green. chmod is the obvious way to provoke it and a
// useless one here, since CI runs as root and root reads a 0000 file anyway;
// an empty import path reproduces the same resolver state directly.
func TestSchemaResolverRefusesToServeOurSourcesFromTheBindings(t *testing.T) {
	const registered = "codefly/base/v0/scope.proto"
	_, registeredErr := protoregistry.GlobalFiles.FindFileByPath(registered)
	require.NoError(t, registeredErr, "fixture must be a path the bindings really do register")

	unreadable := schemaResolver(t.TempDir(), []string{registered})

	result, err := unreadable.FindFileByPath(registered)
	require.Error(t, err,
		"a schema source that cannot be read resolved anyway — from the bindings under "+
			"test, which makes the comparison downstream compare them with themselves")
	require.Nil(t, result.Desc, "the bindings were served in place of the unreadable source")
	require.ErrorIs(t, err, fs.ErrNotExist, "the read failure is what the caller needs to see")

	// The legitimate fallback still has to work, or every proto importing
	// protovalidate stops compiling.
	external, err := unreadable.FindFileByPath("buf/validate/validate.proto")
	require.NoError(t, err, "imports outside proto/ have no source to read and must still resolve")
	require.NotNil(t, external.Desc)
}

// The same property at the level that matters: a compile whose sources cannot
// be read fails, rather than quietly succeeding against the bindings.
func TestCompilingUnreadableSourcesFails(t *testing.T) {
	compiler := protocompile.Compiler{
		Resolver:       schemaResolver(t.TempDir(), []string{"codefly/base/v0/scope.proto"}),
		SourceInfoMode: protocompile.SourceInfoNone,
	}
	_, err := compiler.Compile(context.Background(), "codefly/base/v0/scope.proto")
	require.Error(t, err, "compiling sources that cannot be read must fail, not fall back to the bindings")
}

func TestSchemaCandidatesReadGrpcNamedSchemasAsThemselves(t *testing.T) {
	for _, tc := range []struct {
		binding string
		want    []string
	}{
		{"codefly/base/v0/scope.pb.go", []string{"codefly/base/v0/scope.proto"}},
		{"codefly/base/v0/scope_grpc.pb.go", []string{"codefly/base/v0/scope_grpc.proto", "codefly/base/v0/scope.proto"}},
		{"codefly/base/v0/stream_grpc_grpc.pb.go", []string{"codefly/base/v0/stream_grpc_grpc.proto", "codefly/base/v0/stream_grpc.proto"}},
	} {
		t.Run(tc.binding, func(t *testing.T) {
			require.Equal(t, tc.want, schemaCandidates(tc.binding))
		})
	}

	// The regression: a schema named *_grpc.proto generates a *_grpc.pb.go of
	// its own, and stripping _grpc unconditionally reported it as an orphan of
	// a .proto that never existed.
	require.Contains(t, schemaCandidates("codefly/base/v0/stream_grpc.pb.go"),
		"codefly/base/v0/stream_grpc.proto",
		"a schema legitimately named *_grpc.proto must be recognised as its own source")
}
