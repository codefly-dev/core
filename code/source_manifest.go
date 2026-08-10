package code

// ARCHITECTURE: source manifests are computed inside Codefly because their
// identities require reading project artifacts or inspecting Git objects.
// Callers receive only typed metadata and digests; project bytes never cross
// this boundary merely so an orchestrator can build or compare snapshots.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

func (s *DefaultCodeServer) getSourceManifest(ctx context.Context, req *codev0.GetSourceManifestRequest) (*codev0.CodeResponse, error) {
	if req == nil {
		req = &codev0.GetSourceManifestRequest{}
	}
	var (
		value *basev0.SourceManifest
		err   error
	)
	identityMode := req.GetIdentityMode()
	if identityMode != basev0.SourceManifestIdentityMode_SOURCE_MANIFEST_IDENTITY_MODE_NATIVE && identityMode != basev0.SourceManifestIdentityMode_SOURCE_MANIFEST_IDENTITY_MODE_CONTENT_SHA256 {
		return codeFailure(
			&codev0.CodeResponse{Result: &codev0.CodeResponse_GetSourceManifest{GetSourceManifest: &basev0.SourceManifest{}}},
			basev0.FailureCode_FAILURE_CODE_INVALID_ARGUMENT,
			"code.get-source-manifest",
			fmt.Sprintf("unsupported source manifest identity mode %s", identityMode),
		), nil
	}
	if revision := strings.TrimSpace(req.GetRevision()); revision != "" {
		if err := validateGitRef(revision); err != nil {
			return nil, err
		}
		value, err = s.revisionSourceManifest(ctx, revision, identityMode)
	} else {
		value, err = s.worktreeSourceManifest(ctx)
	}
	if err != nil {
		return codeFailure(
			&codev0.CodeResponse{Result: &codev0.CodeResponse_GetSourceManifest{GetSourceManifest: &basev0.SourceManifest{}}},
			basev0.FailureCode_FAILURE_CODE_IO_FAILED,
			"code.get-source-manifest",
			err.Error(),
		), nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetSourceManifest{GetSourceManifest: value}}, nil
}

func (s *DefaultCodeServer) worktreeSourceManifest(ctx context.Context) (*basev0.SourceManifest, error) {
	entries := make([]*basev0.SourceManifestEntry, 0, 256)
	err := s.FS.WalkDir(s.SourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != s.SourceDir && (strings.HasPrefix(entry.Name(), ".") || isGeneratedSourceDirectory(entry.Name())) {
				return fs.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(s.SourceDir, path)
		if err != nil {
			return fmt.Errorf("relative path %s: %w", path, err)
		}
		info, err := sourceEntryInfo(s.FS, path, entry)
		if err != nil {
			return fmt.Errorf("metadata %s: %w", relative, err)
		}
		kind, mode, err := worktreeEntryKind(info.Mode())
		if err != nil {
			return fmt.Errorf("metadata %s: %w", relative, err)
		}
		body, err := sourceEntryIdentityBytes(s.FS, path, kind)
		if err != nil {
			return fmt.Errorf("identity %s: %w", relative, err)
		}
		if int64(len(body)) != info.Size() {
			return fmt.Errorf("source changed while hashing %s: metadata size %d, read %d", relative, info.Size(), len(body))
		}
		digest := sha256.Sum256(body)
		entries = append(entries, &basev0.SourceManifestEntry{
			Path: filepath.ToSlash(relative), Mode: mode, Kind: kind,
			Identity: &basev0.SourceIdentity{
				Algorithm: basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_SHA256,
				Digest:    hex.EncodeToString(digest[:]), SizeBytes: int64(len(body)),
			},
			Attributes: classifySourceAttributes(filepath.ToSlash(relative), kind, body, true),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].GetPath() < entries[j].GetPath() })
	return &basev0.SourceManifest{Entries: entries}, nil
}

func sourceEntryInfo(vfs VFS, path string, entry fs.DirEntry) (os.FileInfo, error) {
	if source, ok := vfs.(interface {
		Lstat(string) (os.FileInfo, error)
	}); ok {
		return source.Lstat(path)
	}
	return entry.Info()
}

func sourceEntryIdentityBytes(vfs VFS, path string, kind basev0.SourceEntryKind) ([]byte, error) {
	if kind != basev0.SourceEntryKind_SOURCE_ENTRY_KIND_SYMLINK {
		return vfs.ReadFile(path)
	}
	source, ok := vfs.(interface{ Readlink(string) (string, error) })
	if !ok {
		return nil, fmt.Errorf("symlink target reads are unsupported by %T", vfs)
	}
	target, err := source.Readlink(path)
	return []byte(target), err
}

func worktreeEntryKind(mode os.FileMode) (basev0.SourceEntryKind, uint32, error) {
	switch {
	case mode&os.ModeSymlink != 0:
		return basev0.SourceEntryKind_SOURCE_ENTRY_KIND_SYMLINK, 0o120000, nil
	case mode.IsRegular():
		if mode.Perm()&0o111 != 0 {
			return basev0.SourceEntryKind_SOURCE_ENTRY_KIND_FILE, 0o100755, nil
		}
		return basev0.SourceEntryKind_SOURCE_ENTRY_KIND_FILE, 0o100644, nil
	default:
		return basev0.SourceEntryKind_SOURCE_ENTRY_KIND_UNSPECIFIED, 0, fmt.Errorf("unsupported source entry mode %s", mode)
	}
}

func (s *DefaultCodeServer) revisionSourceManifest(ctx context.Context, revision string, identityMode basev0.SourceManifestIdentityMode) (*basev0.SourceManifest, error) {
	resolved, err := s.runGit(ctx, "rev-parse", "--verify", revision+"^{commit}")
	if err != nil {
		return nil, err
	}
	resolved = strings.TrimSpace(resolved)
	output, err := s.runGit(ctx, "ls-tree", "-rlz", "--full-tree", resolved)
	if err != nil {
		return nil, err
	}
	records := strings.Split(output, "\x00")
	entries := make([]*basev0.SourceManifestEntry, 0, len(records))
	for _, record := range records {
		if record == "" {
			continue
		}
		entry, err := parseRevisionManifestEntry(record)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if identityMode == basev0.SourceManifestIdentityMode_SOURCE_MANIFEST_IDENTITY_MODE_CONTENT_SHA256 {
		if err := s.normalizeRevisionContentIdentities(ctx, entries); err != nil {
			return nil, err
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].GetPath() < entries[j].GetPath() })
	return &basev0.SourceManifest{Revision: resolved, Entries: entries}, nil
}

func parseRevisionManifestEntry(record string) (*basev0.SourceManifestEntry, error) {
	header, path, found := strings.Cut(record, "\t")
	if !found || path == "" {
		return nil, fmt.Errorf("invalid Git tree record %q", record)
	}
	fields := strings.Fields(header)
	if len(fields) != 4 {
		return nil, fmt.Errorf("invalid Git tree header %q", header)
	}
	modeValue, err := strconv.ParseUint(fields[0], 8, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid Git mode %q: %w", fields[0], err)
	}
	size := int64(-1)
	if fields[3] != "-" {
		size, err = strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid Git size %q: %w", fields[3], err)
		}
	}
	kind, algorithm, err := revisionEntryIdentity(fields[0], fields[1], fields[2])
	if err != nil {
		return nil, err
	}
	entry := &basev0.SourceManifestEntry{
		Path: filepath.ToSlash(path), Mode: uint32(modeValue), Kind: kind,
		Identity: &basev0.SourceIdentity{Algorithm: algorithm, Digest: fields[2], SizeBytes: size},
	}
	entry.Attributes = classifySourceAttributes(entry.GetPath(), kind, nil, false)
	return entry, nil
}

// normalizeRevisionContentIdentities streams all Git blobs through one
// cat-file process. Bytes remain inside Codefly; only SHA-256 identities and
// versioned attributes are returned to the caller. Gitlinks retain their
// native commit-object identities because they do not name file content.
func (s *DefaultCodeServer) normalizeRevisionContentIdentities(ctx context.Context, entries []*basev0.SourceManifestEntry) error {
	targets := make([]*basev0.SourceManifestEntry, 0, len(entries))
	var requests strings.Builder
	for _, entry := range entries {
		if entry.GetKind() != basev0.SourceEntryKind_SOURCE_ENTRY_KIND_FILE && entry.GetKind() != basev0.SourceEntryKind_SOURCE_ENTRY_KIND_SYMLINK {
			continue
		}
		targets = append(targets, entry)
		requests.WriteString(entry.GetIdentity().GetDigest())
		requests.WriteByte('\n')
	}
	if len(targets) == 0 {
		return nil
	}

	command := exec.CommandContext(ctx, "git", "cat-file", "--batch")
	command.Dir = s.SourceDir
	command.Stdin = strings.NewReader(requests.String())
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("git cat-file stdout: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("git cat-file start: %w", err)
	}
	completed := false
	defer func() {
		if !completed {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	reader := bufio.NewReader(stdout)
	for _, entry := range targets {
		header, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("git cat-file header for %s: %w", entry.GetPath(), err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != entry.GetIdentity().GetDigest() || fields[1] != "blob" {
			return fmt.Errorf("unexpected git cat-file header for %s: %q", entry.GetPath(), strings.TrimSpace(header))
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 {
			return fmt.Errorf("invalid git blob size for %s: %q", entry.GetPath(), fields[2])
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(reader, body); err != nil {
			return fmt.Errorf("git cat-file body for %s: %w", entry.GetPath(), err)
		}
		separator, err := reader.ReadByte()
		if err != nil || separator != '\n' {
			return fmt.Errorf("git cat-file separator for %s is invalid", entry.GetPath())
		}
		digest := sha256.Sum256(body)
		entry.Identity = &basev0.SourceIdentity{
			Algorithm: basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_SHA256,
			Digest:    hex.EncodeToString(digest[:]),
			SizeBytes: size,
		}
		entry.Attributes = classifySourceAttributes(entry.GetPath(), entry.GetKind(), body, true)
	}
	if err := command.Wait(); err != nil {
		completed = true
		return fmt.Errorf("git cat-file: %s", strings.TrimSpace(stderr.String()))
	}
	completed = true
	return nil
}

func revisionEntryIdentity(mode, objectType, digest string) (basev0.SourceEntryKind, basev0.SourceIdentityAlgorithm, error) {
	sha256Repository := len(digest) == 64
	algorithm := func(blob bool) basev0.SourceIdentityAlgorithm {
		if blob && sha256Repository {
			return basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_GIT_BLOB_SHA256
		}
		if blob {
			return basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_GIT_BLOB_SHA1
		}
		if sha256Repository {
			return basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_GIT_OBJECT_SHA256
		}
		return basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_GIT_OBJECT_SHA1
	}
	switch {
	case objectType == "blob" && (mode == "100644" || mode == "100755"):
		return basev0.SourceEntryKind_SOURCE_ENTRY_KIND_FILE, algorithm(true), nil
	case objectType == "blob" && mode == "120000":
		return basev0.SourceEntryKind_SOURCE_ENTRY_KIND_SYMLINK, algorithm(true), nil
	case objectType == "commit" && mode == "160000":
		return basev0.SourceEntryKind_SOURCE_ENTRY_KIND_GITLINK, algorithm(false), nil
	default:
		return basev0.SourceEntryKind_SOURCE_ENTRY_KIND_UNSPECIFIED, basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_UNSPECIFIED,
			fmt.Errorf("unsupported Git tree entry mode=%q type=%q", mode, objectType)
	}
}
