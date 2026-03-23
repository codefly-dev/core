package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/wool"
)

// lspLocation represents the LSP Location structure.
type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

// Definition returns the definition location(s) for the symbol at position.
func (c *companionClient) Definition(ctx context.Context, file string, line, col int) ([]LocationResult, error) {
	w := wool.Get(ctx).In("lsp.Definition")

	fileURI, err := c.openFile(file)
	if err != nil {
		return nil, err
	}

	result, err := c.tp.call(ctx, "textDocument/definition", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fileURI,
		},
		"position": map[string]interface{}{
			"line":      line - 1, // LSP uses 0-based
			"character": col - 1,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("textDocument/definition: %w", err)
	}

	locs := c.parseLocations(result)
	w.Debug("definition", wool.Field("file", file), wool.Field("results", len(locs)))
	return locs, nil
}

// References returns all usage locations of the symbol at position.
func (c *companionClient) References(ctx context.Context, file string, line, col int) ([]LocationResult, error) {
	w := wool.Get(ctx).In("lsp.References")

	fileURI, err := c.openFile(file)
	if err != nil {
		return nil, err
	}

	result, err := c.tp.call(ctx, "textDocument/references", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fileURI,
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": col - 1,
		},
		"context": map[string]interface{}{
			"includeDeclaration": true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("textDocument/references: %w", err)
	}

	locs := c.parseLocations(result)
	w.Debug("references", wool.Field("file", file), wool.Field("results", len(locs)))
	return locs, nil
}

// parseLocations parses LSP location results. The response may be a single
// Location, an array of Locations, or an array of LocationLink objects.
func (c *companionClient) parseLocations(raw json.RawMessage) []LocationResult {
	// Try array of Locations first.
	var locs []lspLocation
	if err := json.Unmarshal(raw, &locs); err == nil {
		return c.convertLocations(locs)
	}

	// Try single Location.
	var loc lspLocation
	if err := json.Unmarshal(raw, &loc); err == nil && loc.URI != "" {
		return c.convertLocations([]lspLocation{loc})
	}

	// Try LocationLink array (targetUri + targetRange).
	type locationLink struct {
		TargetURI   string   `json:"targetUri"`
		TargetRange lspRange `json:"targetRange"`
	}
	var links []locationLink
	if err := json.Unmarshal(raw, &links); err == nil {
		var out []LocationResult
		for _, link := range links {
			out = append(out, LocationResult{
				File:      c.uriToRelPath(link.TargetURI),
				Line:      link.TargetRange.Start.Line + 1,
				Column:    link.TargetRange.Start.Character + 1,
				EndLine:   link.TargetRange.End.Line + 1,
				EndColumn: link.TargetRange.End.Character + 1,
			})
		}
		return out
	}

	return nil
}

func (c *companionClient) convertLocations(locs []lspLocation) []LocationResult {
	var out []LocationResult
	for _, l := range locs {
		out = append(out, LocationResult{
			File:      c.uriToRelPath(l.URI),
			Line:      l.Range.Start.Line + 1,
			Column:    l.Range.Start.Character + 1,
			EndLine:   l.Range.End.Line + 1,
			EndColumn: l.Range.End.Character + 1,
		})
	}
	return out
}

// uriToRelPath converts a file:// URI to a relative path within the workspace.
func (c *companionClient) uriToRelPath(uri string) string {
	// Strip file:// prefix and URL-decode.
	path := strings.TrimPrefix(uri, "file://")
	decoded, err := url.PathUnescape(path)
	if err != nil {
		decoded = path
	}

	// Container path: /workspace/... -> relative
	if strings.HasPrefix(decoded, "/workspace/") {
		return strings.TrimPrefix(decoded, "/workspace/")
	}

	// Host path: try to make relative to rootDir.
	if rel, err := filepath.Rel(c.rootDir, decoded); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}

	return decoded
}
