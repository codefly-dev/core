package runners

import (
	"context"
	"fmt"
	"os/exec"

	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	"github.com/codefly-dev/core/wool"
)

func CheckPythonPath() (string, error) {
	pythonVersions := []string{"python", "python3"}
	for _, version := range pythonVersions {
		if _, err := exec.LookPath(version); err == nil {
			return version, nil
		}
	}
	return "", fmt.Errorf("python/python3 is required and is not installed")
}

func CheckForRuntimes(ctx context.Context, requirements []*agentv0.Runtime) error {
	w := wool.Get(ctx).In("CheckForRuntimes")
	for _, req := range requirements {
		switch req.Type {
		case agentv0.Runtime_GO:
			_, err := exec.LookPath("go")
			if err != nil {
				w.Warn("Go is required to run in native mode. But don't worry, you can still run in container mode!")
			}
		case agentv0.Runtime_NPM:
			_, err := exec.LookPath("npm")
			if err != nil {
				w.Warn("NPM is required to run in native mode. But don't worry, you can still run in container mode!")
			}
		case agentv0.Runtime_PYTHON:
			_, err := CheckPythonPath()
			if err != nil {
				w.Warn("Python is required to run in native mode. But don't worry, you can still run in container mode!")
			}
		case agentv0.Runtime_PYTHON_POETRY:
			_, err := exec.LookPath("poetry")
			if err != nil {
				w.Warn("Poetry is required to run in native mode. But don't worry, you can still run in container mode!")
			}
		}
	}
	return nil
}
