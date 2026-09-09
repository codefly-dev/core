package sdk

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

func resolveSessionIdentity(ctx context.Context, dir string) (*resolvedIdentity, error) {
	mod, svc, err := resources.LoadModuleAndServiceUpFrom(ctx, dir)
	if err != nil {
		return nil, err
	}
	if svc == nil {
		return nil, fmt.Errorf("no service.codefly.yaml found from %s", dir)
	}
	if mod == nil {
		mod, err = flatLayoutModule(ctx, dir)
		if err != nil {
			return nil, err
		}
		if mod != nil {
			svc.WithModule(mod.Name)
		}
	}
	if mod == nil {
		return nil, fmt.Errorf("no module.codefly.yaml found from %s", dir)
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

// environ overlays the session values onto base, replacing any entry base
// already carries for the same key.
func (e *sessionEnvironment) environ(base []string) []string {
	out := make([]string, 0, len(base)+len(e.keys))
	for _, entry := range base {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, owned := e.values[key]; owned {
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
var globalEnvironment struct {
	mu    sync.Mutex
	owner *Dependencies
}

// apply installs env into the process environment transactionally: every value
// is already resolved, ownership is claimed before the first write, and a write
// that fails restores the keys touched in this round. Error messages name keys
// only, never values, because configuration values include secrets.
func (l *Dependencies) apply(env *sessionEnvironment) error {
	globalEnvironment.mu.Lock()
	defer globalEnvironment.mu.Unlock()
	if globalEnvironment.owner != nil && globalEnvironment.owner != l {
		return fmt.Errorf("another dependency session owns the process environment; " +
			"use WithCommandScopedEnvironment and Dependencies.Environ to run sessions side by side")
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
			return fmt.Errorf("cannot inject %s into the process environment: %w", key, err)
		}
		round = append(round, change{key: key, previous: previous, existed: existed})
	}
	for _, applied := range round {
		if existing, held := l.ownedVariable(applied.key); held {
			existing.applied = env.values[applied.key]
			l.setOwnedVariable(existing)
			continue
		}
		l.setOwnedVariable(ownedVariable{
			key:      applied.key,
			previous: applied.previous,
			existed:  applied.existed,
			applied:  env.values[applied.key],
		})
	}
	globalEnvironment.owner = l
	return nil
}

// ownedVariable and setOwnedVariable are only called while globalEnvironment.mu
// is held, which is what protects the ownership records.
func (l *Dependencies) ownedVariable(key string) (ownedVariable, bool) {
	for _, owned := range l.owned {
		if owned.key == key {
			return owned, true
		}
	}
	return ownedVariable{}, false
}

func (l *Dependencies) setOwnedVariable(variable ownedVariable) {
	for i, owned := range l.owned {
		if owned.key == variable.key {
			l.owned[i] = variable
			return
		}
	}
	l.owned = append(l.owned, variable)
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
	for i := len(l.owned) - 1; i >= 0; i-- {
		owned := l.owned[i]
		if current, exists := os.LookupEnv(owned.key); !exists || current != owned.applied {
			continue
		}
		restore(owned.key, owned.previous, owned.existed)
	}
	l.owned = nil
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
	env := l.resolved()
	if env == nil {
		return os.Environ()
	}
	return env.environ(os.Environ())
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
	dependencyMappings, err := resources.ResolveDependencyNetworkMappings(svc.ServiceDependencies, mappings.NetworkMappings)
	if err != nil {
		return nil, w.Wrapf(err, "failed to resolve dependencies network mappings")
	}
	// Enforce visibility only over the endpoints this service actually
	// consumes. The dependency graph may surface sibling endpoints of a
	// producer (e.g. an internal admin endpoint next to the public one),
	// and rejecting a run because of an endpoint the consumer never
	// references would be a false positive — the static workspace pass
	// (Workspace.ValidateServiceDependencies) scopes the same way.
	if err := validateConsumedMappingVisibility(mod.Name, svc.ServiceDependencies, dependencyMappings); err != nil {
		return nil, w.Wrap(err)
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
	if conf := configuration.Configuration; conf != nil {
		variables = append(variables, configurationVariables(conf)...)
	}

	dependencies, err := l.cli.GetDependenciesConfigurations(ctx, request)
	if err != nil {
		return nil, w.Wrapf(err, "failed to get dependencies configurations")
	}
	for _, conf := range resources.FilterConfigurations(dependencies.Configurations, l.runtimeContext) {
		variables = append(variables, configurationVariables(conf)...)
	}
	return newSessionEnvironment(variables), nil
}

func configurationVariables(conf *basev0.Configuration) []*resources.EnvironmentVariable {
	variables := resources.ConfigurationAsEnvironmentVariables(conf, false)
	return append(variables, resources.ConfigurationAsEnvironmentVariables(conf, true)...)
}
