package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	runners "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/wool"
)

// companionClient implements Client by running an LSP server inside a
// companion environment (Docker, Nix, or local) and talking to it over
// TCP (JSON-RPC 2.0).
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

	// File version tracking for incremental indexing (LSP spec requirement).
	versions map[string]int
	// Track which files have been opened via didOpen.
	opened map[string]bool

	// Ensure Close is safe to call multiple times.
	closeOnce sync.Once
	closeErr  error
}

// newCompanionClient starts a companion, launches the LSP server, connects
// over TCP, and initializes the LSP session.
// Ports are picked dynamically -- never hardcoded.
func newCompanionClient(ctx context.Context, cfg *LanguageConfig, sourceDir string) (*companionClient, error) {
	w := wool.Get(ctx).In("lsp.newCompanionClient")

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
	// the port gopls listens on; for local/nix the host port is used directly.
	containerPort := hostPort

	// Get the companion image (may be nil for local/nix backends).
	img, err := cfg.CompanionImage(ctx)
	if err != nil {
		w.Warn("companion image not available, falling back", wool.ErrField(err))
	}

	name := fmt.Sprintf("lsp-%s-%d", cfg.LanguageID, time.Now().UnixMilli())

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

	// Build LSP args with the container port (where the LSP server listens).
	args := cfg.LSPListenArgs(containerPort)

	proc, err := runner.NewProcess(cfg.LSPBinary, args...)
	if err != nil {
		runner.Shutdown(ctx)
		return nil, w.Wrapf(err, "cannot create LSP process")
	}

	err = proc.Start(ctx)
	if err != nil {
		runner.Shutdown(ctx)
		return nil, w.Wrapf(err, "cannot start LSP server")
	}

	w.Info("LSP companion started",
		wool.Field("lang", cfg.LanguageID),
		wool.Field("binary", cfg.LSPBinary),
		wool.Field("hostPort", hostPort),
		wool.Field("containerPort", containerPort))

	// Connect from the host to the host port (Docker forwards to container).
	conn, err := waitForConnection(ctx, hostPort)
	if err != nil {
		runner.Shutdown(ctx)
		return nil, w.Wrapf(err, "cannot connect to LSP server")
	}

	c := &companionClient{
		cfg:      cfg,
		runner:   runner,
		proc:     proc,
		tp:       newTransport(conn),
		rootDir:  absSource,
		hostPort: hostPort,
		versions: make(map[string]int),
		opened:   make(map[string]bool),
	}

	if err := c.initialize(ctx); err != nil {
		c.Close(ctx)
		return nil, w.Wrapf(err, "LSP initialize failed")
	}
	if err := c.waitForReady(ctx); err != nil {
		c.Close(ctx)
		return nil, w.Wrapf(err, "LSP not ready")
	}
	return c, nil
}

// initialize sends the LSP initialize/initialized handshake.
func (c *companionClient) initialize(ctx context.Context) error {
	rootURI := "file:///workspace"

	initParams := map[string]interface{}{
		"processId": nil,
		"rootUri":   rootURI,
		"capabilities": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"documentSymbol": map[string]interface{}{
					"hierarchicalDocumentSymbolSupport": true,
				},
				"synchronization": map[string]interface{}{
					"dynamicRegistration": true,
					"willSave":            true,
					"didSave":             true,
					"willSaveWaitUntil":   false,
				},
				"definition": map[string]interface{}{
					"dynamicRegistration": true,
				},
				"references": map[string]interface{}{
					"dynamicRegistration": true,
				},
				"rename": map[string]interface{}{
					"dynamicRegistration": true,
					"prepareSupport":      true,
				},
				"hover": map[string]interface{}{
					"dynamicRegistration": true,
					"contentFormat":       []string{"markdown", "plaintext"},
				},
				"publishDiagnostics": map[string]interface{}{
					"relatedInformation": true,
				},
			},
		},
	}

	_, err := c.tp.call(ctx, "initialize", initParams)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	return c.tp.notify("initialized", map[string]interface{}{})
}

// waitForReady probes the LSP server until it responds to workspace/symbol (with backoff).
// Handles cold-start delay where the server needs time to index after initialize.
func (c *companionClient) waitForReady(ctx context.Context) error {
	backoff := 200 * time.Millisecond
	const maxBackoff = 2 * time.Second
	const maxElapsed = 30 * time.Second
	deadline := time.Now().Add(maxElapsed)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_, err := c.tp.call(ctx, "workspace/symbol", map[string]interface{}{"query": ""})
		if err == nil {
			return nil
		}
		time.Sleep(backoff)
		if backoff < maxBackoff {
			backoff += 200 * time.Millisecond
		}
	}
	return fmt.Errorf("LSP server did not become ready within %v", maxElapsed)
}

// Close shuts down the LSP server and companion environment.
// Safe to call multiple times -- only the first call does anything.
func (c *companionClient) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		// Give the LSP shutdown a short deadline so we don't hang.
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		// Graceful LSP shutdown.
		if c.tp != nil {
			_, _ = c.tp.call(shutdownCtx, "shutdown", nil)
			_ = c.tp.notify("exit", nil)
			_ = c.tp.close()
		}

		// Stop LSP process.
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
