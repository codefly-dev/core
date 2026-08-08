package python

// ARCHITECTURE: persistent per-workspace venv. The default test path runs
// `uv run --with-editable .`, which RE-RESOLVES and REBUILDS the editable
// project on every invocation. A persistent venv avoids repeating both normal
// PEP 517 builds and expensive compiled builds while preserving uv's standard
// isolated-backend behavior unless recovery explicitly disables it.
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

	// 2) Install declared requirements and build dependencies into the venv.
	//    This MUST be a separate uv invocation from the editable install below.
	//    A single `uv pip install setuptools ... -e . --no-build-isolation` may
	//    prepare the editable metadata before installing its peer requirements,
	//    so the backend still fails to import setuptools/cython/numpy even though
	//    they are present in the argv. Astropy exposed this exact ordering bug.
	if dependencyArgs := venvDependencyInstallArgs(pyPath, spec); len(dependencyArgs) > 0 {
		if out, err := runUv(ctx, sourceDir, dependencyArgs); err != nil {
			return "", fmt.Errorf("uv pip install requirements and build dependencies failed: %v\n%s", err, out)
		}
	}

	// 3) Build/install the editable project after its declared build environment
	//    is materialized. --no-build-isolation is now safe because the backend
	//    can import every package derived from [build-system].requires.
	installArgs := venvEditableInstallArgs(pyPath, spec)
	if out, err := runUv(ctx, sourceDir, installArgs); err != nil {
		if !editableHookUnavailable(out) {
			return "", fmt.Errorf("uv pip install (editable project) failed: %v\n%s", err, out)
		}

		// PEP 660 was standardized before every historical build backend
		// implemented build_editable. The pip version available at the source
		// commit date is already installed in this venv and owns the standard
		// compatibility path for those backends (setup.py develop for old
		// setuptools). Reuse that real packaging implementation instead of
		// reproducing it in Codefly or allowing a post-commit setuptools release.
		fallbackArgs := venvHistoricalEditableInstallArgs(spec)
		if fallbackOut, fallbackErr := runExecutable(ctx, sourceDir, pyPath, fallbackArgs); fallbackErr != nil {
			return "", fmt.Errorf("editable project install failed with PEP 660 and historical pip fallback: uv: %v\n%s\npip: %v\n%s", err, out, fallbackErr, fallbackOut)
		}
	}

	if err := os.WriteFile(marker, []byte(want), 0o644); err != nil {
		return "", fmt.Errorf("write venv marker: %w", err)
	}
	return pyPath, nil
}

// venvDependencyInstallArgs builds the first `uv pip install` argv that
// materializes requirements, build dependencies, and the pip release available
// at ExcludeNewer. Historical pip is the standards-compliant compatibility
// implementation for editable backends that predate PEP 660. setuptools is
// materialized alongside it because historical pip's setup.py-develop path
// imports setuptools from the target venv; build isolation's private copy is
// not visible to that interpreter.
func venvDependencyInstallArgs(pyPath string, spec TestFormulaSpec) []string {
	args := []string{"pip", "install", "--python", pyPath}
	if spec.ExcludeNewer != "" {
		args = append(args, "--exclude-newer", spec.ExcludeNewer)
	}
	args = append(args, "pip", "setuptools")
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
	for _, group := range spec.DependencyGroups {
		if group != "" {
			args = append(args, "--group", group)
		}
	}
	return args
}

// venvEditableInstallArgs builds the second `uv pip install` argv. The
// dependency step above has already populated the venv, so a no-isolation
// editable build can safely import its declared backend requirements.
func venvEditableInstallArgs(pyPath string, spec TestFormulaSpec) []string {
	args := []string{"pip", "install", "--python", pyPath}
	if spec.ExcludeNewer != "" {
		args = append(args, "--exclude-newer", spec.ExcludeNewer)
	}
	if spec.NoBuildIsolation {
		args = append(args, "--no-build-isolation")
	}
	for _, extra := range spec.Extras {
		if extra != "" {
			args = append(args, "--extra", extra)
		}
	}
	target := spec.EditableTarget
	if target == "" {
		target = "."
	}
	args = append(args, "-e", target)
	return args
}

// editableHookUnavailable recognizes the backend capability failure defined by
// PEP 660. Other build failures are project failures and must remain visible;
// only absence of the standardized hook selects historical pip's compatibility
// implementation.
func editableHookUnavailable(output string) bool {
	low := strings.ToLower(output)
	if !strings.Contains(low, "build_editable") {
		return false
	}
	return strings.Contains(low, "has no attribute") ||
		strings.Contains(low, "does not support") ||
		strings.Contains(low, "not supported")
}

// venvHistoricalEditableInstallArgs builds argv for `<venv-python> -m pip`.
// Declared requirements and backend dependencies were materialized by uv
// first. Historical pip then owns the complete editable-install contract,
// including the project's runtime dependencies, just as it did at that date.
func venvHistoricalEditableInstallArgs(spec TestFormulaSpec) []string {
	args := []string{"-m", "pip", "install"}
	if spec.NoBuildIsolation {
		args = append(args, "--no-build-isolation")
	}
	args = append(args, "-e")
	target := spec.EditableTarget
	if target == "" {
		target = "."
	}
	return append(args, editableTargetWithExtras(target, spec.Extras))
}

// editableTargetWithExtras preserves optional dependencies when the historical
// pip compatibility path owns the editable install. uv accepts typed --extra
// flags; pip expresses the same standard contract on the editable requirement.
func editableTargetWithExtras(target string, extras []string) string {
	clean := make([]string, 0, len(extras))
	for _, extra := range extras {
		if extra = strings.TrimSpace(extra); extra != "" {
			clean = append(clean, extra)
		}
	}
	if len(clean) == 0 {
		return target
	}
	return target + "[" + strings.Join(clean, ",") + "]"
}

// venvProvisionHash fingerprints the inputs that affect the built venv so a
// changed python pin / dep set forces a rebuild but an unchanged one reuses.
func venvProvisionHash(spec TestFormulaSpec) string {
	parts := []string{"py=" + spec.Python, "exclude-newer=" + spec.ExcludeNewer, "editable=" + spec.EditableTarget}
	if spec.NoBuildIsolation {
		parts = append(parts, "nobuildiso")
	}
	reqs := append([]string{}, spec.Requirements...)
	withs := append([]string{}, spec.With...)
	groups := append([]string{}, spec.DependencyGroups...)
	extras := append([]string{}, spec.Extras...)
	sort.Strings(reqs)
	sort.Strings(withs)
	sort.Strings(groups)
	sort.Strings(extras)
	parts = append(parts, "req="+strings.Join(reqs, ","))
	parts = append(parts, "with="+strings.Join(withs, ","))
	parts = append(parts, "groups="+strings.Join(groups, ","))
	parts = append(parts, "extras="+strings.Join(extras, ","))
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
	return runExecutable(ctx, dir, "uv", args)
}

func runExecutable(ctx context.Context, dir, executable string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	return string(out), err
}
