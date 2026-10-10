package configurations

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// ConfigurationOrigin is a value-free origin for a final workspace configuration
// key, or for a whole structured document (Document=true, Key empty). File is the
// slash-separated path relative to the consuming workspace directory. No values,
// secret URIs or content hashes are exposed.
// This covers disk composition before invocation overrides, reference resolution
// and injection. It is not a complete input receipt or an attestation.
type ConfigurationOrigin = basev0.ConfigurationOrigin

// ConfigurationDecision records one executed precedence rule. It is deliberately
// narrower than a complete resolver trace: workspace/import and module-default
// group replacement plus derived profile keys/documents are instrumented.
// No values or equality comparisons escape.
type ConfigurationDecision = basev0.ConfigurationDecision

// ConfigurationProfileSelection is the resolver's location-level choice. Layers
// are ordered base first; they are not per-file fallback candidates.
type ConfigurationProfileSelection = basev0.ConfigurationProfileSelection

type configurationOriginsKey struct{}

// Pointer identity follows the objects actually selected by the resolver. No
// origin is guessed by comparing values, so equal-valued overrides keep the
// overriding file's identity. State belongs to one synchronous workspace read.
type configurationOrigins struct {
	root             string
	defaultDecisions map[moduleGroup]int
	profiles         []*ConfigurationProfileSelection
	profileTargets   map[int]profileOriginTarget
	selectedGroups   []*basev0.ConfigurationInformation
	decisions        []*ConfigurationDecision
	values           map[*basev0.ConfigurationValue]string
	valueParents     map[*basev0.ConfigurationValue]*basev0.ConfigurationValue
	documents        map[*basev0.ConfigurationData]string
}

type moduleGroup struct {
	module string
	group  string
}

type profileOriginTarget struct {
	value    *basev0.ConfigurationValue
	document *basev0.ConfigurationData
}

func newConfigurationOrigins(root string) (*configurationOrigins, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve configuration evidence root: %w", err)
	}
	return &configurationOrigins{
		root:             absolute,
		defaultDecisions: make(map[moduleGroup]int),
		values:           make(map[*basev0.ConfigurationValue]string),
		documents:        make(map[*basev0.ConfigurationData]string),
	}, nil
}

// relativePath removes host-specific prefixes at the evidence boundary. The
// composition root stays private; callers already supplied the workspace.
func (t *configurationOrigins) relativePath(file string) (string, error) {
	absolute, err := filepath.Abs(file)
	if err != nil {
		return "", fmt.Errorf("resolve configuration evidence path: %w", err)
	}
	relative, err := filepath.Rel(t.root, absolute)
	if err != nil {
		return "", fmt.Errorf("relativize configuration evidence path: %w", err)
	}
	return filepath.ToSlash(relative), nil
}

func (t *configurationOrigins) profileSelection(location string, candidates, layers []string, found bool) error {
	relative, err := t.relativePath(location)
	if err != nil {
		return err
	}
	selection := &ConfigurationProfileSelection{
		Location: relative, Candidates: append([]string{}, candidates...), Found: found,
	}
	for _, layer := range layers {
		relative, err := t.relativePath(layer)
		if err != nil {
			return err
		}
		selection.Layers = append(selection.Layers, relative)
	}
	t.profiles = append(t.profiles, selection)
	return nil
}

func configurationOriginsFrom(ctx context.Context) *configurationOrigins {
	t, _ := ctx.Value(configurationOriginsKey{}).(*configurationOrigins)
	return t
}

func (t *configurationOrigins) shadowedImport(workspace, imported string, candidate *basev0.ConfigurationInformation, selected []*basev0.ConfigurationInformation) {
	for _, winner := range selected {
		if winner.Name == candidate.Name {
			t.selectedGroups = append(t.selectedGroups, winner)
			t.decisions = append(t.decisions, &ConfigurationDecision{
				Group: candidate.Name, Workspace: workspace, ImportedWorkspace: imported,
				Rule:     basev0.ConfigurationRule_CONFIGURATION_RULE_WORKSPACE_GROUP_REPLACES_IMPORTED_GROUP,
				Selected: t.project([]*basev0.ConfigurationInformation{winner}),
				Shadowed: t.project([]*basev0.ConfigurationInformation{candidate}),
			})
			return
		}
	}
}

func (t *configurationOrigins) finalDecisions(infos []*basev0.ConfigurationInformation) []*ConfigurationDecision {
	final := make(map[*basev0.ConfigurationInformation]bool)
	values := make(map[*basev0.ConfigurationValue]bool)
	documents := make(map[*basev0.ConfigurationData]bool)
	for _, info := range infos {
		final[info] = true
		for _, value := range info.GetConfigurationValues() {
			for v := value; v != nil; v = t.valueParents[v] {
				values[v] = true
			}
		}
		if info.GetData() != nil {
			documents[info.GetData()] = true
		}
	}
	for i := range t.decisions {
		t.decisions[i].Final = final[t.selectedGroups[i]]
		if target, ok := t.profileTargets[i]; ok {
			t.decisions[i].Final = (target.value != nil && values[target.value]) || (target.document != nil && documents[target.document])
		}
	}
	return t.decisions
}

func (t *configurationOrigins) profileReplacement(profile, group string, selected, shadowed *basev0.ConfigurationInformation) {
	rule := basev0.ConfigurationRule_CONFIGURATION_RULE_PROFILE_KEY_REPLACES_BASE_KEY
	target := profileOriginTarget{}
	if selected.GetData() != nil {
		rule = basev0.ConfigurationRule_CONFIGURATION_RULE_PROFILE_DOCUMENT_REPLACES_BASE_DOCUMENT
		target.document = selected.GetData()
	} else {
		target.value = selected.GetConfigurationValues()[0]
	}
	if t.profileTargets == nil {
		t.profileTargets = make(map[int]profileOriginTarget)
	}
	t.profileTargets[len(t.decisions)] = target
	t.selectedGroups = append(t.selectedGroups, nil)
	t.decisions = append(t.decisions, &ConfigurationDecision{Group: group, Profile: profile, Rule: rule,
		Selected: t.project([]*basev0.ConfigurationInformation{selected}),
		Shadowed: t.project([]*basev0.ConfigurationInformation{shadowed})})
}

func (t *configurationOrigins) shadowedDefault(module string, candidate *basev0.ConfigurationInformation, selected []*basev0.ConfigurationInformation) {
	for _, winner := range selected {
		if winner.Name == candidate.Name {
			key := moduleGroup{module: module, group: candidate.Name}
			if index, exists := t.defaultDecisions[key]; exists {
				t.decisions[index].Shadowed = append(t.decisions[index].Shadowed, t.project([]*basev0.ConfigurationInformation{candidate})...)
				return
			}
			t.defaultDecisions[key] = len(t.decisions)
			t.selectedGroups = append(t.selectedGroups, winner)
			t.decisions = append(t.decisions, &ConfigurationDecision{
				Group: candidate.Name, Module: module, Rule: basev0.ConfigurationRule_CONFIGURATION_RULE_WORKSPACE_GROUP_REPLACES_MODULE_DEFAULT,
				Selected: t.project([]*basev0.ConfigurationInformation{winner}),
				Shadowed: t.project([]*basev0.ConfigurationInformation{candidate}),
			})
			return
		}
	}
}

// workspaceOverlay follows the successful resolver overlay, including its cloned
// retained keys. Identity is transferred by the resolver's key mapping, never by
// comparing values.
func (t *configurationOrigins) workspaceOverlay(base, override, result *basev0.ConfigurationInformation, module string) {
	if t.valueParents == nil {
		t.valueParents = make(map[*basev0.ConfigurationValue]*basev0.ConfigurationValue)
	}
	if t.profileTargets == nil {
		t.profileTargets = make(map[int]profileOriginTarget)
	}
	if result.GetData() != nil {
		t.profileTargets[len(t.decisions)] = profileOriginTarget{document: result.GetData()}
		t.selectedGroups = append(t.selectedGroups, nil)
		t.decisions = append(t.decisions, &ConfigurationDecision{Group: result.Name, Module: module, Rule: basev0.ConfigurationRule_CONFIGURATION_RULE_WORKSPACE_DOCUMENT_REPLACES_MODULE_DEFAULT,
			Selected: t.project([]*basev0.ConfigurationInformation{override}), Shadowed: t.project([]*basev0.ConfigurationInformation{base})})
		return
	}
	for _, value := range result.GetConfigurationValues() {
		replacement := findConfigurationValue(override, value.GetKey())
		previous := findConfigurationValue(base, value.GetKey())
		if replacement == nil {
			if file, ok := t.values[previous]; ok {
				t.values[value] = file
				t.valueParents[value] = previous
			}
			continue
		}
		t.profileTargets[len(t.decisions)] = profileOriginTarget{value: value}
		t.selectedGroups = append(t.selectedGroups, nil)
		t.decisions = append(t.decisions, &ConfigurationDecision{Group: result.Name, Module: module, Rule: basev0.ConfigurationRule_CONFIGURATION_RULE_WORKSPACE_KEY_REPLACES_MODULE_DEFAULT,
			Selected: t.project([]*basev0.ConfigurationInformation{{Name: result.Name, ConfigurationValues: []*basev0.ConfigurationValue{replacement}}}),
			Shadowed: t.project([]*basev0.ConfigurationInformation{{Name: result.Name, ConfigurationValues: []*basev0.ConfigurationValue{previous}}})})
	}
}

func (t *configurationOrigins) record(info *basev0.ConfigurationInformation, file string) error {
	file, err := t.relativePath(file)
	if err != nil {
		return err
	}
	for _, value := range info.GetConfigurationValues() {
		t.values[value] = file
	}
	if info.GetData() != nil {
		t.documents[info.GetData()] = file
	}
	return nil
}

func (t *configurationOrigins) project(infos []*basev0.ConfigurationInformation) []*ConfigurationOrigin {
	out := make([]*ConfigurationOrigin, 0)
	for _, info := range infos {
		for _, value := range info.GetConfigurationValues() {
			if file, ok := t.values[value]; ok {
				out = append(out, &ConfigurationOrigin{Group: info.GetName(), Key: value.GetKey(), File: file})
			}
		}
		if file, ok := t.documents[info.GetData()]; ok {
			out = append(out, &ConfigurationOrigin{Group: info.GetName(), File: file, Document: true})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Key < out[j].Key
	})
	return out
}
