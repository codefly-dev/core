package dap

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	runners "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/wool"
)

// companionClient implements Client by running a debug adapter inside a
// companion environment (Docker, Nix, or local) and talking to it over
// TCP (DAP protocol).
//
// It uses the golden wrapper (CompanionRunner) so it doesn't care which
// backend is underneath.
type companionClient struct {
	cfg    *LanguageConfig
	runner runners.CompanionRunner
	proc   runners.Proc
	tp     *transport

	rootDir  string // absolute host path to source
	hostPort int

	// Ensure Close is safe to call multiple times.
	closeOnce sync.Once
	closeErr  error
}

// newCompanionClient starts a companion, launches the DAP server, connects
// over TCP, and initializes the DAP session.
// Ports are picked dynamically -- never hardcoded.
func newCompanionClient(ctx context.Context, cfg *LanguageConfig, sourceDir string) (*companionClient, error) {
	w := wool.Get(ctx).In("dap.newCompanionClient")

	absSource, err := filepath.Abs(sourceDir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot resolve source directory")
	}

	// Pick a free host port dynamically -- never hardcode.
	hostPort, err := runners.FindFreePort()
	if err != nil {
		return nil, w.Wrapf(err, "cannot find free port")
	}
	// Inside the container we also use a dynamic port. For Docker this is
	// the port the adapter listens on; for local/nix the host port is used directly.
	containerPort := hostPort

	// Get the companion image (may be nil for local/nix backends).
	img, err := cfg.CompanionImage(ctx)
	if err != nil {
		w.Warn("companion image not available, falling back", wool.ErrField(err))
	}

	name := fmt.Sprintf("dap-%s-%d", cfg.LanguageID, time.Now().UnixMilli())

	// Create the companion runner via the golden wrapper.
	// It picks the best available backend (Docker > Nix > Local).
	runner, err := runners.NewCompanionRunner(ctx, runners.CompanionOpts{
		Name:      name,
		SourceDir: absSource,
		Image:     img,
	})
	if err != nil {
		return nil, w.Wrapf(err, "cannot create companion runner")
	}

	runner.WithMount(absSource, "/workspace")
	runner.WithWorkDir("/workspace")
	runner.WithPause()
	runner.WithPortMapping(ctx, uint16(hostPort), uint16(containerPort))

	// Language-specific runner setup (e.g. mount Go module cache).
	if cfg.SetupRunner != nil {
		cfg.SetupRunner(ctx, runner, absSource)
	}

	err = runner.Init(ctx)
	if err != nil {
		runner.Shutdown(ctx)
		return nil, w.Wrapf(err, "cannot init companion environment")
	}

	// Build DAP args with the container port (where the adapter listens).
	args := cfg.DAPListenArgs(containerPort)

	proc, err := runner.NewProcess(cfg.DAPBinary, args...)
	if err != nil {
		runner.Shutdown(ctx)
		return nil, w.Wrapf(err, "cannot create DAP process")
	}

	err = proc.Start(ctx)
	if err != nil {
		runner.Shutdown(ctx)
		return nil, w.Wrapf(err, "cannot start DAP server")
	}

	w.Info("DAP companion started",
		wool.Field("lang", cfg.LanguageID),
		wool.Field("binary", cfg.DAPBinary),
		wool.Field("hostPort", hostPort),
		wool.Field("containerPort", containerPort))

	// Connect from the host to the host port (Docker forwards to container).
	conn, err := waitForConnection(ctx, hostPort)
	if err != nil {
		runner.Shutdown(ctx)
		return nil, w.Wrapf(err, "cannot connect to DAP server")
	}

	c := &companionClient{
		cfg:      cfg,
		runner:   runner,
		proc:     proc,
		tp:       newTransport(conn),
		rootDir:  absSource,
		hostPort: hostPort,
	}

	if err := c.initialize(ctx); err != nil {
		c.Close(ctx)
		return nil, w.Wrapf(err, "DAP initialize failed")
	}

	return c, nil
}

// initialize sends the DAP initialize handshake.
// Note: configurationDone is sent after launch/attach, per DAP spec.
func (c *companionClient) initialize(ctx context.Context) error {
	initArgs := map[string]interface{}{
		"clientID":                     "codefly",
		"clientName":                   "codefly-dap",
		"adapterID":                    c.cfg.LanguageID,
		"pathFormat":                   "path",
		"linesStartAt1":               true,
		"columnsStartAt1":             true,
		"supportsRunInTerminalRequest": false,
	}

	_, err := c.tp.request(ctx, "initialize", initArgs)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	return nil
}

// configurationDone tells the adapter that configuration is complete.
// Must be called after launch or attach per the DAP spec.
func (c *companionClient) configurationDone(ctx context.Context) error {
	_, err := c.tp.request(ctx, "configurationDone", nil)
	return err
}

func (c *companionClient) Launch(ctx context.Context, program string, args []string, env map[string]string) error {
	launchArgs := map[string]interface{}{
		"program":     program,
		"args":        args,
		"env":         env,
		"cwd":         "/workspace",
		"stopOnEntry": false,
	}
	_, err := c.tp.request(ctx, "launch", launchArgs)
	if err != nil {
		return err
	}
	return c.configurationDone(ctx)
}

func (c *companionClient) Attach(ctx context.Context, pid int) error {
	attachArgs := map[string]interface{}{
		"processId": pid,
	}
	_, err := c.tp.request(ctx, "attach", attachArgs)
	if err != nil {
		return err
	}
	return c.configurationDone(ctx)
}

func (c *companionClient) SetBreakpoints(ctx context.Context, file string, lines []int) ([]BreakpointResult, error) {
	bps := make([]map[string]interface{}, len(lines))
	for i, line := range lines {
		bps[i] = map[string]interface{}{"line": line}
	}
	args := map[string]interface{}{
		"source":      map[string]interface{}{"path": file},
		"breakpoints": bps,
	}
	resp, err := c.tp.request(ctx, "setBreakpoints", args)
	if err != nil {
		return nil, err
	}

	var body struct {
		Breakpoints []struct {
			ID       int    `json:"id"`
			Verified bool   `json:"verified"`
			Line     int    `json:"line"`
			Message  string `json:"message"`
		} `json:"breakpoints"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return nil, err
	}

	results := make([]BreakpointResult, len(body.Breakpoints))
	for i, bp := range body.Breakpoints {
		results[i] = BreakpointResult{
			ID: bp.ID, Verified: bp.Verified, File: file, Line: bp.Line, Message: bp.Message,
		}
	}
	return results, nil
}

func (c *companionClient) SetFunctionBreakpoints(ctx context.Context, names []string) ([]BreakpointResult, error) {
	bps := make([]map[string]interface{}, len(names))
	for i, name := range names {
		bps[i] = map[string]interface{}{"name": name}
	}
	args := map[string]interface{}{
		"breakpoints": bps,
	}
	resp, err := c.tp.request(ctx, "setFunctionBreakpoints", args)
	if err != nil {
		return nil, err
	}

	var body struct {
		Breakpoints []struct {
			ID       int    `json:"id"`
			Verified bool   `json:"verified"`
			Line     int    `json:"line"`
			Message  string `json:"message"`
		} `json:"breakpoints"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return nil, err
	}

	results := make([]BreakpointResult, len(body.Breakpoints))
	for i, bp := range body.Breakpoints {
		results[i] = BreakpointResult{
			ID: bp.ID, Verified: bp.Verified, Line: bp.Line, Message: bp.Message,
		}
	}
	return results, nil
}

func (c *companionClient) Continue(ctx context.Context, threadID int) error {
	_, err := c.tp.request(ctx, "continue", map[string]interface{}{"threadId": threadID})
	return err
}

func (c *companionClient) Next(ctx context.Context, threadID int) error {
	_, err := c.tp.request(ctx, "next", map[string]interface{}{"threadId": threadID})
	return err
}

func (c *companionClient) StepIn(ctx context.Context, threadID int) error {
	_, err := c.tp.request(ctx, "stepIn", map[string]interface{}{"threadId": threadID})
	return err
}

func (c *companionClient) StepOut(ctx context.Context, threadID int) error {
	_, err := c.tp.request(ctx, "stepOut", map[string]interface{}{"threadId": threadID})
	return err
}

func (c *companionClient) Pause(ctx context.Context, threadID int) error {
	_, err := c.tp.request(ctx, "pause", map[string]interface{}{"threadId": threadID})
	return err
}

func (c *companionClient) Threads(ctx context.Context) ([]ThreadInfo, error) {
	resp, err := c.tp.request(ctx, "threads", nil)
	if err != nil {
		return nil, err
	}
	var body struct {
		Threads []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"threads"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return nil, err
	}
	threads := make([]ThreadInfo, len(body.Threads))
	for i, t := range body.Threads {
		threads[i] = ThreadInfo{ID: t.ID, Name: t.Name}
	}
	return threads, nil
}

func (c *companionClient) StackTrace(ctx context.Context, threadID int) ([]StackFrame, error) {
	resp, err := c.tp.request(ctx, "stackTrace", map[string]interface{}{"threadId": threadID})
	if err != nil {
		return nil, err
	}
	var body struct {
		StackFrames []struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			Source struct {
				Path string `json:"path"`
			} `json:"source"`
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"stackFrames"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return nil, err
	}
	frames := make([]StackFrame, len(body.StackFrames))
	for i, f := range body.StackFrames {
		frames[i] = StackFrame{ID: f.ID, Name: f.Name, File: f.Source.Path, Line: f.Line, Column: f.Column}
	}
	return frames, nil
}

func (c *companionClient) Scopes(ctx context.Context, frameID int) ([]Scope, error) {
	resp, err := c.tp.request(ctx, "scopes", map[string]interface{}{"frameId": frameID})
	if err != nil {
		return nil, err
	}
	var body struct {
		Scopes []struct {
			Name               string `json:"name"`
			VariablesReference int    `json:"variablesReference"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return nil, err
	}
	scopes := make([]Scope, len(body.Scopes))
	for i, s := range body.Scopes {
		scopes[i] = Scope{Name: s.Name, Ref: s.VariablesReference}
	}
	return scopes, nil
}

func (c *companionClient) Variables(ctx context.Context, ref int) ([]Variable, error) {
	resp, err := c.tp.request(ctx, "variables", map[string]interface{}{"variablesReference": ref})
	if err != nil {
		return nil, err
	}
	var body struct {
		Variables []struct {
			Name               string `json:"name"`
			Value              string `json:"value"`
			Type               string `json:"type"`
			VariablesReference int    `json:"variablesReference"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return nil, err
	}
	vars := make([]Variable, len(body.Variables))
	for i, v := range body.Variables {
		vars[i] = Variable{Name: v.Name, Value: v.Value, Type: v.Type, Ref: v.VariablesReference}
	}
	return vars, nil
}

func (c *companionClient) Evaluate(ctx context.Context, frameID int, expression string) (*Variable, error) {
	resp, err := c.tp.request(ctx, "evaluate", map[string]interface{}{
		"frameId":    frameID,
		"expression": expression,
		"context":    "repl",
	})
	if err != nil {
		return nil, err
	}
	var body struct {
		Result             string `json:"result"`
		Type               string `json:"type"`
		VariablesReference int    `json:"variablesReference"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return nil, err
	}
	return &Variable{
		Name:  expression,
		Value: body.Result,
		Type:  body.Type,
		Ref:   body.VariablesReference,
	}, nil
}

func (c *companionClient) OnStopped(handler func(StoppedEvent)) {
	c.tp.eventMu.Lock()
	defer c.tp.eventMu.Unlock()
	c.tp.onStopped = handler
}

func (c *companionClient) OnOutput(handler func(OutputEvent)) {
	c.tp.eventMu.Lock()
	defer c.tp.eventMu.Unlock()
	c.tp.onOutput = handler
}

func (c *companionClient) OnTerminated(handler func()) {
	c.tp.eventMu.Lock()
	defer c.tp.eventMu.Unlock()
	c.tp.onTerminated = handler
}

// Close shuts down the debug session and companion environment.
// Safe to call multiple times -- only the first call does anything.
func (c *companionClient) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		// Give the DAP disconnect a short deadline so we don't hang.
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		// Graceful DAP disconnect.
		if c.tp != nil {
			_, _ = c.tp.request(shutdownCtx, "disconnect", map[string]interface{}{
				"terminateDebuggee": true,
			})
			_ = c.tp.close()
		}

		// Stop DAP process.
		if c.proc != nil {
			_ = c.proc.Stop(ctx)
		}

		// Shut down the companion environment (container, nix shell, etc.).
		if c.runner != nil {
			c.closeErr = c.runner.Shutdown(ctx)
		}
	})
	return c.closeErr
}
