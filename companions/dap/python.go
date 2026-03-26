package dap

import (
	"context"
	"fmt"
	"os"

	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/resources"
	runners "github.com/codefly-dev/core/runners/base"
)

func init() {
	Register(languages.PYTHON, &LanguageConfig{
		CompanionImage: func(_ context.Context) (*resources.DockerImage, error) {
			// Use the Python companion image (which includes debugpy).
			return &resources.DockerImage{Name: "codeflydev/python", Tag: "0.0.5"}, nil
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
func setupPythonRunner(_ context.Context, runner runners.CompanionRunner, _ string) {
	if cache := os.Getenv("UV_CACHE_DIR"); cache != "" {
		runner.WithMount(cache, "/root/.cache/uv")
	}
}
