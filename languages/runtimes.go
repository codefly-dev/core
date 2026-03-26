package languages

import (
	"context"
	"os"

	"os/exec"

	"github.com/codefly-dev/core/wool"
)

type VersionRequirement struct {
	Minimum string
}

type GoRuntimeConfiguration struct {
	//versionRequirement *VersionRequirement
}

// HasGoRuntime checks if the Go runtime is available on the system.
// and verify minimum version.
func HasGoRuntime(_ *GoRuntimeConfiguration) bool {
	if _, err := exec.LookPath("go"); err == nil {
		return true
	}
	return false
}

type PythonPoetryRuntimeConfiguration struct {
	//versionRequirement *VersionRequirement
}

// HasPythonPoetryRuntime checks if Poetry is available and functional.
func HasPythonPoetryRuntime(_ *GoRuntimeConfiguration) bool {
	p, err := exec.LookPath("poetry")
	if err != nil {
		return false
	}
	// Verify the binary can actually run (symlink target may be broken).
	cmd := exec.Command(p, "--version")
	return cmd.Run() == nil
}

type NodeRuntimeConfiguration struct {
}

// HasNodeRuntime checks if the Go runtime is available on the system.
// and verify minimum version.
func HasNodeRuntime(_ *NodeRuntimeConfiguration) bool {
	if _, err := exec.LookPath("node"); err == nil {
		return true
	}
	return false
}

type RailsRuntimeConfiguration struct {
}

// HasRailsRuntime checks if the Go runtime is available on the system.
// and verify minimum version.
func HasRailsRuntime(ctx context.Context, _ *RailsRuntimeConfiguration) bool {
	w := wool.Get(ctx).In("HasRails")
	if _, err := exec.LookPath("ruby"); err != nil {
		return false
	}
	cmd := exec.Command("ruby", "-v")
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	w.Debug("out", wool.Field("out", string(out)))
	if _, err := exec.LookPath("rails"); err != nil {
		return false
	}
	return true
}
