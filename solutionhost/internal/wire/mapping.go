package wire

import "gopkg.in/yaml.v3"

// Mapping is what reading one keyed mapping produced: the first unknown field
// it carried, and the first value that would not decode.
type Mapping struct {
	// Unknown is the first key the model does not declare, if any.
	Unknown string
	// HasUnknown distinguishes an absent unknown field from one spelled "".
	HasUnknown bool
	// Err is the first value that would not decode into the field it names.
	Err error
}

// ReadMapping is THE path a custom decoder in this directory reads a mapping
// by. A type with its own UnmarshalYAML still gets the strictness the typed
// decoder would have given it, because the strictness is here and not in the
// decoder.
//
// Each of these was fixed separately in a different custom decoder, one review
// round apart, which is the argument for there being one path:
//
//   - The KEY is decoded with its type, never read off Node.Value. Reading
//     Value took an alias's ANCHOR NAME for the field name, so `{*from : x}`
//     beside `&from assistant` was read as the field "from" while the
//     document's key is "assistant" — core interpreting a different mapping
//     than the document expresses.
//   - An unknown field is REPORTED rather than ignored, because yaml's
//     KnownFields does not reach a type that decodes itself.
//   - A value that will not decode is kept as an error rather than dropped,
//     since a custom decoder returning nil makes a malformed value look like
//     an absent one.
//
// assign is called for each known key with its value node, and reports whether
// it accepted the key at all.
func ReadMapping(node *yaml.Node, assign func(key string, value *yaml.Node) (known bool, err error)) Mapping {
	var mapping Mapping
	if node.Kind != yaml.MappingNode {
		return mapping
	}
	seen := make(map[string]bool, len(node.Content)/2)
	for index := 0; index+1 < len(node.Content); index += 2 {
		var key string
		if err := node.Content[index].Decode(&key); err != nil {
			if mapping.Err == nil {
				mapping.Err = err
			}
			continue
		}
		if seen[key] {
			// One key per mapping is a DOCUMENT property, reported by Check
			// over every mapping including the dynamic ones a model never
			// names. Recording it again here would refuse it twice, with two
			// messages.
			continue
		}
		seen[key] = true
		known, err := assign(key, node.Content[index+1])
		if err != nil && mapping.Err == nil {
			mapping.Err = err
		}
		if !known && !mapping.HasUnknown {
			mapping.Unknown, mapping.HasUnknown = key, true
		}
	}
	return mapping
}
