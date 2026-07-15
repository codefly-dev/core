package base

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/wool"
)

// NixInstallCommand returns the command to install Nix based on OS
func NixInstallCommand() string {
	return "curl --proto '=https' --tlsv1.2 -sSf -L https://install.determinate.systems/nix | sh -s -- install"
}

// CheckNixInstalled checks if Nix is available in PATH
func CheckNixInstalled() bool {
	_, err := exec.LookPath("nix")
	return err == nil
}

// IsNixSupported returns true if the current OS supports Nix
func IsNixSupported() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

func CheckPythonPath() (string, error) {
	pythonVersions := []string{"python", "python3"}
	for _, version := range pythonVersions {
		if _, err := exec.LookPath(version); err == nil {
			return version, nil
		}
	}
	return "", fmt.Errorf("python/python3 is required and is not installed")
}

// CheckToolchains warns (does not fail) when a host toolchain required for LOCAL
// mode is missing. Toolchains are irrelevant for the NIX/DOCKER backends (nix
// availability is checked separately at runtime-context resolution), so this
// only concerns the native/LOCAL path.
func CheckToolchains(ctx context.Context, toolchains []*agentv0.Toolchain) error {
	w := wool.Get(ctx).In("CheckToolchains")
	warnMissing := func(bin, label string) {
		if _, err := exec.LookPath(bin); err != nil {
			w.Warn(label + " is required to run in native mode. But don't worry, you can still run in container mode!")
		}
	}
	for _, tc := range toolchains {
		switch tc.Type {
		case agentv0.Toolchain_GO:
			warnMissing("go", "Go")
		case agentv0.Toolchain_NPM:
			warnMissing("npm", "NPM")
		case agentv0.Toolchain_PYTHON:
			if _, err := CheckPythonPath(); err != nil {
				w.Warn("Python is required to run in native mode. But don't worry, you can still run in container mode!")
			}
		case agentv0.Toolchain_PYTHON_POETRY:
			warnMissing("poetry", "Poetry")
		case agentv0.Toolchain_RUST:
			warnMissing("rustc", "Rust")
		case agentv0.Toolchain_CARGO:
			warnMissing("cargo", "Cargo")
		case agentv0.Toolchain_SWIFT:
			warnMissing("swift", "Swift")
		}
	}
	return nil
}
