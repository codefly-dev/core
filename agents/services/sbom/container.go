package sbom

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const (
	// SyftVersion and SyftImage pin the managed container generator used when
	// an operator-managed syft binary is not available.
	SyftVersion = "v1.48.0"
	SyftImage   = "anchore/syft@sha256:b4f1df79f97b817682d8b5ff941eb6bfe74f6172553a5e312c75bbc2eabc405c"
)

// Container generates a package-level inventory for a registry image. The
// managed fallback is digest pinned, runs read-only, and does not mount the
// Docker socket or workspace into the scanner container.
func Container(ctx context.Context, image string) (*Result, error) {
	if image == "" {
		return nil, fmt.Errorf("container SBOM requires an image reference")
	}
	name := "syft"
	args := []string{"registry:" + image, "-o", "cyclonedx-json@1.5"}
	tool := "syft"
	if _, err := exec.LookPath(name); err != nil {
		if _, dockerErr := exec.LookPath("docker"); dockerErr != nil {
			return nil, fmt.Errorf("%w: neither syft nor docker is installed", ErrUnsupported)
		}
		name = "docker"
		args = managedSyftArgs(image)
		tool = "syft@" + SyftVersion
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, wrapSyftError(tool, image, err, stderr.String())
	}
	return parseCycloneDX(stdout.Bytes(), tool, "DOCKER")
}

func managedSyftArgs(image string) []string {
	return []string{
		"run", "--rm", "--network", "bridge", "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		// An unsized tmpfs defaults to half of host memory, so extraction is
		// bounded by real resources rather than a constant that fails on any
		// image whose expanded layers exceed it.
		"--tmpfs", "/tmp:rw,noexec,nosuid",
		// Point HOME at the writable tmpfs so syft's cache lands there instead
		// of failing to create /.cache/syft on the read-only root filesystem.
		"--env", "HOME=/tmp",
		SyftImage, "registry:" + image, "-o", "cyclonedx-json@1.5",
	}
}

// wrapSyftError surfaces syft's stderr on failure instead of swallowing it, and
// names the memory-bounded scratch and image when the scan runs out of space.
func wrapSyftError(tool, image string, runErr error, stderr string) error {
	detail := strings.TrimSpace(stderr)
	if strings.Contains(detail, "no space left on device") {
		return fmt.Errorf("%s container SBOM for %s exhausted its /tmp tmpfs (bounded by container memory); rerun on a host with more memory: %w: %s", tool, image, runErr, detail)
	}
	return fmt.Errorf("%s container SBOM for %s failed: %w: %s", tool, image, runErr, detail)
}
