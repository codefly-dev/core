package ciguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func runPublishInspection(t *testing.T, fixture releaseFixture, script string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "-c", "export INSPECTION_PID=$$\n"+script)
	cmd.Dir = fixture.dir
	// Model the step's declared token environment, not the test runner's.
	// Keeping the YAML value (including expressions) is sufficient for the
	// presence probe and makes a step-split mutant observable without a secret.
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "GH_TOKEN=") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	if token, declared := publishStep(t, publishRefusalStep).Env["GH_TOKEN"]; declared {
		cmd.Env = append(cmd.Env, "GH_TOKEN="+token)
	}
	cmd.Env = append(cmd.Env, "GITHUB_SHA="+fixture.tip, "GITHUB_REF_NAME=main")
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func removePublishBlock(t *testing.T, script, start, end string) string {
	t.Helper()
	a, b := strings.Index(script, start), strings.Index(script, end)
	require.GreaterOrEqual(t, a, 0)
	require.Greater(t, b, a)
	return script[:a] + script[b:]
}

func TestThePublishActionNeverExecutesGitFilters(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-runner-token-not-declared-by-step")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	for _, tc := range []struct{ scope, driver, attributes string }{
		{"local", "clean", "info"}, {"worktree", "clean", "info"},
		{"global", "clean", "committed"}, {"local", "process", "info"},
		{"worktree", "process", "committed"}, {"local", "clean", "alternate"},
	} {
		t.Run(tc.scope+"/"+tc.driver+"/"+tc.attributes, func(t *testing.T) {
			fixture := newPublishFixture(t)
			attributes := filepath.Join(fixture.dir, ".git", "info", "attributes")
			if tc.attributes == "committed" {
				attributes = filepath.Join(fixture.dir, ".gitattributes")
			}
			if tc.attributes == "alternate" {
				attributes = filepath.Join(t.TempDir(), "attributes")
			}
			require.NoError(t, os.MkdirAll(filepath.Dir(attributes), 0o700))
			require.NoError(t, os.WriteFile(attributes, []byte("Dockerfile filter=fixture\n"), 0o600))
			if tc.attributes == "committed" {
				publishGit(t, fixture.dir, "add", ".gitattributes")
				publishGit(t, fixture.dir, "commit", "-m", "committed filter attributes")
				fixture.tip = publishGit(t, fixture.dir, "rev-parse", "HEAD")
				publishGit(t, fixture.dir, "push", "origin", "HEAD:refs/heads/main")
				publishResolveAuthority(t, fixture)
			}
			if tc.scope == "worktree" {
				publishGit(t, fixture.dir, "config", "extensions.worktreeConfig", "true")
			}
			if tc.scope == "global" {
				t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "config"))
			}
			marker, driver := filepath.Join(t.TempDir(), "executed"), filepath.Join(t.TempDir(), "filter")
			t.Setenv("FILTER_MARKER", marker)
			require.NoError(t, os.WriteFile(driver, []byte(`#!/bin/sh
printf '%s\n' executed >> "$FILTER_MARKER"
# Linux also exposes the parent shell's starting environment; unexporting a
# variable is insufficient. The real inspection process never receives it.
for pid in "$PPID" "$INSPECTION_PID"; do
  if [ -r "/proc/$pid/environ" ]; then
    if tr '\000' '\n' < "/proc/$pid/environ" | grep -q '^GH_TOKEN='; then
      printf '%s\n' credential-visible >> "$FILTER_MARKER"
    fi
  fi
done
[ -z "${GH_TOKEN:-}" ] || printf '%s\n' credential-visible >> "$FILTER_MARKER"
exec cat
`), 0o700))
			publishGit(t, fixture.dir, "config", "--"+tc.scope, "filter.fixture."+tc.driver, driver)
			if tc.attributes == "alternate" {
				publishGit(t, fixture.dir, "config", "--local", "core.attributesFile", attributes)
			}
			// Force Git to re-hash unchanged content, so a real filter must execute
			// if the refusals are neutralised (process drivers need not speak a valid
			// protocol: launching the marker program is already the violation).
			mtime := time.Now().Add(time.Minute)
			require.NoError(t, os.Chtimes(filepath.Join(fixture.dir, "Dockerfile"), mtime, mtime))
			script := publishStep(t, publishRefusalStep).Run
			output, err := runPublishInspection(t, fixture, script)
			require.Error(t, err)
			if tc.scope == "global" {
				require.Contains(t, output, "filter attributes")
			} else {
				require.Contains(t, output, "Git config-defined hooks, filter drivers and alternate attributes files are unsupported")
			}
			require.NoFileExists(t, marker)

			mutant := removePublishBlock(t, script, "# Driver names are arbitrary.", "# No recorded caller")
			mutant = removePublishBlock(t, mutant, "# Check all in-tree attributes", "# These variables are set by GitHub")
			if tc.scope == "global" {
				mutant = strings.ReplaceAll(mutant, "export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_ATTR_NOSYSTEM=1", ":")
			}
			_, _ = runPublishInspection(t, fixture, mutant)
			executed, err := os.ReadFile(marker)
			require.NoError(t, err, "the real driver must run when the refusals are removed")
			require.Contains(t, string(executed), "executed")
			require.NotContains(t, string(executed), "credential-visible", "even a missed execution path has no inspection credential to steal")
		})
	}
}

func TestThePublishActionRefusesFilterAttributesWithoutADriver(t *testing.T) {
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	for _, location := range []string{".gitattributes", "nested/.gitattributes", ".git/info/attributes"} {
		t.Run(location, func(t *testing.T) {
			fixture := newPublishFixture(t)
			path := filepath.Join(fixture.dir, location)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(t, os.WriteFile(path, []byte("* filter=lfs\n"), 0o600))
			if !strings.HasPrefix(location, ".git/") {
				publishGit(t, fixture.dir, "add", location)
				publishGit(t, fixture.dir, "commit", "-m", "filter attributes without a driver")
				fixture.tip = publishGit(t, fixture.dir, "rev-parse", "HEAD")
				publishGit(t, fixture.dir, "push", "origin", "HEAD:refs/heads/main")
				publishResolveAuthority(t, fixture)
			}
			script := publishStep(t, publishRefusalStep).Run
			output, err := runPublishInspection(t, fixture, script)
			require.Error(t, err)
			require.Contains(t, output, "filter attributes")
			mutant := removePublishBlock(t, script, "# Check all in-tree attributes", "# These variables are set by GitHub")
			output, err = runPublishInspection(t, fixture, mutant)
			require.NoError(t, err, output)
		})
	}
}

func TestThePublishActionMatchesOnlyFilterAttributeTokens(t *testing.T) {
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	fixture := newPublishFixture(t)
	for _, tc := range []struct {
		attributes string
		refused    bool
	}{
		// Words and patterns that only look like assignments select nothing.
		{"# a filter comment\n  # filter=lfs\n", false},
		{"filter text\nfilter=lfs text\n", false},
		{"\"path filter=lfs\" text\n", false},
		{"Dockerfile !filter\n", false},
		{"[attr]custom filter=lfs\n", false},
		{"other/path filter=lfs\n", false},
		// Every assignment Git would apply to a build-context path is refused,
		// however it is spelled.
		{"* text filter=lfs\n", true},
		{"* -filter\n", true},
		{"* filter\n", true},
		{"Dockerfile\ttext\tfilter=lfs\r\n", true},
		{"\"Dockerfile\" text filter=lfs\n", true},
		{"\"Dockerfile\"filter=lfs\n", true},
		{"\"[attr]x\"filter=lfs\n* x\n", true},
		{"[attr]custom filter=lfs\n* custom\n", true},
		{"[attr]a b\n[attr]b filter=lfs\n* a\n", true},
		{"[!\xff]ockerfile filter=lfs\n", true},
	} {
		t.Run(tc.attributes, func(t *testing.T) {
			require.NoError(t, os.WriteFile(filepath.Join(fixture.dir, ".git/info/attributes"), []byte(tc.attributes), 0o600))
			output, err := runPublishInspection(t, fixture, publishStep(t, publishRefusalStep).Run)
			if tc.refused {
				require.Error(t, err, output)
				require.Contains(t, output, "filter attributes")
			} else {
				require.NoError(t, err, output)
			}
		})
	}
}

func TestThePublishActionRefusesSubmodulesAndTheirCredentials(t *testing.T) {
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	for _, credential := range []string{"http.extraheader", "credential.helper", "retained metadata"} {
		t.Run(credential, func(t *testing.T) {
			fixture := newPublishFixture(t)
			source := newReleaseFixture(t)
			publishGit(t, fixture.dir, "-c", "protocol.file.allow=always", "submodule", "add", source.dir, "sub")
			publishGit(t, fixture.dir, "commit", "-am", "committed submodule")
			key := credential
			if credential == "retained metadata" {
				key = "http.extraheader"
			}
			publishGit(t, filepath.Join(fixture.dir, "sub"), "config", "--local", key, "fixture-secret")
			if credential == "retained metadata" {
				publishGit(t, fixture.dir, "rm", "-f", "sub", ".gitmodules")
				publishGit(t, fixture.dir, "commit", "-m", "removed submodule checkout")
			}
			fixture.tip = publishGit(t, fixture.dir, "rev-parse", "HEAD")
			publishGit(t, fixture.dir, "push", "origin", "HEAD:refs/heads/main")
			publishResolveAuthority(t, fixture)
			script := publishStep(t, publishRefusalStep).Run
			output, err := runPublishInspection(t, fixture, script)
			require.Error(t, err)
			require.Contains(t, output, "submodules and retained submodule metadata are unsupported")
			require.NotContains(t, output, "fixture-secret")
			mutant := removePublishBlock(t, script, "# No recorded caller", "# Check all in-tree attributes")
			output, err = runPublishInspection(t, fixture, mutant)
			require.NoError(t, err, output)
		})
	}
}

func TestThePublishAuthorityDoesNotReadCheckoutOrInheritedGitConfig(t *testing.T) {
	fixture := newPublishFixture(t)
	hostile := filepath.Join(t.TempDir(), "invalid-config")
	require.NoError(t, os.WriteFile(hostile, []byte("this is not a Git config\n"), 0o600))
	t.Setenv("GIT_CONFIG_GLOBAL", hostile)
	t.Setenv("GIT_DIR", filepath.Join(fixture.dir, ".git"))
	t.Setenv("GIT_WORK_TREE", fixture.dir)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.invalid://.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "file://")
	publishResolveAuthority(t, fixture)
}

func TestThePublishInspectionRefusesMissingDefaultBranchHistory(t *testing.T) {
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	fixture := newPublishFixture(t)
	other := filepath.Join(t.TempDir(), "other")
	origin := publishGit(t, fixture.dir, "remote", "get-url", "origin")
	publishGit(t, fixture.dir, "clone", origin, other)
	require.NoError(t, os.WriteFile(filepath.Join(other, "new"), []byte("merged elsewhere"), 0o600))
	publishGit(t, other, "add", "new")
	publishGit(t, other, "commit", "-m", "advance default branch")
	publishGit(t, other, "push", "origin", "HEAD:refs/heads/main")
	remote := fixture
	remote.tip = publishGit(t, other, "rev-parse", "HEAD")
	publishResolveAuthority(t, remote)
	output, err := runPublishInspection(t, fixture, publishStep(t, publishRefusalStep).Run)
	require.Error(t, err)
	require.Contains(t, output, "history is missing locally; refusing to publish")
}
