package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTrivyCacheLockSerializesAgentProcesses(t *testing.T) {
	cache := t.TempDir()

	first, err := lockTrivyCache(context.Background(), cache)
	require.NoError(t, err)

	waitContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = lockTrivyCache(waitContext, cache)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.NoError(t, first.Unlock())
	second, err := lockTrivyCache(context.Background(), cache)
	require.NoError(t, err)
	require.NoError(t, second.Unlock())
}

func TestResetTrivyDatabasesRemovesDatabasesAndPreservesOtherCacheState(t *testing.T) {
	cache := testTrivyCacheDir(t)
	writeTrivyCacheFile(t, cache, "db/trivy.db")
	writeTrivyCacheFile(t, cache, "java-db/trivy-java.db")
	writeTrivyCacheFile(t, cache, "fanal/fanal.db")
	writeTrivyCacheFile(t, cache, ".codefly.lock")

	require.NoError(t, resetTrivyDatabases(context.Background()))
	require.NoDirExists(t, filepath.Join(cache, "db"))
	require.NoDirExists(t, filepath.Join(cache, "java-db"))
	require.FileExists(t, filepath.Join(cache, "fanal", "fanal.db"))
	require.FileExists(t, filepath.Join(cache, ".codefly.lock"))
}

func TestResetTrivyDatabasesUsesContainerForRootOwnedCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions are required")
	}
	cache := testTrivyCacheDir(t)
	db := writeTrivyCacheFile(t, cache, "db/trivy.db")
	javaDB := writeTrivyCacheFile(t, cache, "java-db/trivy-java.db")
	makeDirectoryReadOnly(t, filepath.Dir(db))
	makeDirectoryReadOnly(t, filepath.Dir(javaDB))

	argsFile := filepath.Join(t.TempDir(), "docker-args")
	installFakeDocker(t, `#!/bin/sh
printf '%s\n' "$*" > "$CODEFLY_TEST_DOCKER_ARGS"
mount=
previous=
for argument in "$@"; do
	if [ "$previous" = "-v" ]; then
		mount="$argument"
		break
	fi
	previous="$argument"
done
cache="${mount%:/root/.cache/trivy}"
/bin/chmod -R u+w "$cache/db" "$cache/java-db"
/bin/rm -rf "$cache/db" "$cache/java-db"
`)
	t.Setenv("CODEFLY_TEST_DOCKER_ARGS", argsFile)

	require.NoError(t, resetTrivyDatabases(context.Background()))
	require.NoDirExists(t, filepath.Join(cache, "db"))
	require.NoDirExists(t, filepath.Join(cache, "java-db"))
	args, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Contains(t, string(args), TrivyImage)
	require.Contains(t, string(args), "clean --vuln-db --java-db")
}

func TestResetTrivyDatabasesReportsJavaAndContainerCleanupFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions are required")
	}
	cache := testTrivyCacheDir(t)
	writeTrivyCacheFile(t, cache, "db/trivy.db")
	javaDB := writeTrivyCacheFile(t, cache, "java-db/trivy-java.db")
	makeDirectoryReadOnly(t, filepath.Dir(javaDB))
	installFakeDocker(t, "#!/bin/sh\necho cleanup denied >&2\nexit 23\n")

	err := resetTrivyDatabases(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "remove Trivy Java database")
	require.Contains(t, err.Error(), "cleanup denied")
	require.NoDirExists(t, filepath.Join(cache, "db"))
	require.DirExists(t, filepath.Join(cache, "java-db"))
}

func TestResetTrivyDatabasesWaitsForScanLock(t *testing.T) {
	cache := testTrivyCacheDir(t)
	writeTrivyCacheFile(t, cache, "db/trivy.db")
	scanLock, err := lockTrivyCache(context.Background(), cache)
	require.NoError(t, err)

	result := make(chan error, 1)
	go func() {
		result <- resetTrivyDatabases(context.Background())
	}()
	select {
	case err := <-result:
		t.Fatalf("reset returned while scan lock was held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	require.NoError(t, scanLock.Unlock())
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("reset did not resume after scan lock was released")
	}
	require.NoDirExists(t, filepath.Join(cache, "db"))
}

func TestResetTrivyDatabasesHonorsCallerCancellationWhileWaiting(t *testing.T) {
	cache := testTrivyCacheDir(t)
	writeTrivyCacheFile(t, cache, "db/trivy.db")
	scanLock, err := lockTrivyCache(context.Background(), cache)
	require.NoError(t, err)
	defer func() { require.NoError(t, scanLock.Unlock()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = resetTrivyDatabases(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.DirExists(t, filepath.Join(cache, "db"))
}

func TestDockerAndResetTrivyDatabasesShareOneCacheContract(t *testing.T) {
	cache := testTrivyCacheDir(t)
	bin := t.TempDir()
	trivy := filepath.Join(bin, "trivy")
	script := `#!/bin/sh
cache=
previous=
for argument in "$@"; do
	if [ "$previous" = "--cache-dir" ]; then
		cache="$argument"
		break
	fi
	previous="$argument"
done
/bin/mkdir -p "$cache/db"
printf '%s\n' torn > "$cache/db/trivy.db"
printf '%s\n' '{"Results":[]}'
`
	require.NoError(t, os.WriteFile(trivy, []byte(script), 0o755))
	t.Setenv("PATH", bin)

	result, err := Docker(context.Background(), "postgres:16")
	require.NoError(t, err)
	require.Equal(t, "trivy", result.Tool)
	require.FileExists(t, filepath.Join(cache, "db", "trivy.db"))

	require.NoError(t, resetTrivyDatabases(context.Background()))
	require.NoDirExists(t, filepath.Join(cache, "db"))
}

func TestDockerRecoversDatabaseBeforeAnotherScanCanUseIt(t *testing.T) {
	cache := testTrivyCacheDir(t)
	bin := t.TempDir()
	stateFile := filepath.Join(t.TempDir(), "trivy-calls")
	trivy := filepath.Join(bin, "trivy")
	script := `#!/bin/sh
cache=
previous=
for argument in "$@"; do
	if [ "$previous" = "--cache-dir" ]; then
		cache="$argument"
		break
	fi
	previous="$argument"
done
calls=0
if [ -f "$CODEFLY_TEST_TRIVY_CALLS" ]; then
	read -r calls < "$CODEFLY_TEST_TRIVY_CALLS"
fi
calls=$((calls + 1))
printf '%s\n' "$calls" > "$CODEFLY_TEST_TRIVY_CALLS"
case "$calls" in
1)
	/bin/mkdir -p "$cache/db"
	printf '%s\n' torn > "$cache/db/trivy.db"
	printf '%s\n' 'fatal error: fault' >&2
	printf '%s\n' 'go.etcd.io/bbolt/internal/common.(*Page).FastCheck' >&2
	printf '%s\n' 'github.com/aquasecurity/trivy-db/pkg/db.Config.forEach' >&2
	exit 2
	;;
2)
	if [ -e "$cache/db/trivy.db" ]; then
		printf '%s\n' 'second scan observed the torn database' >&2
		exit 9
	fi
	/bin/mkdir -p "$cache/db"
	printf '%s\n' clean > "$cache/db/trivy.db"
	;;
*)
	if [ "$(/bin/cat "$cache/db/trivy.db")" != clean ]; then
		printf '%s\n' 'later scan did not observe the repaired database' >&2
		exit 10
	fi
	;;
esac
printf '%s\n' '{"Results":[]}'
`
	require.NoError(t, os.WriteFile(trivy, []byte(script), 0o755))
	t.Setenv("PATH", bin)
	t.Setenv("CODEFLY_TEST_TRIVY_CALLS", stateFile)

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := Docker(context.Background(), "postgres:16")
			results <- err
		}()
	}
	close(start)

	require.NoError(t, <-results)
	require.NoError(t, <-results)
	calls, err := os.ReadFile(stateFile)
	require.NoError(t, err)
	require.Equal(t, "3\n", string(calls), "one failed scan, one retry, and one waiting scan must run")
	database, err := os.ReadFile(filepath.Join(cache, "db", "trivy.db"))
	require.NoError(t, err)
	require.Equal(t, "clean\n", string(database))
}

func testTrivyCacheDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "cache"))
	cache, err := trivyCacheDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(cache, 0o700))
	return cache
}

func writeTrivyCacheFile(t *testing.T, cache, relative string) string {
	t.Helper()
	path := filepath.Join(cache, filepath.FromSlash(relative))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("cache"), 0o600))
	return path
}

func makeDirectoryReadOnly(t *testing.T, directory string) {
	t.Helper()
	require.NoError(t, os.Chmod(directory, 0o500))
	t.Cleanup(func() {
		_ = os.Chmod(directory, 0o700)
	})
}

func installFakeDocker(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755))
	t.Setenv("PATH", bin)
}

func TestResetTrivyDatabasesRejectsSuccessfulNoopContainerCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions are required")
	}
	cache := testTrivyCacheDir(t)
	db := writeTrivyCacheFile(t, cache, "db/trivy.db")
	makeDirectoryReadOnly(t, filepath.Dir(db))
	installFakeDocker(t, "#!/bin/sh\nexit 0\n")

	err := resetTrivyDatabases(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrExist) || strings.Contains(err.Error(), "still exists"), err)
}
