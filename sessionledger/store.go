package sessionledger

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

const (
	// storeDirName is the record contract's own directory. Records written
	// under a schema are never read by a release that speaks another one, so
	// two contracts can coexist during a rollout without either quarantining
	// the other's files. It sits under a subdirectory of ~/.codefly/runs so
	// the process-group reaper's legacy scan, which reads only loose files in
	// that root, never sees a session record.
	storeDirName = "session-ledger"
	// schemaDirName is the on-disk name of SchemaV1.
	schemaDirName = "v1"

	sessionsDirName = "sessions"
	locksDirName    = "locks"

	lockRetry      = 25 * time.Millisecond
	maxRecordBytes = 1 << 20
	// invalidSuffix marks a record this release could not decode. The suffix
	// takes it out of the .json listing, so it is neither parsed nor reported
	// again, and leaves the bytes on disk for a human to look at.
	invalidSuffix = ".invalid"

	// DefaultRetention is how long a released session record is kept after its
	// last resource reached a terminal disposition. It is long enough that a
	// developer can still read yesterday's receipt and short enough that the
	// directory stays bounded.
	DefaultRetention = 7 * 24 * time.Hour
)

// ErrSessionNotFound is returned when no record exists for an invocation.
var ErrSessionNotFound = errors.New("session-ledger record not found")

// Store is the on-disk session ledger.
type Store struct {
	root      string
	retention time.Duration
	now       func() time.Time
}

// Open returns the ledger rooted at dir. Pass an empty dir for the default
// location under the user's ~/.codefly.
func Open(dir string) (*Store, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine home directory: %w", err)
		}
		dir = filepath.Join(home, ".codefly", "runs")
	}
	root := filepath.Join(dir, storeDirName, schemaDirName)
	for _, sub := range []string{sessionsDirName, locksDirName} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o700); err != nil {
			return nil, fmt.Errorf("cannot create session-ledger directory: %w", err)
		}
	}
	return &Store{root: root, retention: DefaultRetention, now: func() time.Time {
		return time.Now().UTC()
	}}, nil
}

// WithRetention returns a store that keeps finished session records for the
// given duration instead of DefaultRetention.
func (store *Store) WithRetention(retention time.Duration) *Store {
	clone := *store
	clone.retention = retention
	return &clone
}

// Root is the directory holding this store's records.
func (store *Store) Root() string { return store.root }

func (store *Store) sessionPath(invocation string) string {
	return filepath.Join(store.root, sessionsDirName, invocation+".json")
}

func (store *Store) keyLockPath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(store.root, locksDirName, hex.EncodeToString(sum[:])+".lock")
}

// processLocks serializes goroutines of this process before they contend for
// the file lock. The file lock alone is what makes the ledger safe across
// processes; this makes contention within one process deterministic instead of
// leaving two goroutines to race their retry timers.
//
// Entries are reference-counted and dropped when the last holder leaves, so a
// long-lived process working through many keys does not accumulate a mutex per
// key it has ever touched.
var (
	processLocksMu sync.Mutex
	processLocks   = map[string]*processLock{}
)

type processLock struct {
	guard   sync.Mutex
	holders int
}

func acquireProcessLock(path string) *processLock {
	processLocksMu.Lock()
	entry, ok := processLocks[path]
	if !ok {
		entry = &processLock{}
		processLocks[path] = entry
	}
	entry.holders++
	processLocksMu.Unlock()

	entry.guard.Lock()
	return entry
}

func releaseProcessLock(path string, entry *processLock) {
	entry.guard.Unlock()

	processLocksMu.Lock()
	entry.holders--
	if entry.holders == 0 {
		delete(processLocks, path)
	}
	processLocksMu.Unlock()
}

// heldLock pairs a file lock with the in-process mutex taken before it, so both
// are released in the right order.
type heldLock struct {
	path    string
	file    *flock.Flock
	process *processLock
}

// lockPath takes an advisory lock, serializing whichever transition the caller
// is about to make against every other holder of the same key.
func lockPath(ctx context.Context, path string) (*heldLock, error) {
	entry := acquireProcessLock(path)

	lock := flock.New(path, flock.SetPermissions(0o600))
	locked, err := lock.TryLockContext(ctx, lockRetry)
	if err != nil {
		releaseProcessLock(path, entry)
		return nil, err
	}
	if !locked {
		releaseProcessLock(path, entry)
		return nil, errors.New("session-ledger lock was not acquired")
	}
	return &heldLock{path: path, file: lock, process: entry}, nil
}

func unlock(lock *heldLock) error {
	err := errors.Join(lock.file.Unlock(), lock.file.Close())
	releaseProcessLock(lock.path, lock.process)
	return err
}

// Read returns the recorded session for invocation.
func (store *Store) Read(invocation string) (*Session, error) {
	return readSession(store.sessionPath(invocation))
}

func readSession(path string) (*Session, error) {
	file, err := os.Open(path) // #nosec G304 -- path is built from a validated invocation id
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("cannot read session record: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot stat session record: %w", err)
	}
	if info.Size() > maxRecordBytes {
		return nil, fmt.Errorf("%w: session record exceeds %d bytes", ErrInvalid, maxRecordBytes)
	}
	var session Session
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&session); err != nil {
		return nil, fmt.Errorf("%w: cannot decode session record: %v", ErrInvalid, err)
	}
	if err := session.validate(); err != nil {
		return nil, err
	}
	return &session, nil
}

// write publishes the record atomically: a reader either sees the previous
// bytes or the new ones, never a partial write. Both file and directory are
// synced, so a record that names a resource is on disk before that resource is
// created — which is what makes a crash during creation recoverable.
func (store *Store) write(session *Session) error {
	session.UpdatedAt = store.now()
	if err := session.validate(); err != nil {
		return err
	}
	content, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode session record: %w", err)
	}
	content = append(content, '\n')
	dir := filepath.Join(store.root, sessionsDirName)
	file, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("cannot create session record: %w", err)
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("cannot make session record private: %w", err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("cannot write session record: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("cannot sync session record: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("cannot close session record: %w", err)
	}
	if err := os.Rename(temporary, store.sessionPath(session.InvocationID)); err != nil {
		return fmt.Errorf("cannot publish session record: %w", err)
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	handle, err := os.Open(dir) // #nosec G304 -- store-owned directory
	if err != nil {
		return fmt.Errorf("cannot open session-ledger directory for sync: %w", err)
	}
	return errors.Join(handle.Sync(), handle.Close())
}

// List returns every readable session record.
//
// A record that cannot be decoded is quarantined — renamed beside itself with a
// .invalid suffix — and reported once. It is never deleted: it may still name
// live resources and nothing here can read them, so it is kept for a human.
// Quarantining is what stops it being re-reported on every invocation forever,
// which would leave Recover permanently returning an error no caller can clear.
// This mirrors how runners/base handles an unreadable process-group record.
func (store *Store) List() ([]*Session, error) {
	dir := filepath.Join(store.root, sessionsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot list session records: %w", err)
	}
	var sessions []*Session
	var failures []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		session, err := readSession(path)
		if err != nil {
			failures = append(failures, fmt.Errorf("session record %s: %w", entry.Name(), err))
			if quarantineErr := quarantineRecord(path); quarantineErr != nil {
				failures = append(failures,
					fmt.Errorf("quarantine session record %s: %w", entry.Name(), quarantineErr))
			}
			continue
		}
		sessions = append(sessions, session)
	}
	return sessions, errors.Join(failures...)
}

// quarantineRecord moves an undecodable record aside without touching anything
// it might name.
func quarantineRecord(path string) error {
	if err := os.Rename(path, path+invalidSuffix); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

// Forget removes a session record. The resources it named are untouched: this
// drops Codefly's memory of them, so it is only correct once every resource has
// reached a terminal disposition.
func (store *Store) Forget(invocation string) error {
	if err := os.Remove(store.sessionPath(invocation)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot remove session record: %w", err)
	}
	return syncDir(filepath.Join(store.root, sessionsDirName))
}

// Expire removes records for released sessions that name nothing still on the
// machine and whose last update is older than the retention window. A session
// still naming retained data or a resource that may be running is never
// expired, however old.
func (store *Store) Expire(ctx context.Context) (int, error) {
	sessions, listErr := store.List()
	cutoff := store.now().Add(-store.retention)
	removed := 0
	var failures []error
	for _, session := range sessions {
		if session.Lease != nil || session.UpdatedAt.After(cutoff) {
			continue
		}
		if !allForgettable(session) {
			continue
		}
		expired, err := store.expireOne(ctx, session.InvocationID, cutoff)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if expired {
			removed++
		}
	}
	return removed, errors.Join(listErr, errors.Join(failures...))
}

// expireOne re-reads the record under the key lock before deleting it. The
// listing above is a snapshot: between it and here another process can acquire
// the session and take a live lease, and forgetting a record someone is holding
// orphans whatever they create under it.
func (store *Store) expireOne(ctx context.Context, invocation string, cutoff time.Time) (bool, error) {
	session, err := store.Read(invocation)
	if errors.Is(err, ErrSessionNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	lock, err := lockPath(ctx, store.keyLockPath(session.Key))
	if err != nil {
		return false, fmt.Errorf("cannot lock session key %q: %w", session.Key, err)
	}
	defer func() { _ = unlock(lock) }()

	current, err := store.Read(invocation)
	if errors.Is(err, ErrSessionNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if current.Lease != nil || current.UpdatedAt.After(cutoff) || !allForgettable(current) {
		return false, nil
	}
	return true, store.Forget(invocation)
}

func allForgettable(session *Session) bool {
	for _, resource := range session.Resources {
		if !resource.Forgettable() {
			return false
		}
	}
	return true
}

func newInvocationID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("cannot mint invocation id: %w", err)
	}
	return hex.EncodeToString(value), nil
}
