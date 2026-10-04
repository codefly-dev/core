package sbom

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Pnpm inventories the authoritative pnpm lock through pnpm's read-only
// CycloneDX exporter. It neither installs packages nor runs lifecycle scripts.
// The caller must provide pnpm 11.17.0 or newer with the tested sbom contract.
func Pnpm(ctx context.Context, dir string, includeDev bool) (*Result, error) {
	if _, err := os.Stat(filepath.Join(dir, "pnpm-lock.yaml")); err != nil {
		return nil, fmt.Errorf("%w: pnpm-lock.yaml is required: %v", ErrUnsupported, err)
	}
	// Older pnpm versions interpret unknown commands as project scripts. Check
	// the selected version before invoking sbom, so "inventory" cannot execute
	// a package.json script named sbom on an unsupported toolchain.
	versionCommand := exec.CommandContext(ctx, "pnpm", "--version")
	versionCommand.Dir = dir
	versionOutput, err := versionCommand.Output()
	if err != nil {
		return nil, fmt.Errorf("inspect pnpm version: %w", err)
	}
	if err := requirePnpmSBOMVersion(string(versionOutput)); err != nil {
		return nil, err
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

func requirePnpmSBOMVersion(raw string) error {
	version, err := semver.StrictNewVersion(strings.TrimSpace(raw))
	if err != nil || version.Prerelease() != "" || version.LessThan(semver.MustParse("11.17.0")) {
		return fmt.Errorf("%w: pnpm 11.17.0 or newer is required for lockfile SBOM export (reported %q)", ErrUnsupported, strings.TrimSpace(raw))
	}
	return nil
}
