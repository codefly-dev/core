package proto

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/languages"
)

func TestGoClientGenerationPreservesProtovalidatePackage(t *testing.T) {
	dir := t.TempDir()
	if err := CreateBufConfiguration(context.Background(), dir, "users-api", languages.GO); err != nil {
		t.Fatalf("CreateBufConfiguration: %v", err)
	}
	configuration, err := os.ReadFile(filepath.Join(dir, "buf.gen.yaml"))
	if err != nil {
		t.Fatalf("read generated buf config: %v", err)
	}
	if !strings.Contains(string(configuration), "buf.build/bufbuild/protovalidate") {
		t.Fatal("Go client generation rewrites Protovalidate into the client package")
	}
}
