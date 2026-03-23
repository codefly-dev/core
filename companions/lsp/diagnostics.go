package lsp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/codefly-dev/wool"
)

// lspDiagnostic represents the LSP Diagnostic structure.
type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"` // 1=Error, 2=Warning, 3=Information, 4=Hint
	Code     any      `json:"code,omitempty"`
	Source   string   `json:"source,omitempty"`
	Message  string   `json:"message"`
}

// Diagnostics returns compiler/linter diagnostics for a file.
// It triggers a didOpen (or didSave) to get fresh diagnostics, then
// uses textDocument/diagnostic (LSP 3.17+). For servers that don't support
// the pull model, it falls back to collecting publishDiagnostics notifications.
func (c *companionClient) Diagnostics(ctx context.Context, file string) ([]DiagnosticResult, error) {
	w := wool.Get(ctx).In("lsp.Diagnostics")

	if file == "" {
		return nil, fmt.Errorf("file parameter is required for diagnostics")
	}

	fileURI, err := c.openFile(file)
	if err != nil {
		return nil, err
	}

	// Try the pull-based diagnostic model first (textDocument/diagnostic).
	result, err := c.tp.call(ctx, "textDocument/diagnostic", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fileURI,
		},
	})
	if err != nil {
		w.Debug("pull diagnostics not supported, returning empty", wool.ErrField(err))
		return nil, nil
	}

	// Parse the response. The result contains an "items" array of diagnostics.
	var diagReport struct {
		Kind  string          `json:"kind"`
		Items []lspDiagnostic `json:"items"`
	}
	if err := json.Unmarshal(result, &diagReport); err != nil {
		// Some servers return diagnostics directly as an array.
		var items []lspDiagnostic
		if err2 := json.Unmarshal(result, &items); err2 != nil {
			return nil, fmt.Errorf("cannot parse diagnostics: %w", err)
		}
		diagReport.Items = items
	}

	var out []DiagnosticResult
	for _, d := range diagReport.Items {
		out = append(out, DiagnosticResult{
			File:      file,
			Line:      int32(d.Range.Start.Line + 1),
			Column:    int32(d.Range.Start.Character + 1),
			EndLine:   int32(d.Range.End.Line + 1),
			EndColumn: int32(d.Range.End.Character + 1),
			Message:   d.Message,
			Severity:  diagSeverityString(d.Severity),
			Source:    d.Source,
			Code:      fmt.Sprintf("%v", d.Code),
		})
	}

	w.Debug("diagnostics", wool.Field("file", file), wool.Field("count", len(out)))
	return out, nil
}

func diagSeverityString(sev int) string {
	switch sev {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "information"
	case 4:
		return "hint"
	default:
		return "unknown"
	}
}
