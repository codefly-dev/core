package treesitter

// ARCHITECTURE: fileScopedClient is the shared tree-sitter Client implementation.
// It parses files on demand, caches parsed trees keyed by (path, content hash),
// and dispatches language-specific symbol extraction through the LanguageConfig.
//
// Concurrency: each client owns ONE *sitter.Parser. sitter.Parser is NOT
// goroutine-safe, so we guard parses with a mutex. The resulting *sitter.Tree
// IS safe for concurrent read.
//
// The cache is bounded indirectly: entries are keyed by file path and replaced
// when content hash changes. For long-running daemons the cache will hold one
// entry per source file, which is the right trade-off for small/medium repos.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"github.com/codefly-dev/core/wool"
)

// fileScopedClient is the default Client implementation.
// "File-scoped" because parsing happens per-file; cross-file resolution is
// layered via the parsed-tree cache + identifier scan.
type fileScopedClient struct {
	cfg       *LanguageConfig
	sourceDir string

	parserMu sync.Mutex // guards parser (sitter.Parser is not safe for concurrent use)
	parser   *sitter.Parser
	grammar  *sitter.Language

	cacheMu sync.RWMutex
	cache   map[string]*parsedFile // relPath -> parsed entry
}

// parsedFile is a cached parse result.
type parsedFile struct {
	content []byte
	hash    string
	tree    *sitter.Tree
}

func newFileScopedClient(_ context.Context, cfg *LanguageConfig, sourceDir string) (Client, error) {
	grammar := cfg.Grammar()
	if grammar == nil {
		return nil, fmt.Errorf("treesitter %s: Grammar() returned nil", cfg.LanguageID)
	}
	p := sitter.NewParser()
	p.SetLanguage(grammar)
	return &fileScopedClient{
		cfg:       cfg,
		sourceDir: sourceDir,
		parser:    p,
		grammar:   grammar,
		cache:     map[string]*parsedFile{},
	}, nil
}

// parseFile parses a file relative to sourceDir, using the cache when the
// file content hash matches. Returns the parsed tree and the raw bytes.
func (c *fileScopedClient) parseFile(ctx context.Context, relPath string) (*sitter.Tree, []byte, error) {
	abs := filepath.Join(c.sourceDir, relPath)
	content, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", relPath, err)
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	c.cacheMu.RLock()
	pf, ok := c.cache[relPath]
	c.cacheMu.RUnlock()
	if ok && pf.hash == hash {
		return pf.tree, pf.content, nil
	}

	c.parserMu.Lock()
	tree, err := c.parser.ParseCtx(ctx, nil, content)
	c.parserMu.Unlock()
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", relPath, err)
	}

	c.cacheMu.Lock()
	// Evict the old tree if we are replacing the entry.
	if old, found := c.cache[relPath]; found && old.tree != nil {
		old.tree.Close()
	}
	c.cache[relPath] = &parsedFile{content: content, hash: hash, tree: tree}
	c.cacheMu.Unlock()

	return tree, content, nil
}

// walkSourceFiles calls fn for every source file under sourceDir that matches
// the language's extensions and is not in a skipped directory or suffix.
func (c *fileScopedClient) walkSourceFiles(fn func(relPath string) error) error {
	skipDirs := map[string]bool{}
	for _, d := range c.cfg.SkipDirs {
		skipDirs[d] = true
	}
	exts := map[string]bool{}
	for _, e := range c.cfg.FileExtensions {
		exts[e] = true
	}

	return filepath.WalkDir(c.sourceDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// Surface read errors on the root, skip unreadable subtrees.
			if p == c.sourceDir {
				return err
			}
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(p)
		if !exts[ext] {
			return nil
		}
		for _, suf := range c.cfg.SkipSuffixes {
			if strings.HasSuffix(p, suf) {
				return nil
			}
		}
		rel, err := filepath.Rel(c.sourceDir, p)
		if err != nil {
			return nil
		}
		return fn(rel)
	})
}

// NotifyChange is a no-op: tree-sitter is stateless and re-parses on demand.
// The cache keys on content hash, so changes automatically invalidate.
func (c *fileScopedClient) NotifyChange(_ context.Context, _ string, _ string) error {
	return nil
}

// NotifySave is a no-op.
func (c *fileScopedClient) NotifySave(_ context.Context, _ string) error { return nil }

// Close releases the parser and all cached trees.
func (c *fileScopedClient) Close(ctx context.Context) error {
	w := wool.Get(ctx).In("treesitter.Close")
	c.cacheMu.Lock()
	for _, pf := range c.cache {
		if pf.tree != nil {
			pf.tree.Close()
		}
	}
	c.cache = map[string]*parsedFile{}
	c.cacheMu.Unlock()
	c.parserMu.Lock()
	c.parser = nil
	c.parserMu.Unlock()
	w.Trace("closed tree-sitter client")
	return nil
}

// nodeText returns the source text covered by a node.
func nodeText(n *sitter.Node, content []byte) string {
	if n == nil {
		return ""
	}
	return string(content[n.StartByte():n.EndByte()])
}

// pointToLocation converts a tree-sitter Range to a codev0.Location (1-based).
func pointToLocation(file string, n *sitter.Node) *codev0.Location {
	start := n.StartPoint()
	end := n.EndPoint()
	return &codev0.Location{
		File:      file,
		Line:      int32(start.Row) + 1,
		Column:    int32(start.Column) + 1,
		EndLine:   int32(end.Row) + 1,
		EndColumn: int32(end.Column) + 1,
	}
}
