// Package code provides the DefaultCodeServer: a complete, language-agnostic
// implementation of the unified Code.Execute RPC. Plugins embed it and override
// only language-specific operations (LSP, Fix, dependency management).
package code

import (
	"context"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// OperationHandler processes a single code operation within Execute.
type OperationHandler func(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error)

// ServerOption configures a DefaultCodeServer.
type ServerOption func(*DefaultCodeServer)

// WithVFS sets a custom VFS backend. Defaults to LocalVFS.
func WithVFS(vfs VFS) ServerOption {
	return func(s *DefaultCodeServer) { s.FS = vfs }
}

// DefaultCodeServer implements every Code.Execute operation with sensible,
// language-agnostic defaults. Plugins embed this and call Override to replace
// handlers for operations they specialize (e.g. Fix, ListSymbols, deps).
type DefaultCodeServer struct {
	codev0.UnimplementedCodeServer

	SourceDir string
	FS        VFS
	overrides map[string]OperationHandler
}

// NewDefaultCodeServer creates a server rooted at sourceDir.
// By default it uses LocalVFS (direct os.* calls).
func NewDefaultCodeServer(sourceDir string, opts ...ServerOption) *DefaultCodeServer {
	s := &DefaultCodeServer{
		SourceDir: sourceDir,
		FS:        LocalVFS{},
		overrides: make(map[string]OperationHandler),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// FileOps returns a FileOperation view over this server's VFS and root.
// Use it when only file operations (read, write, list, delete, move, copy,
// search, replace) are needed, so Search and ReplaceInFile always run on the
// virtual layer (e.g. overlay) instead of disk.
func (s *DefaultCodeServer) FileOps() FileOperation {
	return NewFileOps(s.FS, s.SourceDir)
}

// GetVFS returns the VFS backend used by this server (implements VFSProvider).
func (s *DefaultCodeServer) GetVFS() VFS { return s.FS }

// GetSourceDir returns the root directory for this server (implements VFSProvider).
func (s *DefaultCodeServer) GetSourceDir() string { return s.SourceDir }

// Override registers a custom handler for an operation name.
// Names match the oneof field names: "read_file", "write_file", "fix", etc.
func (s *DefaultCodeServer) Override(op string, handler OperationHandler) {
	s.overrides[op] = handler
}

// Execute dispatches the incoming CodeRequest to the appropriate handler.
func (s *DefaultCodeServer) Execute(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	opName := OperationName(req)
	if opName == "" {
		return nil, status.Error(codes.InvalidArgument, "empty CodeRequest: no operation set")
	}
	if h, ok := s.overrides[opName]; ok {
		return h(ctx, req)
	}
	return s.dispatch(ctx, req)
}

func (s *DefaultCodeServer) dispatch(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	switch op := req.Operation.(type) {

	// --- Core operations (implemented here) ---

	case *codev0.CodeRequest_ApplyEdit:
		return s.applyEdit(ctx, op.ApplyEdit)
	case *codev0.CodeRequest_GetProjectInfo:
		return s.getProjectInfo(ctx, op.GetProjectInfo)
	case *codev0.CodeRequest_Fix:
		return s.fixDefault(ctx, op.Fix)

	// --- LSP operations (stubs -- plugins override) ---

	case *codev0.CodeRequest_ListSymbols:
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_ListSymbols{ListSymbols: &codev0.ListSymbolsResponse{
			Status: &codev0.ListSymbolsStatus{State: codev0.ListSymbolsStatus_ERROR, Message: "LSP not available: no language plugin override"},
		}}}, nil
	case *codev0.CodeRequest_GetDiagnostics:
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetDiagnostics{GetDiagnostics: &codev0.GetDiagnosticsResponse{}}}, nil
	case *codev0.CodeRequest_GoToDefinition:
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GoToDefinition{GoToDefinition: &codev0.GoToDefinitionResponse{}}}, nil
	case *codev0.CodeRequest_FindReferences:
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_FindReferences{FindReferences: &codev0.FindReferencesResponse{}}}, nil
	case *codev0.CodeRequest_RenameSymbol:
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_RenameSymbol{RenameSymbol: &codev0.RenameSymbolResponse{
			Success: false, Error: "rename not available: no language plugin override",
		}}}, nil
	case *codev0.CodeRequest_GetHoverInfo:
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetHoverInfo{GetHoverInfo: &codev0.GetHoverInfoResponse{}}}, nil
	case *codev0.CodeRequest_GetCompletions:
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetCompletions{GetCompletions: &codev0.GetCompletionsResponse{}}}, nil

	// --- Dependency management (stubs -- plugins override) ---

	case *codev0.CodeRequest_ListDependencies:
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_ListDependencies{ListDependencies: &codev0.ListDependenciesResponse{
			Error: "dependency listing not available: no language plugin override",
		}}}, nil
	case *codev0.CodeRequest_AddDependency:
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_AddDependency{AddDependency: &codev0.AddDependencyResponse{
			Success: false, Error: "add dependency not available: no language plugin override",
		}}}, nil
	case *codev0.CodeRequest_RemoveDependency:
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_RemoveDependency{RemoveDependency: &codev0.RemoveDependencyResponse{
			Success: false, Error: "remove dependency not available: no language plugin override",
		}}}, nil

	default:
		return nil, status.Errorf(codes.Unimplemented, "unknown operation: %T", req.Operation)
	}
}

// --- File operations ---

func (s *DefaultCodeServer) readFile(_ context.Context, req *codev0.ReadFileRequest) (*codev0.CodeResponse, error) {
	absPath := filepath.Join(s.SourceDir, req.Path)
	data, err := s.FS.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &codev0.CodeResponse{Result: &codev0.CodeResponse_ReadFile{ReadFile: &codev0.ReadFileResponse{Exists: false}}}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", req.Path, err)
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_ReadFile{ReadFile: &codev0.ReadFileResponse{Content: string(data), Exists: true}}}, nil
}

func (s *DefaultCodeServer) writeFile(_ context.Context, req *codev0.WriteFileRequest) (*codev0.CodeResponse, error) {
	absPath := filepath.Join(s.SourceDir, req.Path)
	if err := s.FS.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_WriteFile{WriteFile: &codev0.WriteFileResponse{Success: false, Error: err.Error()}}}, nil
	}
	if err := s.FS.WriteFile(absPath, []byte(req.Content), 0o644); err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_WriteFile{WriteFile: &codev0.WriteFileResponse{Success: false, Error: err.Error()}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_WriteFile{WriteFile: &codev0.WriteFileResponse{Success: true}}}, nil
}

func (s *DefaultCodeServer) listFiles(_ context.Context, req *codev0.ListFilesRequest) (*codev0.CodeResponse, error) {
	root := s.SourceDir
	base := root
	if req.Path != "" {
		base = filepath.Join(root, req.Path)
	}

	extSet := make(map[string]bool, len(req.Extensions))
	for _, ext := range req.Extensions {
		e := ext
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		extSet[e] = true
	}

	var files []*codev0.FileInfo
	walkFn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && path != base {
			return fs.SkipDir
		}
		if !req.Recursive && d.IsDir() && path != base {
			return fs.SkipDir
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		if len(extSet) > 0 && !d.IsDir() && !extSet[filepath.Ext(path)] {
			return nil
		}
		info, _ := d.Info()
		var size int64
		if info != nil {
			size = info.Size()
		}
		files = append(files, &codev0.FileInfo{Path: rel, SizeBytes: size, IsDirectory: d.IsDir()})
		return nil
	}
	if err := s.FS.WalkDir(base, walkFn); err != nil {
		return nil, fmt.Errorf("walking %s: %w", base, err)
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_ListFiles{ListFiles: &codev0.ListFilesResponse{Files: files}}}, nil
}

func (s *DefaultCodeServer) deleteFile(_ context.Context, req *codev0.DeleteFileRequest) (*codev0.CodeResponse, error) {
	absPath := filepath.Join(s.SourceDir, req.Path)
	if err := s.FS.Remove(absPath); err != nil {
		msg := err.Error()
		if os.IsNotExist(err) {
			msg = "file not found"
		}
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_DeleteFile{DeleteFile: &codev0.DeleteFileResponse{Success: false, Error: msg}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_DeleteFile{DeleteFile: &codev0.DeleteFileResponse{Success: true}}}, nil
}

func (s *DefaultCodeServer) moveFile(_ context.Context, req *codev0.MoveFileRequest) (*codev0.CodeResponse, error) {
	oldAbs := filepath.Join(s.SourceDir, req.OldPath)
	newAbs := filepath.Join(s.SourceDir, req.NewPath)
	if err := s.FS.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_MoveFile{MoveFile: &codev0.MoveFileResponse{Success: false, Error: fmt.Sprintf("mkdir: %v", err)}}}, nil
	}
	if err := s.FS.Rename(oldAbs, newAbs); err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_MoveFile{MoveFile: &codev0.MoveFileResponse{Success: false, Error: err.Error()}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_MoveFile{MoveFile: &codev0.MoveFileResponse{Success: true}}}, nil
}

func (s *DefaultCodeServer) createFile(_ context.Context, req *codev0.CreateFileRequest) (*codev0.CodeResponse, error) {
	absPath := filepath.Join(s.SourceDir, req.Path)
	if !req.Overwrite {
		if _, err := s.FS.Stat(absPath); err == nil {
			return &codev0.CodeResponse{Result: &codev0.CodeResponse_CreateFile{CreateFile: &codev0.CreateFileResponse{Success: false, Error: "file already exists"}}}, nil
		}
	}
	if err := s.FS.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_CreateFile{CreateFile: &codev0.CreateFileResponse{Success: false, Error: fmt.Sprintf("mkdir: %v", err)}}}, nil
	}
	if err := s.FS.WriteFile(absPath, []byte(req.Content), 0o644); err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_CreateFile{CreateFile: &codev0.CreateFileResponse{Success: false, Error: err.Error()}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_CreateFile{CreateFile: &codev0.CreateFileResponse{Success: true}}}, nil
}

// --- Search ---

func (s *DefaultCodeServer) search(ctx context.Context, req *codev0.SearchRequest) (*codev0.CodeResponse, error) {
	opts := SearchOpts{
		Pattern:         req.Pattern,
		Literal:         req.Literal,
		CaseInsensitive: req.CaseInsensitive,
		Path:            req.Path,
		Extensions:      req.Extensions,
		Exclude:         req.Exclude,
		MaxResults:      int(req.MaxResults),
		ContextLines:    int(req.ContextLines),
	}

	var result *SearchResult
	var err error
	if _, ok := s.FS.(LocalVFS); ok {
		result, err = Search(ctx, s.SourceDir, opts)
	} else {
		result, err = SearchVFS(ctx, s.FS, s.SourceDir, opts)
	}
	if err != nil {
		return nil, err
	}

	var matches []*codev0.SearchMatch
	for _, m := range result.Matches {
		matches = append(matches, &codev0.SearchMatch{File: m.File, Line: int32(m.Line), Text: m.Text})
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_Search{Search: &codev0.SearchResponse{
		Matches: matches, Truncated: result.Truncated, TotalMatches: int32(len(matches)),
	}}}, nil
}

// --- ApplyEdit ---

func (s *DefaultCodeServer) applyEdit(_ context.Context, req *codev0.ApplyEditRequest) (*codev0.CodeResponse, error) {
	absPath := filepath.Join(s.SourceDir, req.File)
	data, err := s.FS.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &codev0.CodeResponse{Result: &codev0.CodeResponse_ApplyEdit{ApplyEdit: &codev0.ApplyEditResponse{
				Success: false, Error: fmt.Sprintf("file not found: %s", req.File),
			}}}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", req.File, err)
	}

	result := SmartEdit(string(data), req.Find, req.Replace)
	if !result.OK {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_ApplyEdit{ApplyEdit: &codev0.ApplyEditResponse{
			Success: false, Error: "FIND block does not match any content in the file",
		}}}, nil
	}

	if err := s.FS.WriteFile(absPath, []byte(result.Content), 0o644); err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_ApplyEdit{ApplyEdit: &codev0.ApplyEditResponse{
			Success: false, Error: fmt.Sprintf("write: %v", err),
		}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_ApplyEdit{ApplyEdit: &codev0.ApplyEditResponse{
		Success: true, Content: result.Content, Strategy: result.Strategy,
	}}}, nil
}

// --- Fix (no-op default: returns file content unchanged) ---

func (s *DefaultCodeServer) fixDefault(_ context.Context, req *codev0.FixRequest) (*codev0.CodeResponse, error) {
	absPath := filepath.Join(s.SourceDir, req.File)
	data, err := s.FS.ReadFile(absPath)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_Fix{Fix: &codev0.FixResponse{
			Success: false, Error: fmt.Sprintf("file not found: %s", req.File),
		}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_Fix{Fix: &codev0.FixResponse{Success: true, Content: string(data)}}}, nil
}

// --- GetProjectInfo (generic: file walk + hashes) ---

// maxHashFileSize is the maximum file size we'll read for hashing.
// Files larger than this are skipped to avoid unbounded memory usage.
const maxHashFileSize = 10 * 1024 * 1024 // 10MB

func (s *DefaultCodeServer) getProjectInfo(_ context.Context, _ *codev0.GetProjectInfoRequest) (*codev0.CodeResponse, error) {
	info := &codev0.GetProjectInfoResponse{FileHashes: ComputeFileHashes(s.FS, s.SourceDir, nil)}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetProjectInfo{GetProjectInfo: info}}, nil
}

// ComputeFileHashes walks a directory and hashes source files.
// If extensions is nil, all files are included. Otherwise only matching extensions.
// Files larger than maxHashFileSize are skipped.
// Shared by DefaultCodeServer and language-specific servers.
func ComputeFileHashes(vfs VFS, dir string, extensions map[string]bool) map[string]string {
	hashes := make(map[string]string)
	_ = vfs.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case "vendor", ".git", "node_modules", "testdata", "__pycache__",
				"dist", "build", "target", ".cache":
				return fs.SkipDir
			}
			return nil
		}
		// Filter by extension if specified
		if extensions != nil {
			if !extensions[filepath.Ext(name)] {
				return nil
			}
		}
		// Skip large files
		info, statErr := vfs.Stat(path)
		if statErr != nil || info.Size() > maxHashFileSize {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		data, readErr := vfs.ReadFile(path)
		if readErr != nil {
			return nil
		}
		h := sha256.Sum256(data)
		hashes[rel] = hex.EncodeToString(h[:])
		return nil
	})
	return hashes
}

// --- Git operations ---

func (s *DefaultCodeServer) gitLog(ctx context.Context, req *codev0.GitLogRequest) (*codev0.CodeResponse, error) {
	maxCount := int(req.MaxCount)
	if maxCount <= 0 {
		maxCount = 50
	}

	args := []string{"log", fmt.Sprintf("--max-count=%d", maxCount),
		"--format=%H%x00%h%x00%an%x00%aI%x00%s%x00", "--numstat"}
	if req.Since != "" {
		args = append(args, "--since="+req.Since)
	}
	if req.Ref != "" {
		args = append(args, req.Ref)
	}
	if req.Path != "" {
		args = append(args, "--", req.Path)
	}

	out, err := s.runGit(ctx, args...)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GitLog{GitLog: &codev0.GitLogResponse{Error: err.Error()}}}, nil
	}

	commits := parseGitLog(out)
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GitLog{GitLog: &codev0.GitLogResponse{Commits: commits}}}, nil
}

func parseGitLog(output string) []*codev0.GitCommit {
	var commits []*codev0.GitCommit
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var current *codev0.GitCommit
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x00")
		if len(parts) >= 5 && len(parts[0]) == 40 {
			current = &codev0.GitCommit{
				Hash:      parts[0],
				ShortHash: parts[1],
				Author:    parts[2],
				Date:      parts[3],
				Message:   parts[4],
			}
			commits = append(commits, current)
		} else if current != nil && strings.Contains(line, "\t") {
			current.FilesChanged++
		}
	}
	return commits
}

func (s *DefaultCodeServer) gitDiff(ctx context.Context, req *codev0.GitDiffRequest) (*codev0.CodeResponse, error) {
	args := []string{"diff"}
	if req.ContextLines > 0 {
		args = append(args, fmt.Sprintf("-U%d", req.ContextLines))
	}
	if req.StatOnly {
		args = append(args, "--stat", "--numstat")
	}
	if req.BaseRef != "" {
		if req.HeadRef != "" {
			args = append(args, req.BaseRef, req.HeadRef)
		} else {
			args = append(args, req.BaseRef)
		}
	}
	if req.Path != "" {
		args = append(args, "--", req.Path)
	}

	out, err := s.runGit(ctx, args...)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GitDiff{GitDiff: &codev0.GitDiffResponse{Error: err.Error()}}}, nil
	}

	files := parseGitDiffStats(out, req.StatOnly)
	diff := out
	if req.StatOnly {
		diff = ""
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GitDiff{GitDiff: &codev0.GitDiffResponse{Diff: diff, Files: files}}}, nil
}

func parseGitDiffStats(output string, statOnly bool) []*codev0.GitDiffFile {
	if !statOnly {
		return nil
	}
	var files []*codev0.GitDiffFile
	for _, line := range strings.Split(output, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		adds, errA := strconv.Atoi(parts[0])
		dels, errD := strconv.Atoi(parts[1])
		if errA != nil || errD != nil {
			continue
		}
		st := "modified"
		if adds > 0 && dels == 0 {
			st = "added"
		} else if adds == 0 && dels > 0 {
			st = "deleted"
		}
		files = append(files, &codev0.GitDiffFile{
			Path: parts[2], Additions: int32(adds), Deletions: int32(dels), Status: st,
		})
	}
	return files
}

func (s *DefaultCodeServer) gitShow(ctx context.Context, req *codev0.GitShowRequest) (*codev0.CodeResponse, error) {
	ref := req.Ref
	if ref == "" {
		ref = "HEAD"
	}
	out, err := s.runGit(ctx, "show", ref+":"+req.Path)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "does not exist") || strings.Contains(errStr, "fatal: path") {
			return &codev0.CodeResponse{Result: &codev0.CodeResponse_GitShow{GitShow: &codev0.GitShowResponse{Exists: false}}}, nil
		}
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GitShow{GitShow: &codev0.GitShowResponse{Error: errStr}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GitShow{GitShow: &codev0.GitShowResponse{Content: out, Exists: true}}}, nil
}

func (s *DefaultCodeServer) gitBlame(ctx context.Context, req *codev0.GitBlameRequest) (*codev0.CodeResponse, error) {
	args := []string{"blame", "--porcelain"}
	if req.StartLine > 0 {
		end := req.EndLine
		if end <= 0 {
			end = req.StartLine + 1000
		}
		args = append(args, fmt.Sprintf("-L%d,%d", req.StartLine, end))
	}
	args = append(args, "--", req.Path)

	out, err := s.runGit(ctx, args...)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_GitBlame{GitBlame: &codev0.GitBlameResponse{Error: err.Error()}}}, nil
	}

	blameLines := parseGitBlame(out)
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GitBlame{GitBlame: &codev0.GitBlameResponse{Lines: blameLines}}}, nil
}

func parseGitBlame(output string) []*codev0.GitBlameLine {
	var result []*codev0.GitBlameLine
	lines := strings.Split(output, "\n")
	var currentHash, currentAuthor, currentDate string
	lineNum := int32(0)
	for _, line := range lines {
		if len(line) >= 40 && !strings.HasPrefix(line, "\t") {
			parts := strings.Fields(line)
			if len(parts) >= 3 && len(parts[0]) == 40 {
				currentHash = parts[0]
				if n, err := strconv.Atoi(parts[2]); err == nil {
					lineNum = int32(n)
				}
			}
		}
		if strings.HasPrefix(line, "author ") {
			currentAuthor = strings.TrimPrefix(line, "author ")
		}
		if strings.HasPrefix(line, "author-time ") {
			currentDate = strings.TrimPrefix(line, "author-time ")
		}
		if strings.HasPrefix(line, "\t") {
			result = append(result, &codev0.GitBlameLine{
				Hash: currentHash, Author: currentAuthor, Date: currentDate,
				Line: lineNum, Content: strings.TrimPrefix(line, "\t"),
			})
		}
	}
	return result
}

func (s *DefaultCodeServer) runGit(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = s.SourceDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// --- Helpers ---

// OperationName returns the oneof field name for dispatch/override keys.
// This is the SINGLE source of truth for operation name mapping.
// If you add a new operation to the proto, add it here AND in dispatch().
// The test TestOperationName_MatchesDispatch verifies they stay in sync.
func OperationName(req *codev0.CodeRequest) string {
	switch req.Operation.(type) {
	// Core operations
	case *codev0.CodeRequest_ApplyEdit:
		return "apply_edit"
	case *codev0.CodeRequest_GetProjectInfo:
		return "get_project_info"
	case *codev0.CodeRequest_Fix:
		return "fix"
	// LSP stubs
	case *codev0.CodeRequest_ListSymbols:
		return "list_symbols"
	case *codev0.CodeRequest_GetDiagnostics:
		return "get_diagnostics"
	case *codev0.CodeRequest_GoToDefinition:
		return "go_to_definition"
	case *codev0.CodeRequest_FindReferences:
		return "find_references"
	case *codev0.CodeRequest_RenameSymbol:
		return "rename_symbol"
	case *codev0.CodeRequest_GetHoverInfo:
		return "get_hover_info"
	case *codev0.CodeRequest_GetCompletions:
		return "get_completions"
	// Dependency stubs
	case *codev0.CodeRequest_ListDependencies:
		return "list_dependencies"
	case *codev0.CodeRequest_AddDependency:
		return "add_dependency"
	case *codev0.CodeRequest_RemoveDependency:
		return "remove_dependency"
	default:
		return ""
	}
}
