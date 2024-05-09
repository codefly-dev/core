package languages

import "os/exec"

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

// HasPythonPoetryRuntime checks if the Go runtime is available on the system.
// and verify minimum version.
func HasPythonPoetryRuntime(_ *GoRuntimeConfiguration) bool {
	if _, err := exec.LookPath("poetry"); err == nil {
		return true
	}
	return false
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
