package sdk

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
)

// packageDir is this package's directory, captured before any test can change
// the process working directory. Fixture paths and the test CLI build must not
// depend on where a previous test left the process.
var packageDir = func() string {
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return dir
}()

func fixtureDir(t *testing.T, elements ...string) string {
	t.Helper()
	return filepath.Join(append([]string{packageDir, "testdata", "sessions"}, elements...)...)
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

// A flat workspace has no module.codefly.yaml: the module is the workspace
// itself. Resolving that module must not adopt the service, because adopting it
// stamps the module name onto every dependency that declared none — and
// serviceDependencyCandidates matches a dependency to a producer endpoint by
// exact module equality, so stamping it silently changes which endpoints a flat
// service resolves.
func TestFlatLayoutIdentityLeavesDeclaredDependencyModulesAlone(t *testing.T) {
	session := &Dependencies{dir: fixtureDir(t, "flat", "services", "gateway")}

	mod, err := session.Module(t.Context())
	if err != nil {
		t.Fatalf("Module() error = %v", err)
	}
	svc, err := session.Service(t.Context())
	if err != nil {
		t.Fatalf("Service() error = %v", err)
	}
	if mod.Name != "flat-session" || svc.Name != "gateway" || svc.Version != "7.8.9" {
		t.Fatalf("identity = %s/%s@%s, want flat-session/gateway@7.8.9", mod.Name, svc.Name, svc.Version)
	}
	if len(svc.ServiceDependencies) != 1 {
		t.Fatalf("dependencies = %v, want exactly one", svc.ServiceDependencies)
	}
	if got := svc.ServiceDependencies[0].Module; got != "" {
		t.Fatalf("declared dependency module = %q, want it left as declared", got)
	}
}

// A directory that owns no service answers with a nil service and no error:
// callers of these package functions branch on nil, not on an error.
func TestPackageIdentityIsNilOutsideAService(t *testing.T) {
	t.Chdir(t.TempDir())

	svc, err := Service()
	if err != nil || svc != nil {
		t.Fatalf("Service() = %v, %v, want nil, nil", svc, err)
	}
	mod, err := Module()
	if err != nil || mod != nil {
		t.Fatalf("Module() = %v, %v, want nil, nil", mod, err)
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
	// The holder has to be identifiable, or a caller cannot tell a leaked
	// session from a live one.
	if !strings.Contains(err.Error(), first.dir) {
		t.Fatalf("rejection %q does not name the session holding the environment", err)
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

// A command-scoped session's child must not inherit the endpoints another
// session injected into os.Environ: those belong to a different workspace and
// point at dependencies this session never declared. The caller's own values,
// which the SDK never wrote, are borrowed unchanged.
func TestEnvironDropsAnotherSessionsInjectedValues(t *testing.T) {
	t.Setenv("CODEFLY_TEST_BORROWED", "caller")
	t.Cleanup(func() { os.Unsetenv("CODEFLY_TEST_FOREIGN_ENDPOINT") })

	owner := &Dependencies{dir: "owner"}
	if err := owner.apply(sessionEnvironmentOf("CODEFLY_TEST_FOREIGN_ENDPOINT", "127.0.0.1:1")); err != nil {
		t.Fatalf("apply() error = %v", err)
	}
	t.Cleanup(owner.ReleaseEnvironment)

	commandScoped := &Dependencies{dir: "command-scoped"}
	commandScoped.setResolved(sessionEnvironmentOf("CODEFLY_TEST_OWN_ENDPOINT", "127.0.0.1:2"))

	environ := commandScoped.Environ()
	for _, entry := range environ {
		if strings.HasPrefix(entry, "CODEFLY_TEST_FOREIGN_ENDPOINT=") {
			t.Fatalf("Environ() leaked another session's endpoint: %s", entry)
		}
	}
	if !slices.Contains(environ, "CODEFLY_TEST_OWN_ENDPOINT=127.0.0.1:2") {
		t.Fatal("Environ() is missing the session's own endpoint")
	}
	if !slices.Contains(environ, "CODEFLY_TEST_BORROWED=caller") {
		t.Fatal("Environ() dropped a borrowed value the SDK never wrote")
	}
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
