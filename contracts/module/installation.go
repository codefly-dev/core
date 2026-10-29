package module

import (
	"fmt"

	"github.com/codefly-dev/core/internal/wire"
	"gopkg.in/yaml.v3"
)

// Check scalar types before yaml's typed decoder can turn a number or boolean
// into a string. Mapping traversal stays on the existing strict wire path.
func checkInstallationStrings(root *yaml.Node) error {
	unalias := func(node *yaml.Node) *yaml.Node {
		for node.Kind == yaml.AliasNode {
			node = node.Alias
		}
		return node
	}
	check := func(field string, node *yaml.Node) error {
		node = unalias(node)
		if node.Kind != yaml.ScalarNode || node.ShortTag() != "!!str" {
			return fmt.Errorf("%w: installation declaration %s must be a string", ErrInvalid, field)
		}
		return nil
	}
	mapping := wire.ReadMapping(root, func(key string, value *yaml.Node) (bool, error) {
		switch key {
		case "installation_role":
			return true, check(key, value)
		case "edge_dependencies":
			value = unalias(value)
			if value.Kind == yaml.SequenceNode {
				for _, entry := range value.Content {
					fields := wire.ReadMapping(unalias(entry), func(field string, node *yaml.Node) (bool, error) {
						if field == "module" || field == "presence" {
							return true, check("edge_dependencies."+field, node)
						}
						return true, nil
					})
					if fields.Err != nil {
						return true, fields.Err
					}
				}
			}
		}
		return true, nil
	})
	return mapping.Err
}
