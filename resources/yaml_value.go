package resources

import "gopkg.in/yaml.v3"

// YAMLValue preserves host-owned YAML without resolving scalar values into Go
// types. In particular, a host string field must retain 00123, not the integer 83.
type YAMLValue struct {
	yaml.Node
}

func (value *YAMLValue) UnmarshalYAML(node *yaml.Node) error {
	// Use the decoder's cycle and alias-expansion guards before copying aliases.
	// Their anchors can live in Core-owned fields that are serialized separately.
	var checked any
	if err := node.Decode(&checked); err != nil {
		return err
	}
	value.Node = *copyYAMLValue(node)
	return nil
}

func (value YAMLValue) MarshalYAML() (any, error) {
	return &value.Node, nil
}

func copyYAMLValue(node *yaml.Node) *yaml.Node {
	if node.Kind == yaml.AliasNode {
		return copyYAMLValue(node.Alias)
	}
	copy := *node
	copy.Anchor = ""
	copy.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		copy.Content[i] = copyYAMLValue(child)
	}
	return &copy
}
