package base

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/codefly-dev/core/internal/wire"
	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
	"io"
	"path/filepath"
	"strings"
)

// ProcessGroupRecoveryExpectation is an untrusted, owner-produced selection
// receipt. Persist the complete receipt from PreviewProcessGroupRecovery;
// callers must not construct it from registry internals. It grants no authority
// by itself. Every field is checked again before recovery begins.
type ProcessGroupRecoveryExpectation struct {
	Namespace    string            `json:"namespace"`
	RecordSHA256 string            `json:"record_sha256"`
	PGID         int               `json:"pgid"`
	Leader       ProcessIdentity   `json:"leader"`
	Owner        ProcessIdentity   `json:"owner"`
	Members      []ProcessIdentity `json:"members"`
}

// ParseProcessGroupRecoveryExpectation strictly decodes a saved preview receipt.
// Unknown fields, trailing values and incomplete member evidence are refused.
// Parsing is not authentication; RecoverProcessGroup revalidates every fact.
func ParseProcessGroupRecoveryExpectation(data []byte) (ProcessGroupRecoveryExpectation, error) {
	var expected ProcessGroupRecoveryExpectation
	if len(data) > maxRecordSize {
		return expected, errors.New("recovery expectation is too large")
	}
	// Reuse Core's document checks for duplicate keys/nulls; JSON syntax and
	// typed field admission remain encoding/json's responsibility.
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return expected, err
	}
	if defect := wire.Check(&document); defect.Kind != wire.NoDefect {
		return expected, fmt.Errorf("invalid recovery evidence at %s: %v (%s)", defect.Path, defect.Kind, defect.Detail)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&expected); err != nil {
		return expected, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return expected, errors.New("recovery expectation has trailing data")
	}
	if err := expected.validate(); err != nil {
		return expected, err
	}
	return expected, nil
}

// ProcessGroupRecoveryResult reports observed postconditions, including on
// error. Success requires both fields true. No workspace attribution is inferred.
type ProcessGroupRecoveryResult struct {
	GroupDead     bool `json:"group_dead"`
	RecordRemoved bool `json:"record_removed"`
}

// PreviewProcessGroupRecovery authenticates one explicitly selected orphan
// registration without signaling or modifying it. Unlike ordinary inspection,
// this operation supports Darwin's historical sec:usec boot representation,
// only when it still equals the running kernel's value. Recorded identities
// remain unchanged; Members carry current identities for safe signal delivery.
func PreviewProcessGroupRecovery(ctx context.Context, pgid int) (expected *ProcessGroupRecoveryExpectation, err error) {
	unlock, err := lockRecoveryRegistries(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	group, snapshot, err := readRecoveryRegistration(pgid)
	if err != nil {
		return nil, err
	}
	members, ok, err := authenticateRecoveryGroup(ctx, group.record)
	if err != nil {
		return nil, err
	}
	if !ok || len(members) == 0 {
		return nil, errors.New("recovery requires an authenticated live generation")
	}
	expected = recoveryExpectation(group, snapshot, members)
	// A noncooperating writer must not change the receipt during observation.
	if _, _, err = readExpectedRecoveryRegistration(*expected, snapshot); err != nil {
		return nil, err
	}
	return expected, nil
}

// RecoverProcessGroup conditionally terminates one selected orphan generation.
// The original owner must remain absent and the registration must remain exact.
// Initial expected members must still have their observed births at admission;
// later sweeps may reach authenticated descendants of that same registration.
// It uses current kernel-bound delivery, never legacy PID-only signaling.
// Failed admission has no effects. Partial termination is returned as an error;
// a replacement registration is preserved and never reported removed.
func RecoverProcessGroup(ctx context.Context, expected ProcessGroupRecoveryExpectation) (result *ProcessGroupRecoveryResult, err error) {
	result = &ProcessGroupRecoveryResult{}
	if err := expected.validate(); err != nil {
		return result, err
	}
	unlock, err := lockRecoveryRegistries(ctx)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	group, snapshot, err := readExpectedRecoveryRegistration(expected, recordSnapshot{})
	if err != nil {
		return result, err
	}
	members, ok, err := authenticateRecoveryGroup(ctx, group.record)
	if err != nil {
		return result, err
	}
	if !ok || len(members) == 0 {
		return result, errors.New("recovery requires an authenticated live generation")
	}
	if err = validateRecoveryMembers(expected.Members, members); err != nil {
		return result, err
	}
	// Pin the exact file as well as its bytes. Check before every signal sweep,
	// including the first; both namespace locks cover admission through removal.
	firstSweep := true
	authenticate := func(ctx context.Context, rec pgidRecord) ([]processIdentity, bool, error) {
		if _, _, checkErr := readExpectedRecoveryRegistration(expected, snapshot); checkErr != nil {
			return nil, false, errors.Join(errProcessGroupIdentityChanged, checkErr)
		}
		found, authenticated, checkErr := authenticateRecoveryGroup(ctx, rec)
		if checkErr != nil {
			return nil, false, errors.Join(errProcessGroupIdentityChanged, checkErr)
		}
		if firstSweep {
			if checkErr = validateRecoveryMembers(expected.Members, found); checkErr != nil {
				return nil, false, errors.Join(errProcessGroupIdentityChanged, checkErr)
			}
			firstSweep = false
		}
		if _, _, checkErr = readExpectedRecoveryRegistration(expected, snapshot); checkErr != nil {
			return nil, false, errors.Join(errProcessGroupIdentityChanged, checkErr)
		}
		return found, authenticated, nil
	}
	err = terminateGroup(ctx, group.record, authenticate, sigtermGrace)
	result.GroupDead = !isProcessGroupAlive(expected.PGID)
	if err != nil {
		return result, err
	}
	if !result.GroupDead {
		return result, errors.New("recovery target remains alive")
	}
	current, finalSnapshot, err := readExpectedRecoveryRegistration(expected, snapshot)
	if err != nil {
		return result, err
	}
	if err = recoveryOwnerAbsent(current.record); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if isProcessGroupAlive(expected.PGID) {
		result.GroupDead = false
		return result, errors.New("recovery group became live before removal")
	}
	dir, err := pgidNamespaceDir(expected.Namespace)
	if err != nil {
		return result, err
	}
	if err = removeRecord(filepath.Join(dir, fmt.Sprintf("%d.pgid", expected.PGID)), finalSnapshot); err != nil {
		return result, err
	}
	// Prove absence in all supported namespaces, not merely a nil remove result.
	_, checkErr := LookupProcessGroup(expected.PGID)
	if !errors.Is(checkErr, ErrProcessGroupNotRegistered) {
		return result, errors.Join(errors.New("registration remains after recovery"), checkErr)
	}
	result.RecordRemoved = true
	return result, nil
}

func lockRecoveryRegistries(ctx context.Context) (func() error, error) {
	// acquireRegistryLock also holds the package-wide in-process mutex. Take it
	// once, then lock the second namespace file in the same fixed order.
	currentDir, err := pgidNamespaceDir(pgidRegistryNamespace)
	if err != nil {
		return nil, err
	}
	current, err := acquireRegistryLock(ctx, currentDir)
	if err != nil {
		return nil, err
	}
	previousDir, err := pgidNamespaceDir(pgidPreviousNamespace)
	if err != nil {
		return nil, errors.Join(err, releaseRegistryLock(current))
	}
	previous := flock.New(filepath.Join(previousDir, pgidLockName), flock.SetPermissions(0600))
	locked, err := previous.TryLockContext(ctx, pgidLockRetry)
	if err != nil || !locked {
		return nil, errors.Join(errors.New("lock previous recovery namespace"), err, previous.Close(), releaseRegistryLock(current))
	}
	return func() error { return errors.Join(previous.Unlock(), previous.Close(), releaseRegistryLock(current)) }, nil
}

func readRecoveryRegistration(pgid int) (*TrackedProcessGroup, recordSnapshot, error) {
	if pgid <= 1 {
		return nil, recordSnapshot{}, errors.New("recovery requires a valid process group")
	}
	group, err := LookupProcessGroup(pgid)
	if err != nil {
		return nil, recordSnapshot{}, err
	}
	dir, err := pgidNamespaceDir(group.namespace)
	if err != nil {
		return nil, recordSnapshot{}, err
	}
	rec, snapshot, err := readPgidRecord(filepath.Join(dir, fmt.Sprintf("%d.pgid", pgid)))
	if err != nil {
		return nil, recordSnapshot{}, err
	}
	if !sameProcessGroupRegistration(rec, group.record) {
		return nil, recordSnapshot{}, errors.New("registration changed during recovery lookup")
	}
	return &TrackedProcessGroup{namespace: group.namespace, record: rec}, snapshot, nil
}

func recoveryExpectation(group *TrackedProcessGroup, snapshot recordSnapshot, members []processIdentity) *ProcessGroupRecoveryExpectation {
	recorded := func(identity recordedProcessIdentity, pgid int) ProcessIdentity {
		return ProcessIdentity{PID: identity.PID, PGID: pgid, BootID: identity.BootID, StartID: identity.StartID, Executable: identity.Executable}
	}
	e := &ProcessGroupRecoveryExpectation{Namespace: group.namespace, RecordSHA256: snapshot.digest, PGID: group.record.PGID,
		Leader: recorded(group.record.Leader, group.record.PGID), Owner: recorded(group.record.Owner, 0)}
	for _, m := range members {
		e.Members = append(e.Members, ProcessIdentity{PID: m.pid, PGID: m.pgid, BootID: m.bootID, StartID: m.startID, Executable: m.executable})
	}
	return e
}

func readExpectedRecoveryRegistration(expected ProcessGroupRecoveryExpectation, original recordSnapshot) (*TrackedProcessGroup, recordSnapshot, error) {
	group, snapshot, err := readRecoveryRegistration(expected.PGID)
	if err != nil {
		return nil, snapshot, err
	}
	actual := recoveryExpectation(group, snapshot, nil)
	if expected.Namespace != actual.Namespace || expected.RecordSHA256 != actual.RecordSHA256 || expected.Leader != actual.Leader || expected.Owner != actual.Owner {
		return nil, snapshot, errors.New("recovery registration does not match expected evidence")
	}
	if original.info != nil && !sameRecordFile(original.info, snapshot.info) {
		return nil, snapshot, errors.New("recovery registration file was replaced")
	}
	return group, snapshot, nil
}

func validateRecoveryMembers(expected []ProcessIdentity, members []processIdentity) error {
	if len(expected) == 0 || len(expected) != len(members) {
		return errors.New("recovery requires expected member births")
	}
	seen := make(map[int]bool)
	for _, want := range expected {
		if seen[want.PID] {
			return errors.New("duplicate expected member")
		}
		seen[want.PID] = true
		found := false
		for _, m := range members {
			got := ProcessIdentity{PID: m.pid, PGID: m.pgid, BootID: m.bootID, StartID: m.startID, Executable: m.executable}
			if got == want {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("expected recovery member %d changed or disappeared", want.PID)
		}
	}
	return nil
}

// Historical representations are selected by syntax, not producer versions.
func recoveryBootMatches(recorded string, current string) (bool, error) {
	if !strings.Contains(recorded, ":") {
		return recorded == current, nil
	}
	return recoveryLegacyBootMatches(recorded)
}

func recoveryOwnerAbsent(rec pgidRecord) error {
	owner, err := inspectProcess(rec.Owner.PID)
	if errors.Is(err, errProcessNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// Same start with incompatible boot evidence is uncertainty, never absence.
	if owner.startID == rec.Owner.StartID {
		return errors.New("original recovery owner is live or has incompatible boot evidence")
	}
	return nil
}

func authenticateRecoveryGroup(ctx context.Context, rec pgidRecord) ([]processIdentity, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := recoveryOwnerAbsent(rec); err != nil {
		return nil, false, err
	}
	members, err := inspectProcessGroup(ctx, rec.PGID)
	if err != nil {
		return nil, false, err
	}
	if len(members) == 0 {
		return nil, false, errProcessGroupNotSignalable
	}
	matches, err := recoveryBootMatches(rec.Leader.BootID, members[0].bootID)
	if err != nil || !matches {
		return nil, false, errors.Join(errors.New("recovery boot evidence does not match current boot"), err)
	}
	for _, member := range members {
		if member.pid == rec.Leader.PID {
			return members, member.startID == rec.Leader.StartID, nil
		}
	}
	credentialed, _, err := credentialEvidence(members, rec.Authentication)
	return members, credentialed, err
}

func (expected ProcessGroupRecoveryExpectation) validate() error {
	digest, err := hex.DecodeString(expected.RecordSHA256)
	if err != nil || len(digest) != 32 || expected.RecordSHA256 != strings.ToLower(expected.RecordSHA256) || expected.PGID <= 1 || (expected.Namespace != pgidRegistryNamespace && expected.Namespace != pgidPreviousNamespace) || len(expected.Members) == 0 {
		return errors.New("recovery expectation is incomplete or malformed")
	}
	for _, identity := range append([]ProcessIdentity{expected.Leader, expected.Owner}, expected.Members...) {
		if identity.PID <= 0 || identity.BootID == "" || identity.StartID == 0 || identity.Executable == "" {
			return errors.New("recovery expected birth is incomplete")
		}
	}
	return nil
}
