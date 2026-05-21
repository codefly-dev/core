package code

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// TypeScriptCodeServer extends DefaultCodeServer with TypeScript /
// JavaScript-specific operations: GetProjectInfo (package.json +
// per-file import scan) and ListSymbols (via the pluggable
// SymbolProvider; TSASTSymbolProvider is the default when Node.js
// is available).
//
// Per-file import extraction is line-based Go scanning — same
// philosophy as PythonCodeServer's scanPackageImports. We avoid
// requiring Node.js for the dependency view (the symbol provider
// is the only thing that needs the TypeScript compiler API).
type TypeScriptCodeServer struct {
	*DefaultCodeServer
	symbols SymbolProvider
}

// TypeScriptServerOption configures a TypeScriptCodeServer.
type TypeScriptServerOption func(*TypeScriptCodeServer)

// WithTypeScriptSymbolProvider overrides the default symbol
// provider. Pass nil to disable list_symbols entirely (useful when
// Node.js isn't installed and the test only exercises
// GetProjectInfo).
func WithTypeScriptSymbolProvider(sp SymbolProvider) TypeScriptServerOption {
	return func(s *TypeScriptCodeServer) {
		s.symbols = sp
	}
}

// NewTypeScriptCodeServer creates a TS-aware code server. The
// default symbol provider is TSASTSymbolProvider which requires
// Node.js; tests on Node-free CI should pass
// WithTypeScriptSymbolProvider(nil).
func NewTypeScriptCodeServer(dir string, serverOpts []ServerOption, tsOpts ...TypeScriptServerOption) *TypeScriptCodeServer {
	base := NewDefaultCodeServer(dir, serverOpts...)
	s := &TypeScriptCodeServer{
		DefaultCodeServer: base,
		symbols:           NewTSASTSymbolProvider(dir),
	}
	for _, o := range tsOpts {
		o(s)
	}
	s.registerTypeScriptOverrides()
	return s
}

func (s *TypeScriptCodeServer) registerTypeScriptOverrides() {
	s.Override("get_project_info", s.handleGetProjectInfo)
	if s.symbols != nil {
		s.Override("list_symbols", s.handleListSymbols)
	}
}

// --- Standalone gRPC RPCs ---

func (s *TypeScriptCodeServer) GetProjectInfo(ctx context.Context, _ *codev0.GetProjectInfoRequest) (*codev0.GetProjectInfoResponse, error) {
	resp, err := s.handleGetProjectInfo(ctx, nil)
	if err != nil {
		return nil, err
	}
	return resp.GetGetProjectInfo(), nil
}

func (s *TypeScriptCodeServer) ListSymbols(ctx context.Context, req *codev0.ListSymbolsRequest) (*codev0.ListSymbolsResponse, error) {
	if s.symbols == nil {
		return &codev0.ListSymbolsResponse{
			Status: &codev0.ListSymbolsStatus{State: codev0.ListSymbolsStatus_ERROR, Message: "no symbol provider configured"},
		}, nil
	}
	codeReq := &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ListSymbols{ListSymbols: req},
	}
	resp, err := s.handleListSymbols(ctx, codeReq)
	if err != nil {
		return nil, err
	}
	return resp.GetListSymbols(), nil
}

// --- Handlers ---

func (s *TypeScriptCodeServer) handleListSymbols(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	r := req.GetListSymbols()
	file := ""
	if r != nil {
		file = r.File
	}
	symbols, err := s.symbols.ListSymbols(ctx, file)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_ListSymbols{ListSymbols: &codev0.ListSymbolsResponse{
			Status: &codev0.ListSymbolsStatus{State: codev0.ListSymbolsStatus_ERROR, Message: err.Error()},
		}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_ListSymbols{ListSymbols: &codev0.ListSymbolsResponse{
		Status:  &codev0.ListSymbolsStatus{State: codev0.ListSymbolsStatus_SUCCESS},
		Symbols: symbols,
	}}}, nil
}

func (s *TypeScriptCodeServer) handleGetProjectInfo(_ context.Context, _ *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	srcDir := s.SourceDir
	resp := &codev0.GetProjectInfoResponse{Language: "typescript"}

	// Parse package.json for the module name + declared deps.
	if data, err := s.FS.ReadFile(filepath.Join(srcDir, "package.json")); err == nil {
		resp.Module, resp.Dependencies = parsePackageJSON(data)
	}

	resp.Packages = s.discoverTSPackages(srcDir)
	resp.FileHashes = s.computeTSFileHashes(srcDir)

	return wrapProjectInfoTS(resp), nil
}

// --- Helpers ---

func wrapProjectInfoTS(resp *codev0.GetProjectInfoResponse) *codev0.CodeResponse {
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetProjectInfo{GetProjectInfo: resp}}
}

// packageJSON is the minimal slice of fields we care about.
type packageJSON struct {
	Name            string            `json:"name"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// parsePackageJSON returns the module name + a flat list of
// declared deps from package.json. Direct=true for production
// dependencies; dev deps are also Direct=true (they're explicitly
// declared, just at a different lifecycle).
func parsePackageJSON(data []byte) (string, []*codev0.Dependency) {
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", nil
	}
	var deps []*codev0.Dependency
	for name, version := range pkg.Dependencies {
		deps = append(deps, &codev0.Dependency{Name: name, Version: version, Direct: true})
	}
	for name, version := range pkg.DevDependencies {
		deps = append(deps, &codev0.Dependency{Name: name, Version: version, Direct: true})
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Name < deps[j].Name })
	return pkg.Name, deps
}

// discoverTSPackages walks the source tree treating each directory
// containing .ts/.tsx/.js/.jsx files as a "package" (the
// TypeScript equivalent of Python's directory-as-package
// convention). Skips node_modules + .next/dist build outputs.
func (s *TypeScriptCodeServer) discoverTSPackages(srcDir string) []*codev0.PackageInfo {
	type pkgAccum struct {
		files []string
	}
	pkgs := map[string]*pkgAccum{}

	skipDir := func(name string) bool {
		return name == "node_modules" || name == ".next" || name == "dist" ||
			name == "build" || name == ".git" || name == "out" ||
			name == "coverage" || strings.HasPrefix(name, ".")
	}

	// Walk the tree breadth-first via os.WalkDir is simpler.
	filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if path != srcDir && skipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isTSSource(info.Name()) {
			return nil
		}
		dir := filepath.Dir(path)
		rel, err := filepath.Rel(srcDir, dir)
		if err != nil {
			return nil
		}
		if pkgs[rel] == nil {
			pkgs[rel] = &pkgAccum{}
		}
		pkgs[rel].files = append(pkgs[rel].files, info.Name())
		return nil
	})

	out := make([]*codev0.PackageInfo, 0, len(pkgs))
	relPaths := make([]string, 0, len(pkgs))
	for rel := range pkgs {
		relPaths = append(relPaths, rel)
	}
	sort.Strings(relPaths)
	for _, rel := range relPaths {
		accum := pkgs[rel]
		sort.Strings(accum.files)
		name := rel
		if name == "." {
			name = "."
		}
		out = append(out, &codev0.PackageInfo{
			Name:         name,
			RelativePath: rel,
			Files:        accum.files,
			Imports:      s.scanTSImports(srcDir, rel, accum.files),
		})
	}
	return out
}

func isTSSource(name string) bool {
	switch filepath.Ext(name) {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		// Skip type-declaration files; they're API surfaces, not
		// runtime imports — would over-count package usage.
		if strings.HasSuffix(name, ".d.ts") {
			return false
		}
		return true
	}
	return false
}

// scanTSImports extracts the set of imported MODULE specifiers
// from each .ts/.tsx/.js/.jsx file in the package. Returns a
// deduplicated, sorted list of bare-module specifiers (relative
// imports like `./foo` and `../foo/bar` are filtered out —
// they're internal to the project).
//
// Matches:
//
//	import x from 'mod'
//	import { x } from 'mod'
//	import * as ns from 'mod'
//	import 'side-effect-only'
//	import type { X } from 'mod'
//	export { x } from 'mod'    // re-export, counts as a use
//	const x = require('mod')   // CommonJS
//
// Bare specifiers preserve scope prefixes (`@scope/pkg`) and
// subpath imports (`react/jsx-runtime`). Aggregation to module
// root is the consumer's job — Mind's ImportsCollector handles
// it via topLevelDependency.
func (s *TypeScriptCodeServer) scanTSImports(srcDir, pkgRel string, files []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(spec string) {
		spec = strings.TrimSpace(spec)
		spec = strings.Trim(spec, `"'`)
		if spec == "" || strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") {
			// Relative or absolute path → internal, skip.
			return
		}
		if seen[spec] {
			return
		}
		seen[spec] = true
		out = append(out, spec)
	}
	for _, file := range files {
		path := filepath.Join(srcDir, pkgRel, file)
		if pkgRel == "." {
			path = filepath.Join(srcDir, file)
		}
		data, err := s.FS.ReadFile(path)
		if err != nil {
			continue
		}
		for _, m := range tsImportRe.FindAllStringSubmatch(string(data), -1) {
			// Two alternative capture groups — `import...from "X"`
			// and the side-effect-only `import "X"` form.
			if m[1] != "" {
				add(m[1])
			} else if m[2] != "" {
				add(m[2])
			}
		}
		for _, m := range tsRequireRe.FindAllStringSubmatch(string(data), -1) {
			if m[1] != "" {
				add(m[1])
			}
		}
	}
	sort.Strings(out)
	return out
}

// tsImportRe matches both ESM forms:
//
//	import ... from "X" / 'X'
//	import "X" / 'X'   (side-effect only)
//	export ... from "X" / 'X'  (re-export)
//
// Group 1 captures the "from" form's quoted module; group 2
// captures the side-effect form's quoted module. Heuristic: the
// pattern is line-anchored to skip false matches inside string
// literals or comments in the middle of expressions.
var tsImportRe = regexp.MustCompile(`(?m)^\s*(?:import|export)\b[^'"\n;]*?\bfrom\s+["']([^"']+)["']|^\s*import\s+["']([^"']+)["']`)

// tsRequireRe matches CommonJS `require("X")` calls. Less common
// in modern TS but still seen in config files and Node-only
// modules.
var tsRequireRe = regexp.MustCompile(`\brequire\s*\(\s*["']([^"']+)["']\s*\)`)

func (s *TypeScriptCodeServer) computeTSFileHashes(srcDir string) map[string]string {
	hashes := make(map[string]string)
	filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !isTSSource(info.Name()) && !strings.HasSuffix(path, "package.json") {
			return nil
		}
		for _, skip := range []string{"node_modules", ".next", "dist", "build", ".git", "out"} {
			if strings.Contains(path, "/"+skip+"/") {
				return nil
			}
		}
		data, err := s.FS.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(srcDir, path)
		hashes[rel] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	return hashes
}
