package sbom

import (
	"bytes"
	"context"
	"fmt"
	"os"
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
	scratch := ""
	if _, err := exec.LookPath(name); err != nil {
		if _, dockerErr := exec.LookPath("docker"); dockerErr != nil {
			return nil, fmt.Errorf("%w: neither syft nor docker is installed", ErrUnsupported)
		}
		// A disk-backed scratch dir bounds layer extraction by real free space
		// instead of a hardcoded tmpfs size, which failed once per oversized image.
		dir, err := os.MkdirTemp("", "codefly-syft-")
		if err != nil {
			return nil, fmt.Errorf("container SBOM: create scratch dir: %w", err)
		}
		defer os.RemoveAll(dir)
		// The syft image runs as a non-root user, so the bind-mounted scratch
		// must be writable regardless of that user's uid.
		if err := os.Chmod(dir, 0o777); err != nil {
			return nil, fmt.Errorf("container SBOM: prepare scratch dir: %w", err)
		}
		scratch = dir
		name = "docker"
		args = managedSyftArgs(image, dir)
		tool = "syft@" + SyftVersion
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, wrapSyftError(tool, image, scratch, err, stderr.String())
	}
	return parseCycloneDX(stdout.Bytes(), tool, "DOCKER")
}

func managedSyftArgs(image, scratch string) []string {
	return []string{
		"run", "--rm", "--network", "bridge", "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--mount", "type=bind,source=" + scratch + ",target=/tmp",
		// Point HOME at the writable scratch so syft's cache lands there instead
		// of failing to create /.cache/syft on the read-only root filesystem.
		"--env", "HOME=/tmp",
		SyftImage, "registry:" + image, "-o", "cyclonedx-json@1.5",
	}
}

// wrapSyftError surfaces syft's stderr on failure instead of swallowing it, and
// names the scratch dir and image when the scan ran out of disk space.
func wrapSyftError(tool, image, scratch string, runErr error, stderr string) error {
	detail := strings.TrimSpace(stderr)
	if strings.Contains(detail, "no space left on device") {
		location := "the host disk"
		if scratch != "" {
			location = "scratch dir " + scratch
		}
		return fmt.Errorf("%s container SBOM for %s ran out of space in %s; free up disk space and retry: %w: %s", tool, image, location, runErr, detail)
	}
	return fmt.Errorf("%s container SBOM for %s failed: %w: %s", tool, image, runErr, detail)
}
