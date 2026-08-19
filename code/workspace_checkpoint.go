package code

import (
	"archive/tar"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/codefly-dev/core/failures"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"github.com/codefly-dev/core/resources"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const workspaceCheckpointSchemaVersion = 1

var workspaceCheckpointIDPattern = regexp.MustCompile(`^wcp_[0-9a-f]{64}$`)

type workspaceCheckpointFile struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Kind   string `json:"kind"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
	Link   string `json:"link,omitempty"`
}

type workspaceCheckpointCapacity struct {
	AuthorityID         string `json:"authority_id"`
	TotalBytes          uint64 `json:"total_bytes"`
	AvailableBytes      uint64 `json:"available_bytes"`
	RequiredBytes       uint64 `json:"required_bytes"`
	ProjectedBytes      uint64 `json:"projected_available_bytes"`
	ShortfallBytes      uint64 `json:"shortfall_bytes"`
	Admitted            bool   `json:"admitted"`
	AllocationUnitBytes uint64 `json:"allocation_unit_bytes"`
}

type workspaceCheckpointRestore struct {
	AuthorizationID string    `json:"authorization_id"`
	IdempotencyKey  string    `json:"idempotency_key"`
	ExpectedVersion string    `json:"expected_version"`
	PriorVersion    string    `json:"prior_version"`
	PriorDigest     string    `json:"prior_digest"`
	RestoredAt      time.Time `json:"restored_at,omitempty"`
}

type workspaceCheckpointMetadata struct {
	SchemaVersion  int                                       `json:"schema_version"`
	CheckpointID   string                                    `json:"checkpoint_id"`
	Nonce          string                                    `json:"nonce"`
	WorkspaceID    string                                    `json:"workspace_id"`
	LeaseID        string                                    `json:"lease_id"`
	SourceVersion  string                                    `json:"source_version"`
	SourceDigest   string                                    `json:"source_digest"`
	CallerID       string                                    `json:"caller_id"`
	IdempotencyKey string                                    `json:"idempotency_key"`
	Retention      basev0.WorkspaceCheckpointRetentionIntent `json:"retention"`
	CreatedAt      time.Time                                 `json:"created_at"`
	ArchiveSHA256  string                                    `json:"archive_sha256"`
	Files          []workspaceCheckpointFile                 `json:"files"`
	Capacity       workspaceCheckpointCapacity               `json:"capacity"`
	Restores       []workspaceCheckpointRestore              `json:"restores,omitempty"`
	ReleasedAt     time.Time                                 `json:"released_at,omitempty"`
}

func (s *DefaultCodeServer) createWorkspaceCheckpoint(ctx context.Context, req *basev0.CreateWorkspaceCheckpointRequest) (*codev0.CodeResponse, error) {
	s.workspaceCheckpointMu.Lock()
	defer s.workspaceCheckpointMu.Unlock()
	if failure := validateCreateWorkspaceCheckpoint(ctx, req); failure != nil {
		return createWorkspaceCheckpointResult(nil, failure), nil
	}
	store, failure := s.workspaceCheckpointStoreDir()
	if failure != nil {
		return createWorkspaceCheckpointResult(nil, failure), nil
	}
	if err := os.MkdirAll(store, 0o700); err != nil {
		return createWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.create-workspace-checkpoint", err)), nil
	}
	metadata, err := findWorkspaceCheckpointByIdempotency(store, req.GetLease(), req.GetCallerId(), req.GetIdempotencyKey())
	if err != nil {
		return createWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.create-workspace-checkpoint", err)), nil
	}
	if metadata != nil {
		if metadata.SourceVersion != req.GetExpectedWorkspaceVersion() || metadata.Retention != req.GetRetentionIntent() {
			return createWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_CONFLICT, "code.create-workspace-checkpoint", errors.New("idempotency key was already used for another checkpoint request"))), nil
		}
		return createWorkspaceCheckpointResult(workspaceCheckpointProto(metadata), nil), nil
	}
	files, state, err := s.currentWorkspaceCheckpointState(ctx)
	if err != nil {
		return createWorkspaceCheckpointResult(nil, checkpointOperationFailure("code.create-workspace-checkpoint", err)), nil
	}
	if state.GetVersion() != req.GetExpectedWorkspaceVersion() {
		return createWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.create-workspace-checkpoint", errors.New("workspace version is stale"))), nil
	}
	id, nonce, err := newWorkspaceCheckpointID(req.GetLease(), state.GetDigest())
	if err != nil {
		return createWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_INTERNAL, "code.create-workspace-checkpoint", err)), nil
	}
	metadata = &workspaceCheckpointMetadata{
		SchemaVersion: workspaceCheckpointSchemaVersion,
		CheckpointID:  id, Nonce: nonce, WorkspaceID: req.GetLease().GetWorkspaceId(), LeaseID: req.GetLease().GetLeaseId(),
		SourceVersion: state.GetVersion(), SourceDigest: state.GetDigest(), CallerID: req.GetCallerId(),
		IdempotencyKey: req.GetIdempotencyKey(), Retention: req.GetRetentionIntent(), CreatedAt: time.Now().UTC(), Files: files,
	}
	filesystem, err := resources.InspectStorageFilesystem(store)
	if err != nil {
		return createWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.create-workspace-checkpoint", err)), nil
	}
	required, err := workspaceCheckpointRequiredBytes(files, metadata, filesystem.AllocationUnitBytes)
	if err != nil {
		return createWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_RESOURCE_EXHAUSTED, "code.create-workspace-checkpoint", err)), nil
	}
	metadata.Capacity = workspaceCheckpointCapacity{
		AuthorityID: filesystem.AuthorityID, TotalBytes: filesystem.TotalBytes, AvailableBytes: filesystem.AvailableBytes,
		RequiredBytes: required, AllocationUnitBytes: filesystem.AllocationUnitBytes,
	}
	if filesystem.AvailableBytes >= required {
		metadata.Capacity.Admitted = true
		metadata.Capacity.ProjectedBytes = filesystem.AvailableBytes - required
	} else {
		metadata.Capacity.ShortfallBytes = required - filesystem.AvailableBytes
		return createWorkspaceCheckpointResult(workspaceCheckpointProto(metadata), checkpointFailure(basev0.FailureCode_FAILURE_CODE_RESOURCE_EXHAUSTED, "code.create-workspace-checkpoint", errors.New("checkpoint storage capacity was not admitted"))), nil
	}
	archivePath := workspaceCheckpointArchivePath(store, id)
	archiveTemp, err := os.CreateTemp(store, ".checkpoint-*.tar")
	if err != nil {
		return createWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.create-workspace-checkpoint", err)), nil
	}
	archiveTempPath := archiveTemp.Name()
	archiveCommitted := false
	defer func() {
		_ = archiveTemp.Close()
		_ = os.Remove(archiveTempPath)
		if !archiveCommitted {
			_ = os.Remove(archivePath)
		}
	}()
	hasher := sha256.New()
	if err := s.writeWorkspaceCheckpointArchive(ctx, io.MultiWriter(archiveTemp, hasher), files); err != nil {
		return createWorkspaceCheckpointResult(nil, checkpointOperationFailure("code.create-workspace-checkpoint", err)), nil
	}
	if err := archiveTemp.Sync(); err != nil {
		return createWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.create-workspace-checkpoint", err)), nil
	}
	if err := archiveTemp.Close(); err != nil {
		return createWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.create-workspace-checkpoint", err)), nil
	}
	metadata.ArchiveSHA256 = hex.EncodeToString(hasher.Sum(nil))
	if err := os.Rename(archiveTempPath, archivePath); err != nil {
		return createWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.create-workspace-checkpoint", err)), nil
	}
	if err := validateWorkspaceCheckpointArchiveContents(ctx, archivePath, files); err != nil {
		return createWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.create-workspace-checkpoint", fmt.Errorf("workspace changed while checkpointing: %w", err))), nil
	}
	if err := writeWorkspaceCheckpointMetadata(store, metadata); err != nil {
		return createWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.create-workspace-checkpoint", err)), nil
	}
	archiveCommitted = true
	return createWorkspaceCheckpointResult(workspaceCheckpointProto(metadata), nil), nil
}

func (s *DefaultCodeServer) restoreWorkspaceCheckpoint(ctx context.Context, req *basev0.RestoreWorkspaceCheckpointRequest) (*codev0.CodeResponse, error) {
	s.workspaceCheckpointMu.Lock()
	defer s.workspaceCheckpointMu.Unlock()
	if failure := validateRestoreWorkspaceCheckpoint(ctx, req); failure != nil {
		return restoreWorkspaceCheckpointResult(nil, failure), nil
	}
	store, failure := s.workspaceCheckpointStoreDir()
	if failure != nil {
		return restoreWorkspaceCheckpointResult(nil, failure), nil
	}
	metadata, err := readWorkspaceCheckpointMetadata(store, req.GetCheckpointId())
	if err != nil {
		return restoreWorkspaceCheckpointResult(nil, checkpointReadFailure("code.restore-workspace-checkpoint", err)), nil
	}
	if !sameWorkspaceCheckpointLease(metadata, req.GetLease()) {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_PERMISSION_DENIED, "code.restore-workspace-checkpoint", errors.New("checkpoint belongs to another workspace lease"))), nil
	}
	if !metadata.ReleasedAt.IsZero() {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.restore-workspace-checkpoint", errors.New("checkpoint has been released"))), nil
	}
	for index := range metadata.Restores {
		restored := &metadata.Restores[index]
		if restored.AuthorizationID != req.GetAuthorizationId() || restored.IdempotencyKey != req.GetIdempotencyKey() {
			continue
		}
		if restored.ExpectedVersion != req.GetExpectedWorkspaceVersion() {
			return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_CONFLICT, "code.restore-workspace-checkpoint", errors.New("idempotency key was already used for another restore request"))), nil
		}
		if !restored.RestoredAt.IsZero() {
			return restoreWorkspaceCheckpointResult(workspaceCheckpointRestoreResponse(metadata, restored), nil), nil
		}
		_, current, stateErr := s.currentWorkspaceCheckpointState(ctx)
		if stateErr != nil {
			return restoreWorkspaceCheckpointResult(nil, checkpointOperationFailure("code.restore-workspace-checkpoint", stateErr)), nil
		}
		if current.GetVersion() == metadata.SourceVersion {
			restored.RestoredAt = time.Now().UTC()
			if err := writeWorkspaceCheckpointMetadata(store, metadata); err != nil {
				return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.restore-workspace-checkpoint", err)), nil
			}
			return restoreWorkspaceCheckpointResult(workspaceCheckpointRestoreResponse(metadata, restored), nil), nil
		}
		if current.GetVersion() != restored.PriorVersion {
			return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_CONFLICT, "code.restore-workspace-checkpoint", errors.New("workspace diverged while recovering an interrupted restore"))), nil
		}
		return s.completeWorkspaceCheckpointRestore(ctx, store, metadata, restored)
	}
	_, current, err := s.currentWorkspaceCheckpointState(ctx)
	if err != nil {
		return restoreWorkspaceCheckpointResult(nil, checkpointOperationFailure("code.restore-workspace-checkpoint", err)), nil
	}
	if current.GetVersion() != req.GetExpectedWorkspaceVersion() {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.restore-workspace-checkpoint", errors.New("workspace version is stale"))), nil
	}
	metadata.Restores = append(metadata.Restores, workspaceCheckpointRestore{
		AuthorizationID: req.GetAuthorizationId(), IdempotencyKey: req.GetIdempotencyKey(), ExpectedVersion: req.GetExpectedWorkspaceVersion(),
		PriorVersion: current.GetVersion(), PriorDigest: current.GetDigest(),
	})
	restored := &metadata.Restores[len(metadata.Restores)-1]
	if err := writeWorkspaceCheckpointMetadata(store, metadata); err != nil {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.restore-workspace-checkpoint", err)), nil
	}
	return s.completeWorkspaceCheckpointRestore(ctx, store, metadata, restored)
}

func (s *DefaultCodeServer) completeWorkspaceCheckpointRestore(ctx context.Context, store string, metadata *workspaceCheckpointMetadata, restored *workspaceCheckpointRestore) (*codev0.CodeResponse, error) {
	archivePath := workspaceCheckpointArchivePath(store, metadata.CheckpointID)
	if err := verifyWorkspaceCheckpointArchive(archivePath, metadata.ArchiveSHA256); err != nil {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_INTEGRITY_FAILED, "code.restore-workspace-checkpoint", err)), nil
	}
	parent := filepath.Dir(s.SourceDir)
	stage, err := os.MkdirTemp(parent, ".codefly-workspace-restore-*")
	if err != nil {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.restore-workspace-checkpoint", err)), nil
	}
	stageOwned := true
	defer func() {
		if stageOwned {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := extractWorkspaceCheckpointArchive(ctx, archivePath, stage, metadata.Files); err != nil {
		return restoreWorkspaceCheckpointResult(nil, checkpointOperationFailure("code.restore-workspace-checkpoint", err)), nil
	}
	if err := verifyWorkspaceCheckpointFiles(ctx, stage, metadata.Files); err != nil {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_INTEGRITY_FAILED, "code.restore-workspace-checkpoint", err)), nil
	}
	rootInfo, err := os.Stat(s.SourceDir)
	if err != nil {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.restore-workspace-checkpoint", err)), nil
	}
	if err := os.Chmod(stage, rootInfo.Mode().Perm()); err != nil {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.restore-workspace-checkpoint", err)), nil
	}
	currentFiles, current, err := s.currentWorkspaceCheckpointState(ctx)
	if err != nil {
		return restoreWorkspaceCheckpointResult(nil, checkpointOperationFailure("code.restore-workspace-checkpoint", err)), nil
	}
	if current.GetVersion() != restored.PriorVersion {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.restore-workspace-checkpoint", errors.New("workspace changed during restore preparation"))), nil
	}
	priorGitlinks := make(map[string]string)
	for _, file := range currentFiles {
		if file.Kind == "gitlink" {
			priorGitlinks[file.Path] = file.Digest
		}
	}
	backup := filepath.Join(parent, ".codefly-workspace-backup-"+metadata.CheckpointID[4:20])
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_CONFLICT, "code.restore-workspace-checkpoint", errors.New("workspace restore backup already exists"))), nil
	}
	if err := os.Rename(s.SourceDir, backup); err != nil {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.restore-workspace-checkpoint", err)), nil
	}
	if err := os.Rename(stage, s.SourceDir); err != nil {
		if rollbackErr := os.Rename(backup, s.SourceDir); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("restore workspace rollback: %w", rollbackErr))
		}
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.restore-workspace-checkpoint", err)), nil
	}
	stageOwned = false
	movedMetadata := make([]string, 0, 4)
	preserved := []string{".git", ".jj", ".hg", ".svn"}
	for _, file := range metadata.Files {
		if file.Kind == "gitlink" {
			preserved = append(preserved, filepath.FromSlash(file.Path))
		}
	}
	for _, name := range preserved {
		oldPath := filepath.Join(backup, name)
		if _, err := os.Lstat(oldPath); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			err = errors.Join(err, s.rollbackWorkspaceCheckpointRestore(backup, movedMetadata, priorGitlinks))
			return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.restore-workspace-checkpoint", err)), nil
		}
		newPath := filepath.Join(s.SourceDir, name)
		if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
			err = errors.Join(err, s.rollbackWorkspaceCheckpointRestore(backup, movedMetadata, priorGitlinks))
			return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.restore-workspace-checkpoint", err)), nil
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			err = errors.Join(err, s.rollbackWorkspaceCheckpointRestore(backup, movedMetadata, priorGitlinks))
			return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.restore-workspace-checkpoint", err)), nil
		}
		movedMetadata = append(movedMetadata, name)
	}
	for _, file := range metadata.Files {
		if file.Kind != "gitlink" {
			continue
		}
		command := exec.Command("git", "checkout", "--detach", "--force", file.Digest)
		command.Dir = filepath.Join(s.SourceDir, filepath.FromSlash(file.Path))
		if output, err := command.CombinedOutput(); err != nil {
			restoreErr := fmt.Errorf("restore gitlink %s: %w: %s", file.Path, err, strings.TrimSpace(string(output)))
			restoreErr = errors.Join(restoreErr, s.rollbackWorkspaceCheckpointRestore(backup, movedMetadata, priorGitlinks))
			return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.restore-workspace-checkpoint", restoreErr)), nil
		}
	}
	_, finalState, err := s.currentWorkspaceCheckpointState(context.Background())
	if err != nil || finalState.GetVersion() != metadata.SourceVersion {
		if err == nil {
			err = errors.New("restored workspace does not match checkpoint source identity")
		}
		err = errors.Join(err, s.rollbackWorkspaceCheckpointRestore(backup, movedMetadata, priorGitlinks))
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_INTEGRITY_FAILED, "code.restore-workspace-checkpoint", err)), nil
	}
	if err := os.RemoveAll(backup); err != nil {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.restore-workspace-checkpoint", err)), nil
	}
	restored.RestoredAt = time.Now().UTC()
	if err := writeWorkspaceCheckpointMetadata(store, metadata); err != nil {
		return restoreWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.restore-workspace-checkpoint", err)), nil
	}
	return restoreWorkspaceCheckpointResult(workspaceCheckpointRestoreResponse(metadata, restored), nil), nil
}

func (s *DefaultCodeServer) rollbackWorkspaceCheckpointRestore(backup string, moved []string, priorGitlinks map[string]string) error {
	var rollbackErrors []error
	for index := len(moved) - 1; index >= 0; index-- {
		if err := os.Rename(filepath.Join(s.SourceDir, moved[index]), filepath.Join(backup, moved[index])); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	marker, err := os.CreateTemp(filepath.Dir(backup), ".codefly-workspace-failed-*")
	if err != nil {
		return errors.Join(append(rollbackErrors, err)...)
	}
	failed := marker.Name()
	if err := marker.Close(); err != nil {
		rollbackErrors = append(rollbackErrors, err)
	}
	if err := os.Remove(failed); err != nil {
		rollbackErrors = append(rollbackErrors, err)
		return errors.Join(rollbackErrors...)
	}
	if err := os.Rename(s.SourceDir, failed); err != nil {
		rollbackErrors = append(rollbackErrors, err)
		return errors.Join(rollbackErrors...)
	}
	if err := os.Rename(backup, s.SourceDir); err != nil {
		rollbackErrors = append(rollbackErrors, err)
		return errors.Join(rollbackErrors...)
	}
	for path, digest := range priorGitlinks {
		command := exec.Command("git", "checkout", "--detach", "--force", digest)
		command.Dir = filepath.Join(s.SourceDir, filepath.FromSlash(path))
		if output, err := command.CombinedOutput(); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore prior gitlink %s: %w: %s", path, err, strings.TrimSpace(string(output))))
		}
	}
	if err := os.RemoveAll(failed); err != nil {
		rollbackErrors = append(rollbackErrors, err)
	}
	return errors.Join(rollbackErrors...)
}

func (s *DefaultCodeServer) releaseWorkspaceCheckpoint(ctx context.Context, req *basev0.ReleaseWorkspaceCheckpointRequest) (*codev0.CodeResponse, error) {
	s.workspaceCheckpointMu.Lock()
	defer s.workspaceCheckpointMu.Unlock()
	if failure := validateReleaseWorkspaceCheckpoint(ctx, req); failure != nil {
		return releaseWorkspaceCheckpointResult(nil, failure), nil
	}
	store, failure := s.workspaceCheckpointStoreDir()
	if failure != nil {
		return releaseWorkspaceCheckpointResult(nil, failure), nil
	}
	metadata, err := readWorkspaceCheckpointMetadata(store, req.GetCheckpointId())
	if err != nil {
		return releaseWorkspaceCheckpointResult(nil, checkpointReadFailure("code.release-workspace-checkpoint", err)), nil
	}
	if !sameWorkspaceCheckpointLease(metadata, req.GetLease()) {
		return releaseWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_PERMISSION_DENIED, "code.release-workspace-checkpoint", errors.New("checkpoint belongs to another workspace lease"))), nil
	}
	if metadata.ReleasedAt.IsZero() {
		metadata.ReleasedAt = time.Now().UTC()
		if err := writeWorkspaceCheckpointMetadata(store, metadata); err != nil {
			return releaseWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.release-workspace-checkpoint", err)), nil
		}
	}
	if err := os.Remove(workspaceCheckpointArchivePath(store, metadata.CheckpointID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return releaseWorkspaceCheckpointResult(nil, checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.release-workspace-checkpoint", err)), nil
	}
	receipt := &basev0.WorkspaceCheckpointReleaseReceipt{
		CheckpointId: metadata.CheckpointID, Lease: workspaceCheckpointLeaseProto(metadata), ReleasedAt: timestamppb.New(metadata.ReleasedAt),
	}
	return releaseWorkspaceCheckpointResult(receipt, nil), nil
}

func (s *DefaultCodeServer) currentWorkspaceCheckpointState(ctx context.Context) ([]workspaceCheckpointFile, *basev0.WorkspaceStateIdentity, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	files, err := s.workspaceCheckpointFiles(ctx)
	if err != nil {
		return nil, nil, err
	}
	state, err := workspaceCheckpointStateForFiles(files)
	if err != nil {
		return nil, nil, err
	}
	return files, state, nil
}

func (s *DefaultCodeServer) workspaceCheckpointFiles(ctx context.Context) ([]workspaceCheckpointFile, error) {
	tracked, trackedDirectories, gitlinks, err := s.workspaceCheckpointGitState(ctx)
	if err != nil {
		return nil, err
	}
	files := make([]workspaceCheckpointFile, 0, len(tracked)+128)
	for _, gitlink := range gitlinks {
		files = append(files, gitlink)
	}
	err = filepath.WalkDir(s.SourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(s.SourceDir, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			return nil
		}
		if _, gitlink := gitlinks[relative]; gitlink {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if workspaceCheckpointAlwaysExcluded(relative) {
				return fs.SkipDir
			}
			if isExcludedSourceDirectory(entry.Name()) {
				if _, ok := trackedDirectories[relative]; !ok {
					return fs.SkipDir
				}
			}
			return nil
		}
		if workspaceCheckpointAlwaysExcluded(relative) {
			return nil
		}
		if workspaceCheckpointGeneratedPath(relative) {
			if _, ok := tracked[relative]; !ok {
				return nil
			}
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		record := workspaceCheckpointFile{Path: relative}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			record.Mode, record.Kind = 0o120000, "symlink"
			record.Link, err = os.Readlink(path)
			if err != nil {
				return err
			}
			record.Size = int64(len(record.Link))
			sum := sha256.Sum256([]byte(record.Link))
			record.Digest = hex.EncodeToString(sum[:])
		case info.Mode().IsRegular():
			record.Mode, record.Kind = 0o100644, "file"
			if info.Mode().Perm()&0o111 != 0 {
				record.Mode = 0o100755
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			record.Size = int64(len(body))
			sum := sha256.Sum256(body)
			record.Digest = hex.EncodeToString(sum[:])
		default:
			return fmt.Errorf("checkpoint path %q has unsupported mode %s", relative, info.Mode())
		}
		files = append(files, record)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("inspect workspace checkpoint state: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func (s *DefaultCodeServer) workspaceCheckpointGitState(ctx context.Context) (map[string]struct{}, map[string]struct{}, map[string]workspaceCheckpointFile, error) {
	tracked := map[string]struct{}{}
	directories := map[string]struct{}{}
	gitlinks := map[string]workspaceCheckpointFile{}
	inside, err := s.runGit(ctx, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		return tracked, directories, gitlinks, nil
	}
	output, err := s.runGit(ctx, "ls-files", "--stage", "-z", "--", ".")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("inspect Git index: %w", err)
	}
	for _, item := range strings.Split(output, "\x00") {
		if item == "" {
			continue
		}
		header, name, ok := strings.Cut(item, "\t")
		fields := strings.Fields(header)
		name = filepath.ToSlash(name)
		if !ok || len(fields) != 3 || fields[2] != "0" || name == "" || strings.HasPrefix(name, "../") {
			return nil, nil, nil, fmt.Errorf("invalid Git index entry %q", item)
		}
		tracked[name] = struct{}{}
		for directory := filepath.ToSlash(filepath.Dir(name)); directory != "." && directory != "/"; directory = filepath.ToSlash(filepath.Dir(directory)) {
			directories[directory] = struct{}{}
		}
		if fields[0] != "160000" {
			continue
		}
		resolved, err := s.runGit(ctx, "-C", name, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return nil, nil, nil, fmt.Errorf("resolve gitlink %s: %w", name, err)
		}
		dirty, err := s.runGit(ctx, "-C", name, "status", "--porcelain=v1", "--untracked-files=normal")
		if err != nil || strings.TrimSpace(dirty) != "" {
			return nil, nil, nil, fmt.Errorf("gitlink %s must be initialized and clean", name)
		}
		digest := strings.TrimSpace(resolved)
		gitlinks[name] = workspaceCheckpointFile{Path: name, Mode: 0o160000, Kind: "gitlink", Digest: digest, Size: -1}
	}
	return tracked, directories, gitlinks, nil
}

func (s *DefaultCodeServer) writeWorkspaceCheckpointArchive(ctx context.Context, destination io.Writer, files []workspaceCheckpointFile) error {
	archive := tar.NewWriter(destination)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			_ = archive.Close()
			return err
		}
		if file.Kind == "gitlink" {
			continue
		}
		header := &tar.Header{Name: file.Path, Mode: int64(file.Mode & 0o777), ModTime: time.Unix(0, 0), AccessTime: time.Unix(0, 0), ChangeTime: time.Unix(0, 0)}
		switch file.Kind {
		case "file":
			header.Typeflag, header.Size = tar.TypeReg, file.Size
		case "symlink":
			header.Typeflag, header.Linkname = tar.TypeSymlink, file.Link
		default:
			_ = archive.Close()
			return fmt.Errorf("unsupported checkpoint entry kind %q", file.Kind)
		}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if file.Kind == "file" {
			source, err := os.Open(filepath.Join(s.SourceDir, filepath.FromSlash(file.Path)))
			if err != nil {
				return err
			}
			written, copyErr := io.Copy(archive, source)
			closeErr := source.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if written != file.Size {
				return fmt.Errorf("checkpoint source %q changed while archiving", file.Path)
			}
		}
	}
	return archive.Close()
}

func extractWorkspaceCheckpointArchive(ctx context.Context, archivePath, destination string, files []workspaceCheckpointFile) error {
	source, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer source.Close()
	expected := make(map[string]workspaceCheckpointFile, len(files))
	for _, file := range files {
		if file.Kind != "gitlink" {
			expected[file.Path] = file
		}
	}
	seen := make(map[string]struct{}, len(expected))
	reader := tar.NewReader(source)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		record, ok := expected[header.Name]
		if !ok {
			return fmt.Errorf("archive contains unexpected path %q", header.Name)
		}
		if _, duplicate := seen[header.Name]; duplicate {
			return fmt.Errorf("archive repeats path %q", header.Name)
		}
		seen[header.Name] = struct{}{}
		target, err := resolvePath(destination, filepath.FromSlash(header.Name))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		switch record.Kind {
		case "file":
			if header.Typeflag != tar.TypeReg || header.Size != record.Size {
				return fmt.Errorf("archive metadata for %q does not match checkpoint", header.Name)
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(record.Mode&0o777))
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(file, reader, record.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case "symlink":
			if header.Typeflag != tar.TypeSymlink || header.Linkname != record.Link {
				return fmt.Errorf("archive symlink %q does not match checkpoint", header.Name)
			}
			if err := os.Symlink(record.Link, target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported checkpoint entry kind %q", record.Kind)
		}
	}
	if len(seen) != len(expected) {
		return errors.New("archive does not contain every checkpoint path")
	}
	return nil
}

func verifyWorkspaceCheckpointFiles(ctx context.Context, root string, files []workspaceCheckpointFile) error {
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if file.Kind == "gitlink" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		var body []byte
		if file.Kind == "symlink" {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			body = []byte(target)
		} else {
			body, err = os.ReadFile(path)
			if err != nil {
				return err
			}
			if uint32(info.Mode().Perm()) != file.Mode&0o777 {
				return fmt.Errorf("checkpoint mode mismatch for %q", file.Path)
			}
		}
		digest := sha256.Sum256(body)
		if int64(len(body)) != file.Size || hex.EncodeToString(digest[:]) != file.Digest {
			return fmt.Errorf("checkpoint content mismatch for %q", file.Path)
		}
	}
	return nil
}

func verifyWorkspaceCheckpointArchive(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}
	if hex.EncodeToString(hasher.Sum(nil)) != expected {
		return errors.New("checkpoint archive digest mismatch")
	}
	return nil
}

func validateWorkspaceCheckpointArchiveContents(ctx context.Context, path string, files []workspaceCheckpointFile) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer source.Close()
	expected := make(map[string]workspaceCheckpointFile, len(files))
	for _, file := range files {
		if file.Kind != "gitlink" {
			expected[file.Path] = file
		}
	}
	seen := make(map[string]struct{}, len(expected))
	reader := tar.NewReader(source)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		record, ok := expected[header.Name]
		if !ok {
			return fmt.Errorf("archive contains unexpected path %q", header.Name)
		}
		if _, duplicate := seen[header.Name]; duplicate {
			return fmt.Errorf("archive repeats path %q", header.Name)
		}
		seen[header.Name] = struct{}{}
		switch record.Kind {
		case "file":
			if header.Typeflag != tar.TypeReg || header.Size != record.Size {
				return fmt.Errorf("archive metadata for %q does not match", header.Name)
			}
			hasher := sha256.New()
			written, err := io.CopyN(hasher, reader, record.Size)
			if err != nil || written != record.Size || hex.EncodeToString(hasher.Sum(nil)) != record.Digest {
				return fmt.Errorf("archive content for %q does not match", header.Name)
			}
		case "symlink":
			if header.Typeflag != tar.TypeSymlink || header.Linkname != record.Link {
				return fmt.Errorf("archive symlink %q does not match", header.Name)
			}
		default:
			return fmt.Errorf("unsupported checkpoint entry kind %q", record.Kind)
		}
	}
	if len(seen) != len(expected) {
		return errors.New("archive does not contain every checkpoint path")
	}
	return nil
}

func (s *DefaultCodeServer) workspaceCheckpointStoreDir() (string, *basev0.Failure) {
	switch s.FS.(type) {
	case LocalVFS, *LocalVFS:
	default:
		return "", checkpointFailure(basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "code.workspace-checkpoint", errors.New("workspace checkpoints require the local filesystem boundary"))
	}
	if strings.TrimSpace(s.workspaceCheckpointStore) != "" {
		absolute, err := filepath.Abs(filepath.Clean(s.workspaceCheckpointStore))
		if err != nil {
			return "", checkpointFailure(basev0.FailureCode_FAILURE_CODE_INVALID_CONFIGURATION, "code.workspace-checkpoint", err)
		}
		if relative, err := filepath.Rel(s.SourceDir, absolute); err == nil && !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			if relative == "." {
				return "", checkpointFailure(basev0.FailureCode_FAILURE_CODE_INVALID_CONFIGURATION, "code.workspace-checkpoint", errors.New("workspace checkpoint store must be outside project bytes"))
			}
			first, _, _ := strings.Cut(filepath.ToSlash(relative), "/")
			switch first {
			case ".git", ".jj", ".hg", ".svn":
			default:
				return "", checkpointFailure(basev0.FailureCode_FAILURE_CODE_INVALID_CONFIGURATION, "code.workspace-checkpoint", errors.New("workspace checkpoint store must be outside project bytes"))
			}
		}
		return absolute, nil
	}
	command := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	command.Dir = s.SourceDir
	if output, err := command.Output(); err == nil {
		root := strings.TrimSpace(string(output))
		if root != "" {
			return filepath.Join(root, "codefly", "workspace-checkpoints"), nil
		}
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", checkpointFailure(basev0.FailureCode_FAILURE_CODE_INVALID_CONFIGURATION, "code.workspace-checkpoint", err)
	}
	absolute, err := filepath.Abs(s.SourceDir)
	if err != nil {
		return "", checkpointFailure(basev0.FailureCode_FAILURE_CODE_INVALID_CONFIGURATION, "code.workspace-checkpoint", err)
	}
	digest := sha256.Sum256([]byte(absolute))
	return filepath.Join(cache, "codefly", "workspace-checkpoints", hex.EncodeToString(digest[:16])), nil
}

func workspaceCheckpointRequiredBytes(files []workspaceCheckpointFile, metadata *workspaceCheckpointMetadata, allocationUnit uint64) (uint64, error) {
	if allocationUnit == 0 {
		return 0, errors.New("checkpoint storage allocation unit is zero")
	}
	required := allocationUnit * 4
	for _, file := range files {
		entryBytes := uint64(0)
		if file.Size > 0 {
			entryBytes = uint64(file.Size)
		}
		allocated, ok := checkpointRoundBytes(entryBytes+allocationUnit, allocationUnit)
		if !ok || math.MaxUint64-required < allocated {
			return 0, errors.New("checkpoint storage requirement overflows uint64")
		}
		required += allocated
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return 0, err
	}
	metadataBytes, ok := checkpointRoundBytes(uint64(len(encoded))+allocationUnit, allocationUnit)
	if !ok || math.MaxUint64-required < metadataBytes {
		return 0, errors.New("checkpoint metadata requirement overflows uint64")
	}
	return required + metadataBytes, nil
}

func checkpointRoundBytes(size, unit uint64) (uint64, bool) {
	remainder := size % unit
	if remainder == 0 {
		return size, true
	}
	addition := unit - remainder
	if size > math.MaxUint64-addition {
		return 0, false
	}
	return size + addition, true
}

func writeWorkspaceCheckpointMetadata(store string, metadata *workspaceCheckpointMetadata) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(store, ".metadata-*.json")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, workspaceCheckpointMetadataPath(store, metadata.CheckpointID)); err != nil {
		return err
	}
	directory, err := os.Open(store)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func readWorkspaceCheckpointMetadata(store, id string) (*workspaceCheckpointMetadata, error) {
	if !workspaceCheckpointIDPattern.MatchString(id) {
		return nil, errors.New("invalid checkpoint ID")
	}
	encoded, err := os.ReadFile(workspaceCheckpointMetadataPath(store, id))
	if err != nil {
		return nil, err
	}
	var metadata workspaceCheckpointMetadata
	if err := json.Unmarshal(encoded, &metadata); err != nil {
		return nil, err
	}
	if metadata.SchemaVersion != workspaceCheckpointSchemaVersion || metadata.CheckpointID != id {
		return nil, errors.New("checkpoint metadata is invalid")
	}
	state, err := workspaceCheckpointStateForFiles(metadata.Files)
	if err != nil || state.GetVersion() != metadata.SourceVersion || state.GetDigest() != metadata.SourceDigest {
		return nil, errors.New("checkpoint source identity is invalid")
	}
	nonce, err := hex.DecodeString(metadata.Nonce)
	if err != nil || len(nonce) != 32 || workspaceCheckpointID(metadata.WorkspaceID, metadata.LeaseID, metadata.SourceDigest, nonce) != id {
		return nil, errors.New("checkpoint identity binding is invalid")
	}
	return &metadata, nil
}

func findWorkspaceCheckpointByIdempotency(store string, lease *basev0.WorkspaceLeaseIdentity, callerID, key string) (*workspaceCheckpointMetadata, error) {
	entries, err := os.ReadDir(store)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !workspaceCheckpointIDPattern.MatchString(id) {
			continue
		}
		metadata, err := readWorkspaceCheckpointMetadata(store, id)
		if err != nil {
			return nil, err
		}
		if sameWorkspaceCheckpointLease(metadata, lease) && metadata.CallerID == callerID && metadata.IdempotencyKey == key {
			return metadata, nil
		}
	}
	return nil, nil
}

func newWorkspaceCheckpointID(lease *basev0.WorkspaceLeaseIdentity, sourceDigest string) (string, string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", "", err
	}
	return workspaceCheckpointID(lease.GetWorkspaceId(), lease.GetLeaseId(), sourceDigest, random), hex.EncodeToString(random), nil
}

func workspaceCheckpointID(workspaceID, leaseID, sourceDigest string, nonce []byte) string {
	hasher := sha256.New()
	_, _ = io.WriteString(hasher, workspaceID)
	_, _ = io.WriteString(hasher, "\x00")
	_, _ = io.WriteString(hasher, leaseID)
	_, _ = io.WriteString(hasher, "\x00")
	_, _ = io.WriteString(hasher, sourceDigest)
	_, _ = hasher.Write(nonce)
	return "wcp_" + hex.EncodeToString(hasher.Sum(nil))
}

func workspaceCheckpointStateForFiles(files []workspaceCheckpointFile) (*basev0.WorkspaceStateIdentity, error) {
	encoded, err := json.Marshal(files)
	if err != nil {
		return nil, fmt.Errorf("encode workspace identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	value := "sha256:" + hex.EncodeToString(digest[:])
	return &basev0.WorkspaceStateIdentity{Version: value, Digest: value}, nil
}

func workspaceCheckpointMetadataPath(store, id string) string {
	return filepath.Join(store, id+".json")
}
func workspaceCheckpointArchivePath(store, id string) string { return filepath.Join(store, id+".tar") }

func workspaceCheckpointAlwaysExcluded(path string) bool {
	for _, component := range strings.Split(filepath.ToSlash(path), "/") {
		switch component {
		case ".git", ".jj", ".hg", ".svn":
			return true
		}
	}
	return false
}

func workspaceCheckpointGeneratedPath(path string) bool {
	for _, component := range strings.Split(filepath.ToSlash(path), "/") {
		if isExcludedSourceDirectory(component) {
			return true
		}
	}
	return false
}

func validateCreateWorkspaceCheckpoint(ctx context.Context, req *basev0.CreateWorkspaceCheckpointRequest) *basev0.Failure {
	if err := ctx.Err(); err != nil {
		return checkpointOperationFailure("code.create-workspace-checkpoint", err)
	}
	if req == nil || !validWorkspaceCheckpointLease(req.GetLease()) || !validCheckpointValue(req.GetCallerId()) || !validCheckpointValue(req.GetIdempotencyKey()) || !validWorkspaceVersion(req.GetExpectedWorkspaceVersion()) {
		return checkpointFailure(basev0.FailureCode_FAILURE_CODE_INVALID_ARGUMENT, "code.create-workspace-checkpoint", errors.New("lease, expected_workspace_version, caller_id, and idempotency_key are required and must be canonical"))
	}
	if req.GetRetentionIntent() != basev0.WorkspaceCheckpointRetentionIntent_WORKSPACE_CHECKPOINT_RETENTION_INTENT_UNTIL_RELEASE_OR_LEASE_END {
		return checkpointFailure(basev0.FailureCode_FAILURE_CODE_INVALID_ARGUMENT, "code.create-workspace-checkpoint", errors.New("retention_intent must be UNTIL_RELEASE_OR_LEASE_END"))
	}
	return nil
}

func validateRestoreWorkspaceCheckpoint(ctx context.Context, req *basev0.RestoreWorkspaceCheckpointRequest) *basev0.Failure {
	if err := ctx.Err(); err != nil {
		return checkpointOperationFailure("code.restore-workspace-checkpoint", err)
	}
	if req == nil || !workspaceCheckpointIDPattern.MatchString(req.GetCheckpointId()) || !validWorkspaceCheckpointLease(req.GetLease()) || !validWorkspaceVersion(req.GetExpectedWorkspaceVersion()) || !validCheckpointValue(req.GetAuthorizationId()) || !validCheckpointValue(req.GetIdempotencyKey()) {
		return checkpointFailure(basev0.FailureCode_FAILURE_CODE_INVALID_ARGUMENT, "code.restore-workspace-checkpoint", errors.New("checkpoint_id, lease, expected_workspace_version, authorization_id, and idempotency_key are required and must be canonical"))
	}
	return nil
}

func validateReleaseWorkspaceCheckpoint(ctx context.Context, req *basev0.ReleaseWorkspaceCheckpointRequest) *basev0.Failure {
	if err := ctx.Err(); err != nil {
		return checkpointOperationFailure("code.release-workspace-checkpoint", err)
	}
	if req == nil || !workspaceCheckpointIDPattern.MatchString(req.GetCheckpointId()) || !validWorkspaceCheckpointLease(req.GetLease()) || !validCheckpointValue(req.GetCallerId()) || !validCheckpointValue(req.GetIdempotencyKey()) {
		return checkpointFailure(basev0.FailureCode_FAILURE_CODE_INVALID_ARGUMENT, "code.release-workspace-checkpoint", errors.New("checkpoint_id, lease, caller_id, and idempotency_key are required and must be canonical"))
	}
	return nil
}

func validWorkspaceCheckpointLease(lease *basev0.WorkspaceLeaseIdentity) bool {
	return lease != nil && validCheckpointValue(lease.GetWorkspaceId()) && validCheckpointValue(lease.GetLeaseId())
}

func validCheckpointValue(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value
}

func validWorkspaceVersion(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func sameWorkspaceCheckpointLease(metadata *workspaceCheckpointMetadata, lease *basev0.WorkspaceLeaseIdentity) bool {
	return metadata.WorkspaceID == lease.GetWorkspaceId() && metadata.LeaseID == lease.GetLeaseId()
}

func checkpointFailure(code basev0.FailureCode, operation string, err error) *basev0.Failure {
	return failures.New(code, operation, err.Error())
}

func checkpointOperationFailure(operation string, err error) *basev0.Failure {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return failures.FromError(operation, err)
	}
	return checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, operation, err)
}

func checkpointReadFailure(operation string, err error) *basev0.Failure {
	if errors.Is(err, os.ErrNotExist) {
		return checkpointFailure(basev0.FailureCode_FAILURE_CODE_NOT_FOUND, operation, errors.New("workspace checkpoint not found"))
	}
	return checkpointFailure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, operation, err)
}

func createWorkspaceCheckpointResult(checkpoint *basev0.WorkspaceCheckpoint, failure *basev0.Failure) *codev0.CodeResponse {
	nested := &basev0.CreateWorkspaceCheckpointResponse{Checkpoint: checkpoint, Failure: failure}
	return &codev0.CodeResponse{Failure: failure, Result: &codev0.CodeResponse_CreateWorkspaceCheckpoint{CreateWorkspaceCheckpoint: nested}}
}

func restoreWorkspaceCheckpointResult(response *basev0.RestoreWorkspaceCheckpointResponse, failure *basev0.Failure) *codev0.CodeResponse {
	if response == nil {
		response = &basev0.RestoreWorkspaceCheckpointResponse{}
	}
	response.Failure = failure
	return &codev0.CodeResponse{Failure: failure, Result: &codev0.CodeResponse_RestoreWorkspaceCheckpoint{RestoreWorkspaceCheckpoint: response}}
}

func releaseWorkspaceCheckpointResult(receipt *basev0.WorkspaceCheckpointReleaseReceipt, failure *basev0.Failure) *codev0.CodeResponse {
	nested := &basev0.ReleaseWorkspaceCheckpointResponse{Receipt: receipt, Failure: failure}
	return &codev0.CodeResponse{Failure: failure, Result: &codev0.CodeResponse_ReleaseWorkspaceCheckpoint{ReleaseWorkspaceCheckpoint: nested}}
}

func workspaceCheckpointProto(metadata *workspaceCheckpointMetadata) *basev0.WorkspaceCheckpoint {
	requirement := &basev0.StorageCapacityRequirement{
		Component: "workspace_checkpoint", Bytes: metadata.Capacity.RequiredBytes,
		AuthorityKind: basev0.StorageAuthorityKind_STORAGE_AUTHORITY_KIND_GATEWAY_ROOT,
	}
	admission := &basev0.StorageCapacityAdmission{
		AuthorityId:    metadata.Capacity.AuthorityID,
		AuthorityKinds: []basev0.StorageAuthorityKind{basev0.StorageAuthorityKind_STORAGE_AUTHORITY_KIND_GATEWAY_ROOT},
		TotalBytes:     metadata.Capacity.TotalBytes, AvailableBytes: metadata.Capacity.AvailableBytes,
		Requirements: []*basev0.StorageCapacityRequirement{requirement}, RequiredBytes: metadata.Capacity.RequiredBytes,
		ProjectedAvailableBytes: metadata.Capacity.ProjectedBytes, ShortfallBytes: metadata.Capacity.ShortfallBytes, Admitted: metadata.Capacity.Admitted,
	}
	return &basev0.WorkspaceCheckpoint{
		CheckpointId: metadata.CheckpointID, Lease: workspaceCheckpointLeaseProto(metadata),
		Source:             &basev0.WorkspaceStateIdentity{Version: metadata.SourceVersion, Digest: metadata.SourceDigest},
		StorageRequirement: requirement, StorageAdmission: admission, RetentionIntent: metadata.Retention,
		CreatedAt: timestamppb.New(metadata.CreatedAt),
	}
}

func workspaceCheckpointLeaseProto(metadata *workspaceCheckpointMetadata) *basev0.WorkspaceLeaseIdentity {
	return &basev0.WorkspaceLeaseIdentity{WorkspaceId: metadata.WorkspaceID, LeaseId: metadata.LeaseID}
}

func workspaceCheckpointRestoreResponse(metadata *workspaceCheckpointMetadata, restored *workspaceCheckpointRestore) *basev0.RestoreWorkspaceCheckpointResponse {
	state := &basev0.WorkspaceStateIdentity{Version: metadata.SourceVersion, Digest: metadata.SourceDigest}
	receipt := &basev0.WorkspaceCheckpointRestoreReceipt{
		CheckpointId: metadata.CheckpointID, Lease: workspaceCheckpointLeaseProto(metadata),
		Prior: &basev0.WorkspaceStateIdentity{Version: restored.PriorVersion, Digest: restored.PriorDigest}, Restored: state,
		AuthorizationId: restored.AuthorizationID, IdempotencyKey: restored.IdempotencyKey, RestoredAt: timestamppb.New(restored.RestoredAt),
	}
	return &basev0.RestoreWorkspaceCheckpointResponse{Workspace: state, Receipt: receipt}
}
