package python

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	// NoBuildIsolation maps to `uv run --no-build-isolation`: source builds
	// (editable installs of C-extension projects) see the run environment's
	// packages instead of an isolated build env — pair it with With entries
	// carrying the build requirements (numpy, cython, …).
	NoBuildIsolation bool
	// EditableTarget is the path uv installs editable (--with-editable):
	// the executor stamps the ABSOLUTE project root so a Cwd-moved run
	// (django tests/) still installs the project, not the run dir. Empty
	// means "." (historical behavior for pure-argv callers).
	EditableTarget string
	// Cwd is the working directory for the test command, RELATIVE to the code
	// unit's source dir (django's "tests" for runtests.py). Provisioning key
	// "cwd". It is NOT a uv flag — RunFormulaStructured sets cmd.Dir — so
	// BuildUvArgs ignores it. Absolute paths and ".." escapes are rejected at
	// run time (classified as an env error, never silently ignored).
	Cwd string

	// ExtraArgs are appended to the command verbatim (power-user passthrough).
	ExtraArgs []string
	// Env are extra environment variables for the run.
	Env []*resources.EnvironmentVariable

	// PersistentVenv opts into building the editable project + its deps ONCE
	// into a persistent per-workspace venv, then running tests against that venv
	// WITHOUT `--with-editable` — so a C-extension project (numpy/scipy/cython)
	// is compiled a single time instead of on every `uv run` (the reason
	// scikit-class repos, where the agent already produces the gold patch, died
	// env-blocked or timed out). Python edits are still reflected (editable
	// install); only C sources would need a rebuild, which SWE-bench fixes rarely
	// touch. OFF by default: pure-python projects (django) keep the simple
	// `uv run --with-editable` path untouched. Ignored unless Editable is set.
	PersistentVenv bool
	// venvPython, when non-empty, is the interpreter of an already-provisioned
	// persistent venv; BuildUvArgs then runs against it and skips the
	// editable/with/requirements flags (already installed). Set internally by
	// RunFormulaStructured, never by callers.
	venvPython string
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
		Command:          withDjangoKeepDB(tokenizeCommand(command)),
		Output:           output,
		Selectors:        append([]string{}, selectors...),
		NoProject:        provisioning["no_project"] == "true",
		Python:           provisioning["python"],
		Editable:         provisioning["editable"] == "true",
		Requirements:     splitComma(provisioning["requirements"]),
		With:             splitComma(provisioning["with"]),
		NoBuildIsolation: provisioning["no_build_isolation"] == "true",
		Cwd:              strings.TrimSpace(provisioning["cwd"]),
		// Build the editable project ONCE into a persistent venv for
		// C-extension projects (the no_build_isolation case: numpy/scipy/cython
		// build deps present). Pure-Python projects (django) keep the simple
		// per-run `uv run --with-editable` path. An explicit provisioning key can
		// force it on/off.
		PersistentVenv: provisioning["no_build_isolation"] == "true" || provisioning["persistent_venv"] == "true",
	}
	if provisioning["persistent_venv"] == "false" {
		spec.PersistentVenv = false
	}
	for k, v := range env {
		spec.Env = append(spec.Env, &resources.EnvironmentVariable{Key: k, Value: v})
	}
	return spec
}

// splitComma splits a provisioning list value. Commas are the canonical
// separator, but healer LLMs routinely write space-separated requirement
// lists ("numpy>=1.14 scipy>=0.19"), and uv then fails parsing the whole
// string as ONE spec ("Trailing ... is not allowed") — so whitespace between
// specs separates too. Version constraints never contain spaces, making the
// split unambiguous for requirement specs and file paths alike.
func splitComma(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var parts []string
	for _, chunk := range strings.Split(s, ",") {
		for _, p := range strings.Fields(chunk) {
			if p != "" {
				parts = append(parts, p)
			}
		}
	}
	return parts
}

// BuildUvArgs renders a TestFormulaSpec into the argv for `uv` (excluding the
// leading "uv"). Pure and deterministic so the data→command translation is
// unit-tested without executing anything. junitFile is the path the plugin
// allocated for junit-xml output ("" for non-junit formats).
func BuildUvArgs(spec TestFormulaSpec, junitFile string) []string {
	// Persistent-venv path: the project + deps are ALREADY installed in the
	// venv, so run against it with NO --with-editable / --with / --with-
	// requirements (which would re-resolve and rebuild). This is what makes a
	// C-extension project compile once instead of every run.
	if spec.venvPython != "" {
		args := []string{"run", "--python", spec.venvPython, "--no-project"}
		args = append(args, spec.Command...)
		if spec.Output == OutputJUnitXML && junitFile != "" {
			args = append(args, "--junitxml="+junitFile)
		}
		args = append(args, spec.ExtraArgs...)
		args = append(args, selectorsForCommand(spec.Command, spec.Cwd, spec.Selectors)...)
		return args
	}
	args := []string{"run"}
	if spec.NoProject {
		args = append(args, "--no-project")
	}
	if spec.Python != "" {
		args = append(args, "--python", spec.Python)
	}
	if spec.Editable {
		// The editable target is the PROJECT ROOT, not the run directory:
		// with Cwd set (django runs from tests/), a bare "." would point uv
		// at a dir with no setup.py/pyproject and fail to resolve. The
		// executor stamps EditableTarget with the absolute source dir; ""
		// keeps the historical "." for pure-argv callers.
		target := spec.EditableTarget
		if target == "" {
			target = "."
		}
		args = append(args, "--with-editable", target)
	}
	if spec.NoBuildIsolation {
		args = append(args, "--no-build-isolation")
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
	args = append(args, selectorsForCommand(spec.Command, spec.Cwd, spec.Selectors)...)
	return args
}

// selectorsForCommand renders test selectors in the form the RUNNER expects.
// pytest takes file paths / node-ids verbatim. django's runtests.py takes
// DOTTED LABELS relative to its run dir (tests/) — a workspace file path like
// "tests/admin_docs/test_utils.py" is NOT a valid label, so django ignores it
// and runs the ENTIRE suite (8-12 min). That was the real reason every django
// test.run ran the full suite despite a target: the selector was unusable.
// Translate cwd-relative .py paths → dotted module labels for runtests.py.
func selectorsForCommand(command []string, cwd string, selectors []string) []string {
	if len(selectors) == 0 || !commandIsDjangoRuntests(command) {
		return selectors
	}
	root := djangoTestRoot(command, cwd)
	out := make([]string, 0, len(selectors))
	for _, s := range selectors {
		out = append(out, djangoTestLabel(s, root))
	}
	return out
}

func commandIsDjangoRuntests(command []string) bool {
	for _, a := range command {
		if strings.Contains(a, "runtests.py") {
			return true
		}
	}
	return false
}

// djangoTestRoot is the directory django's test labels are relative to — the
// directory CONTAINING runtests.py. It works for both invocation shapes:
//
//	python tests/runtests.py   (run from repo root)  → "tests"
//	python runtests.py + cwd=tests (run from tests/) → "tests" (from cwd)
//
// django's runtests.py inserts its own directory on sys.path, so labels are
// ALWAYS relative to that dir regardless of where the process is launched.
func djangoTestRoot(command []string, cwd string) string {
	// Scan WORD TOKENS, not raw args: the command may arrive tokenized
	// (["python","runtests.py"]) OR as a single healed string
	// ("python runtests.py", even "cd tests && python runtests.py"). Match only
	// a clean runtests.py path token — matching the substring inside "python
	// runtests.py" once yielded "python" as the root and mangled every label.
	for _, a := range command {
		for _, tok := range strings.Fields(a) {
			if tok == "runtests.py" {
				// bare runtests.py: labels are relative to its dir. Use cwd when
				// known; otherwise fall back to django's conventional tests/ (a
				// bare runtests.py runs from there), NOT the repo root — an empty
				// root leaves the "tests/" prefix on and django runs the whole
				// suite.
				if root := strings.Trim(cwd, "/"); root != "" {
					return root
				}
				return "tests"
			}
			if strings.HasSuffix(tok, "/runtests.py") {
				return strings.Trim(strings.TrimSuffix(tok, "/runtests.py"), "/")
			}
		}
	}
	return strings.Trim(cwd, "/")
}

// tokenizeCommand splits a single-element command that is really a whitespace-
// separated argv ("python runtests.py") into tokens, so `uv run` receives a
// program + args instead of trying to spawn a file literally named "python
// runtests.py". A healer's `configure test.command=python runtests.py` stores
// the whole string as one element; derived commands arrive already tokenized.
// Multi-element commands and shell strings (containing operators) pass through.
func tokenizeCommand(command []string) []string {
	if len(command) != 1 {
		return append([]string{}, command...)
	}
	only := command[0]
	if !strings.ContainsAny(only, " \t") || strings.ContainsAny(only, "&|;<>") {
		return append([]string{}, command...) // atomic program or a shell string
	}
	return strings.Fields(only)
}

// djangoTestLabel converts a workspace-relative test path into the dotted
// module label runtests.py expects (relative to root — the runtests.py dir).
// Already-dotted labels (not a .py path) pass through untouched.
func djangoTestLabel(sel, root string) string {
	if !strings.HasSuffix(sel, ".py") {
		return sel // already a label, e.g. admin_docs.test_utils.TestCase.test_x
	}
	p := strings.TrimSuffix(sel, ".py")
	if root != "" {
		p = strings.TrimPrefix(p, root+"/")
	}
	return strings.ReplaceAll(strings.TrimPrefix(p, "/"), "/", ".")
}

// materializeWatcher accumulates a probe run's output and fires onMaterialize
// once the runner has demonstrably LAUNCHED (EnvironmentMaterialized) so the
// caller can cancel it early. os/exec serializes writes when Stdout==Stderr, so
// the guard needs no lock, but keep one for safety. Fires at most once.
type materializeWatcher struct {
	mu            sync.Mutex
	buf           *bytes.Buffer
	fired         bool
	onMaterialize func()
}

func (w *materializeWatcher) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	fire := !w.fired && EnvironmentMaterialized(w.buf.String())
	if fire {
		w.fired = true
	}
	w.mu.Unlock()
	if fire && w.onMaterialize != nil {
		w.onMaterialize()
	}
	return n, err
}

// RunFormulaStructured runs a test formula through `uv run` and returns the
// structured result, parsed by the formula's output format. One executor for
// every python test runner — the formula data is what differs.
func RunFormulaStructured(ctx context.Context, sourceDir string, spec TestFormulaSpec) (*StructuredTestRun, error) {
	// The formula's cwd (provisioning "cwd") moves the RUN directory, not the
	// uv args — django's `python runtests.py` only works from tests/. An
	// invalid cwd is an ENVIRONMENT error the healer can act on, never a
	// silent fallback to sourceDir (which would run the wrong thing and — pre
	// zero-case classification — read as a green run).
	runDir, cwdErr := resolveFormulaRunDir(sourceDir, spec.Cwd)
	if cwdErr != nil {
		return &StructuredTestRun{
			RawOutput: cwdErr.Error(),
			EnvError:  &RunEnvError{Reason: EnvErrorInvalidCwd, Detail: cwdErr.Error()},
		}, nil
	}
	// Editable installs must target the project root even when Cwd moves the
	// run directory — stamp the absolute source dir (see EditableTarget doc).
	if spec.Editable && spec.EditableTarget == "" {
		if abs, err := filepath.Abs(sourceDir); err == nil {
			spec.EditableTarget = abs
		}
	}

	// Persistent venv: build the editable project + deps ONCE, then run against
	// the venv (no per-run rebuild). Failure to provision is an ENV error the
	// healer can act on. Only the opt-in C-extension path takes this branch; the
	// default `uv run --with-editable` path below is untouched.
	if spec.PersistentVenv && spec.Editable {
		venvPython, venvErr := ensurePersistentVenv(ctx, sourceDir, spec)
		if venvErr != nil {
			detail := venvErr.Error()
			return &StructuredTestRun{
				RawOutput: detail,
				EnvError:  &RunEnvError{Reason: EnvErrorProvisioningFailed, Detail: detail},
			}, nil
		}
		spec.venvPython = venvPython
	}

	var junitFile string
	if spec.Output == OutputJUnitXML {
		junitDir := filepath.Join(sourceDir, ".cache")
		if err := os.MkdirAll(junitDir, 0o755); err != nil {
			junitDir = os.TempDir()
		}
		junitFile = filepath.Join(junitDir, fmt.Sprintf("formula-junit-%d.xml", time.Now().UnixNano()))
		// cmd.Dir may differ from the caller's cwd (spec.Cwd) — make the junit
		// path absolute so pytest writes it where we read it.
		if abs, err := filepath.Abs(junitFile); err == nil {
			junitFile = abs
		}
		defer os.Remove(junitFile)
	}

	args := BuildUvArgs(spec, junitFile)

	// PROBE MODE: a health/pre-warm probe runs the default command with NO
	// selectors purely to prove the environment MATERIALIZES (uv resolves, the
	// project imports, the runner launches). Stream the output and cancel the
	// INSTANT the runner launches instead of waiting out a full multi-thousand-
	// test suite — turning a ~15-min django pre-warm into ~1-2 min. Real runs
	// (agent test.run, grader) always carry selectors, so they run to
	// completion and never early-stop.
	probe := len(spec.Selectors) == 0
	runCtx := ctx
	var probeCancel context.CancelFunc
	if probe {
		runCtx, probeCancel = context.WithCancel(ctx)
		defer probeCancel()
	}
	cmd := exec.CommandContext(runCtx, "uv", args...)
	cmd.Dir = runDir
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
	var materializedEarly bool
	if probe {
		w := &materializeWatcher{buf: &raw, onMaterialize: func() {
			materializedEarly = true
			probeCancel()
		}}
		cmd.Stdout = w
		cmd.Stderr = w
	} else {
		cmd.Stdout = &raw
		cmd.Stderr = &raw
	}
	cmd.Env = os.Environ()
	if probe {
		// Force unbuffered child output so the materialization marker reaches the
		// watcher immediately — python BLOCK-buffers stdout to a pipe, which
		// would hide "Creating test database" until flush and defeat early-stop.
		cmd.Env = append(cmd.Env, "PYTHONUNBUFFERED=1")
	}
	for _, ev := range spec.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", ev.Key, ev.Value))
	}

	runErr := cmd.Run()
	rawStr := raw.String()
	// Early-stop cancellation IS a healthy materialization, not a real failure.
	if materializedEarly {
		runErr = nil
	}

	var run *StructuredTestRun
	if spec.Output == OutputJUnitXML {
		xmlBytes, _ := os.ReadFile(junitFile) //nolint:gosec // path under sourceDir
		run = ParsePytestJUnit(string(xmlBytes), scrapeCoverageFromOutput(rawStr))
	} else {
		run = ParseUnittestText(rawStr)
	}
	run.RawOutput = rawStr

	// Classify the run from its STRUCTURE, not the raw exit code:
	//   - cases produced            → the tests EXECUTED; a non-zero exit is just
	//     "tests failed" (already encoded as FAILED via per-case state). The exit
	//     error is EXPECTED, not fatal — do not propagate it.
	//   - zero cases + non-zero exit → the ENVIRONMENT blocked the run (dep
	//     install / import / interpreter). Mark it so ToProtoResponse reports
	//     ERRORED with a classified reason — the signal the Mind tooling inner
	//     loop heals from.
	//   - zero cases + exit 0       → still NOT a clean run (see the no-tests
	//     classification below).
	// MATERIALIZED-BUT-INTERRUPTED: our own budget (ctx deadline/cancel) killed a
	// run that had already launched the test runner (env resolved, project
	// imported, execution started) before any case completed. This is NOT an env
	// block — it is proof the environment is usable, which is exactly what a
	// pre-warm/health probe needs. Detect it FIRST so it never gets laundered
	// into a "no-tests-executed"/"unknown" block (django's 7757-test suite,
	// killed mid-setup at "Creating test database", was mis-blocked this way).
	if runErr != nil && run.caseCount() == 0 && ctx.Err() != nil && EnvironmentMaterialized(rawStr) {
		run.Materialized = true
	}
	// Streaming early-stop (probe mode): we cancelled the run the instant the
	// runner materialized. That is the healthy signal — mark it directly rather
	// than relying on the budget-cut path above.
	if materializedEarly {
		run.Materialized = true
	}
	if !run.Materialized && runErr != nil && run.caseCount() == 0 {
		run.EnvError = ClassifyEnvError(rawStr, runErr)
	}
	// Even when SOME cases ran (caseCount > 0), a dependency incompatibility can
	// repeat across many of them (a mis-resolved package surfacing at fixture
	// setup) while the unaffected tests pass — an env block that the zero-collected
	// check above misses. Detect the shared error so it heals instead of reading as
	// an ordinary test failure.
	if run.EnvError == nil && !run.Materialized {
		if ev := detectSharedEnvFailure(rawStr); ev != nil {
			run.EnvError = ev
		}
	}
	// ZERO CASES EXECUTED IS NEVER A CLEAN RUN — regardless of exit code. A
	// test command that discovers nothing (a healed `sh -c python`, a wrong
	// cwd, an empty selection) is a broken invocation, and letting exit 0 read
	// as "pass" made a Reproduce objective structurally unwinnable: three
	// layers called a bare `python` healthy. Classify distinctly:
	//   - selectors were supplied → "no-tests-matched-selectors" (the command
	//     may be fine; the selection matched nothing) so healers/callers can
	//     tell it apart from a broken command,
	//   - otherwise → "no-tests-executed" (the invocation itself is wrong).
	if run.EnvError == nil && !run.Materialized && run.caseCount() == 0 {
		invocation := "uv " + strings.Join(args, " ")
		if len(spec.Selectors) > 0 {
			run.EnvError = &RunEnvError{
				Reason: EnvErrorNoTestsMatchedSelectors,
				Detail: fmt.Sprintf("selectors %v matched zero tests (%s%s) — the selectors do not name any collectible test", spec.Selectors, invocation, exitSuffix(runErr)),
			}
		} else {
			run.EnvError = &RunEnvError{
				Reason: EnvErrorNoTestsExecuted,
				Detail: fmt.Sprintf("test command executed zero tests (%s%s) — a command that discovers nothing is a broken invocation, not a passing run; fix the test command, output format, or cwd", invocation, exitSuffix(runErr)),
			}
		}
	}
	return run, nil
}

// exitSuffix renders the process exit for a zero-case diagnostic: "exit 0" is
// the treacherous case (the command ran "successfully" while testing nothing),
// so it is always named explicitly.
func exitSuffix(runErr error) string {
	if runErr == nil {
		return "; exit status 0"
	}
	return "; " + runErr.Error()
}

// resolveFormulaRunDir resolves the formula's relative cwd against sourceDir.
// Rejects absolute paths and ".." escapes (the formula must stay inside the
// code unit) and requires the directory to exist.
func resolveFormulaRunDir(sourceDir, cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || cwd == "." {
		return sourceDir, nil
	}
	if filepath.IsAbs(cwd) {
		return "", fmt.Errorf("formula cwd %q must be relative to the code unit source dir", cwd)
	}
	clean := filepath.Clean(filepath.FromSlash(cwd))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("formula cwd %q escapes the code unit source dir", cwd)
	}
	dir := filepath.Join(sourceDir, clean)
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("formula cwd %q does not exist under %s: %v", cwd, sourceDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("formula cwd %q is not a directory under %s", cwd, sourceDir)
	}
	return dir, nil
}
