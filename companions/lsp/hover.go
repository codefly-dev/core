package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codefly-dev/core/wool"
)

// lspHover represents the LSP Hover response.
type lspHover struct {
	Contents lspMarkupContent `json:"contents"`
}

// lspMarkupContent can be a string, a MarkupContent object, or a MarkedString.
type lspMarkupContent struct {
	Kind  string `json:"kind,omitempty"`
	Value string `json:"value,omitempty"`
	// For plain string responses.
	raw string
}

func (m *lspMarkupContent) UnmarshalJSON(data []byte) error {
	// Try MarkupContent object first.
	type mc struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	var obj mc
	if err := json.Unmarshal(data, &obj); err == nil && obj.Value != "" {
		m.Kind = obj.Kind
		m.Value = obj.Value
		return nil
	}

	// Try plain string.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		m.Kind = "plaintext"
		m.Value = s
		return nil
	}

	// Try MarkedString (language + value).
	type markedString struct {
		Language string `json:"language"`
		Value    string `json:"value"`
	}
	var ms markedString
	if err := json.Unmarshal(data, &ms); err == nil {
		m.Kind = "markdown"
		m.Value = fmt.Sprintf("```%s\n%s\n```", ms.Language, ms.Value)
		return nil
	}

	// Try array of strings/MarkedStrings.
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err == nil {
		var parts []string
		for _, item := range arr {
			var sub lspMarkupContent
			if err := sub.UnmarshalJSON(item); err == nil {
				parts = append(parts, sub.Value)
			}
		}
		m.Kind = "markdown"
		m.Value = strings.Join(parts, "\n\n")
		return nil
	}

	return fmt.Errorf("cannot parse hover contents")
}

// Hover returns type information and documentation at the given position.
func (c *companionClient) Hover(ctx context.Context, file string, line, col int) (*HoverResult, error) {
	w := wool.Get(ctx).In("lsp.Hover")

	fileURI, err := c.openFile(file)
	if err != nil {
		return nil, err
	}

	result, err := c.tp.call(ctx, "textDocument/hover", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fileURI,
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": col - 1,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("textDocument/hover: %w", err)
	}

	// Null result means no hover info.
	if string(result) == "null" || len(result) == 0 {
		return &HoverResult{}, nil
	}

	var hover lspHover
	if err := json.Unmarshal(result, &hover); err != nil {
		return nil, fmt.Errorf("cannot parse hover response: %w", err)
	}

	lang := ""
	if hover.Contents.Kind == "markdown" {
		lang = c.cfg.LanguageID
	}

	w.Debug("hover", wool.Field("file", file), wool.Field("hasContent", hover.Contents.Value != ""))
	return &HoverResult{
		Content:  hover.Contents.Value,
		Language: lang,
	}, nil
}
