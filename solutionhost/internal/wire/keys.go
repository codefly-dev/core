// Package wire holds the document-level checks the two wire contracts of this
// directory share, so a property of every YAML mapping is implemented once
// rather than once per typed decoder — and once per decoder is how a dynamic
// map came to have no guard at all.
package wire

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// DuplicateKey reports the first mapping in the document that names one key
// twice AFTER aliases are resolved, with the path to it.
//
// yaml.v3's own duplicate detection compares a key node's Kind and Value
// BEFORE resolving an alias (decode.go), so
//
//	&op invoke: [read]
//	*op : [invoke, read]
//
// is two distinct raw keys and one decoded key: the second silently replaced
// the first, changing the scopes a binding requests. The same shape in a
// selector — {&label app: api, *label : other} — changes which pods a policy
// selects. Neither is reachable by a typed decoder's own field checks, because
// both live in ordinary maps, which is why this walks the document instead.
func DuplicateKey(node *yaml.Node) (path, key string, found bool) {
	return walk(node, "")
}

func walk(node *yaml.Node, at string) (string, string, bool) {
	if node == nil {
		return "", "", false
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if path, key, found := walk(child, at); found {
				return path, key, found
			}
		}
	case yaml.MappingNode:
		seen := make(map[string]bool, len(node.Content)/2)
		for index := 0; index+1 < len(node.Content); index += 2 {
			var name string
			// Decoded, so an alias is the key it resolves to. A key that is
			// not a string at all is another decoder's refusal, not this one's.
			if err := node.Content[index].Decode(&name); err != nil {
				continue
			}
			if seen[name] {
				return at, name, true
			}
			seen[name] = true
			if path, key, found := walk(node.Content[index+1], join(at, name)); found {
				return path, key, found
			}
		}
	case yaml.SequenceNode:
		for index, child := range node.Content {
			if path, key, found := walk(child, fmt.Sprintf("%s[%d]", at, index)); found {
				return path, key, found
			}
		}
	}
	return "", "", false
}

func join(at, name string) string {
	if at == "" {
		return name
	}
	return at + "." + name
}

// Where names the mapping a duplicate was found in, for a refusal's message.
func Where(path string) string {
	if path == "" {
		return "the document's top level"
	}
	return path
}
