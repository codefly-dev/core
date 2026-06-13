package python

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/codefly-dev/core/resources"
)

// formula_run is the GENERIC python test executor. There is no pytest path and
// no django path here — there is ONE path that runs a test FORMULA. The formula
// is DATA: the inner command (captured from the project's declarations), the
// per-instance provisioning (uv flags — python pin, editable install, extra
// deps), and the output format. The plugin (which is allowed to know uv) renders
// that data into a `uv run …` invocation and parses the output by format. It
// hardcodes no command, no framework, no provisioning — those all arrive as data.

// TestFormulaSpec is everything needed to run one test formula. Every field is
// DATA supplied by the caller (Mind's captured formula + the per-instance
// environment); nothing here is framework-specific knowledge.
type TestFormulaSpec struct {
	// Command is the inner test command from the formula, already split into
	// argv, e.g. ["pytest"] or ["python","tests/runtests.py","--verbosity=2"].
	Command []string
	// Selectors are the specific tests to run, appended positionally (pytest
	// node-ids, django dotted labels — the plugin doesn't interpret them).
	Selectors []string
	// Output names the format parser: "junit-xml" (the plugin adds --junitxml
	// and parses the file) or "unittest-text" (parses unittest's text output).
	Output string

	// --- provisioning (per-instance environment, all DATA) ---
	// These map to `uv run` flags. The plugin knows the FLAG NAMES (it is the
	// uv plugin); the VALUES come from the caller, so SWE-bench's per-instance
	// python version / editable install / dependency pins are injected as data
	// instead of a hardcoded brain-side command.
	NoProject    bool
	Python       string   // uv run --python <v>
	Editable     bool     // uv run --with-editable .
	Requirements []string // uv run --with-requirements <file>
	With         []string // uv run --with <spec>

	// ExtraArgs are appended to the command verbatim (power-user passthrough).
	ExtraArgs []string
	// Env are extra environment variables for the run.
	Env []*resources.EnvironmentVariable
}

const (
	OutputJUnitXML     = "junit-xml"
	OutputUnittestText = "unittest-text"
)

// SpecFromFormula translates a LANGUAGE-AGNOSTIC formula (generic command +
// output + env + an opaque provisioning map) into the python/uv TestFormulaSpec.
// This is the ONLY place python/uv knowledge interprets the provisioning bag:
// the keys python / editable / with / requirements / no_project become uv flags.
// Callers (Mind, the agent handler) stay framework- and toolchain-blind.
func SpecFromFormula(command []string, output string, env, provisioning map[string]string, selectors []string) TestFormulaSpec {
	spec := TestFormulaSpec{
		Command:      append([]string{}, command...),
		Output:       output,
		Selectors:    append([]string{}, selectors...),
		NoProject:    provisioning["no_project"] == "true",
		Python:       provisioning["python"],
		Editable:     provisioning["editable"] == "true",
		Requirements: splitComma(provisioning["requirements"]),
		With:         splitComma(provisioning["with"]),
	}
	for k, v := range env {
		spec.Env = append(spec.Env, &resources.EnvironmentVariable{Key: k, Value: v})
	}
	return spec
}

func splitComma(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var parts []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// BuildUvArgs renders a TestFormulaSpec into the argv for `uv` (excluding the
// leading "uv"). Pure and deterministic so the data→command translation is
// unit-tested without executing anything. junitFile is the path the plugin
// allocated for junit-xml output ("" for non-junit formats).
func BuildUvArgs(spec TestFormulaSpec, junitFile string) []string {
	args := []string{"run"}
	if spec.NoProject {
		args = append(args, "--no-project")
	}
	if spec.Python != "" {
		args = append(args, "--python", spec.Python)
	}
	if spec.Editable {
		args = append(args, "--with-editable", ".")
	}
	for _, r := range spec.Requirements {
		if r != "" {
			args = append(args, "--with-requirements", r)
		}
	}
	for _, w := range spec.With {
		if w != "" {
			args = append(args, "--with", w)
		}
	}
	args = append(args, spec.Command...)
	if spec.Output == OutputJUnitXML && junitFile != "" {
		args = append(args, "--junitxml="+junitFile)
	}
	args = append(args, spec.ExtraArgs...)
	args = append(args, spec.Selectors...)
	return args
}

// RunFormulaStructured runs a test formula through `uv run` and returns the
// structured result, parsed by the formula's output format. One executor for
// every python test runner — the formula data is what differs.
func RunFormulaStructured(ctx context.Context, sourceDir string, spec TestFormulaSpec) (*StructuredTestRun, error) {
	var junitFile string
	if spec.Output == OutputJUnitXML {
		junitDir := filepath.Join(sourceDir, ".cache")
		if err := os.MkdirAll(junitDir, 0o755); err != nil {
			junitDir = os.TempDir()
		}
		junitFile = filepath.Join(junitDir, fmt.Sprintf("formula-junit-%d.xml", time.Now().UnixNano()))
		defer os.Remove(junitFile)
	}

	args := BuildUvArgs(spec, junitFile)
	cmd := exec.CommandContext(ctx, "uv", args...)
	cmd.Dir = sourceDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid := cmd.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		time.AfterFunc(5*time.Second, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
		return nil
	}

	var raw bytes.Buffer
	cmd.Stdout = &raw
	cmd.Stderr = &raw
	cmd.Env = os.Environ()
	for _, ev := range spec.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", ev.Key, ev.Value))
	}

	runErr := cmd.Run()

	if spec.Output == OutputJUnitXML {
		xmlBytes, _ := os.ReadFile(junitFile) //nolint:gosec // path under sourceDir
		return ParsePytestJUnit(string(xmlBytes), scrapeCoverageFromOutput(raw.String())), runErr
	}
	return ParseUnittestText(raw.String()), runErr
}
