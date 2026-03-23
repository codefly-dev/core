package code

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

//go:embed ts_symbols.js
var tsSymbolsScript embed.FS

// TSASTSymbolProvider extracts symbols from TypeScript/JavaScript files using
// the TypeScript compiler API via a Node.js subprocess.
type TSASTSymbolProvider struct {
	sourceDir  string
	scriptPath string
}

// NewTSASTSymbolProvider creates a provider that parses TS/JS files.
func NewTSASTSymbolProvider(sourceDir string) *TSASTSymbolProvider {
	return &TSASTSymbolProvider{sourceDir: sourceDir}
}

func (p *TSASTSymbolProvider) ensureScript() error {
	if p.scriptPath != "" {
		return nil
	}
	data, err := tsSymbolsScript.ReadFile("ts_symbols.js")
	if err != nil {
		return fmt.Errorf("read embedded script: %w", err)
	}
	f, err := os.CreateTemp("", "codefly-ts-symbols-*.js")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	f.Close()
	p.scriptPath = f.Name()
	return nil
}

// Close removes the temporary script file.
func (p *TSASTSymbolProvider) Close() {
	if p.scriptPath != "" {
		os.Remove(p.scriptPath)
		p.scriptPath = ""
	}
}

// ListSymbols extracts symbols from a file or the entire source directory.
func (p *TSASTSymbolProvider) ListSymbols(ctx context.Context, file string) ([]*codev0.Symbol, error) {
	if err := p.ensureScript(); err != nil {
		return nil, err
	}

	target := p.sourceDir
	if file != "" {
		target = filepath.Join(p.sourceDir, file)
	}

	cmd := exec.CommandContext(ctx, "node", p.scriptPath, target)
	// Set NODE_PATH so the script can find globally installed typescript.
	// Also check the target directory's node_modules.
	globalNodeModules := resolveGlobalNodeModules()
	localNodeModules := filepath.Join(p.sourceDir, "node_modules")
	nodePath := localNodeModules
	if globalNodeModules != "" {
		nodePath = localNodeModules + ":" + globalNodeModules
	}
	cmd.Env = append(os.Environ(), "NODE_PATH="+nodePath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ts ast parse: %w (stderr: %s)", err, stderr.String())
	}

	var result map[string][]tsSymbol
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("parse symbols json: %w", err)
	}

	var symbols []*codev0.Symbol
	for _, fileSymbols := range result {
		for _, sym := range fileSymbols {
			symbols = append(symbols, sym.toProto())
		}
	}
	return symbols, nil
}

// ListSymbolsByFile returns symbols grouped by relative file path.
func (p *TSASTSymbolProvider) ListSymbolsByFile(ctx context.Context) (map[string][]*codev0.Symbol, error) {
	if err := p.ensureScript(); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "node", p.scriptPath, p.sourceDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ts ast parse: %w (stderr: %s)", err, stderr.String())
	}

	var result map[string][]tsSymbol
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("parse symbols json: %w", err)
	}

	out := make(map[string][]*codev0.Symbol, len(result))
	for file, fileSymbols := range result {
		for _, sym := range fileSymbols {
			out[file] = append(out[file], sym.toProto())
		}
	}
	return out, nil
}

type tsSymbol struct {
	Name             string     `json:"name"`
	Kind             string     `json:"kind"`
	Line             int32      `json:"line"`
	EndLine          int32      `json:"end_line"`
	Signature        string     `json:"signature"`
	Documentation    string     `json:"documentation"`
	IsExported       bool       `json:"is_exported,omitempty"`
	IsAsync          bool       `json:"is_async,omitempty"`
	IsDefaultExport  bool       `json:"is_default_export,omitempty"`
	IsReactComponent bool       `json:"is_react_component,omitempty"`
	IsStatic         bool       `json:"is_static,omitempty"`
	Visibility       string     `json:"visibility,omitempty"`
	Bases            []string   `json:"bases,omitempty"`
	Implements       []string   `json:"implements,omitempty"`
	Children         []tsSymbol `json:"children"`
}

func (s *tsSymbol) toProto() *codev0.Symbol {
	sym := &codev0.Symbol{
		Name:          s.Name,
		Kind:          tsKindToProto(s.Kind),
		Signature:     s.Signature,
		Documentation: s.Documentation,
		Location: &codev0.Location{
			Line:    s.Line,
			EndLine: s.EndLine,
		},
	}
	for _, child := range s.Children {
		c := child.toProto()
		c.Parent = s.Name
		sym.Children = append(sym.Children, c)
	}
	return sym
}

func tsKindToProto(kind string) codev0.SymbolKind {
	switch kind {
	case "function":
		return codev0.SymbolKind_SYMBOL_KIND_FUNCTION
	case "method":
		return codev0.SymbolKind_SYMBOL_KIND_METHOD
	case "class":
		return codev0.SymbolKind_SYMBOL_KIND_CLASS
	case "interface":
		return codev0.SymbolKind_SYMBOL_KIND_INTERFACE
	case "type":
		return codev0.SymbolKind_SYMBOL_KIND_STRUCT
	case "variable", "property":
		return codev0.SymbolKind_SYMBOL_KIND_VARIABLE
	default:
		if strings.HasPrefix(kind, "method") {
			return codev0.SymbolKind_SYMBOL_KIND_METHOD
		}
		return codev0.SymbolKind_SYMBOL_KIND_UNKNOWN
	}
}

func resolveGlobalNodeModules() string {
	cmd := exec.Command("npm", "root", "-g")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
