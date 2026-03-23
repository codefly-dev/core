package lsp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/codefly-dev/wool"
)

// lspTextEdit represents the LSP TextEdit structure.
type lspTextEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// lspWorkspaceEdit represents the LSP WorkspaceEdit structure.
type lspWorkspaceEdit struct {
	Changes map[string][]lspTextEdit `json:"changes,omitempty"`
}

// Rename performs a language-aware rename of the symbol at the given position.
func (c *companionClient) Rename(ctx context.Context, file string, line, col int, newName string) ([]TextEditResult, error) {
	w := wool.Get(ctx).In("lsp.Rename")

	fileURI, err := c.openFile(file)
	if err != nil {
		return nil, err
	}

	result, err := c.tp.call(ctx, "textDocument/rename", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fileURI,
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": col - 1,
		},
		"newName": newName,
	})
	if err != nil {
		return nil, fmt.Errorf("textDocument/rename: %w", err)
	}

	var wsEdit lspWorkspaceEdit
	if err := json.Unmarshal(result, &wsEdit); err != nil {
		return nil, fmt.Errorf("cannot parse rename response: %w", err)
	}

	var edits []TextEditResult
	for uri, fileEdits := range wsEdit.Changes {
		relPath := c.uriToRelPath(uri)
		for _, e := range fileEdits {
			edits = append(edits, TextEditResult{
				File:        relPath,
				StartLine:   int32(e.Range.Start.Line + 1),
				StartColumn: int32(e.Range.Start.Character + 1),
				EndLine:     int32(e.Range.End.Line + 1),
				EndColumn:   int32(e.Range.End.Character + 1),
				NewText:     e.NewText,
			})
		}
	}

	w.Debug("rename", wool.Field("file", file), wool.Field("edits", len(edits)))
	return edits, nil
}
