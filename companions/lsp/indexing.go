package lsp

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// openFile sends textDocument/didOpen if the file hasn't been opened yet.
// Returns the file URI inside the companion workspace.
func (c *companionClient) openFile(relPath string) (string, error) {
	containerPath := "/workspace/" + relPath
	fileURI := pathToURI(containerPath)

	if c.opened[relPath] {
		return fileURI, nil
	}

	absPath := filepath.Join(c.rootDir, relPath)
	content, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("cannot read file %s: %w", relPath, err)
	}

	c.versions[relPath] = 1
	c.opened[relPath] = true

	err = c.tp.notify("textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        fileURI,
			"languageId": c.cfg.LanguageID,
			"version":    c.versions[relPath],
			"text":       string(content),
		},
	})
	if err != nil {
		return "", fmt.Errorf("didOpen: %w", err)
	}
	return fileURI, nil
}

// NotifyChange sends textDocument/didChange to the LSP server with the full
// new content. This is used for incremental indexing when the AI agent edits
// a file -- the LSP server updates its in-memory state without re-indexing
// from disk.
func (c *companionClient) NotifyChange(ctx context.Context, file string, content string) error {
	fileURI, err := c.openFile(file)
	if err != nil {
		return err
	}

	c.versions[file]++

	return c.tp.notify("textDocument/didChange", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":     fileURI,
			"version": c.versions[file],
		},
		"contentChanges": []map[string]interface{}{
			{"text": content},
		},
	})
}

// NotifySave sends textDocument/didSave to the LSP server.
// Some LSP servers use this as a trigger for heavier analysis.
func (c *companionClient) NotifySave(ctx context.Context, file string) error {
	fileURI, err := c.openFile(file)
	if err != nil {
		return err
	}

	return c.tp.notify("textDocument/didSave", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fileURI,
		},
	})
}

// pathToURI converts an absolute container path to a file:// URI.
func pathToURI(absPath string) string {
	return "file://" + url.PathEscape(absPath)
}
