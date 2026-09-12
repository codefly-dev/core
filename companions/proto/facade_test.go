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
	return img.FullName()
}

const auditProtoPath = "saas/accounts/v1/audit.proto"

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

// TestFacadeGoStreamingOnly guards the streaming-skip path: a service whose
// only RPC is streaming emits a wrapper with no methods, so the facade must not
// import context/connect.NewRequest (which only methods use) — otherwise the
// file has unused imports and fails to compile.
func TestFacadeGoStreamingOnly(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	testutil.RequireProtoImage(t, ctx)

	dest := t.TempDir()
	err := proto.GenerateClient(ctx, proto.ClientRequest{
		Language:    languages.GO,
		Destination: dest,
		Module:      "feed",
		Facade:      true,
		Sources: []proto.Source{
			{Path: "stream/v1/stream.proto", Content: fixture(t, "streaming/stream.proto")},
		},
	})
	require.NoError(t, err, "proto companion image not built: %s", testutil.BuildCompanionsHint)

	body, err := os.ReadFile(filepath.Join(dest, "stream/v1/feed/feed_facade.pb.go"))
	require.NoError(t, err)
	require.Contains(t, string(body), "// skipped: streaming RPC Watch")
	require.NotContains(t, string(body), "\"context\"", "context must not be imported when no method uses it")

	// go vet fails on an unused import, so this proves the file compiles.
	goVet(t, ctx, dest, "github.com/codefly-dev/cli/pkg/builder/clients/feed")
}

// TestFacadeGoMultiDirectorySubset guards the `strategy: all` wiring. The facade
// plugin validates the services= subset against the files buf hands each
// invocation. Under buf's default per-directory strategy the plugin runs once
// per proto directory, so a subset naming a service from one directory is
// reported "declared services not found" by every other directory's invocation
// and the whole run fails. These sources span two directories (saas/accounts/v1
// and stream/v1) while the subset names only AuditService, so generation
// succeeds only when the facade is invoked with strategy: all — a single-file
// facade test (one directory) passes either way and would not catch a regression.
func TestFacadeGoMultiDirectorySubset(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	testutil.RequireProtoImage(t, ctx)

	dest := t.TempDir()
	err := proto.GenerateClient(ctx, proto.ClientRequest{
		Language:    languages.GO,
		Destination: dest,
		Module:      "accounts",
		Services:    []string{"AuditService"},
		Facade:      true,
		Sources: []proto.Source{
			{Path: auditProtoPath, Content: fixture(t, auditProtoPath)},
			{Path: "stream/v1/stream.proto", Content: fixture(t, "streaming/stream.proto")},
		},
	})
	require.NoError(t, err, "facade must run with strategy: all so the services= subset resolves across directories: %s", testutil.BuildCompanionsHint)

	body, err := os.ReadFile(filepath.Join(dest, "saas/accounts/v1/accounts/accounts_facade.pb.go"))
	require.NoError(t, err)
	facade := string(body)
	require.Contains(t, facade, "func (c *Client) Audit()")
	// The subset excluded the other directory's service.
	require.NotContains(t, facade, "FeedService")
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

// TestFacadeTypeScriptIgnoresDependencyServices covers what asking buf for the
// imports does to the facade plugin. Those files land in file_to_generate
// alongside the module's own, and a CodeGeneratorRequest carries no is_import,
// so a dependency that declares a service is indistinguishable from one of
// ours: it gets a facade of its own, and its package joins the module's in the
// count that decides whether the caller's Module override is honoured.
//
// The fixture imports grpc/health/v1/health.proto — a dependency with a
// service whose module name collides with the fixture's own — so both halves
// fail when the imports reach this plugin: the collision aborts it outright,
// and short of that the Module override is silently replaced by the name
// derived from the proto package.
func TestFacadeTypeScriptIgnoresDependencyServices(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	testutil.RequireProtoImage(t, ctx)

	dest := t.TempDir()
	require.NoError(t, proto.GenerateClient(ctx, proto.ClientRequest{
		Language:    languages.TYPESCRIPT,
		Destination: dest,
		Module:      "rest",
		Facade:      true,
		Sources: []proto.Source{
			{Path: "acme/health/v1/health.proto", Content: fixture(t, "upstream/health.proto")},
		},
	}), "a dependency service must not collide with the module's own facade")

	body, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash("acme/health/v1/rest_facade.ts")))
	require.NoError(t, err, "the Module override names the facade; a second package in the run silently drops it")
	require.Contains(t, string(body), "export const rest")

	_, err = os.Stat(filepath.Join(dest, filepath.FromSlash("grpc/health/v1/health_facade.ts")))
	require.True(t, os.IsNotExist(err), "a facade for someone else's service must not ship in the library")

	// The bindings are still owed: only the facade is kept away from the
	// imports, not the generator that has to resolve them.
	require.FileExists(t, filepath.Join(dest, filepath.FromSlash("grpc/health/v1/health_pb.ts")))

	tsc(t, ctx, dest)
}

// TestTypeScriptReclaimsDroppedDependencyTrees covers the other half of asking
// buf for the imports: which trees the destination is allowed to keep.
//
// removeForeignOutput reclaims a fixed pair of roots, which was the whole set a
// run could write. It no longer is — the run writes a tree per imported
// namespace — so a contract that drops an import used to leave that tree in the
// library forever, shipping bindings from a generator the run no longer invokes.
func TestTypeScriptReclaimsDroppedDependencyTrees(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	testutil.RequireProtoImage(t, ctx)

	dest := t.TempDir()
	const ownPath = "acme/health/v1/health.proto"
	require.NoError(t, proto.GenerateClient(ctx, proto.ClientRequest{
		Language:    languages.TYPESCRIPT,
		Destination: dest,
		Module:      "rest",
		Sources:     []proto.Source{{Path: ownPath, Content: fixture(t, "upstream/health.proto")}},
	}))
	require.FileExists(t, filepath.Join(dest, filepath.FromSlash("grpc/health/v1/health_pb.ts")))

	// Same contract, with the dependency dropped.
	const standalone = `syntax = "proto3";

package acme.health.v1;

message PingRequest {
  string id = 1;
}
`
	require.NoError(t, proto.GenerateClient(ctx, proto.ClientRequest{
		Language:    languages.TYPESCRIPT,
		Destination: dest,
		Module:      "rest",
		Sources:     []proto.Source{{Path: ownPath, Content: []byte(standalone)}},
	}))

	_, err := os.Stat(filepath.Join(dest, "grpc"))
	require.True(t, os.IsNotExist(err), "a namespace the contract stopped importing must be reclaimed")
	require.FileExists(t, filepath.Join(dest, filepath.FromSlash("acme/health/v1/health_pb.ts")),
		"the run's own output must survive the reclaim")
}
