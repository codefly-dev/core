package resources

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ConfigurationExtension adds the workspace's group to the imported group.
// Append explicitly names env keys and the delimiter joining their values.
// Other scalar collisions are refused. YAML maps combine and lists append.
type ConfigurationExtension struct {
	Append map[string]string `yaml:"append,omitempty"`
}

func (extension *ConfigurationExtension) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("configuration extension must be a mapping")
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value != "append" {
			return fmt.Errorf("configuration extension: unknown field %q", node.Content[i].Value)
		}
	}
	type plain ConfigurationExtension
	return node.Decode((*plain)(extension))
}
