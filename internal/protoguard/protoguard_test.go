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
	_ "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/solution/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	_ "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
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

// compileSchema builds descriptors straight from the .proto sources. Imports
// resolve out of proto/ first, so the schema is always read from source; only
// the BSR dependencies buf.lock names — googleapis and protovalidate — fall
// through to the descriptors their Go modules register.
func compileSchema(t *testing.T, dir string, paths []string) []*descriptorpb.FileDescriptorProto {
	t.Helper()
	compiler := protocompile.Compiler{
		Resolver: protocompile.CompositeResolver{
			&protocompile.SourceResolver{ImportPaths: []string{dir}},
			protocompile.ResolverFunc(func(path string) (protocompile.SearchResult, error) {
				file, err := protoregistry.GlobalFiles.FindFileByPath(path)
				if err != nil {
					return protocompile.SearchResult{}, err
				}
				return protocompile.SearchResult{Desc: file}, nil
			}),
		},
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
			t.Errorf("generated/go is stale for %s: the bindings no longer describe the "+
				"schema. Regenerate with `%s` and commit the result alongside the .proto "+
				"change.\n(-proto +generated/go)\n%s", path, regenerate, diff)
		}
	}
}

// A .proto that is deleted or renamed leaves its bindings behind, and the
// comparison above only walks the sources that still exist — so the orphan
// keeps exporting types and registering a file path the schema no longer has.
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
		// protoc-gen-go and protoc-gen-go-grpc both write source_relative: one
		// file and one _grpc file per .proto.
		source := strings.TrimSuffix(strings.TrimSuffix(rel, ".pb.go"), "_grpc") + ".proto"
		if _, err := os.Stat(filepath.Join(root, "proto", source)); err != nil {
			orphans = append(orphans, filepath.ToSlash(rel)+" -> proto/"+filepath.ToSlash(source))
		}
		return nil
	}))
	require.NotZero(t, found, "no bindings under %s", bindings)
	require.Empty(t, orphans,
		"these bindings have no schema left. The .proto was deleted or renamed without "+
			"regenerating; rerun `%s` and delete what it no longer writes.", regenerate)
}
