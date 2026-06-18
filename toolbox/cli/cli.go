// Package cli is a reusable toolbox that turns any single CLI binary into a
// codefly tool. It runs the binary through a RunnerEnvironment — so the binary
// is provisioned and sandboxed by the environment (Nix pulls it from the
// workspace flake; Docker from an image; Native uses PATH) rather than being a
// hard system dependency — captures its output, and compresses it through the
// gortk filter catalog before returning it to the model.
//
// This is the answer to "how does a CLI toolbox get its dependency": it doesn't
// bundle one; it asks the environment to provide it. One instance wraps one
// binary (e.g. terraform, kubectl, helm), matching how `gh`/`git`/etc. each get
// their own canonical owner.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/llmout"
	runners "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/toolbox/registry"
	"github.com/codefly-dev/core/toolbox/respond"
)

// Toolbox wraps one CLI binary, executed via the given RunnerEnvironment.
type Toolbox struct {
	*registry.Base

	env     runners.RunnerEnvironment
	bin     string
	version string
}

// New wraps `bin`, running it through env. The caller constructs and Init's the
// environment (Native/Nix/Docker), which decides how `bin` is provisioned.
func New(env runners.RunnerEnvironment, bin, version string) *Toolbox {
	t := &Toolbox{env: env, bin: bin, version: version}
	t.Base = registry.NewBase(t)
	return t
}

func (t *Toolbox) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return &toolboxv0.IdentityResponse{
		Name:         t.bin,
		Version:      t.version,
		Description:  fmt.Sprintf("Runs `%s` in a provisioned environment; output compressed for LLM context.", t.bin),
		CanonicalFor: []string{t.bin},
		SandboxSummary: fmt.Sprintf(
			"executes `%s` via the configured runner environment (Nix/Docker/Native provisions the binary)", t.bin),
	}, nil
}

func (t *Toolbox) Tools() []*registry.ToolDefinition {
	return []*registry.ToolDefinition{
		{
			Name:               t.bin + ".run",
			SummaryDescription: fmt.Sprintf("Run `%s` with the given arguments. Output is noise-stripped/compressed.", t.bin),
			LongDescription: fmt.Sprintf("Executes `%s <args>` inside the toolbox's runner environment (the binary is "+
				"provisioned there — Nix from the workspace flake, Docker from an image, or Native from PATH) and "+
				"returns the combined output run through the gortk filter for `%s`. A non-zero exit returns the "+
				"compressed output in the error envelope, since that's where the diagnostics are.", t.bin, t.bin),
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"args": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": fmt.Sprintf("Arguments passed to `%s`, e.g. [\"plan\"] or [\"get\",\"pods\"].", t.bin),
					},
				},
				"required": []any{"args"},
			}),
			Tags:        []string{t.bin, "cli"},
			Idempotency: "unknown",
			ErrorModes:  fmt.Sprintf("Returns `%s ...` error text (compressed) on non-zero exit or when the environment can't provide the binary.", t.bin),
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     fmt.Sprintf("Run `%s` with a subcommand.", t.bin),
					Arguments:       mustStruct(map[string]any{"args": []any{"version"}}),
					ExpectedOutcome: "Compressed combined output of the command.",
				},
			},
			Handler: t.run,
		},
	}
}

func (t *Toolbox) run(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := toStrings(respond.Args(req)["args"])

	proc, err := t.env.NewProcess(t.bin, args...)
	if err != nil {
		return respond.Error("%s: cannot start (environment could not provide it?): %v", t.bin, err)
	}
	var buf bytes.Buffer
	proc.WithOutput(&buf)

	runErr := proc.Run(ctx)
	compressed := llmout.Compress(t.bin, args, buf.String())
	if runErr != nil {
		return respond.Error("%s %s failed: %v\n%s", t.bin, strings.Join(args, " "), runErr, compressed)
	}
	return respond.Text(compressed)
}

// toStrings coerces a structpb-decoded []any (or nil) into []string.
func toStrings(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func mustStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("cli toolbox: cannot encode example args: %v", err))
	}
	return s
}
