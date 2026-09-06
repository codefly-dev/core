//go:build proto_companion_required

package proto_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/codefly-dev/core/companions/proto"
	"github.com/codefly-dev/core/companions/testutil"
	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

func facadeDir(t *testing.T) string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "testdata", "facade")
}

func fixture(t *testing.T, rel string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(facadeDir(t), filepath.FromSlash(rel)))
	require.NoError(t, err)
	return content
}

func imageRef(t *testing.T, ctx context.Context) string {
	t.Helper()
	img, err := proto.CompanionImage(ctx)
	require.NoError(t, err)
	return img.Name + ":" + img.Tag
}

const auditProtoPath = "saas/accounts/v1/audit.proto"

func TestFacadePython(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	testutil.RequireProtoImage(t, ctx)

	dest := t.TempDir()
	err := proto.GenerateClient(ctx, proto.ClientRequest{
		Language:    languages.PYTHON,
		Destination: dest,
		Module:      "accounts",
		Services:    []string{"AuditService"},
		Facade:      true,
		Sources: []proto.Source{
			{Path: auditProtoPath, Content: fixture(t, auditProtoPath)},
		},
	})
	require.NoError(t, err, "proto companion image not built: %s", testutil.BuildCompanionsHint)

	body, err := os.ReadFile(filepath.Join(dest, "accounts.py"))
	require.NoError(t, err)
	facade := string(body)
	require.Contains(t, facade, "class AuditServiceClient")
	require.Contains(t, facade, "def query_audit_log(self, request)")
	require.Contains(t, facade, "def audit(self)")
	require.NotContains(t, facade, "IdentityService")

	// The generated facade imports the *_pb2 the companion also produced; prove
	// it loads with the vendored bindings on sys.path inside the companion.
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", dest+":/out", "-w", "/out", imageRef(t, ctx),
		"/venv/bin/python", "-c", "import accounts")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("import accounts failed: %v\n%s", err, output)
	}
}

func TestFacadeGo(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	testutil.RequireProtoImage(t, ctx)

	dest := t.TempDir()
	err := proto.GenerateClient(ctx, proto.ClientRequest{
		Language:    languages.GO,
		Destination: dest,
		Module:      "accounts",
		Facade:      true,
		Sources: []proto.Source{
			{Path: auditProtoPath, Content: fixture(t, auditProtoPath)},
		},
	})
	require.NoError(t, err, "proto companion image not built: %s", testutil.BuildCompanionsHint)

	facadePath := filepath.Join(dest, "saas/accounts/v1/accounts/accounts_facade.pb.go")
	body, err := os.ReadFile(facadePath)
	require.NoError(t, err)
	facade := string(body)
	require.Contains(t, facade, "func New(gw Gateway")
	require.Contains(t, facade, "func (c *Client) Audit()")
	require.Contains(t, facade, "QueryAuditLog(ctx context.Context, req *")

	goVet(t, ctx, dest, "github.com/codefly-dev/cli/pkg/builder/clients/accounts")
}

func TestFacadeTypeScript(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	testutil.RequireProtoImage(t, ctx)

	dest := t.TempDir()
	err := proto.GenerateClient(ctx, proto.ClientRequest{
		Language:    languages.TYPESCRIPT,
		Destination: dest,
		Module:      "accounts",
		Facade:      true,
		Sources: []proto.Source{
			{Path: auditProtoPath, Content: fixture(t, auditProtoPath)},
		},
	})
	require.NoError(t, err, "proto companion image not built: %s", testutil.BuildCompanionsHint)

	facadePath := filepath.Join(dest, "saas/accounts/v1/accounts_facade.ts")
	body, err := os.ReadFile(facadePath)
	require.NoError(t, err)
	require.Contains(t, string(body), "export const accounts")

	tsc(t, ctx, dest)
}

// TestFacadeDescriptorSetMatchesSources builds an image once and asserts the
// Python facade generated from it is byte-identical to the one generated from
// the same proto as sources.
func TestFacadeDescriptorSetMatchesSources(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	testutil.RequireProtoImage(t, ctx)

	fromSources := t.TempDir()
	require.NoError(t, proto.GenerateClient(ctx, proto.ClientRequest{
		Language:    languages.PYTHON,
		Destination: fromSources,
		Module:      "accounts",
		Facade:      true,
		Sources: []proto.Source{
			{Path: auditProtoPath, Content: fixture(t, auditProtoPath)},
		},
	}))

	image := buildImage(t, ctx)
	fromImage := t.TempDir()
	require.NoError(t, proto.GenerateClient(ctx, proto.ClientRequest{
		Language:      languages.PYTHON,
		Destination:   fromImage,
		Module:        "accounts",
		Facade:        true,
		DescriptorSet: image,
	}))

	sourcesFacade, err := os.ReadFile(filepath.Join(fromSources, "accounts.py"))
	require.NoError(t, err)
	imageFacade, err := os.ReadFile(filepath.Join(fromImage, "accounts.py"))
	require.NoError(t, err)
	require.Equal(t, string(sourcesFacade), string(imageFacade))
}

func TestFacadeCollisionFails(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	testutil.RequireProtoImage(t, ctx)

	err := proto.GenerateClient(ctx, proto.ClientRequest{
		Language:    languages.GO,
		Destination: t.TempDir(),
		Module:      "collide",
		Facade:      true,
		Sources: []proto.Source{
			{Path: "collide/v1/collide.proto", Content: fixture(t, "collision/collide.proto")},
		},
	})
	// The facade plugin rejects the collision, which fails buf generate. The
	// companion runner surfaces the non-zero exit but not the plugin's stderr
	// (the "service class collision: ... FooService ... Foo ... FooClient"
	// message shows in the companion logs); the exact wording is asserted by
	// the ported Python plugin tests, which exercise the same collision logic.
	require.Error(t, err)
}

// buildImage compiles the audit fixture into a serialized FileDescriptorSet
// (buf image) inside the companion, the shape a module package carries.
func buildImage(t *testing.T, ctx context.Context) []byte {
	t.Helper()
	dir := t.TempDir()
	protoPath := filepath.Join(dir, filepath.FromSlash(auditProtoPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(protoPath), 0755))
	require.NoError(t, os.WriteFile(protoPath, fixture(t, auditProtoPath), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "buf.yaml"), []byte("version: v2\nmodules:\n  - path: .\n"), 0600))

	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", dir+":/work", "-w", "/work", imageRef(t, ctx),
		"buf", "build", "-o", "/work/image.binpb")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("buf build: %v\n%s", err, output)
	}
	image, err := os.ReadFile(filepath.Join(dir, "image.binpb"))
	require.NoError(t, err)
	return image
}

func goVet(t *testing.T, ctx context.Context, dest, modulePath string) {
	t.Helper()
	mod := t.TempDir()
	require.NoError(t, copyTree(dest, mod))
	require.NoError(t, os.WriteFile(filepath.Join(mod, "go.mod"),
		[]byte("module "+modulePath+"\n\ngo 1.27.0\n"), 0600))
	run(t, ctx, mod, "go", "get", "connectrpc.com/connect@v1.20.0", "google.golang.org/protobuf@v1.36.11")
	run(t, ctx, mod, "go", "vet", "./...")
}

func tsc(t *testing.T, ctx context.Context, dest string) {
	t.Helper()
	run(t, ctx, dest, "docker", "run", "--rm", "-v", dest+":/out", "-w", "/out", imageRef(t, ctx),
		"sh", "-c", "npm init -y >/dev/null 2>&1 && npm install --no-audit --no-fund @connectrpc/connect@2 @bufbuild/protobuf@2 typescript >/dev/null 2>&1 && npx tsc --noEmit --strict --skipLibCheck --moduleResolution bundler --module esnext --target es2022 $(find . -name '*.ts')")
}

func run(t *testing.T, ctx context.Context, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, output)
	}
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		return os.WriteFile(target, body, info.Mode())
	})
}
