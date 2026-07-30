package resources

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	"buf.build/go/protovalidate"
	"github.com/Masterminds/semver"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/version"
	"github.com/codefly-dev/core/wool"
)

const AgentConfigurationName = "agent.codefly.yaml"

type AgentKind string

const (
	ServiceAgent     AgentKind = "codefly:service"
	JobAgent         AgentKind = "codefly:job"
	ApplicationAgent AgentKind = "codefly:application"
	ModuleAgent      AgentKind = "codefly:module"
	ToolboxAgent     AgentKind = "codefly:toolbox"
	ProviderAgent    AgentKind = "codefly:provider"
)

type AgentStore string

const (
	AgentStoreLocal  AgentStore = "local"
	AgentStoreOCI    AgentStore = "oci"
	AgentStoreNix    AgentStore = "nix"
	AgentStoreGitHub AgentStore = "github"
)

type AgentResolutionBehavior string

const (
	AgentResolutionDisabled         AgentResolutionBehavior = ""
	AgentResolutionLocalExecutable  AgentResolutionBehavior = "local-executable"
	AgentResolutionBinaryDigest     AgentResolutionBehavior = "binary-digest"
	AgentResolutionVerifiedArtifact AgentResolutionBehavior = "verified-artifact"
	AgentResolutionGitHubRelease    AgentResolutionBehavior = "github-release"
)

type AgentOperationSupport struct {
	Build   bool
	List    bool
	Install bool
	Version bool
	Publish bool
	CI      bool
	Load    bool
}

type AgentResolutionSupport struct {
	Local  AgentResolutionBehavior
	OCI    AgentResolutionBehavior
	Nix    AgentResolutionBehavior
	GitHub AgentResolutionBehavior
}

func (s AgentResolutionSupport) For(store AgentStore) (AgentResolutionBehavior, error) {
	switch store {
	case AgentStoreLocal:
		return s.Local, nil
	case AgentStoreOCI:
		return s.OCI, nil
	case AgentStoreNix:
		return s.Nix, nil
	case AgentStoreGitHub:
		return s.GitHub, nil
	default:
		return AgentResolutionDisabled, fmt.Errorf("unknown agent store: %q", store)
	}
}

type AgentKindRegistration struct {
	ProtoKind              basev0.Agent_Kind
	Resource               AgentKind
	InstallSubdirectory    string
	ExecutablePrefix       string
	GitHubRepositoryPrefix string
	GitHubAssetPrefix      string
	Resolution             AgentResolutionSupport
	Operations             AgentOperationSupport
	AutoDownload           bool
	aliases                []AgentKind
}

func (r AgentKindRegistration) ExecutableName(name string) string {
	return r.ExecutablePrefix + "-" + name
}

func (r AgentKindRegistration) GitHubRepository(name string) string {
	return r.GitHubRepositoryPrefix + "-" + name
}

func (r AgentKindRegistration) GitHubAsset(name, version, goos, goarch string) string {
	return fmt.Sprintf("%s-%s_%s_%s_%s.tar.gz", r.GitHubAssetPrefix, name, version, goos, goarch)
}

var agentKindRegistry = []AgentKindRegistration{
	{
		ProtoKind:              basev0.Agent_SERVICE,
		Resource:               ServiceAgent,
		InstallSubdirectory:    "services",
		ExecutablePrefix:       "service",
		GitHubRepositoryPrefix: "service",
		GitHubAssetPrefix:      "service",
		Resolution: AgentResolutionSupport{
			Local:  AgentResolutionLocalExecutable,
			OCI:    AgentResolutionBinaryDigest,
			Nix:    AgentResolutionBinaryDigest,
			GitHub: AgentResolutionGitHubRelease,
		},
		Operations:   AgentOperationSupport{Build: true, List: true, Install: true, Version: true, Publish: true, CI: true, Load: true},
		AutoDownload: true,
		aliases:      []AgentKind{BuilderServiceAgent, RuntimeServiceAgent, CodeServiceAgent},
	},
	{
		ProtoKind:              basev0.Agent_JOB,
		Resource:               JobAgent,
		InstallSubdirectory:    "jobs",
		ExecutablePrefix:       "job",
		GitHubRepositoryPrefix: "job",
		GitHubAssetPrefix:      "job",
	},
	{
		ProtoKind:              basev0.Agent_APPLICATION,
		Resource:               ApplicationAgent,
		InstallSubdirectory:    "applications",
		ExecutablePrefix:       "application",
		GitHubRepositoryPrefix: "application",
		GitHubAssetPrefix:      "application",
		Resolution: AgentResolutionSupport{
			Local:  AgentResolutionLocalExecutable,
			OCI:    AgentResolutionBinaryDigest,
			Nix:    AgentResolutionBinaryDigest,
			GitHub: AgentResolutionGitHubRelease,
		},
		Operations:   AgentOperationSupport{Build: true, List: true, Install: true, Version: true, Publish: true, CI: true, Load: true},
		AutoDownload: true,
	},
	{
		ProtoKind:              basev0.Agent_MODULE,
		Resource:               ModuleAgent,
		InstallSubdirectory:    "modules",
		ExecutablePrefix:       "module",
		GitHubRepositoryPrefix: "module",
		GitHubAssetPrefix:      "module",
		Resolution: AgentResolutionSupport{
			Local:  AgentResolutionLocalExecutable,
			OCI:    AgentResolutionBinaryDigest,
			Nix:    AgentResolutionBinaryDigest,
			GitHub: AgentResolutionGitHubRelease,
		},
		Operations:   AgentOperationSupport{Build: true, List: true, Install: true, Version: true, Publish: true, Load: true},
		AutoDownload: true,
	},
	{
		ProtoKind:              basev0.Agent_TOOLBOX,
		Resource:               ToolboxAgent,
		InstallSubdirectory:    "toolboxes",
		ExecutablePrefix:       "toolbox",
		GitHubRepositoryPrefix: "toolbox",
		GitHubAssetPrefix:      "toolbox",
		Resolution: AgentResolutionSupport{
			Local:  AgentResolutionLocalExecutable,
			OCI:    AgentResolutionBinaryDigest,
			Nix:    AgentResolutionBinaryDigest,
			GitHub: AgentResolutionGitHubRelease,
		},
		Operations:   AgentOperationSupport{Build: true, List: true, Install: true, Version: true, Publish: true, CI: true, Load: true},
		AutoDownload: true,
	},
	{
		ProtoKind:              basev0.Agent_PROVIDER,
		Resource:               ProviderAgent,
		InstallSubdirectory:    "providers",
		ExecutablePrefix:       "provider",
		GitHubRepositoryPrefix: "provider",
		GitHubAssetPrefix:      "provider",
		Resolution: AgentResolutionSupport{
			Local: AgentResolutionVerifiedArtifact,
			Nix:   AgentResolutionVerifiedArtifact,
		},
		Operations: AgentOperationSupport{Build: true, List: true, Install: true, Version: true, Publish: true, CI: true, Load: true},
	},
}

var (
	agentKinds  map[basev0.Agent_Kind]AgentKindRegistration
	agentInputs map[AgentKind]AgentKindRegistration
	CLI         *Agent
)

type Agent struct {
	Kind      AgentKind `yaml:"kind" json:"kind"`
	Name      string    `yaml:"name" json:"name"`
	Version   string    `yaml:"version" json:"version"`
	Publisher string    `yaml:"publisher" json:"publisher"`
}

func init() {
	CLI = &Agent{
		Kind:      "codefly:cli",
		Publisher: "codefly.dev",
		Name:      "cli",
		Version:   shared.Must(version.Version(context.Background())),
	}
	if err := initializeAgentKindRegistry(); err != nil {
		panic(err)
	}
}

func initializeAgentKindRegistry() error {
	agentKinds = make(map[basev0.Agent_Kind]AgentKindRegistration, len(agentKindRegistry))
	agentInputs = make(map[AgentKind]AgentKindRegistration, len(agentKindRegistry))
	repositories := make(map[string]AgentKind, len(agentKindRegistry))
	assets := make(map[string]AgentKind, len(agentKindRegistry))
	executables := make(map[string]AgentKind, len(agentKindRegistry))
	for i, registration := range agentKindRegistry {
		if err := ValidateAgentKindRegistration(registration); err != nil {
			return fmt.Errorf("agent kind registry[%d]: %w", i, err)
		}
		if _, exists := agentKinds[registration.ProtoKind]; exists {
			return fmt.Errorf("agent kind registry[%d]: duplicate proto kind %s", i, registration.ProtoKind)
		}
		for _, kind := range append([]AgentKind{registration.Resource}, registration.aliases...) {
			if _, exists := agentInputs[kind]; exists {
				return fmt.Errorf("agent kind registry[%d]: duplicate resource kind %s", i, kind)
			}
			agentInputs[kind] = registration
		}
		agentKinds[registration.ProtoKind] = registration
		if prior, exists := repositories[registration.GitHubRepositoryPrefix]; exists {
			return fmt.Errorf("agent kinds %s and %s share GitHub repository prefix %q", prior, registration.Resource, registration.GitHubRepositoryPrefix)
		}
		repositories[registration.GitHubRepositoryPrefix] = registration.Resource
		if prior, exists := assets[registration.GitHubAssetPrefix]; exists {
			return fmt.Errorf("agent kinds %s and %s share GitHub asset prefix %q", prior, registration.Resource, registration.GitHubAssetPrefix)
		}
		assets[registration.GitHubAssetPrefix] = registration.Resource
		if prior, exists := executables[registration.ExecutablePrefix]; exists {
			return fmt.Errorf("agent kinds %s and %s share executable prefix %q", prior, registration.Resource, registration.ExecutablePrefix)
		}
		executables[registration.ExecutablePrefix] = registration.Resource
	}
	return nil
}

func ValidateAgentKindRegistration(registration AgentKindRegistration) error {
	if registration.ProtoKind == basev0.Agent_UNKNOWN {
		return fmt.Errorf("proto kind is required")
	}
	if registration.Resource == "" {
		return fmt.Errorf("resource kind is required")
	}
	if registration.InstallSubdirectory == "" {
		return fmt.Errorf("install subdirectory is required")
	}
	if registration.ExecutablePrefix == "" {
		return fmt.Errorf("executable prefix is required")
	}
	if registration.GitHubRepositoryPrefix == "" || registration.GitHubAssetPrefix == "" {
		return fmt.Errorf("GitHub repository and asset prefixes are required")
	}
	if registration.AutoDownload && registration.Resolution.GitHub != AgentResolutionGitHubRelease {
		return fmt.Errorf("auto-download requires GitHub release resolution")
	}
	if registration.Operations.Load && registration.Resolution.Local == AgentResolutionDisabled {
		return fmt.Errorf("load support requires local resolution")
	}
	for store, behavior := range map[AgentStore]AgentResolutionBehavior{
		AgentStoreLocal: registration.Resolution.Local, AgentStoreOCI: registration.Resolution.OCI,
		AgentStoreNix: registration.Resolution.Nix, AgentStoreGitHub: registration.Resolution.GitHub,
	} {
		switch behavior {
		case AgentResolutionDisabled, AgentResolutionLocalExecutable, AgentResolutionBinaryDigest,
			AgentResolutionVerifiedArtifact, AgentResolutionGitHubRelease:
		default:
			return fmt.Errorf("%s resolution has unknown behavior %q", store, behavior)
		}
	}
	return nil
}

func AgentKindRegistry() []AgentKindRegistration {
	out := make([]AgentKindRegistration, len(agentKindRegistry))
	copy(out, agentKindRegistry)
	for i := range out {
		out[i].aliases = nil
	}
	return out
}

func AgentKindRegistrationFor(kind AgentKind) (AgentKindRegistration, error) {
	registration, ok := agentInputs[kind]
	if !ok {
		return AgentKindRegistration{}, fmt.Errorf("unknown agent kind: %s", kind)
	}
	return registration, nil
}

func AgentKindRegistrationFromProto(kind basev0.Agent_Kind) (AgentKindRegistration, error) {
	registration, ok := agentKinds[kind]
	if !ok {
		return AgentKindRegistration{}, fmt.Errorf("unknown agent kind: %s", kind)
	}
	return registration, nil
}

func (p *Agent) String() string {
	return fmt.Sprintf("%s/%s:%s", p.Publisher, p.Name, p.Version)
}

func LoadAgent(ctx context.Context, action *basev0.Agent, expectedKind AgentKind) (*Agent, error) {
	w := wool.Get(ctx).In("LoadAgent")
	agent, err := AgentFromProto(action)
	if err != nil {
		return nil, w.Wrapf(err, "invalid agent")
	}
	expected, err := AgentKindRegistrationFor(expectedKind)
	if err != nil {
		return nil, w.Wrapf(err, "invalid expected agent kind")
	}
	if agent.Kind != expected.Resource {
		return nil, w.NewError("agent kind %s cannot be used as %s", agent.Kind, expected.Resource)
	}
	return agent, nil
}

func AgentKindFromProto(kind basev0.Agent_Kind) (*AgentKind, error) {
	registration, err := AgentKindRegistrationFromProto(kind)
	if err != nil {
		return nil, err
	}
	resource := registration.Resource
	return &resource, nil
}

func ValidateAgent(agent *basev0.Agent) error {
	if agent == nil {
		return fmt.Errorf("agent is required")
	}
	v, err := protovalidate.New()
	if err != nil {
		return err
	}
	if err := v.Validate(agent); err != nil {
		return err
	}
	_, err = AgentKindRegistrationFromProto(agent.GetKind())
	return err
}

func (p *Agent) Identifier() string {
	return fmt.Sprintf("%s/%s:%s", p.Publisher, p.Name, p.Version)
}

func (p *Agent) Key(kind AgentKind, unique string) string {
	return fmt.Sprintf("%s::%s::%s", kind, p.Identifier(), unique)
}

func (p *Agent) Unique() string {
	return fmt.Sprintf("%s::%s", p.Kind, p.Identifier())
}

func (p *Agent) Of(kind AgentKind) *Agent {
	out := Agent{
		Kind:      kind,
		Publisher: p.Publisher,
		Name:      p.Name,
		Version:   p.Version,
	}
	return &out
}

func (p *Agent) IsService() bool {
	registration, err := AgentKindRegistrationFor(p.Kind)
	return err == nil && registration.Resource == ServiceAgent
}

func (p *Agent) IsApplication() bool {
	registration, err := AgentKindRegistrationFor(p.Kind)
	return err == nil && registration.Resource == ApplicationAgent
}

func (p *Agent) IsModule() bool {
	registration, err := AgentKindRegistrationFor(p.Kind)
	return err == nil && registration.Resource == ModuleAgent
}

func (p *Agent) IsToolbox() bool {
	registration, err := AgentKindRegistrationFor(p.Kind)
	return err == nil && registration.Resource == ToolboxAgent
}

func (p *Agent) IsProvider() bool {
	registration, err := AgentKindRegistrationFor(p.Kind)
	return err == nil && registration.Resource == ProviderAgent
}

func isRunningInDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}

func AgentBase(ctx context.Context) string {
	w := wool.Get(ctx).In("AgentBase")
	if isRunningInDocker() {
		w.Trace("running inside docker")
		return path.Join(CodeflyHomeDir(), "containers")
	}
	w.Trace("running natively")
	return CodeflyHomeDir()
}

func (p *Agent) Path(ctx context.Context) (string, error) {
	registration, err := AgentKindRegistrationFor(p.Kind)
	if err != nil {
		return "", err
	}
	if !registration.Operations.Load {
		return "", fmt.Errorf("agent kind %s does not support load", p.Kind)
	}
	name := strings.Replace(p.Identifier(), ":", "__", 1)
	return path.Join(AgentBase(ctx), "agents", registration.InstallSubdirectory, name), nil
}

func (p *Agent) Patch() (*Agent, error) {
	patch := &Agent{
		Kind:      p.Kind,
		Publisher: p.Publisher,
		Name:      p.Name,
	}

	v, err := semver.NewVersion(p.Version)
	if err != nil {
		return nil, err
	}
	n := v.IncPatch()
	patch.Version = n.String()
	return patch, nil
}

func ParseAgent(ctx context.Context, kind AgentKind, input string) (*Agent, error) {
	agent, err := parseAgent(ctx, kind, input)
	if err != nil {
		return nil, err
	}
	return AgentFromProto(agent)
}

func parseAgent(ctx context.Context, kind AgentKind, input string) (*basev0.Agent, error) {
	w := wool.Get(ctx).In("parseAgent", wool.Field("kind", kind), wool.Field("agent", input))
	if input == "" {
		return nil, fmt.Errorf("empty")
	}
	tokens := strings.SplitN(input, "/", 2)
	publisher := "codefly.dev"
	rest := input
	if len(tokens) == 2 {
		publisher = tokens[0]
		rest = tokens[1]
	}
	tokens = strings.Split(rest, ":")
	if len(tokens) > 2 || len(tokens) == 0 {
		return nil, fmt.Errorf("invalid agent name (should be of the form identifier:version): %s", input)
	}
	name := tokens[0]
	agentVersion := "latest"
	if len(tokens) == 1 {
		w.Warn("no version for agent specified, using latest")
	} else {
		agentVersion = tokens[1]
	}
	registration, err := AgentKindRegistrationFor(kind)
	if err != nil {
		return nil, err
	}
	agent := &basev0.Agent{Kind: registration.ProtoKind, Publisher: publisher, Name: name, Version: agentVersion}
	if err := ValidateAgent(agent); err != nil {
		return nil, err
	}
	return agent, nil
}

func AgentFromProto(agent *basev0.Agent) (*Agent, error) {
	if err := ValidateAgent(agent); err != nil {
		return nil, err
	}
	registration, err := AgentKindRegistrationFromProto(agent.Kind)
	if err != nil {
		return nil, err
	}
	return &Agent{
		Kind:      registration.Resource,
		Publisher: agent.Publisher,
		Name:      agent.Name,
		Version:   agent.Version,
	}, nil
}

func (p *Agent) Proto() (*basev0.Agent, error) {
	registration, err := AgentKindRegistrationFor(p.Kind)
	if err != nil {
		return nil, err
	}
	agent := &basev0.Agent{
		Kind:      registration.ProtoKind,
		Publisher: p.Publisher,
		Name:      p.Name,
		Version:   p.Version,
	}
	if err := ValidateAgent(agent); err != nil {
		return nil, err
	}
	return agent, nil
}

func (p *Agent) AsResource() *wool.Resource {
	return &wool.Resource{
		Kind:   "agent",
		Unique: p.Identifier(),
	}
}
