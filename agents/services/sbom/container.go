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
	// SyftScratchSize bounds temporary layer extraction. 64 MiB is too small
	// for normal service images such as Redis once their layers are expanded.
	SyftScratchSize = "512m"
)

// Container generates a package-level inventory for a registry image. The
// managed fallback is digest pinned, runs read-only, and does not mount the
// Docker socket or workspace into the scanner container.
func Container(ctx context.Context, image string) (*Result, error) {
	if image == "" {
		return nil, fmt.Errorf("container SBOM requires an image reference")
	}
	name := "syft"
	args := []string{"registry:" + image, "-q", "-o", "cyclonedx-json@1.5"}
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
		return nil, fmt.Errorf("%s container SBOM failed: %w: %s", tool, err, strings.TrimSpace(stderr.String()))
	}
	return parseCycloneDX(stdout.Bytes(), tool, "DOCKER")
}

func managedSyftArgs(image string) []string {
	return []string{
		"run", "--rm", "--network", "bridge", "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,noexec,nosuid,size=" + SyftScratchSize,
		SyftImage, "registry:" + image, "-q", "-o", "cyclonedx-json@1.5",
	}
}
