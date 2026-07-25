package dap

import (
	"context"
	"fmt"
	"os"

	"github.com/codefly-dev/core/companions/python"
	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/companion"
)

func init() {
	Register(languages.PYTHON, &LanguageConfig{
		CompanionImage: func(ctx context.Context) (*resources.DockerImage, error) {
			return python.CompanionImage(ctx)
		},
		DAPBinary: "python",
		DAPListenArgs: func(port int) []string {
			return []string{"-m", "debugpy.adapter", "--host", "0.0.0.0", "--port", fmt.Sprintf("%d", port)}
		},
		LanguageID:  "python",
		SetupRunner: setupPythonRunner,
	})
}

// setupPythonRunner mounts the UV cache into the companion when available.
func setupPythonRunner(_ context.Context, runner companion.CompanionRunner, _ string) {
	if cache := os.Getenv("UV_CACHE_DIR"); cache != "" {
		runner.WithMount(cache, "/root/.cache/uv")
	}
}
