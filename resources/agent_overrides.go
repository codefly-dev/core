package resources

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// AgentOverridesKey is the top-level workspace.codefly.yaml key moving the
// agent version of composed services in committed config:
//
//	agent-overrides:
//	    codefly.dev/go-grpc: 0.1.47
//
// Each key is an agent identity, <publisher>/<name>; its value is the version
// every composed service built on that agent runs against, whatever version the
// module that composed it pins. Only the version moves: the key names the
// publisher and name it applies to, and the kind is never touched.
//
// It is a top-level key carried through Workspace.Extensions, like the host's
// `module-trust` and `module-resolution`, so a load-and-save of the workspace
// preserves it.
//
// It exists because an agent is published and versioned independently of the
// modules that compose it: a fix to one agent must be able to reach a
// deployment without re-tagging every module that uses that agent. It does not
// widen what a module is trusted to contain — the module's own content is
// untouched, only which published agent release interprets it.
const AgentOverridesKey = "agent-overrides"

// AgentOverride is one parsed entry of the workspace's agent-overrides block.
type AgentOverride struct {
	Publisher string
	Name      string
	Version   string
}

// Key is the override's spelling in workspace.codefly.yaml: <publisher>/<name>.
func (override AgentOverride) Key() string {
	return override.Publisher + "/" + override.Name
}

// Matches reports whether the override applies to an agent: same publisher and
// name. The kind is not part of the identity an override names.
func (override AgentOverride) Matches(agent *Agent) bool {
	return agent != nil && agent.Publisher == override.Publisher && agent.Name == override.Name
}

// AgentOverrides parses the workspace's committed agent-overrides block, sorted
// by key. It returns nil when the workspace declares none. A malformed key or a
// version that is not an exact semantic version is an error naming the key:
// either silently ignored would run the module's own pin while the workspace
// reads as if it moved it.
func (workspace *Workspace) AgentOverrides() ([]AgentOverride, error) {
	value, ok := workspace.Extensions[AgentOverridesKey]
	if !ok {
		return nil, nil
	}
	var raw map[string]string
	if err := value.Node.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s in %s must map <publisher>/<name> to a version: %w", AgentOverridesKey, WorkspaceConfigurationName, err)
	}
	return ParseAgentOverrides(raw)
}

// ParseAgentOverrides validates a raw agent-overrides map. See AgentOverrides.
func ParseAgentOverrides(raw map[string]string) ([]AgentOverride, error) {
	var overrides []AgentOverride
	for key, version := range raw {
		publisher, name, found := strings.Cut(key, "/")
		if !found || !agentIdentityComponent.MatchString(publisher) || !agentIdentityComponent.MatchString(name) {
			return nil, fmt.Errorf("%s key %q in %s is not an agent identity; spell it <publisher>/<name>, e.g. codefly.dev/go-grpc", AgentOverridesKey, key, WorkspaceConfigurationName)
		}
		version = strings.TrimSpace(version)
		if _, err := semver.StrictNewVersion(version); err != nil {
			return nil, fmt.Errorf("%s.%s in %s is %q, not an exact semantic version (e.g. 0.1.47)", AgentOverridesKey, key, WorkspaceConfigurationName, version)
		}
		overrides = append(overrides, AgentOverride{Publisher: publisher, Name: name, Version: version})
	}
	sort.Slice(overrides, func(i, j int) bool { return overrides[i].Key() < overrides[j].Key() })
	return overrides, nil
}

// applyAgentOverride moves the service's agent to the version the workspace
// overrides it to. Publisher, name and kind are never changed.
func (mod *Module) applyAgentOverride(service *Service) {
	if service.Agent == nil {
		return
	}
	for _, override := range mod.agentOverrides {
		if override.Matches(service.Agent) {
			agent := *service.Agent
			agent.Version = override.Version
			service.Agent = &agent
			return
		}
	}
}
