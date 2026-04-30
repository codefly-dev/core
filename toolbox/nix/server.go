package nix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/internal/respond"
)

// DefaultEvalTimeout caps any single `nix eval` call. Nix evaluation
// can be unbounded (an infinite recursion in a flake will hang
// forever); a per-call ceiling keeps the toolbox honest. Configurable
// via the timeout_ms argument; this is the floor when none is given.
const DefaultEvalTimeout = 30 * time.Second

// MaxEvalOutputBytes caps how much stdout we keep from any nix
// invocation. Above this we truncate with a flag; defends against a
// hostile or buggy expression that prints multi-GB to stdout.
const MaxEvalOutputBytes = 4 * 1024 * 1024 // 4 MiB

// Server implements codefly.services.toolbox.v0.Toolbox for nix flake
// introspection and expression evaluation.
//
// Construction is cheap — no nix binary check, no daemon connection.
// The first tool call exec's `nix` directly; if nix isn't on PATH the
// tool surfaces a clear error. This mirrors docker.Server's lazy
// philosophy: tests that exercise schema/dispatch don't need a live
// nix install.
type Server struct {
	toolboxv0.UnimplementedToolboxServer

	version string

	// nixBinary lets tests inject a fake nix path. Empty means "use
	// PATH lookup of `nix`."
	nixBinary string
}

// New returns a Server.
func New(version string) *Server {
	return &Server{version: version}
}

// WithBinary overrides the nix executable path. Production callers
// leave this unset and rely on PATH; tests use it to point at a
// scripted fake.
func (s *Server) WithBinary(path string) *Server {
	s.nixBinary = path
	return s
}

// --- Identity ----------------------------------------------------

func (s *Server) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return &toolboxv0.IdentityResponse{
		Name:           "nix",
		Version:        s.version,
		Description:    "Nix flake introspection and evaluation. Canonical owner of the `nix` binary.",
		CanonicalFor:   []string{"nix"},
		SandboxSummary: "reads /nix/store; writes /nix/store + /tmp; network: nix substituters only",
	}, nil
}

// --- Tools -------------------------------------------------------

func (s *Server) ListTools(_ context.Context, _ *toolboxv0.ListToolsRequest) (*toolboxv0.ListToolsResponse, error) {
	return &toolboxv0.ListToolsResponse{
		Tools: []*toolboxv0.Tool{
			{
				Name:        "nix.flake_metadata",
				Description: "Run `nix flake metadata --json` against a flake; returns the parsed metadata object (description, lastModified, narHash, original ref).",
				InputSchema: respond.Schema(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"flake": map[string]any{
							"type":        "string",
							"description": "Flake reference. Path or url. Default '.'",
						},
					},
				}),
				Destructive: false,
			},
			{
				Name:        "nix.flake_show",
				Description: "Run `nix flake show --json` against a flake; returns the outputs surface (packages, devShells, apps, ...). Useful for discovering what a flake exposes before depending on it.",
				InputSchema: respond.Schema(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"flake": map[string]any{
							"type":        "string",
							"description": "Flake reference. Path or url. Default '.'",
						},
					},
				}),
				Destructive: false,
			},
			{
				Name:        "nix.eval",
				Description: "Run `nix eval --json` of an expression; returns the parsed JSON result. Read-only — the eval is sandboxed by nix's own --read-only flag and the toolbox's sandbox.",
				InputSchema: respond.Schema(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"expr": map[string]any{
							"type":        "string",
							"description": "Nix expression to evaluate. Must produce a JSON-encodable value.",
						},
						"timeout_ms": map[string]any{
							"type":        "integer",
							"description": "Per-call evaluation timeout. Default 30000.",
							"minimum":     100,
							"maximum":     300000,
						},
					},
					"required": []any{"expr"},
				}),
				Destructive: false,
			},
		},
	}, nil
}

func (s *Server) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	switch req.Name {
	case "nix.flake_metadata":
		return s.flakeMetadata(ctx, req)
	case "nix.flake_show":
		return s.flakeShow(ctx, req)
	case "nix.eval":
		return s.eval(ctx, req)
	default:
		return respond.Error("unknown tool %q (call ListTools to enumerate)", req.Name), nil
	}
}

// --- Tool implementations ----------------------------------------

func (s *Server) flakeMetadata(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	flake := "."
	if v, ok := respond.Args(req)["flake"].(string); ok && v != "" {
		flake = v
	}
	out, _, err := s.runNix(ctx, DefaultEvalTimeout,
		"flake", "metadata", "--json", flake)
	if err != nil {
		return respond.Error("nix flake metadata: %v", err), nil
	}

	var parsed map[string]any
	if jerr := json.Unmarshal(out, &parsed); jerr != nil {
		return respond.Error("nix flake metadata: parse json: %v", jerr), nil
	}
	return respond.Struct(parsed), nil
}

func (s *Server) flakeShow(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	flake := "."
	if v, ok := respond.Args(req)["flake"].(string); ok && v != "" {
		flake = v
	}
	out, _, err := s.runNix(ctx, DefaultEvalTimeout,
		"flake", "show", "--json", flake)
	if err != nil {
		return respond.Error("nix flake show: %v", err), nil
	}

	var parsed map[string]any
	if jerr := json.Unmarshal(out, &parsed); jerr != nil {
		return respond.Error("nix flake show: parse json: %v", jerr), nil
	}
	return respond.Struct(map[string]any{"outputs": parsed}), nil
}

func (s *Server) eval(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	args := respond.Args(req)
	expr, ok := args["expr"].(string)
	if !ok || expr == "" {
		return respond.Error("nix.eval: expr is required"), nil
	}

	timeout := DefaultEvalTimeout
	if v, ok := args["timeout_ms"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Millisecond
	}

	// --read-only forbids store mutations during eval. --json prints
	// the result as JSON for direct parsing. --expr says "argument is
	// the expression literal, not a path/installable."
	out, truncated, err := s.runNix(ctx, timeout,
		"eval", "--json", "--read-only", "--expr", expr)
	if err != nil {
		return respond.Error("nix eval: %v", err), nil
	}

	var parsed any
	if jerr := json.Unmarshal(out, &parsed); jerr != nil {
		return respond.Error("nix eval: parse json: %v", jerr), nil
	}
	payload := map[string]any{
		"value":     parsed,
		"truncated": truncated,
	}
	return respond.Struct(payload), nil
}

// runNix invokes the nix binary with the given args under a per-call
// timeout. Returns stdout (truncated at MaxEvalOutputBytes) and a
// truncation flag. stderr is folded into the error message on
// non-zero exit so the agent sees the actual nix complaint
// ("flake.nix not found", "infinite recursion at ...", etc.).
func (s *Server) runNix(ctx context.Context, timeout time.Duration, args ...string) ([]byte, bool, error) {
	bin := s.nixBinary
	if bin == "" {
		bin = "nix"
	}
	if _, lookErr := exec.LookPath(bin); lookErr != nil {
		return nil, false, fmt.Errorf("nix binary not found on PATH (%v); install nix or set the toolbox's nixBinary", lookErr)
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// --extra-experimental-features ensures `flake` and `nix-command`
	// are enabled regardless of the user's nix.conf. Otherwise a stock
	// nix install refuses every flake operation with a config error.
	full := append([]string{
		"--extra-experimental-features", "nix-command flakes",
	}, args...)

	cmd := exec.CommandContext(callCtx, bin, full...)
	stdout, runErr := cmd.Output()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			// Surface the nix error message verbatim. It's already
			// human-readable; rewrapping just adds noise.
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if stderr != "" {
				return nil, false, fmt.Errorf("%s", stderr)
			}
		}
		return nil, false, runErr
	}

	truncated := false
	if len(stdout) > MaxEvalOutputBytes {
		stdout = stdout[:MaxEvalOutputBytes]
		truncated = true
	}
	return stdout, truncated, nil
}
