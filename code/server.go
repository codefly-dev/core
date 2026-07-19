// Package code provides the DefaultCodeServer: a complete, language-agnostic
// implementation of the unified Code.Execute RPC. Plugins embed it and override
// only language-specific operations (Fix, dependency management).
package code

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/codefly-dev/core/failures"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// OperationHandler processes a single code operation within Execute.
type OperationHandler func(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error)

// ServerOption configures a DefaultCodeServer.
type ServerOption func(*DefaultCodeServer)

// WithVFS sets a custom VFS backend. Defaults to LocalVFS.
func WithVFS(vfs VFS) ServerOption {
	return func(s *DefaultCodeServer) { s.FS = vfs }
}

// WithSourceFixer installs the language-aware formatter/fixer used by Fix and
// ApplyEdit. The server owns all reads and writes; the fixer only transforms an
// in-memory source snapshot.
func WithSourceFixer(fixer SourceFixer) ServerOption {
	return func(s *DefaultCodeServer) { s.sourceFixer = fixer }
}

// WithCachedFS enables in-memory file tree caching backed by fsnotify.
// Metadata operations (Stat, ReadDir, WalkDir) are served from cache.
// File reads/writes pass through to disk. Cache is kept fresh via fsnotify.
func WithCachedFS() ServerOption {
	return func(s *DefaultCodeServer) {
		s.wantCachedFS = true
	}
}

// WithTrigramIndex enables trigram-based search indexing for sub-linear text search.
// Implies WithContentCache (trigram index reads content from cache).
// Files are indexed at startup and updated incrementally via fsnotify.
func WithTrigramIndex() ServerOption {
	return func(s *DefaultCodeServer) {
		s.wantCachedFS = true
		s.wantTrigramIndex = true
		if s.contentCacheBudget <= 0 {
			s.contentCacheBudget = 200 * 1024 * 1024 // 200MB default
		}
	}
}

// WithContentCache enables in-memory file content caching on top of CachedVFS.
// File reads are served from RAM with LRU eviction. Invalidated by fsnotify.
// Implies WithCachedFS. budgetBytes is the maximum memory for cached content
// (default 200MB if 0).
func WithContentCache(budgetBytes int64) ServerOption {
	return func(s *DefaultCodeServer) {
		s.wantCachedFS = true
		if budgetBytes <= 0 {
			budgetBytes = 200 * 1024 * 1024 // 200MB default
		}
		s.contentCacheBudget = budgetBytes
	}
}

// WriteListener is called after every successful file mutation so that
// external indexes or subscribers can stay aligned with the VFS state.
//
// The listener receives the mutation kind ("write", "create", "delete",
// "move"), the relative path (destination for moves), and the new file
// content if available. For deletes and moves the content argument is
// nil. For the move kind, prevPath is the source path; it is empty
// otherwise.
//
// Errors returned by the listener are logged by the caller but do NOT
// fail the mutation; notification is best-effort.
type WriteListener func(ctx context.Context, kind, path, prevPath string, content []byte) error

// DefaultCodeServer implements every Code.Execute operation with sensible,
// language-agnostic defaults. Plugins embed this and call Override to replace
// handlers for operations they specialize (e.g. Fix, deps).
type DefaultCodeServer struct {
	codev0.UnimplementedCodeServer

	SourceDir          string
	FS                 VFS
	overrides          map[string]OperationHandler
	wantCachedFS       bool
	wantTrigramIndex   bool
	contentCacheBudget int64         // 0 = no content cache
	cachedFS           *CachedVFS    // non-nil when CachedVFS is active
	trigramIdx         *TrigramIndex // non-nil when trigram indexing is active
	nativeGit          *NativeGit    // lazily opened go-git repo
	writeListener      WriteListener // optional post-mutation hook
	sourceFixer        SourceFixer   // optional language-aware in-memory fixer
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
		if o != nil {
			o(s)
		}
	}
	// Apply CachedVFS after all options (needs final SourceDir)
	if s.wantCachedFS {
		if cached, err := NewCachedVFS(s.FS, s.SourceDir); err == nil {
			s.cachedFS = cached
			s.FS = cached
			// Wire content cache if requested
			if s.contentCacheBudget > 0 {
				cached.contentCache = NewByteLRU(s.contentCacheBudget)
			}
			// Build trigram index from all files if requested
			if s.wantTrigramIndex {
				s.trigramIdx = NewTrigramIndex()
				cached.trigramIdx = s.trigramIdx
				s.populateTrigramIndex()
			}
		}
	}
	return s
}

// Close releases resources held by the server (e.g. CachedVFS watcher, git repo).
func (s *DefaultCodeServer) Close() error {
	s.closeGit()
	if s.cachedFS != nil {
		return s.cachedFS.Close()
	}
	return nil
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

// SetWriteListener installs a post-mutation hook. It is called after
// every successful WriteFile, CreateFile, DeleteFile, and MoveFile
// dispatched through Execute.
//
// Safe to call after construction; nil clears the listener. The
// listener is invoked synchronously after the mutation commits; if
// you need async fire-and-forget, do the dispatch inside your own
// listener.
func (s *DefaultCodeServer) SetWriteListener(l WriteListener) {
	s.writeListener = l
}

// SetSourceFixer replaces the language-aware fixer used by Fix and ApplyEdit.
// Plugins normally call this once while registering their language overrides.
func (s *DefaultCodeServer) SetSourceFixer(fixer SourceFixer) {
	s.sourceFixer = fixer
}

// notifyWrite fires the installed listener (if any) and swallows any
// error it returns. Notification is best-effort; a broken listener
// should not break file I/O. Callers MUST only invoke this after a
// successful mutation.
func (s *DefaultCodeServer) notifyWrite(ctx context.Context, kind, path, prevPath string, content []byte) {
	if s.writeListener == nil {
		return
	}
	// Recover panics from a misbehaving listener so the handler returns
	// cleanly even if notification wiring is broken.
	defer func() { _ = recover() }()
	_ = s.writeListener(ctx, kind, path, prevPath, content)
}

// Execute dispatches the incoming CodeRequest to the appropriate handler.
func (s *DefaultCodeServer) Execute(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	opName := OperationName(req)
	if opName == "" {
		return nil, failures.GRPC(failures.New(basev0.FailureCode_FAILURE_CODE_INVALID_ARGUMENT, "code.execute", "empty CodeRequest: no operation set"))
	}
	if h, ok := s.overrides[opName]; ok {
		return h(ctx, req)
	}
	return s.dispatch(ctx, req)
}

func (s *DefaultCodeServer) dispatch(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	switch op := req.Operation.(type) {

	// --- File operations ---

	case *codev0.CodeRequest_ReadFile:
		return s.readFile(ctx, op.ReadFile)
	case *codev0.CodeRequest_WriteFile:
		return s.writeFile(ctx, op.WriteFile)
	case *codev0.CodeRequest_CreateFile:
		return s.createFile(ctx, op.CreateFile)
	case *codev0.CodeRequest_DeleteFile:
		return s.deleteFile(ctx, op.DeleteFile)
	case *codev0.CodeRequest_MoveFile:
		return s.moveFile(ctx, op.MoveFile)
	case *codev0.CodeRequest_ListFiles:
		return s.listFiles(ctx, op.ListFiles)
	case *codev0.CodeRequest_Search:
		return s.search(ctx, op.Search)

	// --- Git operations (native go-git with exec fallback) ---

	case *codev0.CodeRequest_GitLog:
		return s.gitLogNative(ctx, op.GitLog)
	case *codev0.CodeRequest_GitDiff:
		return s.gitDiff(ctx, op.GitDiff)
	case *codev0.CodeRequest_GitShow:
		return s.gitShowNative(ctx, op.GitShow)
	case *codev0.CodeRequest_GitBlame:
		return s.gitBlame(ctx, op.GitBlame)

	// --- Core operations ---

	case *codev0.CodeRequest_ApplyEdit:
		return s.applyEdit(ctx, op.ApplyEdit)
	case *codev0.CodeRequest_GetProjectInfo:
		return s.getProjectInfo(ctx, op.GetProjectInfo)
	case *codev0.CodeRequest_Fix:
		return s.fixDefault(ctx, op.Fix)

	// --- Dependency management (stubs -- plugins override) ---

	case *codev0.CodeRequest_ListDependencies:
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_ListDependencies{ListDependencies: &codev0.ListDependenciesResponse{}}},
			basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "code.list-dependencies", "dependency listing not available: no language plugin override"), nil
	case *codev0.CodeRequest_AddDependency:
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_AddDependency{AddDependency: &codev0.AddDependencyResponse{Success: false}}},
			basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "code.add-dependency", "add dependency not available: no language plugin override"), nil
	case *codev0.CodeRequest_RemoveDependency:
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_RemoveDependency{RemoveDependency: &codev0.RemoveDependencyResponse{Success: false}}},
			basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "code.remove-dependency", "remove dependency not available: no language plugin override"), nil

	// --- Shell execution ---

	case *codev0.CodeRequest_ShellExec:
		return s.shellExec(ctx, op.ShellExec)

	default:
		return nil, failures.GRPC(failures.New(basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "code.execute", fmt.Sprintf("unknown operation: %T", req.Operation)))
	}
}

func codeFailure(response *codev0.CodeResponse, code basev0.FailureCode, operation, message string) *codev0.CodeResponse {
	response.Failure = failures.New(code, operation, message)
	return response
}

// --- File operations ---

func (s *DefaultCodeServer) readFile(_ context.Context, req *codev0.ReadFileRequest) (*codev0.CodeResponse, error) {
	absPath, err := resolvePath(s.SourceDir, req.Path)
	if err != nil {
		return nil, err
	}
	data, err := s.FS.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &codev0.CodeResponse{Result: &codev0.CodeResponse_ReadFile{ReadFile: &codev0.ReadFileResponse{Exists: false}}}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", req.Path, err)
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_ReadFile{ReadFile: &codev0.ReadFileResponse{Content: string(data), Exists: true}}}, nil
}

func (s *DefaultCodeServer) writeFile(ctx context.Context, req *codev0.WriteFileRequest) (*codev0.CodeResponse, error) {
	absPath, err := resolvePath(s.SourceDir, req.Path)
	if err != nil {
		return nil, err
	}
	if err := s.FS.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_WriteFile{WriteFile: &codev0.WriteFileResponse{Success: false}}}, basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.write-file", err.Error()), nil
	}
	content := []byte(req.Content)
	if err := s.FS.WriteFile(absPath, content, 0o644); err != nil {
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_WriteFile{WriteFile: &codev0.WriteFileResponse{Success: false}}}, basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.write-file", err.Error()), nil
	}
	s.notifyWrite(ctx, "write", req.Path, "", content)
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_WriteFile{WriteFile: &codev0.WriteFileResponse{Success: true}}}, nil
}

func (s *DefaultCodeServer) listFiles(_ context.Context, req *codev0.ListFilesRequest) (*codev0.CodeResponse, error) {
	root := s.SourceDir
	base := root
	if req.Path != "" {
		var err error
		base, err = resolvePath(root, req.Path)
		if err != nil {
			return nil, err
		}
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
		if d.IsDir() && path != base {
			if strings.HasPrefix(d.Name(), ".") || isGeneratedSourceDirectory(d.Name()) {
				return fs.SkipDir
			}
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

func isGeneratedSourceDirectory(name string) bool {
	switch name {
	case "vendor", "node_modules", "target", "dist", "build", "__pycache__", ".cache":
		return true
	default:
		return false
	}
}

func (s *DefaultCodeServer) deleteFile(ctx context.Context, req *codev0.DeleteFileRequest) (*codev0.CodeResponse, error) {
	absPath, err := resolvePath(s.SourceDir, req.Path)
	if err != nil {
		return nil, err
	}
	if err := s.FS.Remove(absPath); err != nil {
		msg := err.Error()
		failureCode := basev0.FailureCode_FAILURE_CODE_IO_FAILED
		if os.IsNotExist(err) {
			msg = "file not found"
			failureCode = basev0.FailureCode_FAILURE_CODE_NOT_FOUND
		}
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_DeleteFile{DeleteFile: &codev0.DeleteFileResponse{Success: false}}}, failureCode, "code.delete-file", msg), nil
	}
	s.notifyWrite(ctx, "delete", req.Path, "", nil)
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_DeleteFile{DeleteFile: &codev0.DeleteFileResponse{Success: true}}}, nil
}

func (s *DefaultCodeServer) moveFile(ctx context.Context, req *codev0.MoveFileRequest) (*codev0.CodeResponse, error) {
	oldAbs, err := resolvePath(s.SourceDir, req.OldPath)
	if err != nil {
		return nil, err
	}
	newAbs, err := resolvePath(s.SourceDir, req.NewPath)
	if err != nil {
		return nil, err
	}
	if err := s.FS.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_MoveFile{MoveFile: &codev0.MoveFileResponse{Success: false}}}, basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.move-file", fmt.Sprintf("mkdir: %v", err)), nil
	}
	if err := s.FS.Rename(oldAbs, newAbs); err != nil {
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_MoveFile{MoveFile: &codev0.MoveFileResponse{Success: false}}}, basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.move-file", err.Error()), nil
	}
	// After a move, downstream consumers need the destination content
	// and the previous path. We read the new file to give the listener concrete content; a read
	// failure degrades to a nil-content notification rather than
	// failing the move itself.
	var content []byte
	if data, err := s.FS.ReadFile(newAbs); err == nil {
		content = data
	}
	s.notifyWrite(ctx, "move", req.NewPath, req.OldPath, content)
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_MoveFile{MoveFile: &codev0.MoveFileResponse{Success: true}}}, nil
}

func (s *DefaultCodeServer) createFile(ctx context.Context, req *codev0.CreateFileRequest) (*codev0.CodeResponse, error) {
	absPath, err := resolvePath(s.SourceDir, req.Path)
	if err != nil {
		return nil, err
	}
	if !req.Overwrite {
		if _, err := s.FS.Stat(absPath); err == nil {
			return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_CreateFile{CreateFile: &codev0.CreateFileResponse{Success: false}}}, basev0.FailureCode_FAILURE_CODE_ALREADY_EXISTS, "code.create-file", "file already exists"), nil
		}
	}
	if err := s.FS.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_CreateFile{CreateFile: &codev0.CreateFileResponse{Success: false}}}, basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.create-file", fmt.Sprintf("mkdir: %v", err)), nil
	}
	content := []byte(req.Content)
	if err := s.FS.WriteFile(absPath, content, 0o644); err != nil {
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_CreateFile{CreateFile: &codev0.CreateFileResponse{Success: false}}}, basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.create-file", err.Error()), nil
	}
	s.notifyWrite(ctx, "create", req.Path, "", content)
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_CreateFile{CreateFile: &codev0.CreateFileResponse{Success: true}}}, nil
}

// --- Search ---

func (s *DefaultCodeServer) search(ctx context.Context, req *codev0.SearchRequest) (*codev0.CodeResponse, error) {
	if req.Path != "" {
		if _, err := resolvePath(s.SourceDir, req.Path); err != nil {
			return nil, err
		}
	}
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
	if s.trigramIdx != nil {
		// Trigram-accelerated search: find candidates via index, then regex match.
		result, err = SearchTrigram(ctx, s.FS, s.trigramIdx, s.SourceDir, opts)
	} else if _, ok := s.FS.(LocalVFS); ok {
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

func (s *DefaultCodeServer) applyEdit(ctx context.Context, req *codev0.ApplyEditRequest) (*codev0.CodeResponse, error) {
	absPath, err := resolvePath(s.SourceDir, req.File)
	if err != nil {
		return nil, err
	}
	data, err := s.FS.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_ApplyEdit{ApplyEdit: &codev0.ApplyEditResponse{Success: false}}},
				basev0.FailureCode_FAILURE_CODE_NOT_FOUND, "code.apply-edit", fmt.Sprintf("file not found: %s", req.File)), nil
		}
		return nil, fmt.Errorf("reading %s: %w", req.File, err)
	}

	result := SmartEdit(string(data), req.Find, req.Replace)
	if !result.OK {
		errorMessage := "FIND block does not match any content in the file"
		if result.Strategy == "ambiguous" {
			errorMessage = "FIND block matches multiple regions; include more surrounding context"
		}
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_ApplyEdit{ApplyEdit: &codev0.ApplyEditResponse{Success: false}}},
			basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.apply-edit", errorMessage), nil
	}

	content := []byte(result.Content)
	var actions []string
	var output string
	if req.GetFixMode() != basev0.FixMode_FIX_MODE_NONE && s.sourceFixer != nil {
		fixed, fixErr := s.sourceFixer(ctx, FixInput{Path: req.GetFile(), Content: content, Mode: req.GetFixMode()})
		if fixErr != nil {
			return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_ApplyEdit{ApplyEdit: &codev0.ApplyEditResponse{Success: false}}},
				basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED, "code.apply-edit.fix", fixErr.Error()), nil
		}
		content = fixed.Content
		actions = fixed.Actions
		output = boundedFixOutput(fixed.Output)
	}

	changed := !bytes.Equal(data, content)
	wrote := false
	if !req.GetDryRun() && changed {
		if err := s.FS.WriteFile(absPath, content, 0o644); err != nil {
			return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_ApplyEdit{ApplyEdit: &codev0.ApplyEditResponse{Success: false}}},
				basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.apply-edit", fmt.Sprintf("write: %v", err)), nil
		}
		wrote = true
		s.notifyWrite(ctx, "write", req.File, "", content)
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_ApplyEdit{ApplyEdit: &codev0.ApplyEditResponse{
		Success:      true,
		Content:      string(content),
		Strategy:     result.Strategy,
		FixActions:   actions,
		Changed:      changed,
		BeforeSha256: sourceDigest(data),
		AfterSha256:  sourceDigest(content),
		Wrote:        wrote,
		Output:       output,
	}}}, nil
}

// --- Fix ---

func (s *DefaultCodeServer) fixDefault(ctx context.Context, req *codev0.FixRequest) (*codev0.CodeResponse, error) {
	absPath, err := resolvePath(s.SourceDir, req.File)
	if err != nil {
		return nil, err
	}
	data, err := s.FS.ReadFile(absPath)
	if err != nil {
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_Fix{Fix: &codev0.FixResponse{Success: false}}},
			basev0.FailureCode_FAILURE_CODE_NOT_FOUND, "code.fix", fmt.Sprintf("file not found: %s", req.File)), nil
	}
	if req.GetMode() == basev0.FixMode_FIX_MODE_NONE {
		digest := sourceDigest(data)
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_Fix{Fix: &codev0.FixResponse{
			Success: true, Content: string(data), BeforeSha256: digest, AfterSha256: digest,
		}}}, nil
	}
	if s.sourceFixer == nil {
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_Fix{Fix: &codev0.FixResponse{Success: false}}},
			basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "code.fix", "language-aware source fixer is not configured"), nil
	}
	fixed, fixErr := s.sourceFixer(ctx, FixInput{Path: req.GetFile(), Content: data, Mode: req.GetMode()})
	if fixErr != nil {
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_Fix{Fix: &codev0.FixResponse{Success: false}}},
			basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED, "code.fix", fixErr.Error()), nil
	}
	changed := !bytes.Equal(data, fixed.Content)
	wrote := false
	if !req.GetDryRun() && changed {
		if err := s.FS.WriteFile(absPath, fixed.Content, 0o644); err != nil {
			return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_Fix{Fix: &codev0.FixResponse{Success: false}}},
				basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.fix", fmt.Sprintf("write: %v", err)), nil
		}
		wrote = true
		s.notifyWrite(ctx, "write", req.File, "", fixed.Content)
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_Fix{Fix: &codev0.FixResponse{
		Success:      true,
		Content:      string(fixed.Content),
		Actions:      fixed.Actions,
		Changed:      changed,
		BeforeSha256: sourceDigest(data),
		AfterSha256:  sourceDigest(fixed.Content),
		Wrote:        wrote,
		Output:       boundedFixOutput(fixed.Output),
	}}}, nil
}

func sourceDigest(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
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
	if err := validateGitRef(req.Ref); err != nil {
		return nil, err
	}
	if err := validateGitPath(s.SourceDir, req.Path); err != nil {
		return nil, err
	}
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
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_GitLog{GitLog: &codev0.GitLogResponse{}}}, basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED, "code.git-log", err.Error()), nil
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
	if err := validateGitRef(req.BaseRef); err != nil {
		return nil, err
	}
	if err := validateGitRef(req.HeadRef); err != nil {
		return nil, err
	}
	if err := validateGitPath(s.SourceDir, req.Path); err != nil {
		return nil, err
	}
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
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_GitDiff{GitDiff: &codev0.GitDiffResponse{}}}, basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED, "code.git-diff", err.Error()), nil
	}

	// Plain `git diff` omits UNTRACKED files entirely, so a working-tree
	// diff silently dropped any file the caller had just created — a patch
	// built from this response was missing whole new files. For the
	// working-tree case (no BaseRef), synthesize proper new-file blocks for
	// untracked files so the diff reflects the full workspace state.
	if req.BaseRef == "" && !req.StatOnly {
		if extra := s.untrackedWorkingTreeDiff(ctx, req.Path); extra != "" {
			if out != "" && !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			out += extra
		}
	}

	files := parseGitDiffStats(out, req.StatOnly)
	diff := out
	if req.StatOnly {
		diff = ""
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GitDiff{GitDiff: &codev0.GitDiffResponse{Diff: diff, Files: files}}}, nil
}

// untrackedWorkingTreeDiff returns unified new-file diff blocks for every
// untracked (non-ignored) file, optionally filtered to pathFilter. Uses
// `git diff --no-index /dev/null <file>` per file — git special-cases
// /dev/null into proper "new file mode" headers — which keeps this a pure
// READ (no `git add -N` index mutation from a diff RPC).
func (s *DefaultCodeServer) untrackedWorkingTreeDiff(ctx context.Context, pathFilter string) string {
	out, err := s.runGit(ctx, "ls-files", "--others", "--exclude-standard")
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}
	var blocks strings.Builder
	for _, p := range strings.Split(strings.TrimSpace(out), "\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if pathFilter != "" && p != pathFilter && !strings.HasPrefix(p, strings.TrimSuffix(pathFilter, "/")+"/") {
			continue
		}
		blocks.WriteString(s.gitDiffNoIndexNewFile(ctx, p))
	}
	return blocks.String()
}

// gitDiffNoIndexNewFile shells `git diff --no-index -- /dev/null <path>`.
// git exits 1 when the contents differ — for a new file that IS the diff,
// so unlike runGit this tolerates the non-zero exit and keeps stdout.
func (s *DefaultCodeServer) gitDiffNoIndexNewFile(ctx context.Context, path string) string {
	cmd := exec.CommandContext(ctx, "git", "diff", "--no-index", "--", "/dev/null", path)
	cmd.Dir = s.SourceDir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run() // exit 1 = files differ = expected for a non-empty new file
	out := stdout.String()
	if !strings.Contains(out, "diff --git") {
		return ""
	}
	return out
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
	if err := validateGitRef(req.Ref); err != nil {
		return nil, err
	}
	if err := validateGitPath(s.SourceDir, req.Path); err != nil {
		return nil, err
	}
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
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_GitShow{GitShow: &codev0.GitShowResponse{}}}, basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED, "code.git-show", errStr), nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GitShow{GitShow: &codev0.GitShowResponse{Content: out, Exists: true}}}, nil
}

func (s *DefaultCodeServer) gitBlame(ctx context.Context, req *codev0.GitBlameRequest) (*codev0.CodeResponse, error) {
	if err := validateGitPath(s.SourceDir, req.Path); err != nil {
		return nil, err
	}
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
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_GitBlame{GitBlame: &codev0.GitBlameResponse{}}}, basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED, "code.git-blame", err.Error()), nil
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

func validateGitRef(ref string) error {
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("git ref must not be an option: %q", ref)
	}
	return nil
}

func validateGitPath(root, requested string) error {
	if requested == "" {
		return nil
	}
	_, err := resolvePath(root, requested)
	return err
}

// --- Helpers ---

// OperationName returns the oneof field name for dispatch/override keys.
// This is the SINGLE source of truth for operation name mapping.
// If you add a new operation to the proto, add it here AND in dispatch().
// The test TestOperationName_MatchesDispatch verifies they stay in sync.
func OperationName(req *codev0.CodeRequest) string {
	switch req.Operation.(type) {
	// File operations
	case *codev0.CodeRequest_ReadFile:
		return "read_file"
	case *codev0.CodeRequest_WriteFile:
		return "write_file"
	case *codev0.CodeRequest_CreateFile:
		return "create_file"
	case *codev0.CodeRequest_DeleteFile:
		return "delete_file"
	case *codev0.CodeRequest_MoveFile:
		return "move_file"
	case *codev0.CodeRequest_ListFiles:
		return "list_files"
	case *codev0.CodeRequest_Search:
		return "search"
	// Git operations
	case *codev0.CodeRequest_GitLog:
		return "git_log"
	case *codev0.CodeRequest_GitDiff:
		return "git_diff"
	case *codev0.CodeRequest_GitShow:
		return "git_show"
	case *codev0.CodeRequest_GitBlame:
		return "git_blame"
	// Core operations
	case *codev0.CodeRequest_ApplyEdit:
		return "apply_edit"
	case *codev0.CodeRequest_GetProjectInfo:
		return "get_project_info"
	case *codev0.CodeRequest_Fix:
		return "fix"
	// Dependency stubs
	case *codev0.CodeRequest_ListDependencies:
		return "list_dependencies"
	case *codev0.CodeRequest_AddDependency:
		return "add_dependency"
	case *codev0.CodeRequest_RemoveDependency:
		return "remove_dependency"
	// Shell execution (THE sanctioned path for running commands)
	case *codev0.CodeRequest_ShellExec:
		return "shell_exec"
	default:
		return ""
	}
}

// populateTrigramIndex indexes all files in the source directory.
// Called once at startup when WithTrigramIndex is enabled.
func (s *DefaultCodeServer) populateTrigramIndex() {
	if s.trigramIdx == nil {
		return
	}
	_ = s.FS.WalkDir(s.SourceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && path != s.SourceDir {
				return fs.SkipDir
			}
			return nil
		}
		// Skip large files and common non-text files.
		info, infoErr := d.Info()
		if infoErr != nil || info.Size() > 1<<20 { // >1MB
			return nil
		}
		data, readErr := s.FS.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if isBinary(data) {
			return nil
		}
		rel, _ := filepath.Rel(s.SourceDir, path)
		s.trigramIdx.AddFile(rel, data)
		return nil
	})
}
