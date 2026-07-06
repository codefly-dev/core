package python

// ARCHITECTURE: persistent per-workspace venv. The default test path runs
// `uv run --with-editable .`, which RE-RESOLVES and REBUILDS the editable
// project on every invocation — fine for pure Python (django), ruinous for a
// C-extension project (numpy/scipy/cython), where each test run recompiles for
// minutes and the task times out even though the agent's patch is correct.
//
// ensurePersistentVenv builds the editable project + its declared deps ONCE
// into <sourceDir>/.mind-venv, keyed by a hash of the provisioning so a changed
// dep set rebuilds. Subsequent runs execute against that venv (BuildUvArgs' venv
// branch) with no rebuild. Python source edits are still reflected (editable
// install); only C-source edits would need a rebuild, which SWE-bench fixes
// rarely make. This mirrors how the reference SWE-bench harness installs each
// instance ONCE into an image and runs the agent's edits on top.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ensurePersistentVenv provisions (or reuses) the workspace venv and returns its
// python interpreter path. Idempotent: a marker file recording the provisioning
// hash lets a warm venv be reused across runs; a hash mismatch reprovisions.
func ensurePersistentVenv(ctx context.Context, sourceDir string, spec TestFormulaSpec) (string, error) {
	venvDir := filepath.Join(sourceDir, ".mind-venv")
	pyPath := venvInterpreter(venvDir)
	marker := filepath.Join(venvDir, ".mind-provisioned")
	want := venvProvisionHash(spec)

	if got, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(got)) == want {
		if _, statErr := os.Stat(pyPath); statErr == nil {
			return pyPath, nil // already provisioned with this exact dep set
		}
	}
	// (Re)create the venv from scratch to avoid a half-provisioned state.
	_ = os.RemoveAll(venvDir)

	// 1) uv venv [--python X] <venvDir>
	venvArgs := []string{"venv"}
	if spec.Python != "" {
		venvArgs = append(venvArgs, "--python", spec.Python)
	}
	venvArgs = append(venvArgs, venvDir)
	if out, err := runUv(ctx, sourceDir, venvArgs); err != nil {
		return "", fmt.Errorf("uv venv failed: %v\n%s", err, out)
	}

	// 2) uv pip install --python <py> [--no-build-isolation] [-r req...] [deps...] -e <project>
	//    Deps and requirements install FIRST so the editable build (no isolation)
	//    sees numpy/cython already present.
	installArgs := venvInstallArgs(pyPath, spec)
	if out, err := runUv(ctx, sourceDir, installArgs); err != nil {
		return "", fmt.Errorf("uv pip install (editable project + deps) failed: %v\n%s", err, out)
	}

	if err := os.WriteFile(marker, []byte(want), 0o644); err != nil {
		return "", fmt.Errorf("write venv marker: %w", err)
	}
	return pyPath, nil
}

// venvInstallArgs builds the `uv pip install` argv that populates the venv.
// Pure/deterministic so it is unit-tested without executing uv.
func venvInstallArgs(pyPath string, spec TestFormulaSpec) []string {
	args := []string{"pip", "install", "--python", pyPath}
	if spec.NoBuildIsolation {
		args = append(args, "--no-build-isolation")
	}
	for _, r := range spec.Requirements {
		if r != "" {
			args = append(args, "-r", r)
		}
	}
	for _, w := range spec.With {
		if w != "" {
			args = append(args, w)
		}
	}
	target := spec.EditableTarget
	if target == "" {
		target = "."
	}
	args = append(args, "-e", target)
	return args
}

// venvProvisionHash fingerprints the inputs that affect the built venv so a
// changed python pin / dep set forces a rebuild but an unchanged one reuses.
func venvProvisionHash(spec TestFormulaSpec) string {
	parts := []string{"py=" + spec.Python, "editable=" + spec.EditableTarget}
	if spec.NoBuildIsolation {
		parts = append(parts, "nobuildiso")
	}
	reqs := append([]string{}, spec.Requirements...)
	withs := append([]string{}, spec.With...)
	sort.Strings(reqs)
	sort.Strings(withs)
	parts = append(parts, "req="+strings.Join(reqs, ","))
	parts = append(parts, "with="+strings.Join(withs, ","))
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

// venvInterpreter is the python path inside a uv venv, per OS layout.
func venvInterpreter(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts", "python.exe")
	}
	return filepath.Join(venvDir, "bin", "python")
}

func runUv(ctx context.Context, dir string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "uv", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	return string(out), err
}
