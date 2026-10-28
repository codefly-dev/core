package configurations

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

func extendWorkspaceConfiguration(ctx context.Context, base, addition *basev0.ConfigurationInformation, rule resources.ConfigurationExtension) (*basev0.ConfigurationInformation, error) {
	conflict := func(key, reason string) error {
		return fmt.Errorf("configuration %s key %s: %s: %w", base.Name, key, reason, ErrConfigurationConflict)
	}
	result := &basev0.ConfigurationInformation{Name: base.Name}
	if (base.Data != nil) != (addition.Data != nil) {
		return nil, conflict("$", "cannot combine a document and env keys")
	}
	if base.Data != nil {
		if len(rule.Append) != 0 {
			return nil, conflict("$", "append delimiters apply only to env keys")
		}
		if base.Data.Kind != addition.Data.Kind || base.Data.Kind != kindYaml {
			return nil, conflict("$", "extension requires two YAML documents")
		}
		left, err := extensionDocument(base.Data.Content)
		if err != nil {
			return nil, conflict("$", "imported document: "+err.Error())
		}
		right, err := extensionDocument(addition.Data.Content)
		if err != nil {
			return nil, conflict("$", "own document: "+err.Error())
		}
		if err = mergeExtensionYAML(left, right, "$", conflict); err != nil {
			return nil, err
		}
		content, err := yaml.Marshal(left)
		if err != nil {
			return nil, err
		}
		result.Data = &basev0.ConfigurationData{Kind: kindYaml, Content: content, Secret: base.Data.Secret || addition.Data.Secret}
		if trace := configurationOriginsFrom(ctx); trace != nil {
			if trace.documentParents == nil {
				trace.documentParents = make(map[*basev0.ConfigurationData][]*basev0.ConfigurationData)
			}
			trace.documentParents[result.Data] = []*basev0.ConfigurationData{base.Data, addition.Data}
		}
		return result, nil
	}
	for _, info := range []*basev0.ConfigurationInformation{base, addition} {
		if key, _, duplicate := duplicateConfigurationKey(info); duplicate {
			return nil, conflict(key, "declared twice")
		}
	}
	result.ConfigurationValues = append(result.ConfigurationValues, base.ConfigurationValues...)
	keys := make([]string, 0, len(rule.Append))
	for key := range rule.Append {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if rule.Append[key] == "" || findConfigurationValue(base, key) == nil || findConfigurationValue(addition, key) == nil {
			return nil, conflict(key, "append requires a nonempty delimiter and a value in both groups")
		}
	}
	for _, value := range addition.ConfigurationValues {
		previous := findConfigurationValue(base, value.Key)
		if previous == nil {
			result.ConfigurationValues = append(result.ConfigurationValues, value)
			continue
		}
		delimiter, appendValue := rule.Append[value.Key]
		if !appendValue {
			return nil, conflict(value.Key, "already declared by the imported group")
		}
		if hasModuleArtifact(previous) || hasModuleArtifact(value) || previous.Secret || value.Secret || previous.Template != nil || value.Template != nil || strings.Contains(previous.Value+value.Value, ProfileValueMarker) {
			return nil, conflict(value.Key, "append requires supplied plaintext values without templates")
		}
		seen := map[string]bool{}
		for _, item := range strings.Split(previous.Value+delimiter+value.Value, delimiter) {
			item = strings.TrimSpace(item)
			if item == "" || seen[item] {
				return nil, conflict(value.Key, "append contains an empty or duplicate list item")
			}
			seen[item] = true
		}
		combined := proto.Clone(value).(*basev0.ConfigurationValue)
		combined.Value = previous.Value + delimiter + value.Value
		setConfigurationValue(result, combined)
		if trace := configurationOriginsFrom(ctx); trace != nil {
			if trace.valueContributors == nil {
				trace.valueContributors = make(map[*basev0.ConfigurationValue][]*basev0.ConfigurationValue)
			}
			trace.valueContributors[combined] = []*basev0.ConfigurationValue{previous, value}
		}
	}
	return result, nil
}

func extensionDocument(content []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid YAML document")
	}
	if err := decoder.Decode(new(yaml.Node)); err != io.EOF {
		return nil, fmt.Errorf("expected one YAML document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected a mapping document")
	}
	if err := validateExtensionYAML(doc.Content[0], "$"); err != nil {
		return nil, err
	}
	return doc.Content[0], nil
}

// Refuse aliases/merge keys and repeated keys before merging: none may silently
// choose a winner. Do not put values (possibly secrets) into a diagnostic.
func validateExtensionYAML(node *yaml.Node, path string) error {
	switch node.Kind {
	case yaml.MappingNode:
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return fmt.Errorf("key %s: requires string keys without YAML merges", path)
			}
			at := path + "." + key.Value
			if seen[key.Value] {
				return fmt.Errorf("key %s: declared twice", at)
			}
			seen[key.Value] = true
			if err := validateExtensionYAML(node.Content[i+1], at); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for i, child := range node.Content {
			if err := validateExtensionYAML(child, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
	case yaml.AliasNode:
		return fmt.Errorf("key %s: YAML aliases cannot be extended", path)
	default:
		return fmt.Errorf("key %s: unsupported YAML node", path)
	}
	return nil
}

func mergeExtensionYAML(base, addition *yaml.Node, path string, conflict func(string, string) error) error {
	if base.Kind == yaml.SequenceNode && addition.Kind == yaml.SequenceNode {
		base.Content = append(base.Content, addition.Content...)
		return nil
	}
	if base.Kind != yaml.MappingNode || addition.Kind != yaml.MappingNode {
		return conflict(path, "already declared or has incompatible types")
	}
	for i := 0; i < len(addition.Content); i += 2 {
		key, value := addition.Content[i], addition.Content[i+1]
		index := -1
		for j := 0; j < len(base.Content); j += 2 {
			if base.Content[j].Value == key.Value {
				index = j
				break
			}
		}
		if index < 0 {
			base.Content = append(base.Content, key, value)
			continue
		}
		if err := mergeExtensionYAML(base.Content[index+1], value, path+"."+key.Value, conflict); err != nil {
			return err
		}
	}
	return nil
}
