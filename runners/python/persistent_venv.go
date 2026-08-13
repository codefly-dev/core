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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/codefly-dev/core/resources"
	"github.com/pelletier/go-toml/v2"
)

// ensurePersistentVenv provisions (or reuses) the workspace venv and returns its
// python interpreter path. Idempotent: a marker file recording the provisioning
// hash lets a warm venv be reused across runs; a hash mismatch reprovisions.
func ensurePersistentVenv(ctx context.Context, sourceDir string, spec TestFormulaSpec) (string, error) {
	venvDir := filepath.Join(sourceDir, ".mind-venv")
	pyPath := venvInterpreter(venvDir)
	marker := filepath.Join(venvDir, ".mind-provisioned")
	var buildRequirements []string
	if spec.NoBuildIsolation {
		var err error
		buildRequirements, err = readBuildSystemRequirements(sourceDir)
		if err != nil {
			return "", fmt.Errorf("read PEP 517 build requirements: %w", err)
		}
	}
	want := venvProvisionHash(spec, buildRequirements)

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
	if out, err := runUv(ctx, sourceDir, venvArgs, spec.Env); err != nil {
		return "", fmt.Errorf("uv venv failed: %v\n%s", err, out)
	}

	// 2) Install declared requirements and build dependencies into the venv.
	//    This MUST be a separate uv invocation from the editable install below.
	//    A single `uv pip install <requirements> -e . --no-build-isolation` may
	//    prepare the editable metadata before installing its peer requirements,
	//    so the backend can still fail to import them even though they are present
	//    in the argv.
	if dependencyArgs := venvDependencyInstallArgs(pyPath, spec, buildRequirements); len(dependencyArgs) > 0 {
		if out, err := runUv(ctx, sourceDir, dependencyArgs, spec.Env); err != nil {
			return "", fmt.Errorf("uv pip install requirements and build dependencies failed: %v\n%s", err, out)
		}
	}

	// 3) Build/install the editable project after its declared build environment
	//    is materialized. --no-build-isolation is now safe because the backend
	//    can import the static packages declared by [build-system].requires.
	installArgs := venvEditableInstallArgs(pyPath, spec)
	if out, err := runUv(ctx, sourceDir, installArgs, spec.Env); err != nil {
		if !editableHookUnavailable(out) {
			return "", fmt.Errorf("uv pip install (editable project) failed: %v\n%s", err, out)
		}

		// PEP 660 was standardized before every historical build backend
		// implemented build_editable. First let uv resolve and install the same
		// project through its standard wheel contract. That preserves
		// ExcludeNewer for runtime and extra dependencies even though historical
		// pip has no equivalent resolver cutoff. The pip version available at the
		// source commit date then owns only the compatibility link (setup.py
		// develop for old setuptools), with dependency resolution disabled.
		resolvedArgs := venvResolvedProjectInstallArgs(pyPath, spec)
		if resolvedOut, resolvedErr := runUv(ctx, sourceDir, resolvedArgs, spec.Env); resolvedErr != nil {
			return "", fmt.Errorf("editable project install failed with PEP 660 and historical wheel resolution: editable: %v\n%s\nwheel: %v\n%s", err, out, resolvedErr, resolvedOut)
		}
		fallbackArgs := venvHistoricalEditableInstallArgs(spec)
		if fallbackOut, fallbackErr := runExecutable(ctx, sourceDir, pyPath, fallbackArgs, spec.Env); fallbackErr != nil {
			return "", fmt.Errorf("editable project install failed with PEP 660 and historical pip fallback: uv: %v\n%s\npip: %v\n%s", err, out, fallbackErr, fallbackOut)
		}
	}

	if err := os.WriteFile(marker, []byte(want), 0o644); err != nil {
		return "", fmt.Errorf("write venv marker: %w", err)
	}
	return pyPath, nil
}

// venvDependencyInstallArgs builds the first `uv pip install` argv that
// materializes requirements, static PEP 517 build dependencies, and the pip
// release available at ExcludeNewer. Keeping buildRequirements and With in one
// resolver transaction honors explicit recovery constraints without discarding
// the project's standard packaging contract. Historical pip
// is the standards-compliant compatibility implementation for editable
// backends that predate PEP 660. setuptools is materialized alongside it
// because historical pip's setup.py-develop path imports setuptools from the
// target venv; build isolation's private copy is not visible to that
// interpreter.
func venvDependencyInstallArgs(pyPath string, spec TestFormulaSpec, buildRequirements []string) []string {
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
	for _, requirement := range buildRequirements {
		if requirement = strings.TrimSpace(requirement); requirement != "" {
			args = append(args, requirement)
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

// readBuildSystemRequirements returns the static PEP 517 requirements declared
// by the project. The values stay opaque PEP 508 strings: uv remains the
// packaging implementation and evaluates markers, versions, and direct
// references. Missing pyproject metadata is valid for legacy setup.py projects.
func readBuildSystemRequirements(sourceDir string) ([]string, error) {
	payload, err := os.ReadFile(filepath.Join(sourceDir, "pyproject.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var project struct {
		BuildSystem struct {
			Requires []string `toml:"requires"`
		} `toml:"build-system"`
	}
	if err := toml.Unmarshal(payload, &project); err != nil {
		return nil, err
	}
	return project.BuildSystem.Requires, nil
}

// venvEditableInstallArgs builds the second `uv pip install` argv. The
// dependency step above has already populated the venv, so a no-isolation
// editable build can safely import its declared backend requirements.
func venvEditableInstallArgs(pyPath string, spec TestFormulaSpec) []string {
	return venvProjectInstallArgs(pyPath, spec, true)
}

// venvResolvedProjectInstallArgs builds the non-editable wheel invocation used
// before the historical-pip compatibility link. uv remains the sole dependency
// resolver, so ExcludeNewer governs project runtime dependencies and extras on
// both the PEP 660 path and the legacy setup.py-develop path.
func venvResolvedProjectInstallArgs(pyPath string, spec TestFormulaSpec) []string {
	return venvProjectInstallArgs(pyPath, spec, false)
}

func venvProjectInstallArgs(pyPath string, spec TestFormulaSpec, editable bool) []string {
	args := []string{"pip", "install", "--python", pyPath}
	if spec.ExcludeNewer != "" {
		args = append(args, "--exclude-newer", spec.ExcludeNewer)
	}
	if spec.NoBuildIsolation {
		args = append(args, "--no-build-isolation")
	}
	target := spec.EditableTarget
	if target == "" {
		target = "."
	}
	// `uv pip install` resolves extras on an explicit requirement target. Its
	// project-level --extra flag is for lock/project discovery and rejects this
	// standalone target form even when setup.cfg is present. Preserve the
	// standard editable requirement spelling for both modern uv and the
	// historical-pip fallback.
	if editable {
		args = append(args, "-e")
	}
	args = append(args, editableTargetWithExtras(target, spec.Extras))
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
// Declared requirements, backend dependencies, project runtime dependencies,
// and extras were materialized by uv first. Historical pip owns only the
// editable-link compatibility contract and must never re-resolve dependencies
// without uv's historical cutoff.
func venvHistoricalEditableInstallArgs(spec TestFormulaSpec) []string {
	args := []string{"-m", "pip", "install", "--no-deps"}
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
func venvProvisionHash(spec TestFormulaSpec, buildRequirements []string) string {
	parts := []string{"py=" + spec.Python, "exclude-newer=" + spec.ExcludeNewer, "editable=" + spec.EditableTarget}
	if spec.NoBuildIsolation {
		parts = append(parts, "nobuildiso")
	}
	reqs := append([]string{}, spec.Requirements...)
	withs := append([]string{}, spec.With...)
	builds := append([]string{}, buildRequirements...)
	groups := append([]string{}, spec.DependencyGroups...)
	extras := append([]string{}, spec.Extras...)
	environment := make([]string, 0, len(spec.Env))
	for _, variable := range spec.Env {
		if variable == nil || strings.TrimSpace(variable.Key) == "" {
			continue
		}
		environment = append(environment, variable.Key+"="+variable.ValueAsString())
	}
	sort.Strings(reqs)
	sort.Strings(withs)
	sort.Strings(builds)
	sort.Strings(groups)
	sort.Strings(extras)
	sort.Strings(environment)
	parts = append(parts, "req="+strings.Join(reqs, ","))
	parts = append(parts, "with="+strings.Join(withs, ","))
	parts = append(parts, "build="+strings.Join(builds, ","))
	parts = append(parts, "groups="+strings.Join(groups, ","))
	parts = append(parts, "extras="+strings.Join(extras, ","))
	// Build environment is part of the compiled editable installation. A
	// recovered CFLAGS/CC value must invalidate a venv created without it.
	parts = append(parts, "env="+strings.Join(environment, "\x00"))
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

func runUv(ctx context.Context, dir string, args []string, environment []*resources.EnvironmentVariable) (string, error) {
	return runExecutable(ctx, dir, "uv", args, environment)
}

// runExecutable applies the typed test environment to every provisioning
// subprocess. ARCHITECTURE: persistent-venv creation, dependency resolution,
// editable installation, and historical-pip fallback are all part of the same
// test formula contract as execution; delaying these variables until pytest
// starts makes compiler recovery impossible.
func runExecutable(ctx context.Context, dir, executable string, args []string, environment []*resources.EnvironmentVariable) (string, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = dir
	cmd.Env = processEnvironment(environment...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// processEnvironment overlays typed values onto the ambient process
// environment without emitting duplicate keys. Later values win, matching the
// configuration contract used by test formula execution.
func processEnvironment(environment ...*resources.EnvironmentVariable) []string {
	result := append([]string(nil), os.Environ()...)
	positions := make(map[string]int, len(result)+len(environment))
	for index, entry := range result {
		key, _, found := strings.Cut(entry, "=")
		if found {
			positions[key] = index
		}
	}
	for _, variable := range environment {
		if variable == nil || strings.TrimSpace(variable.Key) == "" {
			continue
		}
		entry := variable.Key + "=" + variable.ValueAsString()
		if index, found := positions[variable.Key]; found {
			result[index] = entry
			continue
		}
		positions[variable.Key] = len(result)
		result = append(result, entry)
	}
	return result
}
