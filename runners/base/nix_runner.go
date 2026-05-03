package base

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/sandbox"
	"github.com/codefly-dev/core/wool"
)

// NixEnvironment runs processes inside a Nix development shell.
//
// Two modes:
//   - Wrapped (default before Init): each NewProcess wraps the command in
//     `nix develop <dir> --command <bin> <args...>`. Re-evaluates the flake
//     every call — expensive.
//   - Materialized (after Init): `nix print-dev-env --json <dir>` is run
//     once to capture the devShell's exported variables. Later NewProcess
//     calls exec `bin` directly with that env, skipping `nix develop`
//     entirely on the hot path. This is what makes Test calls fast once
//     the agent has been through Init.
//
// Binaries come from the flake.nix, so WithBinary is a no-op.
type NixEnvironment struct {
	dir       string
	flakePath string

	envs []*resources.EnvironmentVariable

	// materialized holds the devShell's exported env vars captured once
	// during Init via `nix print-dev-env --json`. When non-nil, NewProcess
	// runs binaries directly with this env instead of wrapping in
	// `nix develop --command`.
	materialized map[string]string

	// cacheDir is where the materialized env is persisted between agent
	// runs. When set (via WithCacheDir), Init first tries to load a
	// prior materialization keyed on flake.nix+flake.lock hash — skipping
	// `nix print-dev-env` entirely if nothing has changed. Debug: the
	// cached env is inspectable on disk.
	cacheDir string

	// sb is the OS-level confinement applied to every spawned cmd
	// (mirrors NativeEnvironment). nil means no sandboxing — the
	// legacy default while callers migrate. Nix-wrapped commands
	// still benefit from sandbox.Wrap because the wrap targets the
	// outermost cmd (`nix develop --command bash -c ...`); whatever
	// nix spawns inside is inside the sandbox.
	sb sandbox.Sandbox

	out io.Writer
	ctx context.Context
}

var _ RunnerEnvironment = &NixEnvironment{}

// NewNixEnvironment creates a new Nix runner.
// It verifies that nix is installed and that a flake.nix exists in dir.
func NewNixEnvironment(ctx context.Context, dir string) (*NixEnvironment, error) {
	w := wool.Get(ctx).In("NewNixEnvironment")

	if !CheckNixInstalled() {
		return nil, fmt.Errorf("nix is not installed (install with: %s)", NixInstallCommand())
	}

	flakePath := filepath.Join(dir, "flake.nix")
	if _, err := os.Stat(flakePath); err != nil {
		return nil, fmt.Errorf("no flake.nix found in %s: nix runtime requires a flake.nix", dir)
	}

	w.Info("using nix develop for reproducible environment", wool.DirField(dir))
	return &NixEnvironment{
		dir:       dir,
		flakePath: flakePath,
		out:       w,
	}, nil
}

// WithSandbox attaches a sandbox.Sandbox to this environment. See
// NativeEnvironment.WithSandbox for the contract. Same opt-in
// semantics; same migration path.
func (nix *NixEnvironment) WithSandbox(sb sandbox.Sandbox) *NixEnvironment {
	nix.sb = sb
	return nix
}

// WithCacheDir enables persistent materialization caching. Typically
// set to the agent's cacheLocation so the env survives agent restarts.
// Safe to call before or after Init — but before is the only useful
// order (the cache is consulted during Init).
func (nix *NixEnvironment) WithCacheDir(dir string) {
	nix.cacheDir = dir
}

func (nix *NixEnvironment) Init(ctx context.Context) error {
	nix.ctx = ctx

	// Fast path: reload a previously-materialized env from disk if the
	// flake hasn't changed. Skips `nix print-dev-env --json` entirely.
	if nix.cacheDir != "" {
		if loaded, err := nix.loadCachedMaterialization(ctx); err == nil && loaded != nil {
			nix.materialized = loaded
			wool.Get(ctx).In("NixEnvironment.Init").
				Info("reloaded materialized nix env from cache",
					wool.Field("vars", len(loaded)))
			return nil
		}
	}

	if err := nix.materialize(ctx); err != nil {
		// Materialization is an optimization; failure falls back to
		// wrapped `nix develop --command` on each call.
		wool.Get(ctx).In("NixEnvironment.Init").
			Warn("could not materialize nix dev env; falling back to nix-develop-per-call",
				wool.ErrField(err))
		return nil
	}

	// Best-effort persist. Next agent run will reuse the result without
	// re-evaluating the flake.
	if nix.cacheDir != "" {
		if err := nix.saveCachedMaterialization(ctx); err != nil {
			wool.Get(ctx).In("NixEnvironment.Init").
				Debug("could not persist materialized env (non-fatal)",
					wool.ErrField(err))
		}
	}
	return nil
}

// nixBuildOnlyVars enumerates env vars that `nix print-dev-env`
// emits referencing the evaluator's own build sandbox (TMPDIR,
// derivation paths, build-internal counters). They're nonsensical
// outside the eval and routinely break spawned children — Go's
// `creating work dir: no such file` is the most common symptom.
//
// Keep this list conservative; over-stripping just falls back to the
// host env which is what we want anyway.
var nixBuildOnlyVars = []string{
	"TMPDIR", "TMP", "TEMP", "TEMPDIR",
	"NIX_BUILD_TOP", "NIX_LOG_FD", "NIX_BUILD_CORES",
	"NIX_STORE_DIR", "NIX_STATE_DIR",
	"src", "out", "outputs", "name",
	"PWD", "OLDPWD",
}

// flakeFingerprint hashes flake.nix + flake.lock to produce a stable key
// for the cached materialization. If either file changes, the cache is
// invalidated. If flake.lock is missing, only flake.nix contributes —
// users without a committed lock still get caching across restarts, it
// just invalidates on first `nix flake lock` run.
func (nix *NixEnvironment) flakeFingerprint() (string, error) {
	h := sha256.New()
	for _, name := range []string{"flake.nix", "flake.lock"} {
		f, err := os.Open(filepath.Join(nix.dir, name))
		if err != nil {
			if os.IsNotExist(err) && name == "flake.lock" {
				continue // lock-less flake is valid, just less precise
			}
			return "", err
		}
		_, _ = io.Copy(h, f)
		_ = f.Close()
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// cachedEnvPayload is what we serialize to disk. Versioned so format
// migrations don't silently re-use incompatible caches.
type cachedEnvPayload struct {
	SchemaVersion int               `json:"schema_version"`
	Fingerprint   string            `json:"fingerprint"`
	Env           map[string]string `json:"env"`
}

const cachedEnvSchemaVersion = 1

func (nix *NixEnvironment) cacheFilePath() string {
	return filepath.Join(nix.cacheDir, "nix-devshell.json")
}

// loadCachedMaterialization returns the previously-materialized env iff
// it exists AND the flake fingerprint still matches. Any mismatch, read
// error, or JSON parse error returns (nil, nil) — we fall through to a
// fresh materialize rather than treating stale cache as fatal.
func (nix *NixEnvironment) loadCachedMaterialization(ctx context.Context) (map[string]string, error) {
	w := wool.Get(ctx).In("NixEnvironment.loadCache")

	data, err := os.ReadFile(nix.cacheFilePath())
	if err != nil {
		return nil, nil // miss, not an error
	}
	var payload cachedEnvPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		w.Trace("cache file exists but won't parse; ignoring", wool.ErrField(err))
		return nil, nil
	}
	if payload.SchemaVersion != cachedEnvSchemaVersion {
		w.Trace("cache schema mismatch; ignoring",
			wool.Field("found", payload.SchemaVersion),
			wool.Field("want", cachedEnvSchemaVersion))
		return nil, nil
	}
	current, err := nix.flakeFingerprint()
	if err != nil {
		return nil, err
	}
	if current != payload.Fingerprint {
		w.Trace("flake fingerprint changed; invalidating cache")
		return nil, nil
	}
	return payload.Env, nil
}

// saveCachedMaterialization serializes the current materialized env to
// disk with the current flake fingerprint. Writes atomically via a
// sibling .tmp file + rename — a crash mid-write leaves the previous
// cache intact.
func (nix *NixEnvironment) saveCachedMaterialization(ctx context.Context) error {
	if nix.materialized == nil {
		return nil
	}
	if err := os.MkdirAll(nix.cacheDir, 0o755); err != nil {
		return err
	}
	fp, err := nix.flakeFingerprint()
	if err != nil {
		return err
	}
	payload := cachedEnvPayload{
		SchemaVersion: cachedEnvSchemaVersion,
		Fingerprint:   fp,
		Env:           nix.materialized,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp := nix.cacheFilePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, nix.cacheFilePath())
}

// materialize runs `nix print-dev-env --json` once to capture the devShell's
// exported variables, so subsequent NewProcess calls can exec directly
// without paying `nix develop` evaluation cost.
//
// It also pins GOCACHE / GOMODCACHE to stable paths under the user's home
// when not already set by the flake, so repeat `go test`/`go build` calls
// reuse the compiler cache across invocations.
func (nix *NixEnvironment) materialize(ctx context.Context) error {
	w := wool.Get(ctx).In("NixEnvironment.materialize", wool.DirField(nix.dir))

	// #nosec G204
	cmd := exec.CommandContext(ctx,
		"nix", "--extra-experimental-features", "nix-command flakes",
		"print-dev-env", "--json", nix.dir)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("nix print-dev-env failed: %s: %w",
				strings.TrimSpace(string(exitErr.Stderr)), err)
		}
		return fmt.Errorf("nix print-dev-env failed: %w", err)
	}

	env, err := parseNixDevEnv(out)
	if err != nil {
		return fmt.Errorf("parse nix print-dev-env json: %w", err)
	}

	// Strip env vars that nix print-dev-env captured from its OWN
	// evaluation but are build-time-only — they reference temp dirs
	// and derivation outputs that are gone by the time we exec.
	// Go is the prime victim: a leaked TMPDIR=/private/tmp/nix-build-*
	// makes `go build` fail with "creating work dir: stat ...: no
	// such file or directory" because the build dir was cleaned up
	// after print-dev-env exited.
	for _, v := range nixBuildOnlyVars {
		delete(env, v)
	}

	home := os.Getenv("HOME")
	if home != "" {
		if _, ok := env["GOCACHE"]; !ok {
			env["GOCACHE"] = filepath.Join(home, ".cache", "codefly", "go-build")
		}
		if _, ok := env["GOMODCACHE"]; !ok {
			env["GOMODCACHE"] = filepath.Join(home, ".cache", "codefly", "go-mod")
		}
		if _, ok := env["HOME"]; !ok {
			env["HOME"] = home
		}
	}
	// Hand TMPDIR back to the host's value (or the OS default fallback)
	// so spawned processes have a writable scratch directory that
	// actually exists.
	if t := os.Getenv("TMPDIR"); t != "" {
		env["TMPDIR"] = t
	}

	nix.materialized = env
	w.Info("nix dev env materialized", wool.Field("vars", len(env)))
	return nil
}

// parseNixDevEnv parses `nix print-dev-env --json` output into a flat
// string map. Only `exported` and `var` scalar entries are kept —
// bash arrays, associative arrays, and functions are dropped because
// they cannot round-trip through `exec.Cmd.Env`.
func parseNixDevEnv(data []byte) (map[string]string, error) {
	var payload struct {
		Variables map[string]struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(payload.Variables))
	for k, v := range payload.Variables {
		switch v.Type {
		case "exported", "var":
			var s string
			if err := json.Unmarshal(v.Value, &s); err != nil {
				continue
			}
			out[k] = s
		}
	}
	return out, nil
}

func (nix *NixEnvironment) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	w := wool.Get(ctx).In("NixEnvironment.WithEnvironmentVariables")
	w.Trace("adding", wool.Field("envs", envs))
	nix.envs = append(nix.envs, envs...)
}

// WithBinary is a no-op for Nix -- all binaries come from the flake.
func (nix *NixEnvironment) WithBinary(_ string) error {
	return nil
}

func (nix *NixEnvironment) Stop(context.Context) error {
	return nil
}

func (nix *NixEnvironment) Shutdown(context.Context) error {
	return nil
}

// NewProcess creates a process that runs under the Nix devShell.
//
// If the devShell env has been materialized (see Init), the binary is
// executed directly with that env injected — no `nix develop` wrapper,
// no flake re-evaluation. Otherwise falls back to wrapping the command
// in `nix develop <dir> --command <bin> <args...>`.
func (nix *NixEnvironment) NewProcess(bin string, args ...string) (Proc, error) {
	var cmd []string
	if nix.materialized != nil {
		cmd = append([]string{bin}, args...)
	} else {
		cmd = append([]string{
			"nix", "--extra-experimental-features", "nix-command flakes",
			"develop", nix.dir, "--command", bin,
		}, args...)
	}
	return &NixProc{
		env:     nix,
		cmd:     cmd,
		output:  nix.out,
		stopped: make(chan interface{}),
	}, nil
}

// --- NixProc ---

// NixProc is a process that runs inside a Nix development shell.
type NixProc struct {
	env    *NixEnvironment
	output io.Writer
	cmd    []string
	exec   *exec.Cmd
	envs   []*resources.EnvironmentVariable

	stopped  chan interface{}
	stopOnce sync.Once

	// forwarderWG tracks stdout/stderr forwarding goroutines spawned in
	// start() so Run() can wait for them to drain before returning.
	// Without this, Run's caller could close proc.output while a forwarder
	// is still mid-Write, racing on the underlying writer state.
	forwarderWG sync.WaitGroup

	dir    string
	waitOn string

	// Pipe support for interactive/bidirectional communication.
	stdinReader  *io.PipeReader
	stdinWriter  *io.PipeWriter
	stdoutReader *io.PipeReader
	stdoutWriter *io.PipeWriter
}

func (proc *NixProc) WaitOn(bin string) {
	proc.waitOn = bin
}

func (proc *NixProc) WithDir(dir string) {
	proc.dir = dir
}

func (proc *NixProc) WithRunningCmd(_ string) {
}

func (proc *NixProc) WithOutput(output io.Writer) {
	proc.output = output
}

func (proc *NixProc) StdinPipe() (io.WriteCloser, error) {
	if proc.stdinWriter != nil {
		return nil, fmt.Errorf("StdinPipe already called")
	}
	proc.stdinReader, proc.stdinWriter = io.Pipe()
	return proc.stdinWriter, nil
}

func (proc *NixProc) StdoutPipe() (io.ReadCloser, error) {
	if proc.stdoutReader != nil {
		return nil, fmt.Errorf("StdoutPipe already called")
	}
	proc.stdoutReader, proc.stdoutWriter = io.Pipe()
	return proc.stdoutReader, nil
}

func (proc *NixProc) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	w := wool.Get(ctx).In("NixProc.WithEnvironmentVariables")
	w.Trace("adding", wool.Field("envs", envs))
	proc.envs = append(proc.envs, envs...)
}

func (proc *NixProc) WithEnvironmentVariablesAppend(_ context.Context, added *resources.EnvironmentVariable, sep string) {
	for _, env := range proc.envs {
		if env.Key == added.Key {
			env.Value = fmt.Sprintf("%v%s%v", env.Value, sep, added.Value)
			return
		}
	}
	proc.envs = append(proc.envs, added)
}

func (proc *NixProc) IsRunning(ctx context.Context) (bool, error) {
	w := wool.Get(ctx).In("NixProc.IsRunning")
	// Trust the explicit-stop signal over the PID probe. After Stop()
	// reaps the zombie via the cmd.Wait goroutine, the kernel can
	// reuse the original PID for an unrelated process; ps -p <pid>
	// would then return that process's row and IsRunning would
	// falsely report "running". Caught on Linux CI; macOS reuses PIDs
	// less aggressively but the race exists there too.
	select {
	case <-proc.stopped:
		return false, nil
	default:
	}
	if proc.exec == nil || proc.exec.Process == nil {
		return false, nil
	}
	pid := proc.exec.Process.Pid
	w.Trace("checking if process is running", wool.Field("pid", pid))
	// #nosec G204
	cmd := exec.Command("ps", "-p", fmt.Sprintf("%d", pid))
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(err.Error(), "exit") {
			return false, nil
		}
		return false, err
	}
	if strings.Contains(string(output), fmt.Sprintf("%d", pid)) &&
		!strings.Contains(string(output), "defunct") {
		return true, nil
	}
	return false, nil
}

// Wait blocks until the nix process exits or ctx is cancelled.
// Polls IsRunning at 1s intervals — Nix wraps the binary in `nix develop`,
// so we don't have direct access to the leaf process's cmd.Wait, and the
// existing forwarder goroutines already hold the cmd.Wait result.
func (proc *NixProc) Wait(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			running, err := proc.IsRunning(ctx)
			if err != nil {
				return err
			}
			if !running {
				return nil
			}
		}
	}
}

func (proc *NixProc) Run(ctx context.Context) error {
	w := wool.Get(ctx).In("NixProc.Run")
	w.Trace("running nix process", wool.Field("cmd", proc.cmd))
	if err := proc.start(ctx); err != nil {
		return err
	}
	// Wait for the stdout/stderr forwarders to finish draining before
	// returning — otherwise a concurrent caller closing proc.output
	// would race with an in-flight Write from a forwarder.
	defer proc.forwarderWG.Wait()
	done := make(chan error, 1)
	go func() {
		done <- proc.exec.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			var exitError *exec.ExitError
			if errors.As(err, &exitError) {
				if strings.Contains(exitError.String(), "signal: terminated") {
					return nil
				}
				return exitError
			} else if strings.Contains(err.Error(), "signal: terminated") {
				return nil
			}
			return w.Wrapf(err, "nix process failed")
		}
	case <-proc.stopped:
		w.Trace("nix process was killed")
	case <-ctx.Done():
		w.Trace("context cancelled, stopping nix process")
		_ = proc.Stop(ctx)
		return ctx.Err()
	}
	return nil
}

func (proc *NixProc) Start(ctx context.Context) error {
	w := wool.Get(ctx).In("NixProc.Start")
	w.Trace("starting nix process", wool.Field("cmd", proc.cmd))
	return proc.start(ctx)
}

func (proc *NixProc) start(ctx context.Context) error {
	w := wool.Get(ctx).In("NixProc.start", wool.DirField(proc.env.dir))
	// #nosec G204
	cmd := exec.CommandContext(ctx, proc.cmd[0], proc.cmd[1:]...)

	// Sandbox wrap (parallel to NativeProc.start). For the wrapped
	// path (cmd = `nix develop --command <real>`), the sandbox is
	// applied to nix-develop itself; the inner command inherits the
	// confinement transparently. For the materialized path (cmd =
	// `<real>` directly), it's identical to native semantics.
	if proc.env.sb != nil {
		if err := proc.env.sb.Wrap(cmd); err != nil {
			return w.Wrapf(err, "sandbox wrap")
		}
	}

	cmd.Dir = proc.env.dir
	if proc.dir != "" {
		cmd.Dir = proc.dir
	}
	// Own process group + Go 1.20+ ctx-cancel semantics — mirrors NativeProc.
	// Without Setpgid, Stop()/ctx-cancel only signalled the nix-develop
	// wrapper; any test workers it spawned leaked to PID 1. Now the whole
	// subtree gets SIGTERM via negative-PID broadcast, and WaitDelay handles
	// the SIGKILL fallback + leaked-pipe cleanup the runtime provides.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pgid := cmd.Process.Pid
		return syscall.Kill(-pgid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second
	// Materialized devShell env comes first so that codefly-supplied vars
	// (env.envs, proc.envs) override Nix defaults when keys collide.
	if proc.env.materialized != nil {
		for k, v := range proc.env.materialized {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, resources.EnvironmentVariableAsStrings(proc.env.envs)...)
	cmd.Env = append(cmd.Env, resources.EnvironmentVariableAsStrings(proc.envs)...)

	// Wire stdin pipe if requested
	if proc.stdinReader != nil {
		cmd.Stdin = proc.stdinReader
	}

	// Serialize stdout+stderr forwarders' writes onto proc.output.
	// User-supplied writers (bytes.Buffer, *Wool) aren't safe for
	// concurrent Write; both forwarders here share proc.output.
	// Same Linux-CI flake fix as NativeProc — see lockedWriter doc
	// in runners/base/native_runner.go for the full story.
	if proc.output != nil {
		if _, alreadyLocked := proc.output.(*lockedWriter); !alreadyLocked {
			proc.output = &lockedWriter{w: proc.output}
		}
	}

	// Wire stdout: raw pipe or forwarded through output
	if proc.stdoutWriter != nil {
		cmd.Stdout = proc.stdoutWriter
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return err
		}
		if err = cmd.Start(); err != nil {
			return w.Wrapf(err, "cannot start nix process")
		}
		proc.exec = cmd
		proc.forwarderWG.Add(1)
		go func() {
			defer proc.forwarderWG.Done()
			defer stderr.Close()
			proc.forward(stderr)
		}()
		// Track the stdout-close goroutine too — previously it outlived
		// Run() when ctx cancelled before cmd.Wait returned, racing against
		// any caller closing proc.output.
		proc.forwarderWG.Add(1)
		go func() {
			defer proc.forwarderWG.Done()
			_ = cmd.Wait()
			proc.stdoutWriter.Close()
		}()
	} else {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return err
		}
		if err = cmd.Start(); err != nil {
			return w.Wrapf(err, "cannot start nix process")
		}
		proc.exec = cmd
		proc.forwarderWG.Add(2)
		go func() {
			defer proc.forwarderWG.Done()
			defer stdout.Close()
			proc.forward(stdout)
		}()
		go func() {
			defer proc.forwarderWG.Done()
			defer stderr.Close()
			proc.forward(stderr)
		}()
	}

	// Persist pgid so the orphan-reaping sweep covers nix spawns too.
	// Previously only NativeProc participated; a CLI SIGKILL mid-nix-run
	// would leave test workers orphaned at PID 1.
	if perr := writePgidFile(cmd.Process.Pid, cmd.Dir, proc.cmd); perr != nil {
		w.Warn("could not persist pgid file", wool.Field("err", perr))
	}

	w.Trace("nix process started")
	return nil
}

// forward streams r → proc.output one line at a time, preserving newlines.
// See forwardLines in pgid.go for the rationale. Shared across native/nix
// so both backends have identical output semantics.
func (proc *NixProc) forward(r io.Reader) {
	forwardLines(r, proc.output)
}

func (proc *NixProc) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("NixProc.Stop")
	w.Trace("stopping nix process")

	if proc.exec == nil || proc.exec.Process == nil {
		w.Trace("nix process not started, nothing to stop")
		return nil
	}

	pgid := proc.exec.Process.Pid
	w.Trace("sending SIGTERM to process group", wool.Field("pgid", pgid))
	// Tree-kill via negative PID — previously Signal() only reached the
	// nix-develop wrapper, leaking any test workers it had spawned.
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	// Poll for exit every 100ms up to a 5s SIGTERM grace, honoring ctx.
	const sigtermGrace = 5 * time.Second
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.Now().Add(sigtermGrace)
	exited := false
waitLoop:
	for {
		select {
		case <-ctx.Done():
			// Caller gave up; still ensure we don't leave the process
			// running — fall through to the force-kill path.
			break waitLoop
		case <-ticker.C:
			if err := syscall.Kill(-pgid, syscall.Signal(0)); err != nil {
				exited = true
				break waitLoop
			}
			if time.Now().After(deadline) {
				break waitLoop
			}
		}
	}

	if !exited {
		w.Trace("nix pgroup still alive after SIGTERM grace, sending SIGKILL", wool.Field("pgid", pgid))
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		w.Trace("nix pgroup exited after SIGTERM")
	}

	// Remove the pgid tracking file now that the group is confirmed dead.
	if perr := removePgidFile(pgid); perr != nil {
		w.Trace("could not remove pgid file", wool.Field("err", perr))
	}

	// close-once to avoid the previous chan-send goroutine leak: if Run
	// already exited via the `done` path, nobody was reading `stopped`
	// and the goroutine blocked forever.
	proc.stopOnce.Do(func() { close(proc.stopped) })
	return nil
}
