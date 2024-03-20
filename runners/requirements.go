package runners

import (
	"context"
	"fmt"
	"os/exec"

	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
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
	for _, req := range requirements {
		switch req.Type {
		case agentv0.Runtime_DOCKER:
			ok := DockerEngineRunning(ctx)
			if !ok {
				return fmt.Errorf("Docker is required and is not running")
			}
		case agentv0.Runtime_GO:
			_, err := exec.LookPath("go")
			if err != nil {
				return fmt.Errorf("Go is required and is not installed")
			}
		case agentv0.Runtime_NPM:
			_, err := exec.LookPath("npm")
			if err != nil {
				return fmt.Errorf("npm is required and is not installed")
			}
		case agentv0.Runtime_PYTHON:
			_, err := CheckPythonPath()
			if err != nil {
				return err
			}
		case agentv0.Runtime_PYTHON_POETRY:
			_, err := exec.LookPath("poetry")
			if err != nil {
				return fmt.Errorf("Poetry is required and is not installed")
			}
		}
	}
	return nil
}
