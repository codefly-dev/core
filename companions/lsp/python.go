package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/resources"
	runners "github.com/codefly-dev/core/runners/base"
)

func init() {
	Register(languages.PYTHON, &LanguageConfig{
		CompanionImage: func(ctx context.Context) (*resources.DockerImage, error) {
			return &resources.DockerImage{Name: "codeflydev/python-lsp", Tag: "0.0.1"}, nil
		},
		LSPBinary: "pylsp",
		LSPListenArgs: func(port int) []string {
			return []string{"--tcp", "--host", "0.0.0.0", "--port", fmt.Sprintf("%d", port)}
		},
		LanguageID:     "python",
		FileExtensions: []string{".py", ".pyi"},
		SkipDirs:       []string{".venv", "venv", "__pycache__", ".git", "node_modules", ".mypy_cache", ".pytest_cache", ".ruff_cache"},
		SetupRunner:    setupPythonRunner,
	})
}

// setupPythonRunner mounts the uv cache into the companion so pylsp can
// resolve dependencies without downloading them again.
func setupPythonRunner(_ context.Context, runner runners.CompanionRunner, _ string) {
	uvCache := resolveUVCache()
	if uvCache != "" {
		runner.WithMount(uvCache, "/root/.cache/uv")
	}
}

func resolveUVCache() string {
	if v, ok := os.LookupEnv("UV_CACHE_DIR"); ok {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache/uv")
}
