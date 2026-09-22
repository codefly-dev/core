package dependencies

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	v0 "github.com/codefly-dev/core/generated/go/codefly/cli/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// resolvedIdentity is the module/service pair a dependency session resolved
// once from an absolute directory captured when the session was created.
// Resolution never reads the process working directory, so a later chdir cannot
// move the identity of a live session.
type resolvedIdentity struct {
	module  *resources.Module
	service *resources.Service
}

// resolveSessionIdentity is findSessionIdentity for callers that require an
// identity, turning "nothing here" into an error naming the directory searched.
func resolveSessionIdentity(ctx context.Context, dir string) (*resolvedIdentity, error) {
	identity, err := findSessionIdentity(ctx, dir)
	if err != nil {
		return nil, err
	}
	if identity == nil {
		return nil, fmt.Errorf("no Codefly service found from %s", dir)
	}
	return identity, nil
}

// findSessionIdentity resolves the module and service owning dir, returning a
// nil identity — not an error — when the directory belongs to no service, which
// is the contract the deprecated package-level Service and Module keep.
//
// The module of a service that declares none is left alone: filling it in would
// change which producer endpoints resolveEnvironment matches, since
// serviceDependencyCandidates compares dependency and endpoint modules exactly.
func findSessionIdentity(ctx context.Context, dir string) (*resolvedIdentity, error) {
	mod, svc, err := resources.LoadModuleAndServiceUpFrom(ctx, dir)
	if err != nil {
		return nil, err
	}
	if svc == nil {
		return nil, nil
	}
	if mod == nil {
		mod, err = flatLayoutModule(ctx, dir)
		if err != nil {
			return nil, err
		}
	}
	if mod == nil {
		return nil, nil
	}
	return &resolvedIdentity{module: mod, service: svc}, nil
}

// flatLayoutModule resolves the implicit module of a flat workspace, where
// services live directly under the workspace and no module.codefly.yaml exists.
func flatLayoutModule(ctx context.Context, dir string) (*resources.Module, error) {
	workspace, err := resources.FindWorkspaceUpFrom(ctx, dir)
	if err != nil {
		return nil, err
	}
	if workspace == nil || workspace.Layout != resources.LayoutKindFlat {
		return nil, nil
	}
	return workspace.LoadModuleFromName(ctx, workspace.Name)
}

// sessionEnvironment is the complete set of variables a dependency session
// projects, resolved before anything is mutated. Keys keep their resolution
// order and each one holds the value of its last occurrence, matching the
// last-writer-wins semantics of sequential injection.
type sessionEnvironment struct {
	keys   []string
	values map[string]string
}

func newSessionEnvironment(variables []*resources.EnvironmentVariable) *sessionEnvironment {
	env := &sessionEnvironment{values: make(map[string]string, len(variables))}
	for _, variable := range variables {
		if _, known := env.values[variable.Key]; !known {
			env.keys = append(env.keys, variable.Key)
		}
		env.values[variable.Key] = variable.ValueAsString()
	}
	return env
}

// variables returns a defensive copy of the resolved values.
func (e *sessionEnvironment) variables() map[string]string {
	out := make(map[string]string, len(e.values))
	for key, value := range e.values {
		out[key] = value
	}
	return out
}

// environ overlays the session values onto base. An entry base carries for a
// key this session resolved is replaced, and one for a key in foreign is
// dropped: those were installed in os.Environ by another SDK session, and a
// child of this session must not inherit another session's endpoints. Values
// the SDK never wrote — the caller's own environment — are borrowed as they
// are, which is what keeps borrowed and SDK-owned values distinguishable.
func (e *sessionEnvironment) environ(base []string, foreign map[string]struct{}) []string {
	out := make([]string, 0, len(base)+len(e.keys))
	for _, entry := range base {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, owned := e.values[key]; owned {
				continue
			}
			if _, installed := foreign[key]; installed {
				continue
			}
		}
		out = append(out, entry)
	}
	for _, key := range e.keys {
		out = append(out, key+"="+e.values[key])
	}
	return out
}

// ownedVariable records what a variable looked like before the session first
// injected it, plus the value the session installed. Release restores previous
// only while the process environment still holds applied, so a caller that
// deliberately changed an injected value keeps its change.
type ownedVariable struct {
	key      string
	previous string
	existed  bool
	applied  string
}

// globalEnvironment serializes SDK-owned mutation of the process environment.
// os.Environ is a single process-wide resource: exactly one dependency session
// may own it at a time, and a competing session is rejected before any value
// changes. Sessions that need to coexist project their environment onto child
// commands instead — see Dependencies.Environ.
//
// Lock order is globalEnvironment.mu before any Dependencies.mu. Nothing may
// take globalEnvironment.mu while already holding a session lock.
var globalEnvironment struct {
	mu    sync.Mutex
	owner *Dependencies
}

// claimGlobalEnvironment makes l the owner of the process environment, or
// explains who has it. WithDependencies claims before spawning anything, so a
// competing session is refused before it provisions infrastructure it would
// then have to tear down; apply claims again for a session that injects
// directly through SetEnvironment.
//
// A session whose Codefly process has exited can no longer be released by its
// owner — the values it installed point at dependencies that are gone — so it
// is restored and replaced rather than blocking the process forever.
func claimGlobalEnvironment(l *Dependencies) error {
	globalEnvironment.mu.Lock()
	defer globalEnvironment.mu.Unlock()
	return claimGlobalEnvironmentLocked(l)
}

func claimGlobalEnvironmentLocked(l *Dependencies) error {
	owner := globalEnvironment.owner
	if owner == nil || owner == l {
		globalEnvironment.owner = l
		return nil
	}
	if owner.defunct() {
		owner.restoreOwned()
		globalEnvironment.owner = l
		return nil
	}
	return fmt.Errorf("dependency session %s owns the process environment; "+
		"stop it (Stop, Destroy or ReleaseEnvironment) before starting another one, "+
		"or use WithCommandScopedEnvironment and Dependencies.Environ to run both at once",
		owner.describe())
}

// foreignEnvironmentKeys are the keys another session has installed into
// os.Environ. Callers must not hold a session lock.
func foreignEnvironmentKeys(l *Dependencies) map[string]struct{} {
	globalEnvironment.mu.Lock()
	defer globalEnvironment.mu.Unlock()
	owner := globalEnvironment.owner
	if owner == nil || owner == l {
		return nil
	}
	return owner.ownedKeys()
}

// defunct reports whether the session can no longer own anything: its Codefly
// process is gone, or it is still starting and the caller who started it has
// given up. A caller that abandons a start — a test that times out and never
// reads the result — would otherwise hold the process environment against
// every later session for the life of the process.
//
// An attached or inherited session owns no process and, once live, is never
// defunct: its dependencies may well still be running, so its values stay
// authoritative.
func (l *Dependencies) defunct() bool {
	if l.proc != nil {
		select {
		case <-l.proc.Done():
			return true
		default:
			return false
		}
	}
	l.mu.Lock()
	starting := l.startDone
	l.mu.Unlock()
	if starting == nil {
		return false
	}
	select {
	case <-starting:
		return true
	default:
		return false
	}
}

// started marks the session live, so its ownership no longer depends on the
// context that created it. A live session whose context is cancelled loses its
// Codefly process to exec.CommandContext, which the proc check above sees.
func (l *Dependencies) started() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.startDone = nil
}

// describe names a session for an error message: its identity when it has been
// resolved, otherwise the directory it is anchored to. It never resolves — an
// error path must not do file I/O — and never reports a configuration value.
func (l *Dependencies) describe() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.identity != nil {
		return fmt.Sprintf("%s/%s (%s)", l.identity.module.Name, l.identity.service.Name, l.dir)
	}
	return l.dir
}

// apply installs env into the process environment transactionally: every value
// is already resolved, ownership is claimed before the first write, and a write
// that fails restores the keys touched in this round. Error messages name keys
// only, never values, because configuration values include secrets.
func (l *Dependencies) apply(env *sessionEnvironment) error {
	globalEnvironment.mu.Lock()
	defer globalEnvironment.mu.Unlock()
	if err := claimGlobalEnvironmentLocked(l); err != nil {
		return err
	}
	type change struct {
		key      string
		previous string
		existed  bool
	}
	round := make([]change, 0, len(env.keys))
	for _, key := range env.keys {
		previous, existed := os.LookupEnv(key)
		if err := os.Setenv(key, env.values[key]); err != nil {
			for i := len(round) - 1; i >= 0; i-- {
				restore(round[i].key, round[i].previous, round[i].existed)
			}
			if !l.hasOwned() {
				globalEnvironment.owner = nil
			}
			return fmt.Errorf("cannot inject %s into the process environment: %w", key, err)
		}
		round = append(round, change{key: key, previous: previous, existed: existed})
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, applied := range round {
		if existing, held := l.ownedVariableLocked(applied.key); held {
			existing.applied = env.values[applied.key]
			l.setOwnedVariableLocked(existing)
			continue
		}
		l.setOwnedVariableLocked(ownedVariable{
			key:      applied.key,
			previous: applied.previous,
			existed:  applied.existed,
			applied:  env.values[applied.key],
		})
	}
	return nil
}

// The owned records are guarded by l.mu. Helpers suffixed Locked require the
// caller to hold it; the others take it themselves, and are only ever called
// while holding globalEnvironment.mu, which is the documented order.
func (l *Dependencies) ownedVariableLocked(key string) (ownedVariable, bool) {
	for _, owned := range l.owned {
		if owned.key == key {
			return owned, true
		}
	}
	return ownedVariable{}, false
}

func (l *Dependencies) setOwnedVariableLocked(variable ownedVariable) {
	for i, owned := range l.owned {
		if owned.key == variable.key {
			l.owned[i] = variable
			return
		}
	}
	l.owned = append(l.owned, variable)
}

func (l *Dependencies) hasOwned() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.owned) > 0
}

func (l *Dependencies) ownedKeys() map[string]struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.owned) == 0 {
		return nil
	}
	keys := make(map[string]struct{}, len(l.owned))
	for _, owned := range l.owned {
		keys[owned.key] = struct{}{}
	}
	return keys
}

// restoreOwned returns the session's variables to their pre-session state and
// forgets them. The caller holds globalEnvironment.mu.
func (l *Dependencies) restoreOwned() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.owned) - 1; i >= 0; i-- {
		owned := l.owned[i]
		if current, exists := os.LookupEnv(owned.key); !exists || current != owned.applied {
			continue
		}
		restore(owned.key, owned.previous, owned.existed)
	}
	l.owned = nil
}

func restore(key, previous string, existed bool) {
	if existed {
		_ = os.Setenv(key, previous)
		return
	}
	_ = os.Unsetenv(key)
}

// ReleaseEnvironment gives the process environment back. Every variable the
// session still owns — the value in os.Environ is the one the session installed
// — returns to what it was before the first injection, and one the caller has
// since changed is left alone. Stop and Destroy call this; a second call is a
// no-op, and a released session may inject again with SetEnvironment.
func (l *Dependencies) ReleaseEnvironment() {
	globalEnvironment.mu.Lock()
	defer globalEnvironment.mu.Unlock()
	l.restoreOwned()
	if globalEnvironment.owner == l {
		globalEnvironment.owner = nil
	}
}

// Environ returns a copy of the process environment with this session's
// resolved variables applied on top, ready to hand to exec.Cmd.Env. It is the
// recommended way to give a child process its dependencies: it touches no
// process-global state, so independent sessions — parallel tests, for instance
// — can each drive their own children without contending for os.Environ.
func (l *Dependencies) Environ() []string {
	foreign := foreignEnvironmentKeys(l)
	env := l.resolved()
	if env == nil {
		if len(foreign) == 0 {
			return os.Environ()
		}
		env = newSessionEnvironment(nil)
	}
	return env.environ(os.Environ(), foreign)
}

// EnvironmentVariables returns a defensive copy of the variables this session
// resolved. It is empty for a session that inherited its dependencies from a
// parent Codefly runtime, whose values are already in the process environment.
func (l *Dependencies) EnvironmentVariables() map[string]string {
	env := l.resolved()
	if env == nil {
		return map[string]string{}
	}
	return env.variables()
}

// Connection returns a dependency connection string from this session, falling
// back to the process environment for a session that inherited its dependencies
// from a parent Codefly runtime.
func (l *Dependencies) Connection(service, name string) string {
	env := l.resolved()
	if env == nil {
		return Connection(service, name)
	}
	for _, pattern := range connectionKeys(service, name) {
		if value, ok := env.values[pattern]; ok && value != "" {
			return value
		}
	}
	return ""
}

func connectionKeys(service, name string) []string {
	return []string{
		fmt.Sprintf("CODEFLY__SERVICE_%s__%s__CONNECTION", normalize(service), normalize(name)),
		fmt.Sprintf("CODEFLY__%s__%s__CONNECTION", normalize(service), normalize(name)),
	}
}

// ProcessVariable returns a value the CLI supplied under its own name, such as
// a contract a runtime reads by exact name. It falls back to the process
// environment for a session that inherited its dependencies from a parent
// Codefly runtime: that session drives no CLI of its own, so the parent is what
// installed these, and reading only the session's own resolved values would
// answer "absent" for a variable that is in fact present.
func (l *Dependencies) ProcessVariable(name string) string {
	env := l.resolved()
	if env == nil {
		return os.Getenv(name)
	}
	return env.values[name]
}

// Service returns the service owning this session's directory.
func (l *Dependencies) Service(ctx context.Context) (*resources.Service, error) {
	identity, err := l.sessionIdentity(ctx)
	if err != nil {
		return nil, err
	}
	return identity.service, nil
}

// Module returns the module owning this session's directory.
func (l *Dependencies) Module(ctx context.Context) (*resources.Module, error) {
	identity, err := l.sessionIdentity(ctx)
	if err != nil {
		return nil, err
	}
	return identity.module, nil
}

func (l *Dependencies) sessionIdentity(ctx context.Context) (*resolvedIdentity, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.identity != nil {
		return l.identity, nil
	}
	identity, err := resolveSessionIdentity(ctx, l.dir)
	if err != nil {
		return nil, err
	}
	l.identity = identity
	return identity, nil
}

func (l *Dependencies) resolved() *sessionEnvironment {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.environment
}

func (l *Dependencies) setResolved(env *sessionEnvironment) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.environment = env
}

// resolveEnvironment projects everything the session contributes — identity,
// consumed endpoints, own configuration and dependency configurations — into an
// immutable set of values. It performs every remote call and every validation
// before a caller mutates anything, so a resolution failure leaves both the
// process environment and any previously resolved session values untouched.
func (l *Dependencies) resolveEnvironment(ctx context.Context) (*sessionEnvironment, error) {
	w := wool.Get(ctx).In("sdk.resolveEnvironment")
	identity, err := l.sessionIdentity(ctx)
	if err != nil {
		return nil, err
	}
	svc, mod := identity.service, identity.module

	variables := []*resources.EnvironmentVariable{
		resources.ServiceAsEnvironmentVariable(svc.Name),
		resources.ModuleAsEnvironmentVariable(mod.Name),
		resources.VersionAsEnvironmentVariable(svc.Version),
	}

	networkAccess := resources.NetworkAccessFromRuntimeContext(l.runtimeContext)
	if networkAccess == nil {
		return nil, w.NewError("no network access found")
	}
	mappings, err := l.cli.GetDependenciesNetworkMappings(ctx, &v0.GetNetworkMappingsRequest{
		Module: mod.Name, Service: svc.Name,
	})
	if err != nil {
		return nil, w.Wrapf(err, "failed to get dependencies network mappings")
	}
	dependencyMappings, err := resources.ResolveDependencyNetworkMappings(mod.Name, svc.ServiceDependencies, mappings.NetworkMappings)
	if err != nil {
		return nil, w.Wrapf(err, "failed to resolve dependencies network mappings")
	}
	for _, mapping := range dependencyMappings {
		instance := resources.FilterNetworkInstance(ctx, mapping.Instances, networkAccess)
		if instance == nil {
			return nil, w.NewError("no network instance found")
		}
		variables = append(variables, resources.EndpointAsEnvironmentVariable(&resources.EndpointAccess{
			Endpoint:        mapping.Endpoint,
			NetworkInstance: instance,
		}))
	}

	request := &v0.GetConfigurationRequest{Module: mod.Name, Service: svc.Name}
	configuration, err := l.cli.GetConfiguration(ctx, request)
	if err != nil {
		return nil, w.Wrapf(err, "failed to get configuration")
	}
	supplied, err := processVariables(configuration.GetProcessVariables())
	if err != nil {
		return nil, w.Wrap(err)
	}
	var environment string
	for _, variable := range supplied {
		if variable.Key == resources.EnvironmentPrefix {
			environment = variable.ValueAsString()
		}
	}
	if conf := configuration.Configuration; conf != nil {
		values, err := configurationVariables(conf, environment)
		if err != nil {
			return nil, err
		}
		variables = append(variables, values...)
	}

	dependencies, err := l.cli.GetDependenciesConfigurations(ctx, request)
	if err != nil {
		return nil, w.Wrapf(err, "failed to get dependencies configurations")
	}
	for _, conf := range resources.FilterConfigurations(dependencies.Configurations, l.runtimeContext) {
		values, err := configurationVariables(conf, environment)
		if err != nil {
			return nil, err
		}
		variables = append(variables, values...)
	}

	// Resolved last, so the name the orchestrator chose is the one the process
	// sees: everything above is projected under a key the SDK derives.
	return newSessionEnvironment(append(variables, supplied...)), nil
}

func configurationVariables(conf *basev0.Configuration, environment string) ([]*resources.EnvironmentVariable, error) {
	variables, err := resources.ConfigurationAsEnvironmentVariables(conf, environment, false)
	if err != nil {
		return nil, err
	}
	secrets, err := resources.ConfigurationAsEnvironmentVariables(conf, environment, true)
	if err != nil {
		return nil, err
	}
	return append(variables, secrets...), nil
}

// ProcessVariableNamespace is the only namespace the CLI may install a process
// variable into. The SDK hands these to the process verbatim, so without a
// namespace the orchestrator could replace any variable the caller runs on —
// PATH, HOME, LD_PRELOAD — in a session that injects globally, and could delete
// one from a concurrent session's children, because Environ drops every key
// another session owns. Both effects are invisible at the point of failure.
//
// Every contract this channel exists to carry is already in this namespace, so
// the restriction costs nothing today. It is also the direction that stays
// open: widening it later is backwards compatible, narrowing it would not be.
const ProcessVariableNamespace = "CODEFLY__"

// processVariables projects the values the CLI asked to install under their own
// names rather than under a key derived from a producer's coordinates. They
// carry contracts a runtime reads by exact name, which a prefixed projection
// cannot satisfy.
//
// Anything that cannot become a sound environment entry fails the whole
// resolution instead of being dropped: os.Setenv would reject it with an error
// naming no source, and a command-scoped session never calls os.Setenv at all,
// so it would carry a corrupt entry into its children. Errors name the key,
// never the value, because a supplied value may be a secret.
func processVariables(values []*basev0.ConfigurationValue) ([]*resources.EnvironmentVariable, error) {
	variables := make([]*resources.EnvironmentVariable, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := value.GetKey()
		if !strings.HasPrefix(key, ProcessVariableNamespace) || strings.Contains(key, "=") {
			return nil, fmt.Errorf("the CLI supplied the process variable %q, which is not a usable name in the %s namespace", key, ProcessVariableNamespace)
		}
		if _, duplicate := seen[key]; duplicate {
			// Exact-name delivery is the whole point of the channel, so two
			// values for one name is a fault in the sender rather than an
			// ordering the SDK should silently resolve.
			return nil, fmt.Errorf("the CLI supplied the process variable %s more than once", key)
		}
		seen[key] = struct{}{}
		if strings.ContainsRune(value.GetValue(), 0) {
			return nil, fmt.Errorf("the value of process variable %s contains a NUL byte, which cannot be an environment value", key)
		}
		variables = append(variables, resources.Env(key, value.GetValue()))
	}
	return variables, nil
}
