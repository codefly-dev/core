package sdk

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
)

func fixtureDir(t *testing.T, elements ...string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join(append([]string{"testdata", "sessions"}, elements...)...))
	if err != nil {
		t.Fatalf("resolve fixture: %v", err)
	}
	return dir
}

func sessionEnvironmentOf(values ...string) *sessionEnvironment {
	var variables []*resources.EnvironmentVariable
	for i := 0; i < len(values); i += 2 {
		variables = append(variables, &resources.EnvironmentVariable{Key: values[i], Value: values[i+1]})
	}
	return newSessionEnvironment(variables)
}

// Each session resolves the identity of the directory it was anchored to. The
// process working directory — the sdk package, which owns no service — is never
// consulted, so sessions on different services coexist in one process.
func TestSessionsResolveIdentityFromTheirOwnDirectory(t *testing.T) {
	alpha := &Dependencies{dir: fixtureDir(t, "alpha", "modules", "shop", "services", "web")}
	beta := &Dependencies{dir: fixtureDir(t, "beta", "modules", "office", "services", "portal")}

	for _, session := range []struct {
		deps    *Dependencies
		module  string
		service string
		version string
	}{
		{alpha, "shop", "web", "1.2.3"},
		{beta, "office", "portal", "4.5.6"},
	} {
		mod, err := session.deps.Module(t.Context())
		if err != nil {
			t.Fatalf("Module() error = %v", err)
		}
		svc, err := session.deps.Service(t.Context())
		if err != nil {
			t.Fatalf("Service() error = %v", err)
		}
		if mod.Name != session.module || svc.Name != session.service || svc.Version != session.version {
			t.Fatalf("identity = %s/%s@%s, want %s/%s@%s",
				mod.Name, svc.Name, svc.Version, session.module, session.service, session.version)
		}
	}
}

// A session keeps the identity of its own directory after the process changes
// working directory; the deprecated package-level convenience re-resolves,
// instead of serving the identity it cached for the previous directory.
func TestWorkingDirectoryChangeMovesOnlyTheConvenienceIdentity(t *testing.T) {
	alphaWeb := fixtureDir(t, "alpha", "modules", "shop", "services", "web")
	betaPortal := fixtureDir(t, "beta", "modules", "office", "services", "portal")

	session := &Dependencies{dir: alphaWeb}
	if svc, err := session.Service(t.Context()); err != nil || svc.Name != "web" {
		t.Fatalf("session service = %v, %v, want web", svc, err)
	}

	t.Chdir(alphaWeb)
	svc, err := Service()
	if err != nil || svc.Name != "web" {
		t.Fatalf("Service() = %v, %v, want web", svc, err)
	}

	t.Chdir(betaPortal)
	svc, err = Service()
	if err != nil || svc.Name != "portal" {
		t.Fatalf("Service() after chdir = %v, %v, want portal", svc, err)
	}
	mod, err := Module()
	if err != nil || mod.Name != "office" {
		t.Fatalf("Module() after chdir = %v, %v, want office", mod, err)
	}
	if svc, err := session.Service(t.Context()); err != nil || svc.Name != "web" {
		t.Fatalf("anchored session service = %v, %v, want web", svc, err)
	}
}

// Release returns every variable the session still owns to what it was before
// the session first injected it — including removing one that did not exist.
func TestReleaseRestoresOwnedVariables(t *testing.T) {
	t.Setenv("CODEFLY_TEST_EXISTING", "before")
	t.Cleanup(func() { os.Unsetenv("CODEFLY_TEST_NEW") })

	session := &Dependencies{}
	if err := session.apply(sessionEnvironmentOf(
		"CODEFLY_TEST_EXISTING", "session",
		"CODEFLY_TEST_NEW", "session",
	)); err != nil {
		t.Fatalf("apply() error = %v", err)
	}
	if got := os.Getenv("CODEFLY_TEST_EXISTING"); got != "session" {
		t.Fatalf("injected value = %q, want session", got)
	}

	session.ReleaseEnvironment()
	if got := os.Getenv("CODEFLY_TEST_EXISTING"); got != "before" {
		t.Fatalf("released value = %q, want before", got)
	}
	if _, present := os.LookupEnv("CODEFLY_TEST_NEW"); present {
		t.Fatal("a variable the session created was left behind")
	}
	session.ReleaseEnvironment()
}

// A caller that changes an injected value takes it over: release leaves that
// change alone rather than clobbering it with the pre-session value.
func TestReleaseKeepsValuesTheCallerChanged(t *testing.T) {
	t.Setenv("CODEFLY_TEST_OWNED", "before")

	session := &Dependencies{}
	if err := session.apply(sessionEnvironmentOf("CODEFLY_TEST_OWNED", "session")); err != nil {
		t.Fatalf("apply() error = %v", err)
	}
	os.Setenv("CODEFLY_TEST_OWNED", "caller")
	session.ReleaseEnvironment()

	if got := os.Getenv("CODEFLY_TEST_OWNED"); got != "caller" {
		t.Fatalf("released value = %q, want the caller's own value", got)
	}
}

// Injecting twice from one session keeps the values captured before the first
// injection as the ones release restores.
func TestRepeatedInjectionRestoresPreSessionValues(t *testing.T) {
	t.Setenv("CODEFLY_TEST_REPEATED", "before")

	session := &Dependencies{}
	if err := session.apply(sessionEnvironmentOf("CODEFLY_TEST_REPEATED", "first")); err != nil {
		t.Fatalf("first apply() error = %v", err)
	}
	if err := session.apply(sessionEnvironmentOf("CODEFLY_TEST_REPEATED", "second")); err != nil {
		t.Fatalf("second apply() error = %v", err)
	}
	if got := os.Getenv("CODEFLY_TEST_REPEATED"); got != "second" {
		t.Fatalf("re-injected value = %q, want second", got)
	}

	session.ReleaseEnvironment()
	if got := os.Getenv("CODEFLY_TEST_REPEATED"); got != "before" {
		t.Fatalf("released value = %q, want before", got)
	}
}

// A second session is rejected before it changes anything, and takes over once
// the first one releases.
func TestCompetingGlobalInjectionFailsBeforeMutation(t *testing.T) {
	t.Setenv("CODEFLY_TEST_CONTESTED", "before")
	t.Cleanup(func() { os.Unsetenv("CODEFLY_TEST_SECOND") })

	first := &Dependencies{}
	if err := first.apply(sessionEnvironmentOf("CODEFLY_TEST_CONTESTED", "first")); err != nil {
		t.Fatalf("first apply() error = %v", err)
	}
	second := &Dependencies{}
	err := second.apply(sessionEnvironmentOf(
		"CODEFLY_TEST_CONTESTED", "second",
		"CODEFLY_TEST_SECOND", "second",
	))
	if err == nil || !strings.Contains(err.Error(), "owns the process environment") {
		t.Fatalf("competing injection error = %v, want single-owner rejection", err)
	}
	if got := os.Getenv("CODEFLY_TEST_CONTESTED"); got != "first" {
		t.Fatalf("contested value = %q, want first", got)
	}
	if _, present := os.LookupEnv("CODEFLY_TEST_SECOND"); present {
		t.Fatal("the rejected session mutated the process environment")
	}

	first.ReleaseEnvironment()
	if err := second.apply(sessionEnvironmentOf("CODEFLY_TEST_CONTESTED", "second")); err != nil {
		t.Fatalf("apply() after release error = %v", err)
	}
	second.ReleaseEnvironment()
}

// A write that fails mid-round restores the keys already written and claims no
// ownership, so the process environment is exactly what it was and the next
// session can still take it.
func TestFailedInjectionRollsBackAndClaimsNothing(t *testing.T) {
	t.Setenv("CODEFLY_TEST_ROLLBACK", "before")

	session := &Dependencies{}
	err := session.apply(sessionEnvironmentOf(
		"CODEFLY_TEST_ROLLBACK", "session",
		"CODEFLY_TEST_INVALID=KEY", "session",
	))
	if err == nil || !strings.Contains(err.Error(), "CODEFLY_TEST_INVALID=KEY") {
		t.Fatalf("apply() error = %v, want the rejected key", err)
	}
	if got := os.Getenv("CODEFLY_TEST_ROLLBACK"); got != "before" {
		t.Fatalf("rolled-back value = %q, want before", got)
	}

	other := &Dependencies{}
	if err := other.apply(sessionEnvironmentOf("CODEFLY_TEST_ROLLBACK", "other")); err != nil {
		t.Fatalf("a failed injection left the process environment owned: %v", err)
	}
	other.ReleaseEnvironment()
}

// Environ projects the session onto a child environment — one entry per key,
// overriding whatever the process carries — while leaving os.Environ alone.
func TestEnvironOverridesTheProcessEnvironmentWithoutMutatingIt(t *testing.T) {
	t.Setenv("CODEFLY_TEST_COMMAND_SCOPED", "process")

	session := &Dependencies{}
	session.setResolved(sessionEnvironmentOf("CODEFLY_TEST_COMMAND_SCOPED", "session"))

	environ := session.Environ()
	if got := slices.Contains(environ, "CODEFLY_TEST_COMMAND_SCOPED=session"); !got {
		t.Fatalf("Environ() is missing the session value: %v", environ)
	}
	if slices.Contains(environ, "CODEFLY_TEST_COMMAND_SCOPED=process") {
		t.Fatal("Environ() kept the process value for a session-owned key")
	}
	if got := os.Getenv("CODEFLY_TEST_COMMAND_SCOPED"); got != "process" {
		t.Fatalf("process value = %q, want process", got)
	}

	variables := session.EnvironmentVariables()
	variables["CODEFLY_TEST_COMMAND_SCOPED"] = "mutated"
	if got := session.EnvironmentVariables()["CODEFLY_TEST_COMMAND_SCOPED"]; got != "session" {
		t.Fatalf("EnvironmentVariables() is not a defensive copy: %q", got)
	}
}
