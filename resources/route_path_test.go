package resources

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRoutePathsRejectTraversal(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	for name, test := range map[string]func() error{
		"grpc identity": func() error {
			_, err := FilePathForGRPC(ctx, root, "../../escape/service", "method")
			return err
		},
		"grpc name": func() error {
			_, err := FilePathForGRPC(ctx, root, "module/service", "../method")
			return err
		},
		"rest identity": func() error {
			_, err := FilePathForRest(ctx, root, "../escape", "/route")
			return err
		},
		"rest NUL": func() error {
			_, err := FilePathForRest(ctx, root, "module/service", "/route\x00escape")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := test(); err == nil {
				t.Fatal("unsafe route path was accepted")
			}
		})
	}
}

func TestRoutePathsRejectSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup requires additional privileges on Windows")
	}
	ctx := context.Background()
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "module"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "module", "service")); err != nil {
		t.Fatal(err)
	}

	if _, err := FilePathForGRPC(ctx, root, "module/service", "method"); err == nil {
		t.Fatal("gRPC route directory symlink escape was accepted")
	}
	if _, err := FilePathForRest(ctx, root, "module/service", "/route"); err == nil {
		t.Fatal("REST route directory symlink escape was accepted")
	}
}

func TestRouteSavesUsePrivateFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	route := &GRPCRoute{Module: "module", Service: "service", Name: "method", Package: "pkg", ServiceName: "API"}
	if err := route.Save(ctx, root); err != nil {
		t.Fatalf("save route: %v", err)
	}
	file, err := FilePathForGRPC(ctx, root, route.ServiceUnique(), route.Name)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("route mode = %#o, want 0600", got)
	}
}
