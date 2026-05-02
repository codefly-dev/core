package nix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/registry"
	"github.com/codefly-dev/core/toolbox/respond"
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
	*registry.Base

	version string

	// nixBinary lets tests inject a fake nix path. Empty means "use
	// PATH lookup of `nix`."
	nixBinary string
}

// New returns a Server.
func New(version string) *Server {
	s := &Server{version: version}
	s.Base = registry.NewBase(s)
	return s
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

// Tools is the source of truth — see git/server.go for convention.
func (s *Server) Tools() []*registry.ToolDefinition {
	return []*registry.ToolDefinition{
		{
			Name:               "nix.flake_metadata",
			SummaryDescription: "Read flake metadata (description, lastModified, narHash, ref). Read-only. Run on a path or URL.",
			LongDescription: "Wraps `nix flake metadata --json`. Returns the parsed metadata object — " +
				"description, lastModified timestamp, narHash, and the original ref the flake was pinned " +
				"to. Use to confirm a flake's identity before depending on it, or to surface its " +
				"description for catalog UIs.\n\n" +
				"On first call against an unfamiliar flake, nix may fetch inputs from upstream — that's " +
				"network-dependent and can be slow. Subsequent calls hit the local store.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"flake": map[string]any{
						"type":        "string",
						"description": "Flake reference. Path or url. Default '.'",
					},
				},
			}),
			Tags:        []string{"nix", "read-only", "filesystem"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns 'nix flake metadata: ...' wrapping nix's own error — typically 'no flake.nix found', 'unable to download', or 'invalid flake output'.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "Read metadata of the current directory's flake.",
					Arguments:       mustNixStruct(map[string]any{}),
					ExpectedOutcome: "Object with description, lastModified, narHash, original (the flake ref).",
				},
				{
					Description:     "Read a remote flake by URL.",
					Arguments:       mustNixStruct(map[string]any{"flake": "github:NixOS/nixpkgs/nixos-24.05"}),
					ExpectedOutcome: "Same shape; may be slow on first invocation due to fetch.",
				},
			},
			Handler: s.flakeMetadata,
		},
		{
			Name:               "nix.flake_show",
			SummaryDescription: "List a flake's outputs surface (packages, devShells, apps). Read-only. Useful before depending on it.",
			LongDescription: "Wraps `nix flake show --json`. Surfaces the flake's output structure — what " +
				"packages it exposes, what devShells it ships, what apps are runnable. Use to discover " +
				"what's available before writing a `nix run` or adding the flake as a dependency.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"flake": map[string]any{
						"type":        "string",
						"description": "Flake reference. Path or url. Default '.'",
					},
				},
			}),
			Tags:        []string{"nix", "read-only", "filesystem"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns 'nix flake show: ...' wrapping nix's error — typically a malformed flake or missing input.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "Show the current dir's flake outputs.",
					Arguments:       mustNixStruct(map[string]any{}),
					ExpectedOutcome: "{ outputs: { packages: {...}, devShells: {...}, apps: {...} } }",
				},
			},
			Handler: s.flakeShow,
		},
		{
			Name:               "nix.eval",
			SummaryDescription: "Evaluate a nix expression (read-only) and return its JSON value. Per-call timeout.",
			LongDescription: "Wraps `nix eval --json --read-only --expr <expr>`. Runs in nix's read-only " +
				"mode — refuses any expression that would mutate the store. Returns the parsed JSON " +
				"value. Pure expressions complete in milliseconds; impure ones (with allow-import-from-" +
				"derivation) can be slow.\n\n" +
				"Use to query nix configuration values, derive package set computations, or test " +
				"arithmetic in CI scripts. Output is capped at " + fmt.Sprintf("%d", MaxEvalOutputBytes) +
				" bytes; oversized results surface a `truncated: true` flag.",
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
			Tags:        []string{"nix", "read-only"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns 'nix eval: ...' wrapping nix's error — typically 'syntax error', 'infinite recursion', 'undefined variable', or timeout.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "Trivial arithmetic.",
					Arguments:       mustNixStruct(map[string]any{"expr": "1 + 2"}),
					ExpectedOutcome: "{ value: 3, truncated: false }",
				},
				{
					Description:     "Read a config value.",
					Arguments:       mustNixStruct(map[string]any{"expr": "builtins.toJSON { name = \"hello\"; }"}),
					ExpectedOutcome: "{ value: '{\"name\":\"hello\"}', truncated: false }",
				},
			},
			Handler: s.eval,
		},
	}
}

func mustNixStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("nix toolbox: cannot encode example args: %v", err))
	}
	return s
}

// --- Tool implementations ----------------------------------------

func (s *Server) flakeMetadata(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	flake := "."
	if v, ok := respond.Args(req)["flake"].(string); ok && v != "" {
		flake = v
	}
	out, _, err := s.runNix(ctx, DefaultEvalTimeout,
		"flake", "metadata", "--json", flake)
	if err != nil {
		return respond.Error("nix flake metadata: %v", err)
	}

	var parsed map[string]any
	if jerr := json.Unmarshal(out, &parsed); jerr != nil {
		return respond.Error("nix flake metadata: parse json: %v", jerr)
	}
	return respond.Struct(parsed)
}

func (s *Server) flakeShow(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	flake := "."
	if v, ok := respond.Args(req)["flake"].(string); ok && v != "" {
		flake = v
	}
	out, _, err := s.runNix(ctx, DefaultEvalTimeout,
		"flake", "show", "--json", flake)
	if err != nil {
		return respond.Error("nix flake show: %v", err)
	}

	var parsed map[string]any
	if jerr := json.Unmarshal(out, &parsed); jerr != nil {
		return respond.Error("nix flake show: parse json: %v", jerr)
	}
	return respond.Struct(map[string]any{"outputs": parsed})
}

func (s *Server) eval(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := respond.Args(req)
	expr, ok := args["expr"].(string)
	if !ok || expr == "" {
		return respond.Error("nix.eval: expr is required")
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
		return respond.Error("nix eval: %v", err)
	}

	var parsed any
	if jerr := json.Unmarshal(out, &parsed); jerr != nil {
		return respond.Error("nix eval: parse json: %v", jerr)
	}
	return respond.Struct(map[string]any{
		"value":     parsed,
		"truncated": truncated,
	})
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
