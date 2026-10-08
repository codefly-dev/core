// Package wire holds the document-level checks the wire contracts of this
// repository share, so a property of every YAML node is implemented once rather
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
	// which no model here ever declares.
	FractionalNumber
	// MergeKey is a YAML merge key (`<<`) in a mapping. The typed decoder
	// applies a merge AFTER every node-level check has run, so a mapping
	// carried in through one is a mapping no node rule saw; no model here
	// declares merge semantics.
	MergeKey
)

// Kinds is every rule Check reports, so a reader that must hold one fixture
// per rule drives off this list rather than hardcoding a bound of its own in
// another package, where a rule added here would not reach it. It is
// maintained with the block above — a rule added there is added here, in the
// same edit — and that is the whole of the guarantee: a Kind left out of this
// list is a rule no table built on it asks about. It lives beside the
// constants so the two are one screen apart.
func Kinds() []DefectKind {
	return []DefectKind{DuplicateKey, NullNode, KeyNotAName, FractionalNumber, MergeKey}
}

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
//   - A KEY THAT IS NOT A NAME. The models' mappings are keyed by name, so a
//     sequence or mapping key is a document no reader can act on — and so is
//     a key written under any tag but !!str, since the typed decoder resolves
//     `!!binary YnVpbGRfc2l6ZQ==` to build_size while a rule looking for the
//     key by its written text never sees it.
//   - A MERGE KEY. yaml.v3 applies `<<` inside its typed decoder, after this
//     walk and after any check a reader runs over the tree, so a mapping
//     merged in is one that reached a typed field unseen: a count spelled in
//     octal under `<<: {languages: …}` was repaired exactly as a direct one
//     is refused. A merge is also a second way to spell every key, which is
//     one spelling too many for a document whose keys are compared by name.
func Check(node *yaml.Node) Defect {
	return (&walker{walked: map[*yaml.Node]bool{}}).walk(node, "", 0)
}

// maxAliasDepth bounds alias following, defensively: yaml.v3 gives an alias no
// properties, so a chain is one step long and a cycle is refused as the
// document parses (`anchor value contains itself`), and neither ever reaches
// this walk. The bound keeps a loop over a node graph a document controls from
// being unbounded in principle; it names no rule, because nothing can reach it.
const maxAliasDepth = 100

// A walker visits each node of the document ONCE. The checks are context-free
// — a defect in a node is a defect wherever the node is reached — so an alias
// target that has already been walked carries nothing new, and re-walking it
// is what made the walk exponential: `x0: &a0 [lol]` and then `xN: &aN [*aN-1
// ×10]` for N levels is a 579-byte document that took 132 seconds at nine
// levels, in the loader the CLI runs on every manifest, where yaml.v3's typed
// decoder (whose own aliasing guard this walk runs before) answered at once.
type walker struct {
	walked map[*yaml.Node]bool
}

func (w *walker) walk(node *yaml.Node, at string, depth int) Defect {
	if node == nil {
		return Defect{}
	}
	// An ALIAS is its target. The decoder follows it, so a check that does not
	// is a check the document can step around: `{&fraction 443.9: api}`
	// anchored a fraction, `port: *fraction` used it, and the fraction reached
	// an integer field as 443 — a port no declaration states. The target is
	// walked once: at its anchor, which precedes every alias to it in the
	// document, or here if it somehow did not. A cycle never reaches this
	// walk: yaml.v3 refuses a self-containing anchor as it parses.
	if node.Kind == yaml.AliasNode {
		if depth >= maxAliasDepth || w.walked[node.Alias] {
			return Defect{}
		}
		return w.walk(node.Alias, at, depth+1)
	}
	if w.walked[node] {
		return Defect{}
	}
	w.walked[node] = true
	if defect := scalarDefect(node, at); defect.Kind != NoDefect {
		return defect
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if found := w.walk(child, at, depth); found.Kind != NoDefect {
				return found
			}
		}
	case yaml.MappingNode:
		seen := make(map[string]bool, len(node.Content)/2)
		for index := 0; index+1 < len(node.Content); index += 2 {
			key := node.Content[index]
			// A KEY gets every check a value gets, through the same function.
			// Keys had their own null check and nothing else, so an anchored
			// fraction written as a key was never examined — and a key is
			// where an anchor is most often declared.
			resolved := Resolve(key)
			if resolved == nil {
				return Defect{Kind: KeyNotAName, Path: Where(at)}
			}
			if isMergeKey(resolved) {
				return Defect{Kind: MergeKey, Path: Where(at)}
			}
			if defect := scalarDefect(resolved, at); defect.Kind != NoDefect {
				if defect.Kind == NullNode {
					defect.Detail = "as a key"
				}
				return defect
			}
			key = resolved
			if key.Kind != yaml.ScalarNode {
				// A sequence or a mapping written as a key (`? [a]`) has no
				// text of its own, so a detail taken from its value named
				// nothing: the refusal read `a key that is not a name ("")`
				// and left the author to find it. What it IS, and where, is
				// the detail — Where() names the enclosing mapping only.
				return Defect{Kind: KeyNotAName, Path: Where(at), Detail: fmt.Sprintf("%s at line %d", key.Tag, key.Line)}
			}
			// A name is a STRING, written as one: the key's tag is !!str,
			// plain or quoted. Any other spelling of a key is refused, because
			// the typed decoder resolves it to a string a reader matching keys
			// by their written text does not see — `!!binary YnVpbGRfc2l6ZQ==`
			// decodes to build_size and hid a whole section from every rule
			// that looked for the key by name; an integer, boolean or binary
			// key is a key no model here declares.
			if key.Tag != "!!str" {
				return Defect{Kind: KeyNotAName, Path: Where(at), Detail: key.Tag + " " + key.Value}
			}
			var name string
			if err := key.Decode(&name); err != nil || name == "" {
				return Defect{Kind: KeyNotAName, Path: Where(at), Detail: key.Value}
			}
			if seen[name] {
				return Defect{Kind: DuplicateKey, Path: Where(at), Detail: name}
			}
			seen[name] = true
			if found := w.walk(node.Content[index+1], join(at, name), depth); found.Kind != NoDefect {
				return found
			}
		}
	case yaml.SequenceNode:
		for index, child := range node.Content {
			if found := w.walk(child, fmt.Sprintf("%s[%d]", at, index), depth); found.Kind != NoDefect {
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

// scalarDefect is every check that belongs to a NODE rather than to its place
// in the document, applied identically to a value and to a key.
func scalarDefect(node *yaml.Node, at string) Defect {
	switch node.Tag {
	case nullTag:
		return Defect{Kind: NullNode, Path: Where(at)}
	case floatTag:
		return Defect{Kind: FractionalNumber, Path: Where(at), Detail: node.Value}
	}
	return Defect{}
}

// isMergeKey is yaml.v3's own test for a merge key, spelled the way its
// decoder spells it (decode.go, isMerge): a plain `<<` scalar with no tag, the
// non-specific tag, or the merge tag.
func isMergeKey(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.Value == "<<" &&
		(node.Tag == "" || node.Tag == "!" || node.Tag == "!!merge")
}

// Resolve follows an alias to the node it names, bounded, and returns nil for
// a nil node or a chain the bound cuts, which yaml.v3 never produces. It is
// THE way a reader that looks a key or a value up in the tree before typed
// decoding sees what the typed decoder will see: a reader that read a node's
// written text and skipped aliases let `description: &k composed-module` and
// `kind: *k` load as a module of kind composed-module — the written kind was
// an alias, the resolved one the name the dispatch refuses.
func Resolve(node *yaml.Node) *yaml.Node {
	for depth := 0; node != nil && node.Kind == yaml.AliasNode; depth++ {
		if depth >= maxAliasDepth {
			return nil
		}
		node = node.Alias
	}
	return node
}

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
