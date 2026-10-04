package sbom

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Pnpm inventories the authoritative pnpm lock through pnpm's read-only
// CycloneDX exporter. It neither installs packages nor runs lifecycle scripts.
// The caller must provide pnpm with sbom support (pnpm 11 or newer).
func Pnpm(ctx context.Context, dir string, includeDev bool) (*Result, error) {
	if _, err := os.Stat(filepath.Join(dir, "pnpm-lock.yaml")); err != nil {
		return nil, fmt.Errorf("%w: pnpm-lock.yaml is required: %v", ErrUnsupported, err)
	}
	args := []string{"sbom", "--lockfile-only", "--sbom-format", "cyclonedx", "--sbom-spec-version", "1.5"}
	if !includeDev {
		args = append(args, "--prod")
	}
	cmd := exec.CommandContext(ctx, "pnpm", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pnpm lockfile CycloneDX export (requires pnpm with sbom support): %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseCycloneDX(stdout.Bytes(), "pnpm-lockfile-cyclonedx1.5", "TYPESCRIPT")
}
