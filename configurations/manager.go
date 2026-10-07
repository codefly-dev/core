package configurations

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

type Loader interface {
	Identity() string
	Load(ctx context.Context, env *resources.Environment) error
	// Configurations returns the configurations produced by Load. It must
	// return the same instances on every call (not fresh copies): after Load,
	// the Manager resolves secret references in these objects in place, and
	// LoadConfigurations reads them back expecting the resolved values.
	Configurations() []*basev0.Configuration
	DNS() []*basev0.DNS
}

// compositionRootConfigurationsLoader is an optional capability a Loader may
// implement to tell the Manager which of the workspace configurations it
// produced originate from the composition root itself, rather than from a
// composed module. The Manager injects the former into every service in the run
// (see GetCompositionRootWorkspaceConfigurations).
type compositionRootConfigurationsLoader interface {
	CompositionRootWorkspaceConfigurationNames() []string
}

// ambiguousConfigurationsLoader is an optional capability a Loader may implement
// to report workspace configuration names it could not resolve to a single
// definition. Such a name is absent from the loaded set, so a run that never
// consumes it proceeds; a service that declares it as a dependency fails with
// the loader's diagnostic, which names the providers and the remedy, rather than
// with a bare "not found" that hides why the name is missing.
type ambiguousConfigurationsLoader interface {
	AmbiguousWorkspaceConfigurations() map[string]error
}

type Manager struct {
	workspace *resources.Workspace
	services  map[string]*resources.Service

	loaders []Loader

	// Secret resolvers registered explicitly (tests, custom backends). The
	// environment's own `secrets.provider` adds to these at Load() time.
	secretResolvers []SecretResolver

	// Per Name in
	worspaceConfigurations map[string]*basev0.Configuration

	// Workspace configuration names composed modules defined incompatibly,
	// mapped to the diagnostic raised when a dependency selects one.
	ambiguousWorkspaceConfigurations map[string]error

	// Names of the workspace configurations the composition root itself provides,
	// injected into every service in the run (as opposed to those a composed
	// module carries only for the services that declare them).
	compositionRootWorkspaceConfigurations map[string]bool

	// Per service
	serviceConfigurations map[string]*basev0.Configuration

	exposedFromServiceConfigurations map[string][]*basev0.Configuration

	dns []*basev0.DNS

	reduced  []string
	doReduce bool

	// resolution and env are captured at Load() so workspace-origin secrets,
	// deferred until a caller selects them, resolve through the same per-load
	// URI cache the service-origin pass already used.
	resolution        *secretResolution
	env               *resources.Environment
	resolvedWorkspace map[string]bool

	// Run network mappings used to resolve ${endpoint:…} references in workspace
	// configuration values, alongside the same secret resolution.
	networkMappings []*basev0.NetworkMapping
	networkAccess   *basev0.NetworkAccess

	// selection identifies the consumer these reads resolve for, so a
	// ${endpoint:…} reference is answered by the endpoint it names, judged
	// against the producer's export boundary. A zero value refuses every
	// INTERPOLATING read of a configuration that carries a reference — see
	// ForConsumerModule. The raw getters (GetServiceConfiguration and its
	// siblings) return what was stored, references unresolved, and are not
	// reads that resolve.
	selection resources.EndpointSelectionContext

	// runProducers reports whether a <module>/<service> is part of this run, so
	// a reference the consumer cannot resolve is told apart from one no run
	// could. See WithRunProducers.
	runProducers func(unique string) bool
}

func NewManager(_ context.Context, workspace *resources.Workspace) (*Manager, error) {
	return &Manager{
		workspace:                              workspace,
		services:                               make(map[string]*resources.Service),
		worspaceConfigurations:                 make(map[string]*basev0.Configuration),
		ambiguousWorkspaceConfigurations:       make(map[string]error),
		compositionRootWorkspaceConfigurations: make(map[string]bool),
		serviceConfigurations:                  make(map[string]*basev0.Configuration),
		exposedFromServiceConfigurations:       make(map[string][]*basev0.Configuration),
		resolvedWorkspace:                      make(map[string]bool),
	}, nil
}

func (manager *Manager) WithLoader(loader Loader) *Manager {
	manager.loaders = append(manager.loaders, loader)
	return manager
}

// WithSecretResolver registers a secret resolver. Resolvers selected by the
// environment's `secrets` block are added automatically at Load() time; this
// is for tests and custom backends.
func (manager *Manager) WithSecretResolver(resolvers ...SecretResolver) *Manager {
	manager.secretResolvers = append(manager.secretResolvers, resolvers...)
	return manager
}

// WithNetworkMappings supplies the run's network mappings and the network access
// against which ${endpoint:…} references in workspace configuration values are
// resolved. The composition root sets these once ports are allocated, before any
// workspace configuration is selected.
func (manager *Manager) WithNetworkMappings(mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess) *Manager {
	manager.networkMappings = mappings
	manager.networkAccess = access
	return manager
}

// WithRunProducers supplies which producers this run contains, by
// <module>/<service>. It is what makes the strict read fail-fast: a reference to
// a producer of the run that a consumer cannot resolve is a fault to report,
// while one to a producer the run does not contain — excluded infrastructure, or
// a run of one service rather than the workspace — is dropped for that consumer.
// Without it no producer is provably part of the run, so nothing may be dropped
// and an unresolvable reference is a refusal naming the configuration and key —
// silently omitting a declared address because the caller never said what it
// was rendering is the failure the option exists to make impossible. The
// composition root sets it alongside the mappings.
func (manager *Manager) WithRunProducers(inRun func(unique string) bool) *Manager {
	if manager == nil {
		return nil
	}
	manager.runProducers = inRun
	return manager
}

// ForConsumer returns a view of the manager that resolves ${endpoint:…}
// references against one consumer's network mappings and access. Everything
// else is shared with the manager. Services initialize concurrently, so a
// composition root reads each consumer's configurations through its own view
// rather than setting the mappings on the shared manager before each read.
func (manager *Manager) ForConsumer(mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess) *Manager {
	if manager == nil {
		return nil
	}
	view := *manager
	view.networkMappings = mappings
	view.networkAccess = access
	return &view
}

// ForConsumerModule says WHO this view resolves for: the module receiving the
// addresses, and how to read a producer's declared endpoints by
// <module>/<service>.
//
// Both are what resources.SelectEndpointForReference needs to answer a
// reference the way CheckEndpointReferences answers it, and both are REQUIRED
// once a configuration carries an endpoint reference: a view that has not named
// its consumer refuses to interpolate rather than resolve as though whoever is
// reading may reach whatever it named. The module is the export boundary — a
// reference is an edge into it like any declared dependency, so an endpoint it
// may not reach is refused rather than resolved to a permitted sibling. The
// manifest is what says which endpoint a token names: published mappings do
// not establish the complete declared set, and a producer may publish several
// mappings for one endpoint, so the mappings alone cannot decide it.
func (manager *Manager) ForConsumerModule(consumerModule string, declared resources.DeclaredEndpoints) *Manager {
	if manager == nil {
		return nil
	}
	view := *manager
	view.selection = resources.EndpointSelectionContext{ConsumerModule: consumerModule, Declared: declared}
	return &view
}

func (manager *Manager) Load(ctx context.Context, env *resources.Environment) error {
	if manager == nil {
		return nil
	}
	w := wool.Get(ctx).In("providers.Load")

	for _, loader := range manager.loaders {
		err := loader.Load(ctx, env)
		if err != nil {
			return w.Wrapf(err, "cannot load loader %s", loader.Identity())
		}
	}
	if err := manager.resolveSecrets(ctx, env); err != nil {
		return w.Wrapf(err, "cannot resolve secrets")
	}
	err := manager.LoadConfigurations(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot load configurations")
	}

	err = manager.LoadDNS(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot load DNS")
	}

	w.Debug("loaded", wool.Field("dns", resources.MakeManyDNSSummary(manager.dns)))
	return nil
}

// resolveSecrets resolves reference-valued secrets (op://…) produced by the
// loaders, in place, before they are consolidated. Plaintext secret values
// pass through untouched. Workspace-origin configurations are deferred: Core
// does not yet know which dependencies a caller selects, so their references
// are resolved lazily by resolveWorkspaceConfiguration once names arrive.
func (manager *Manager) resolveSecrets(ctx context.Context, env *resources.Environment) error {
	w := wool.Get(ctx).In("configurations.Manager.resolveSecrets")
	fromEnv, err := ResolversFromEnvironment(env)
	if err != nil {
		return w.Wrapf(err, "cannot build secret resolvers for environment %s", env.Name)
	}
	resolvers := append(append([]SecretResolver{}, manager.secretResolvers...), fromEnv...)
	manager.resolution = newSecretResolution(resolvers)
	manager.env = env
	for _, loader := range manager.loaders {
		for _, conf := range loader.Configurations() {
			if conf.Origin == resources.ConfigurationWorkspace {
				continue
			}
			if manager.skip(conf.Origin) {
				continue
			}
			if err := manager.resolution.resolveConfiguration(ctx, conf, env); err != nil {
				return w.Wrapf(err, "cannot resolve secrets from loader %s", loader.Identity())
			}
		}
	}
	return nil
}

// resolveWorkspaceConfiguration resolves a selected workspace configuration in
// place, at most once per load. The per-load URI cache is shared with every
// other selected configuration and with the service-origin pass, so a reference
// used by several is fetched from its provider only once.
func (manager *Manager) resolveWorkspaceConfiguration(ctx context.Context, name string, conf *basev0.Configuration) error {
	if manager.resolvedWorkspace[name] {
		return nil
	}
	if manager.resolution != nil {
		if err := manager.resolution.resolveConfiguration(ctx, conf, manager.env); err != nil {
			return err
		}
	}
	manager.resolvedWorkspace[name] = true
	return nil
}

// LoadConfigurations fetch different loaders and consolidate
func (manager *Manager) LoadConfigurations(_ context.Context) error {
	for _, loader := range manager.loaders {
		if provider, ok := loader.(compositionRootConfigurationsLoader); ok {
			for _, name := range provider.CompositionRootWorkspaceConfigurationNames() {
				manager.compositionRootWorkspaceConfigurations[name] = true
			}
		}
		if provider, ok := loader.(ambiguousConfigurationsLoader); ok {
			for name, diagnostic := range provider.AmbiguousWorkspaceConfigurations() {
				manager.ambiguousWorkspaceConfigurations[name] = diagnostic
			}
		}
		confs := loader.Configurations()
		for _, conf := range confs {
			if conf.Origin == resources.ConfigurationWorkspace {
				for _, info := range conf.Infos {
					if _, ok := manager.worspaceConfigurations[info.Name]; !ok {
						manager.worspaceConfigurations[info.Name] = &basev0.Configuration{
							Origin: resources.ConfigurationWorkspace,
						}
					}
					manager.worspaceConfigurations[info.Name].Infos = append(manager.worspaceConfigurations[info.Name].Infos, info)
				}
				continue
			}
			if manager.skip(conf.Origin) {
				continue
			}
			for _, info := range conf.Infos {
				if _, ok := manager.serviceConfigurations[conf.Origin]; !ok {
					manager.serviceConfigurations[conf.Origin] = &basev0.Configuration{
						Origin: conf.Origin,
					}
				}
				manager.serviceConfigurations[conf.Origin].Infos = append(manager.serviceConfigurations[conf.Origin].Infos, info)
			}
		}
	}
	return nil
}

func (manager *Manager) GetWorkspaceConfigurations(ctx context.Context) ([]*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	w := wool.Get(ctx).In("Manager.GetWorkspaceConfigurations")
	names := make([]string, 0, len(manager.worspaceConfigurations))
	for name := range manager.worspaceConfigurations {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]*basev0.Configuration, 0, len(names))
	for _, name := range names {
		conf := manager.worspaceConfigurations[name]
		if err := manager.resolveWorkspaceConfiguration(ctx, name, conf); err != nil {
			return nil, w.Wrapf(err, "cannot resolve workspace configuration %s", name)
		}
		resolved, err := manager.interpolateEndpoints(ctx, name, conf)
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

// ErrNotACompositionRootConfiguration refuses a composition-root read that named
// a group the composition root does not provide run-wide: a name no loader
// produced, a composed module's group (delivered only to the services that
// declare it, through GetWorkspaceDependenciesConfigurations), or a name two
// composed modules defined incompatibly. A named read never answers with less
// than it was asked for, so such a name is refused rather than skipped.
var ErrNotACompositionRootConfiguration = errors.New("not a workspace configuration the composition root provides run-wide")

// GetCompositionRootWorkspaceConfigurations returns the workspace configurations
// the composition root itself provides to the whole run — its own
// configurations/<profile>/* and any invocation-scoped override — as opposed to
// the configurations a composed module carries only for the services that
// declare them as dependencies. The composition root injects these into every
// service in the run, so a composed-module service reading
// WorkspaceValue(name, key) resolves a root-provided value it never had to
// redeclare.
//
// This is the WHOLE set: every root-provided group is resolved, and the first
// refusal is the read's. A consumer that receives only some of the root's
// groups reads them by name with GetNamedCompositionRootWorkspaceConfigurations,
// which is a different method on purpose: a named read never means "all", so
// an empty received set spread into a call cannot deliver every group, a
// withheld credential included.
//
// This set and the per-dependency composed-module set are disjoint by name: a
// name the composition root also declares is resolved to the root at load
// (composeModuleWorkspaceConfigurations suppresses the module's), so the root
// fills what a composed module leaves unset and never shadows a name only the
// module provides.
//
// Values are secret-resolved like GetWorkspaceConfigurations, but endpoints are
// interpolated leniently: because these configurations are injected run-wide, a
// value whose ${endpoint:…} does not resolve for the consumer — an endpoint it
// does not depend on, absent from its mapping set — is omitted for that consumer
// rather than failing it (#393). The decision is per endpoint reference: a value
// referencing one endpoint of a service the consumer depends on for a different
// endpoint is dropped too. Only those two facts about this consumer's view are
// omissions: a mistyped reference, an ambiguous one, a producer the workspace
// does not declare or an invalid declaration is refused here, naming the
// configuration and key, whichever consumer meets it. Unresolvable secrets
// still fail the run (they are universal, not consumer-specific).
func (manager *Manager) GetCompositionRootWorkspaceConfigurations(ctx context.Context) ([]*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	return manager.readCompositionRootConfigurations(ctx, manager.loadedCompositionRootConfigurations())
}

// ErrNoCompositionRootConfigurationNamed refuses a named composition-root read
// that named nothing. A named read resolves exactly the groups a consumer
// receives, and "none" is an answer the consumer gives by not reading; it is
// never a request for every group. The whole set is
// GetCompositionRootWorkspaceConfigurations.
var ErrNoCompositionRootConfigurationNamed = errors.New("a named composition-root read names at least one workspace configuration")

// GetNamedCompositionRootWorkspaceConfigurations resolves exactly the named
// root-provided groups: a consumer that receives a subset of the root's groups
// — a service a withheld credential never reaches — is judged on what it
// receives, so a reference that is a fault in a group it does not receive
// (ambiguous, or without an instance for its access) cannot refuse it. The
// consumer names what it receives; the configurations stay core's.
//
// Each name must be a root-provided group that was loaded, or the whole read
// is refused with ErrNotACompositionRootConfiguration naming every such name —
// never answered with the groups that were known. No name at all is refused
// with ErrNoCompositionRootConfigurationNamed: a named read never means the
// whole set, which GetCompositionRootWorkspaceConfigurations is. The result is
// sorted by name with each group once, so the order a caller lists names in
// does not change what it reads. A nil manager refuses too, rather than
// answering a named read with nothing.
func (manager *Manager) GetNamedCompositionRootWorkspaceConfigurations(ctx context.Context, names ...string) ([]*basev0.Configuration, error) {
	w := wool.Get(ctx).In("Manager.GetNamedCompositionRootWorkspaceConfigurations")
	if manager == nil {
		return nil, w.NewError("configurations.Manager: receiver is nil — a named read of %s was attempted before Manager initialization", strings.Join(names, ", "))
	}
	selected, err := manager.selectCompositionRootConfigurations(names)
	if err != nil {
		return nil, w.Wrapf(err, "cannot read the composition root's workspace configurations")
	}
	return manager.readCompositionRootConfigurations(ctx, selected)
}

// readCompositionRootConfigurations resolves the selected root-provided groups,
// in order, refusing on the first fault.
func (manager *Manager) readCompositionRootConfigurations(ctx context.Context, selected []string) ([]*basev0.Configuration, error) {
	w := wool.Get(ctx).In("Manager.GetCompositionRootWorkspaceConfigurations")
	out := make([]*basev0.Configuration, 0, len(selected))
	for _, name := range selected {
		conf := manager.worspaceConfigurations[name]
		if err := manager.resolveWorkspaceConfiguration(ctx, name, conf); err != nil {
			return nil, w.Wrapf(err, "cannot resolve workspace configuration %s", name)
		}
		// Run-wide injection: a value whose ${endpoint:…} the consumer does not
		// depend on is omitted for it rather than failing the service (#393).
		resolved, err := manager.interpolateEndpointsRunWide(ctx, name, conf)
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

// selectCompositionRootConfigurations is the set a named composition-root read
// resolves: exactly the named groups, each of which must be a root-provided
// group that was loaded. Sorted, each name once. No name is refused: a named
// read is never the whole set.
func (manager *Manager) selectCompositionRootConfigurations(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, ErrNoCompositionRootConfigurationNamed
	}
	selected := slices.Clone(names)
	slices.Sort(selected)
	selected = slices.Compact(selected)
	var refused []error
	for _, name := range selected {
		if err := manager.refuseUnlessCompositionRoot(name); err != nil {
			refused = append(refused, err)
		}
	}
	if len(refused) > 0 {
		return nil, errors.Join(refused...)
	}
	return selected, nil
}

// loadedCompositionRootConfigurations names the root-provided groups a loader
// produced a configuration for, sorted.
func (manager *Manager) loadedCompositionRootConfigurations() []string {
	names := make([]string, 0, len(manager.compositionRootWorkspaceConfigurations))
	for name := range manager.compositionRootWorkspaceConfigurations {
		if _, ok := manager.worspaceConfigurations[name]; ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// refuseUnlessCompositionRoot answers a named composition-root read for one
// name: nil when the composition root provides that group run-wide and it was
// loaded, otherwise ErrNotACompositionRootConfiguration saying what the name is
// instead — a composed module's group, an ambiguous name with the loader's
// diagnostic, or nothing this run loaded, beside the groups the root does
// provide.
func (manager *Manager) refuseUnlessCompositionRoot(name string) error {
	_, loaded := manager.worspaceConfigurations[name]
	if manager.compositionRootWorkspaceConfigurations[name] && loaded {
		return nil
	}
	if loaded {
		return fmt.Errorf("%w: %q is a composed module's group, delivered to the services that declare it (GetWorkspaceDependenciesConfigurations), not to every service of the run", ErrNotACompositionRootConfiguration, name)
	}
	if diagnostic, ambiguous := manager.ambiguousWorkspaceConfigurations[name]; ambiguous {
		return fmt.Errorf("%w: %q: %w", ErrNotACompositionRootConfiguration, name, diagnostic)
	}
	provided := manager.loadedCompositionRootConfigurations()
	if len(provided) == 0 {
		return fmt.Errorf("%w: %q; the composition root provides no workspace configuration run-wide", ErrNotACompositionRootConfiguration, name)
	}
	return fmt.Errorf("%w: %q; the composition root provides %s", ErrNotACompositionRootConfiguration, name, strings.Join(provided, ", "))
}

// interpolateEndpoints resolves ${endpoint:…} references against the run's network
// mappings for the configured access. The address is consumer-specific, so it is
// resolved on the way out (never cached into the shared configuration): the
// composition root sets the consumer's access via WithNetworkMappings before each
// read. This is the strict path: a reference to a producer of this run that does
// not resolve fails, because the caller selected this configuration and every
// reference to something the run contains is expected to hold. A reference to a
// producer the run does not contain is dropped for this consumer with a WARN —
// see WithRunProducers, which is what tells the two apart.
func (manager *Manager) interpolateEndpoints(ctx context.Context, name string, conf *basev0.Configuration) (*basev0.Configuration, error) {
	w := wool.Get(ctx).In("Manager.interpolateEndpoints")
	resolved, err := resources.InterpolateConfigurationEndpoints(ctx, conf, manager.networkMappings, manager.networkAccess,
		resources.WithRunProducers(manager.runProducers),
		resources.WithConsumer(manager.selection.ConsumerModule, manager.selection.Declared))
	if err != nil {
		return nil, w.Wrapf(err, "cannot interpolate workspace configuration %s", name)
	}
	return resolved, nil
}

// interpolateEndpointsRunWide is interpolateEndpoints for the composition root's
// run-wide injection set: a value referencing an endpoint the consumer does not
// depend on is dropped for that consumer rather than failing it. Only this path
// is lenient; GetWorkspaceConfigurations and GetWorkspaceDependenciesConfigurations
// stay fail-fast so a mistyped module/service/endpoint still surfaces loudly.
func (manager *Manager) interpolateEndpointsRunWide(ctx context.Context, name string, conf *basev0.Configuration) (*basev0.Configuration, error) {
	w := wool.Get(ctx).In("Manager.interpolateEndpointsRunWide")
	// The run set travels with the run-wide read as it does with the strict one:
	// it is what tells a consumer that legitimately cannot see an endpoint from a
	// render that never bound its network context at all.
	resolved, err := resources.InterpolateRunWideConfigurationEndpoints(ctx, conf, manager.networkMappings, manager.networkAccess,
		resources.WithRunProducers(manager.runProducers),
		resources.WithConsumer(manager.selection.ConsumerModule, manager.selection.Declared))
	if err != nil {
		return nil, w.Wrapf(err, "cannot interpolate workspace configuration %s", name)
	}
	return resolved, nil
}

// WorkspaceEndpointReferences returns the ${endpoint:…} references the named
// workspace configurations carry, deduplicated and in order. A workspace
// configuration is authored by the composition root, which may name an
// endpoint the consuming module cannot know (the host, by the composition's
// name for it); the root reads these to hand each consumer's view the
// producers' mappings the references resolve against. Unknown names are
// skipped: GetWorkspaceDependenciesConfigurations reports them.
func (manager *Manager) WorkspaceEndpointReferences(deps ...string) []string {
	if manager == nil {
		return nil
	}
	seen := make(map[string]bool)
	var references []string
	for _, dep := range deps {
		conf, ok := manager.worspaceConfigurations[dep]
		if !ok || conf == nil {
			continue
		}
		for _, info := range conf.Infos {
			for _, value := range info.GetConfigurationValues() {
				for _, reference := range resources.ConfigurationValueEndpointReferences(value) {
					if !seen[reference] {
						seen[reference] = true
						references = append(references, reference)
					}
				}
			}
		}
	}
	return references
}

func (manager *Manager) GetWorkspaceDependenciesConfigurations(ctx context.Context, deps ...string) ([]*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	w := wool.Get(ctx).In("Manager.GetWorkspaceDependenciesConfigurations")
	out := make([]*basev0.Configuration, 0, len(deps))
	for _, dep := range deps {
		conf, ok := manager.worspaceConfigurations[dep]
		if !ok {
			if diagnostic, ambiguous := manager.ambiguousWorkspaceConfigurations[dep]; ambiguous {
				return nil, w.Wrapf(diagnostic, "cannot select workspace configuration %s", dep)
			}
			return nil, w.NewError("no configuration found for %s", dep)
		}
		if err := manager.resolveWorkspaceConfiguration(ctx, dep, conf); err != nil {
			return nil, w.Wrapf(err, "cannot resolve workspace configuration %s", dep)
		}
		resolved, err := manager.interpolateEndpoints(ctx, dep, conf)
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

func (manager *Manager) GetServiceConfigurations(_ context.Context) ([]*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	origins := make([]string, 0, len(manager.serviceConfigurations))
	for origin := range manager.serviceConfigurations {
		origins = append(origins, origin)
	}
	slices.Sort(origins)
	out := make([]*basev0.Configuration, 0, len(origins))
	for _, origin := range origins {
		out = append(out, manager.serviceConfigurations[origin])
	}
	return out, nil
}

func (manager *Manager) GetServiceConfiguration(_ context.Context, service *resources.ServiceIdentity) (*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	if conf, ok := manager.serviceConfigurations[service.Unique()]; ok {
		return conf, nil
	}
	return nil, nil
}

func (manager *Manager) ExposeConfiguration(ctx context.Context, service *resources.ServiceIdentity, confs ...*basev0.Configuration) error {
	if manager == nil {
		return nil
	}
	w := wool.Get(ctx).In("Manager.ExposeConfiguration", wool.ThisField(service))
	w.Debug("exposing", wool.Field("configurations", resources.MakeManyConfigurationSummary(confs)))
	manager.exposedFromServiceConfigurations[service.Unique()] = confs
	return nil
}

func (manager *Manager) GetSharedServiceConfiguration(_ context.Context, unique string) ([]*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	return manager.exposedFromServiceConfigurations[unique], nil
}

func (manager *Manager) Restrict(_ context.Context, values []*resources.ServiceIdentity) error {
	if manager == nil {
		return nil
	}
	manager.doReduce = true
	for _, svc := range values {
		manager.reduced = append(manager.reduced, svc.Unique())
	}
	return nil
}

func (manager *Manager) skip(origin string) bool {
	return manager.doReduce && !slices.Contains(manager.reduced, origin)
}

func (manager *Manager) LoadDNS(_ context.Context) error {
	for _, loader := range manager.loaders {
		manager.dns = append(manager.dns, loader.DNS()...)
	}
	return nil
}

func (manager *Manager) DNS() []*basev0.DNS {
	if manager == nil {
		return nil
	}
	return manager.dns
}

func (manager *Manager) GetDNS(ctx context.Context, svc *resources.ServiceIdentity, endpointName string) (*basev0.DNS, error) {
	// Returning (nil, error) on a nil receiver lets callers distinguish
	// "uninitialized manager" from "manager has no matching DNS entry".
	// The previous (nil, nil) return swallowed the misconfiguration —
	// network/remote_manager.go would then nil-deref on the result.
	if manager == nil {
		return nil, fmt.Errorf("configurations.Manager: receiver is nil — DNS lookup attempted before Manager initialization")
	}
	w := wool.Get(ctx).In("providers.GetDNS", wool.ThisField(svc))
	for _, dns := range manager.dns {
		if svc.Module == dns.Module &&
			dns.Service == svc.Name &&
			dns.Endpoint == endpointName {
			return dns, nil
		}
	}
	return nil, w.NewError("no DNS found: %s::%s. Available: %s", svc.Unique(), endpointName, resources.MakeManyDNSSummary(manager.dns))
}
