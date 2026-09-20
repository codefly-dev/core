package testgit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAmbientSigningAndEditor(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(t.TempDir(), "gitconfig")
	body := "[commit]\n gpgSign = true\n[tag]\n gpgSign = true\n[core]\n editor = false\n[user]\n useConfigOnly = true\n"
	require.NoError(t, os.WriteFile(config, []byte(body), 0o600))
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"commit", "--allow-empty", "-m", "initial"},
		{"tag", "v1.0.0"},
		{"worktree", "add", "--detach", filepath.Join(t.TempDir(), "checkout"), "v1.0.0"},
	} {
		out, err := Run(t.Context(), dir, nil, args...)
		require.NoError(t, err, "%s", out)
	}
	out, err := Run(t.Context(), dir, nil, "cat-file", "-t", "v1.0.0")
	require.NoError(t, err)
	require.Equal(t, "commit", strings.TrimSpace(string(out)))
	unchanged, err := os.ReadFile(config)
	require.NoError(t, err)
	require.Equal(t, body, string(unchanged))
}

func TestCancellationKillsGitEditorChildren(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint("deadline=", deadline), func(t *testing.T) {
			dir := t.TempDir()
			for _, args := range [][]string{{"init"}, {"commit", "--allow-empty", "-m", "initial"}} {
				out, err := Run(t.Context(), dir, nil, args...)
				require.NoError(t, err, "%s", out)
			}
			editor := filepath.Join(t.TempDir(), "editor")
			pidPath := filepath.Join(t.TempDir(), "pid")
			require.NoError(t, os.WriteFile(editor, []byte("#!/bin/sh\necho $$ > \"$TEST_GIT_EDITOR_PID\"\nexec sleep 60\n"), 0o755))
			ctx, cancel := context.WithCancel(t.Context())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 2*time.Second)
			}
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := Run(ctx, dir, []string{"GIT_EDITOR=" + editor, "TEST_GIT_EDITOR_PID=" + pidPath}, "-c", "tag.gpgSign=true", "tag", "v1.0.0")
				done <- err
			}()
			require.Eventually(t, func() bool { data, _ := os.ReadFile(pidPath); return strings.TrimSpace(string(data)) != "" }, time.Second, 10*time.Millisecond)
			data, err := os.ReadFile(pidPath)
			require.NoError(t, err)
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			require.NoError(t, err)
			select {
			case err := <-done:
				t.Fatalf("signed tag did not wait for editor: %v", err)
			default:
			}
			if !deadline {
				cancel()
			}
			select {
			case err := <-done:
				require.Error(t, err)
			case <-time.After(4 * time.Second):
				t.Fatal("Git survived cancellation")
			}
			require.Eventually(t, func() bool { return syscall.Kill(pid, 0) == syscall.ESRCH }, 3*time.Second, 10*time.Millisecond)
		})
	}
}
