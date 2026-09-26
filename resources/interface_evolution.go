package resources

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// InterfaceEvolution is the computed difference between two published
// versions of one interface. It is structural: it compares the surface a
// definition declares, so it sees a removed method, route, tool or key, but not
// a changed message field behind an unchanged method.
type InterfaceEvolution struct {
	Interface string   `json:"interface"`
	Before    string   `json:"before"`
	After     string   `json:"after"`
	Breaking  []string `json:"breaking,omitempty"`
	Additive  []string `json:"additive,omitempty"`
}

// Compatible reports whether a consumer of the earlier version keeps working
// against the later one.
func (e *InterfaceEvolution) Compatible() bool {
	return len(e.Breaking) == 0
}

// EvolveInterface computes what changed from before to after and rejects a
// version that under-states it. A breaking change must leave the range a
// consumer of before requires (^before): a new major, or a new minor below
// 1.0.0. An additive change must at least bump the minor. Compatibility is
// computed here, so no author's claim about it is consulted.
func EvolveInterface(before, after *Interface) (*InterfaceEvolution, error) {
	if before.Publisher != after.Publisher || before.Name != after.Name {
		return nil, fmt.Errorf("cannot compare interface %s with %s: they are different interfaces", before.Identity(), after.Identity())
	}
	beforeVersion, err := semver.StrictNewVersion(before.Version)
	if err != nil {
		return nil, fmt.Errorf("interface %s: %w", before.Identity(), err)
	}
	afterVersion, err := semver.StrictNewVersion(after.Version)
	if err != nil {
		return nil, fmt.Errorf("interface %s: %w", after.Identity(), err)
	}
	evolution := &InterfaceEvolution{
		Interface: before.Identity().Key(),
		Before:    before.Version,
		After:     after.Version,
	}
	if !afterVersion.GreaterThan(beforeVersion) {
		return evolution, fmt.Errorf("interface %s: version %s must be greater than the published %s", evolution.Interface, after.Version, before.Version)
	}
	if before.Type != after.Type {
		evolution.Breaking = append(evolution.Breaking, fmt.Sprintf("type changed from %s to %s", before.Type, after.Type))
	} else {
		evolution.Breaking, evolution.Additive = diffInterfaceSurfaces(before.surface(), after.surface())
	}
	switch {
	case len(evolution.Breaking) > 0 && !leavesCaretRange(beforeVersion, afterVersion):
		return evolution, fmt.Errorf("interface %s: %s is not a breaking version of %s, but the surface breaks: %s",
			evolution.Interface, after.Version, before.Version, strings.Join(evolution.Breaking, "; "))
	case len(evolution.Additive) > 0 && afterVersion.Major() == beforeVersion.Major() && afterVersion.Minor() == beforeVersion.Minor():
		return evolution, fmt.Errorf("interface %s: %s only bumps the patch of %s, but the surface grows: %s",
			evolution.Interface, after.Version, before.Version, strings.Join(evolution.Additive, "; "))
	}
	return evolution, nil
}

// leavesCaretRange reports whether after is outside ^before, the range a
// consumer pinned to before declares. Below 1.0.0 the minor is the breaking
// component, as the caret range treats it.
func leavesCaretRange(before, after *semver.Version) bool {
	if after.Major() != before.Major() {
		return true
	}
	return before.Major() == 0 && after.Minor() != before.Minor()
}

// surfaceItem is one element of an interface surface. growthBreaks marks an
// item whose appearance breaks existing providers: a new required capability
// key is something no provider of the earlier version emits.
type surfaceItem struct {
	signature    string
	growthBreaks bool
}

func (i *Interface) surface() map[string]surfaceItem {
	items := make(map[string]surfaceItem)
	switch {
	case i.Protobuf != nil:
		for procedure := range i.Protobuf.procedures() {
			items["procedure "+procedure] = surfaceItem{}
		}
	case i.OpenAPI != nil:
		for _, route := range i.OpenAPI.Routes {
			items["route "+route.Method+" "+route.Path] = surfaceItem{}
		}
	case i.MCP != nil:
		for _, tool := range i.MCP.Tools {
			items["tool "+tool] = surfaceItem{}
		}
		for _, resource := range i.MCP.Resources {
			items["resource "+resource] = surfaceItem{}
		}
	case i.Capability != nil:
		items["configuration group"] = surfaceItem{signature: i.Capability.Configuration, growthBreaks: true}
		for _, key := range i.Capability.Keys {
			items["key "+key.Name] = surfaceItem{
				signature:    fmt.Sprintf("secret=%t optional=%t", key.Secret, key.Optional),
				growthBreaks: !key.Optional,
			}
		}
	}
	return items
}

func diffInterfaceSurfaces(before, after map[string]surfaceItem) (breaking, additive []string) {
	for name, item := range before {
		next, kept := after[name]
		switch {
		case !kept:
			breaking = append(breaking, "removed "+name)
		case next.signature != item.signature:
			breaking = append(breaking, fmt.Sprintf("changed %s from %q to %q", name, item.signature, next.signature))
		}
	}
	for name, item := range after {
		if _, existed := before[name]; existed {
			continue
		}
		if item.growthBreaks {
			breaking = append(breaking, "added required "+name)
		} else {
			additive = append(additive, "added "+name)
		}
	}
	sort.Strings(breaking)
	sort.Strings(additive)
	return breaking, additive
}
