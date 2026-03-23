package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/codefly-dev/core/companions/golang"
	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/resources"
	runners "github.com/codefly-dev/core/runners/base"
)

func init() {
	Register(languages.GO, &LanguageConfig{
		CompanionImage: func(ctx context.Context) (*resources.DockerImage, error) {
			return golang.CompanionImage(ctx)
		},
		LSPBinary: "gopls",
		LSPListenArgs: func(port int) []string {
			return []string{"serve", "-listen", fmt.Sprintf(":%d", port)}
		},
		LanguageID:     "go",
		FileExtensions: []string{".go"},
		SkipDirs:       []string{"vendor", ".git", "testdata", "cache", "node_modules"},
		SetupRunner:    setupGoRunner,
	})
}

// setupGoRunner mounts the Go module cache into the companion so gopls can
// resolve dependencies without downloading them again.
func setupGoRunner(_ context.Context, runner runners.CompanionRunner, _ string) {
	goModCache := resolveGoModCache()
	if goModCache != "" {
		runner.WithMount(goModCache, "/go/pkg/mod")
	}
}

// resolveGoModCache finds the Go module cache directory on the host.
func resolveGoModCache() string {
	if v, ok := os.LookupEnv("GOMODCACHE"); ok {
		return v
	}
	if v, ok := os.LookupEnv("GOPATH"); ok {
		return filepath.Join(v, "pkg/mod")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "go/pkg/mod")
}
