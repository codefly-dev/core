// Package wire holds the document-level checks the two wire contracts of this
// directory share, so a property of every YAML node is implemented once rather
// than once per typed decoder — and once per decoder is how a dynamic map came
// to have no guard at all, and how a null crossed a boundary advertised as
// strict.
package wire

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// DefectKind is the rule a Defect belongs to.
type DefectKind int

const (
	// NoDefect means the document carries none.
	NoDefect DefectKind = iota
	// DuplicateKey is one mapping naming one key twice, aliases resolved.
	DuplicateKey
	// NullNode is an explicit null anywhere in the document.
	NullNode
	// KeyNotAName is a mapping key that is not a non-empty scalar name.
	KeyNotAName
	// FractionalNumber is a number written with a fraction or an exponent,
	// which neither model ever declares.
	FractionalNumber
)

// Defect is what a document-level check found: which rule refuses it, where in
// the document, and the text a refusal names.
type Defect struct {
	Kind   DefectKind
	Path   string
	Detail string
}

// Check walks the whole document and reports the first defect.
//
// Three things cross a typed decoder silently, so they are caught here, where
// every node is visited:
//
//   - A DUPLICATE KEY. yaml.v3's own detection compares a key node's Kind and
//     Value BEFORE resolving an alias, so `&op invoke:` beside `*op :` is two
//     raw keys and one decoded key: the second replaced the first, changing the
//     scopes a binding requests or the pods a selector matches.
//   - A NULL. Decoding null into a string returns FALSE rather than an error;
//     yaml's struct decoder then SKIPS a key whose decode returned false, and
//     its sequence decoder omits the element. So `null: {tenancy: dedicated}`
//     discarded a whole subtree past a boundary advertised as strict, and
//     `actions: [redact, null]` lost an entry — differently from the other
//     ceiling spelling, which kept an empty one. Neither model gives an
//     explicit null a meaning: an absent field is absent, and an empty list is
//     written `[]`.
//   - A FRACTIONAL NUMBER. yaml.v3 CONVERTS a float to an integer before it
//     checks overflow, so `revision: 2.9` validated as revision 2 and
//     `port: 443.9` as port 443 — the fraction discarded before any rule saw
//     it, and unrecoverable afterwards: the revision goes into the resolved
//     binding and the ports become policy inputs. Neither model declares a
//     single non-integer field, so a fractional scalar is refused outright
//     rather than per numeric field, and an integer field added later is
//     covered without anyone remembering.
//   - A KEY THAT IS NOT A NAME. Both models' mappings are keyed by name, so a
//     sequence or mapping key is a document no reader can act on.
func Check(node *yaml.Node) Defect {
	return walk(node, "")
}

func walk(node *yaml.Node, at string) Defect {
	if node == nil {
		return Defect{}
	}
	if node.Tag == nullTag {
		return Defect{Kind: NullNode, Path: Where(at)}
	}
	if node.Tag == floatTag {
		return Defect{Kind: FractionalNumber, Path: Where(at), Detail: node.Value}
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if found := walk(child, at); found.Kind != NoDefect {
				return found
			}
		}
	case yaml.MappingNode:
		seen := make(map[string]bool, len(node.Content)/2)
		for index := 0; index+1 < len(node.Content); index += 2 {
			key := node.Content[index]
			if key.Tag == nullTag {
				return Defect{Kind: NullNode, Path: Where(at), Detail: "as a key"}
			}
			if key.Kind != yaml.ScalarNode && key.Kind != yaml.AliasNode {
				return Defect{Kind: KeyNotAName, Path: Where(at)}
			}
			var name string
			if err := key.Decode(&name); err != nil || name == "" {
				return Defect{Kind: KeyNotAName, Path: Where(at), Detail: key.Value}
			}
			if seen[name] {
				return Defect{Kind: DuplicateKey, Path: Where(at), Detail: name}
			}
			seen[name] = true
			if found := walk(node.Content[index+1], join(at, name)); found.Kind != NoDefect {
				return found
			}
		}
	case yaml.SequenceNode:
		for index, child := range node.Content {
			if found := walk(child, fmt.Sprintf("%s[%d]", at, index)); found.Kind != NoDefect {
				return found
			}
		}
	}
	return Defect{}
}

const (
	nullTag  = "!!null"
	floatTag = "!!float"
)

func join(at, name string) string {
	if at == "" {
		return name
	}
	return at + "." + name
}

// Where names the mapping a defect was found in, for a refusal's message.
func Where(path string) string {
	if path == "" {
		return "the document's top level"
	}
	return path
}
