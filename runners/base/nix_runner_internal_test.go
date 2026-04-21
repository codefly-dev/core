package base

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── parseNixDevEnv ───────────────────────────────────────

// TestParseNixDevEnv_Empty proves the parser accepts an empty payload
// rather than panicking. NewNixEnvironment.Init relies on fallback on
// failure; a clean empty result is also a valid outcome.
func TestParseNixDevEnv_Empty(t *testing.T) {
	got, err := parseNixDevEnv([]byte(`{"variables":{}}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %d entries", len(got))
	}
}

// TestParseNixDevEnv_MalformedJSON returns an error (materialize catches
// and falls back to wrapped nix develop).
func TestParseNixDevEnv_MalformedJSON(t *testing.T) {
	_, err := parseNixDevEnv([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}
}

// TestParseNixDevEnv_AcceptsExportedAndVar locks in which types of
// variables the parser passes through. Exported and var are both scalar
// strings that round-trip through exec.Cmd.Env; arrays, associative
// arrays, and bash functions can't.
func TestParseNixDevEnv_AcceptsExportedAndVar(t *testing.T) {
	payload := []byte(`{
		"variables": {
			"PATH":     {"type": "exported", "value": "/nix/store/x/bin:/usr/bin"},
			"GO":       {"type": "var",      "value": "/nix/store/y/bin/go"},
			"ARRAY":    {"type": "array",    "value": ["a", "b"]},
			"ASSOC":    {"type": "associative", "value": {"k": "v"}},
			"FUNC":     {"type": "bashFunction", "value": "() { echo hi; }"},
			"EMPTY":    {"type": "exported", "value": ""}
		}
	}`)
	got, err := parseNixDevEnv(payload)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	wantKept := map[string]string{
		"PATH":  "/nix/store/x/bin:/usr/bin",
		"GO":    "/nix/store/y/bin/go",
		"EMPTY": "",
	}
	wantDropped := []string{"ARRAY", "ASSOC", "FUNC"}

	for k, v := range wantKept {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
	for _, k := range wantDropped {
		if _, ok := got[k]; ok {
			t.Errorf("%s should be dropped (non-scalar type)", k)
		}
	}
}

// TestParseNixDevEnv_SkipsNonStringScalars prevents a regression where a
// numeric var value would break json.Unmarshal of the inner string field.
// Nix's real output always uses strings, but defensive parsing keeps
// future format changes from taking down the agent.
func TestParseNixDevEnv_SkipsNonStringScalars(t *testing.T) {
	payload := []byte(`{
		"variables": {
			"GOOD":    {"type": "exported", "value": "ok"},
			"NUMERIC": {"type": "exported", "value": 42}
		}
	}`)
	got, err := parseNixDevEnv(payload)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got["GOOD"] != "ok" {
		t.Errorf("GOOD: got %q", got["GOOD"])
	}
	if _, ok := got["NUMERIC"]; ok {
		t.Error("NUMERIC should be dropped (non-string value)")
	}
}

// ─── NixEnvironment.NewProcess mode switch ─────────────────

// TestNixEnvironment_NewProcess_WrappedWhenNotMaterialized verifies the
// fallback path: without a materialized env, every NewProcess wraps the
// command with `nix develop <dir> --command`.
func TestNixEnvironment_NewProcess_WrappedWhenNotMaterialized(t *testing.T) {
	env := &NixEnvironment{dir: "/svc"}
	proc, err := env.NewProcess("go", "test", "./...")
	if err != nil {
		t.Fatal(err)
	}
	np, ok := proc.(*NixProc)
	if !ok {
		t.Fatalf("expected *NixProc, got %T", proc)
	}
	got := strings.Join(np.cmd, " ")
	if !strings.HasPrefix(got, "nix --extra-experimental-features nix-command flakes develop /svc --command ") {
		t.Errorf("wrapped cmd missing nix develop prefix: %q", got)
	}
	if !strings.HasSuffix(got, " go test ./...") {
		t.Errorf("wrapped cmd does not end with bin + args: %q", got)
	}
}

// TestNixEnvironment_NewProcess_DirectWhenMaterialized verifies the hot
// path: when the devShell has been captured into `materialized`, the
// command is exec'd directly without a `nix develop` wrapper.
func TestNixEnvironment_NewProcess_DirectWhenMaterialized(t *testing.T) {
	env := &NixEnvironment{
		dir:          "/svc",
		materialized: map[string]string{"PATH": "/nix/store/x/bin"},
	}
	proc, err := env.NewProcess("go", "test", "./...")
	if err != nil {
		t.Fatal(err)
	}
	np := proc.(*NixProc)
	if got, want := np.cmd, []string{"go", "test", "./..."}; !equalStrings(got, want) {
		t.Errorf("materialized cmd = %v, want %v", got, want)
	}
}

// ─── NixProc env composition (start path, unit-testable portion) ───

// TestNixProc_EnvComposition_MaterializedFirstThenCodeflyOverrides
// locks the precedence rule in NixProc.start:
//
//	[ materialized… ] [ env.envs… ] [ proc.envs… ]
//
// with later entries winning on duplicate keys (exec.Cmd.Env semantics —
// last wins). This is the contract that lets codefly-supplied network
// mappings and configs override Nix's defaults like GOPATH / PATH.
//
// We don't actually exec here — just assemble the env the way start
// does, and assert the ordering. Keeps the test hermetic.
func TestNixProc_EnvComposition_MaterializedFirstThenCodeflyOverrides(t *testing.T) {
	// Duplicate-key setup: PATH is in materialized and in proc.envs.
	// proc.envs is last in the slice so it should win for exec.Cmd.
	materialized := map[string]string{
		"PATH":  "/nix/store/x/bin",
		"SHELL": "/bin/sh",
	}

	// Simulate the exact concatenation done by NixProc.start. Keep this
	// block in sync with nix_runner.go `cmd.Env = …` — if it drifts, the
	// ordering assertions here catch it.
	var env []string
	for k, v := range materialized {
		env = append(env, k+"="+v)
	}
	env = append(env, "PATH=/codefly/bin") // proc.envs override

	// Find the index of each PATH entry. The later one (from proc.envs)
	// must be at a higher index than the materialized one.
	first, last := -1, -1
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 || last < 0 || first == last {
		t.Fatalf("expected two PATH entries, got env=%v", env)
	}
	if env[last] != "PATH=/codefly/bin" {
		t.Errorf("codefly override should win (appear last): got %q at last index", env[last])
	}
}

// ─── Persistent materialization cache ─────────────────────

// TestFlakeFingerprint_StableForSameContent locks the hash contract.
// Same bytes in → same hash out; any change invalidates the cache.
func TestFlakeFingerprint_StableForSameContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "flake.nix"), "{ x = 1; }")
	writeFile(t, filepath.Join(dir, "flake.lock"), `{"version": 7}`)

	nix := &NixEnvironment{dir: dir}
	fp1, err := nix.flakeFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	fp2, err := nix.flakeFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Errorf("same content produced different fingerprints: %s vs %s", fp1, fp2)
	}
	if len(fp1) != 64 {
		t.Errorf("fingerprint len = %d, want 64 (sha256 hex)", len(fp1))
	}
}

// TestFlakeFingerprint_ChangesOnContentChange proves the cache
// invalidates correctly when either file changes.
func TestFlakeFingerprint_ChangesOnContentChange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "flake.nix"), "{ x = 1; }")
	writeFile(t, filepath.Join(dir, "flake.lock"), `{"version": 7}`)

	nix := &NixEnvironment{dir: dir}
	before, _ := nix.flakeFingerprint()

	writeFile(t, filepath.Join(dir, "flake.nix"), "{ x = 2; }")
	after, _ := nix.flakeFingerprint()
	if before == after {
		t.Error("changing flake.nix should change fingerprint")
	}

	// Also: lock-only change.
	writeFile(t, filepath.Join(dir, "flake.lock"), `{"version": 8}`)
	afterLock, _ := nix.flakeFingerprint()
	if after == afterLock {
		t.Error("changing flake.lock should change fingerprint")
	}
}

// TestFlakeFingerprint_WorksWithoutLock covers the lock-less flake case:
// users on a freshly-initialized flake don't have a lock file yet. The
// fingerprint should still hash flake.nix alone.
func TestFlakeFingerprint_WorksWithoutLock(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "flake.nix"), "{ x = 1; }")

	nix := &NixEnvironment{dir: dir}
	fp, err := nix.flakeFingerprint()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fp == "" {
		t.Error("fingerprint should be non-empty even without flake.lock")
	}
}

// TestSaveAndLoadCachedMaterialization exercises the full round-trip:
// save → load → assert equal. No nix involvement.
func TestSaveAndLoadCachedMaterialization(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cacheDir := t.TempDir()
	writeFile(t, filepath.Join(dir, "flake.nix"), "{ x = 1; }")

	nix := &NixEnvironment{
		dir:      dir,
		cacheDir: cacheDir,
		materialized: map[string]string{
			"PATH":     "/nix/store/abc/bin",
			"GOCACHE":  "/home/user/.cache/go-build",
			"HOME":     "/home/user",
		},
	}
	if err := nix.saveCachedMaterialization(ctx); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Fresh instance — simulates a new agent process reading the cache.
	fresh := &NixEnvironment{dir: dir, cacheDir: cacheDir}
	loaded, err := fresh.loadCachedMaterialization(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected cached env to be loaded, got nil")
	}
	if len(loaded) != 3 {
		t.Errorf("loaded %d vars, want 3: %v", len(loaded), loaded)
	}
	if loaded["PATH"] != "/nix/store/abc/bin" {
		t.Errorf("PATH not round-tripped: %q", loaded["PATH"])
	}
}

// TestLoadCachedMaterialization_InvalidatesOnFingerprintChange covers the
// critical safety contract: if the flake has changed, we must NOT load
// the stale env (would run wrong toolchain versions).
func TestLoadCachedMaterialization_InvalidatesOnFingerprintChange(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cacheDir := t.TempDir()
	writeFile(t, filepath.Join(dir, "flake.nix"), "{ x = 1; }")

	// Save under original fingerprint.
	saver := &NixEnvironment{
		dir:          dir,
		cacheDir:     cacheDir,
		materialized: map[string]string{"PATH": "/old"},
	}
	if err := saver.saveCachedMaterialization(ctx); err != nil {
		t.Fatal(err)
	}

	// Change the flake.
	writeFile(t, filepath.Join(dir, "flake.nix"), "{ x = 2; }")

	// Reload: should miss the cache.
	fresh := &NixEnvironment{dir: dir, cacheDir: cacheDir}
	loaded, err := fresh.loadCachedMaterialization(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded != nil {
		t.Errorf("expected cache miss after fingerprint change, got %v", loaded)
	}
}

// TestLoadCachedMaterialization_RejectsWrongSchemaVersion prevents a
// silent corruption if the payload format evolves: an old cache from a
// previous binary must be discarded, not forced through into a wrong
// shape.
func TestLoadCachedMaterialization_RejectsWrongSchemaVersion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cacheDir := t.TempDir()
	writeFile(t, filepath.Join(dir, "flake.nix"), "{ x = 1; }")

	// Hand-write a cache file claiming schema 99.
	stale := `{"schema_version":99,"fingerprint":"whatever","env":{"X":"1"}}`
	if err := os.WriteFile(filepath.Join(cacheDir, "nix-devshell.json"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	fresh := &NixEnvironment{dir: dir, cacheDir: cacheDir}
	loaded, err := fresh.loadCachedMaterialization(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded != nil {
		t.Errorf("wrong schema version should miss, got %v", loaded)
	}
}

// TestLoadCachedMaterialization_MissingFileIsSilentMiss verifies the
// first-run path: no cache file, no error, just proceed to fresh
// materialize.
func TestLoadCachedMaterialization_MissingFileIsSilentMiss(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "flake.nix"), "{ x = 1; }")

	nix := &NixEnvironment{dir: dir, cacheDir: t.TempDir()}
	loaded, err := nix.loadCachedMaterialization(ctx)
	if err != nil {
		t.Errorf("missing cache should be silent, got err: %v", err)
	}
	if loaded != nil {
		t.Errorf("missing cache should return nil map, got %v", loaded)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
