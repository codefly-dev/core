package code

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

//go:embed python_symbols.py
var pythonSymbolsScript embed.FS

// PythonASTSymbolProvider extracts symbols from Python files using
// Python's ast module via a subprocess.
type PythonASTSymbolProvider struct {
	sourceDir  string
	scriptPath string
}

// NewPythonASTSymbolProvider creates a provider that parses Python files.
func NewPythonASTSymbolProvider(sourceDir string) *PythonASTSymbolProvider {
	return &PythonASTSymbolProvider{sourceDir: sourceDir}
}

func (p *PythonASTSymbolProvider) ensureScript() error {
	if p.scriptPath != "" {
		return nil
	}
	data, err := pythonSymbolsScript.ReadFile("python_symbols.py")
	if err != nil {
		return fmt.Errorf("read embedded script: %w", err)
	}
	f, err := os.CreateTemp("", "codefly-python-symbols-*.py")
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
func (p *PythonASTSymbolProvider) Close() {
	if p.scriptPath != "" {
		os.Remove(p.scriptPath)
		p.scriptPath = ""
	}
}

// ListSymbols extracts symbols from a file or the entire source directory.
func (p *PythonASTSymbolProvider) ListSymbols(ctx context.Context, file string) ([]*codev0.Symbol, error) {
	if err := p.ensureScript(); err != nil {
		return nil, err
	}

	target := p.sourceDir
	if file != "" {
		target = filepath.Join(p.sourceDir, file)
	}

	cmd := exec.CommandContext(ctx, "python3", p.scriptPath, target)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("python ast parse: %w (stderr: %s)", err, stderr.String())
	}

	var result map[string][]pySymbol
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("parse symbols json: %w", err)
	}

	var symbols []*codev0.Symbol
	for filePath, fileSymbols := range result {
		// Read file content for hash computation.
		absPath := filePath
		if !filepath.IsAbs(filePath) {
			absPath = filepath.Join(p.sourceDir, filePath)
		}
		content, _ := os.ReadFile(absPath)
		lines := strings.Split(string(content), "\n")

		for _, sym := range fileSymbols {
			proto := sym.toProto()
			// Enrich with HyperAST-style hashes.
			enrichPythonSymbolHashes(proto, lines, "")
			symbols = append(symbols, proto)
		}
	}
	return symbols, nil
}

// enrichPythonSymbolHashes computes body_hash, signature_hash, and
// qualified_name for a Python symbol. Same approach as the Go agent.
func enrichPythonSymbolHashes(sym *codev0.Symbol, lines []string, parentQN string) {
	// Qualified name.
	qn := sym.Name
	if sym.Parent != "" {
		qn = sym.Parent + "." + sym.Name
	}
	if parentQN != "" {
		qn = parentQN + "." + sym.Name
	}
	sym.QualifiedName = qn

	// Signature hash.
	if sym.Signature != "" {
		h := sha256.Sum256([]byte(sym.Signature))
		sym.SignatureHash = fmt.Sprintf("%x", h[:8])
	}

	// Body hash: extract lines, normalize, hash.
	if sym.Location != nil {
		start := int(sym.Location.Line)
		end := int(sym.Location.EndLine)
		kind := sym.Kind
		hasBody := kind == codev0.SymbolKind_SYMBOL_KIND_FUNCTION ||
			kind == codev0.SymbolKind_SYMBOL_KIND_METHOD ||
			kind == codev0.SymbolKind_SYMBOL_KIND_CLASS
		if hasBody && start > 0 && end > 0 && end <= len(lines) {
			body := strings.Join(lines[start-1:end], "\n")
			normalized := normalizePythonBody(body)
			if normalized != "" {
				h := sha256.Sum256([]byte(normalized))
				sym.BodyHash = fmt.Sprintf("%x", h[:8])
			}
		}
	}

	for _, child := range sym.Children {
		enrichPythonSymbolHashes(child, lines, qn)
	}
}

// normalizePythonBody strips trailing whitespace and blank lines.
func normalizePythonBody(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, "\n")
}

type pySymbol struct {
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	Line          int32      `json:"line"`
	EndLine       int32      `json:"end_line"`
	Signature     string     `json:"signature"`
	Documentation string     `json:"documentation"`
	Decorators    []string   `json:"decorators,omitempty"`
	Bases         []string   `json:"bases,omitempty"`
	IsAsync       bool       `json:"is_async,omitempty"`
	IsProtocol    bool       `json:"is_protocol,omitempty"`
	IsAbstract    bool       `json:"is_abstract,omitempty"`
	Exports       []string   `json:"exports,omitempty"`
	Children      []pySymbol `json:"children"`
}

func (s *pySymbol) toProto() *codev0.Symbol {
	sig := s.Signature
	// Prepend decorators to signature for visibility
	if len(s.Decorators) > 0 {
		decorStr := ""
		for _, d := range s.Decorators {
			decorStr += "@" + d + " "
		}
		sig = decorStr + sig
	}

	sym := &codev0.Symbol{
		Name:          s.Name,
		Kind:          pyKindToProto(s.Kind),
		Signature:     sig,
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

func pyKindToProto(kind string) codev0.SymbolKind {
	switch kind {
	case "function":
		return codev0.SymbolKind_SYMBOL_KIND_FUNCTION
	case "method", "classmethod", "staticmethod", "property":
		return codev0.SymbolKind_SYMBOL_KIND_METHOD
	case "class":
		return codev0.SymbolKind_SYMBOL_KIND_CLASS
	case "variable":
		return codev0.SymbolKind_SYMBOL_KIND_VARIABLE
	default:
		if strings.HasPrefix(kind, "method") {
			return codev0.SymbolKind_SYMBOL_KIND_METHOD
		}
		return codev0.SymbolKind_SYMBOL_KIND_UNKNOWN
	}
}
