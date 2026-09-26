package resources

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"
)

// marshalKeepingComments renders value in the canonical form yaml.Marshal
// produces, then carries the comments of the file it replaces onto it.
//
// The canonical rendering stays the source of truth for content: every key and
// value comes from value, so a field the caller removed is gone and a field it
// added is present. What is carried over from existing is only what an operator
// wrote around that content and that the resource model cannot hold: head,
// line and foot comments, the order of mapping keys, and the quoting style of a
// scalar whose value did not change.
//
// When existing is empty, is not valid YAML, or carries no comment at all, the
// result is exactly yaml.Marshal(value): a file without comments is written
// byte for byte as it always was.
func marshalKeepingComments(existing []byte, value any) ([]byte, error) {
	canonical, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(existing)) == 0 {
		return canonical, nil
	}
	var previous yaml.Node
	if err := yaml.Unmarshal(existing, &previous); err != nil || !hasYAMLComments(&previous) {
		return canonical, nil
	}
	var next yaml.Node
	if err := yaml.Unmarshal(canonical, &next); err != nil {
		return nil, err
	}
	carryYAMLComments(&previous, &next)
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	// yaml.Marshal's indentation, so the carried file keeps core's style.
	encoder.SetIndent(4)
	if err := encoder.Encode(&next); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func hasYAMLComments(node *yaml.Node) bool {
	if node.HeadComment != "" || node.LineComment != "" || node.FootComment != "" {
		return true
	}
	for _, child := range node.Content {
		if hasYAMLComments(child) {
			return true
		}
	}
	return false
}

// carryYAMLComments copies the comments of from onto to, where the two nodes
// describe the same place in the document. A comment already on to (one a
// YAMLValue extension kept through the load) wins over the one on disk.
func carryYAMLComments(from, to *yaml.Node) {
	if from == nil || to == nil {
		return
	}
	if from.Kind == yaml.AliasNode && from.Alias != nil {
		from = from.Alias
	}
	carryNodeComments(from, to)
	if from.Kind != to.Kind {
		return
	}
	switch to.Kind {
	case yaml.DocumentNode:
		if len(from.Content) > 0 && len(to.Content) > 0 {
			carryYAMLComments(from.Content[0], to.Content[0])
		}
	case yaml.MappingNode:
		carryMappingComments(from, to)
	case yaml.SequenceNode:
		carrySequenceComments(from, to)
	case yaml.ScalarNode:
		if from.Value == to.Value && from.Tag == to.Tag {
			to.Style = from.Style
		}
	}
}

func carryNodeComments(from, to *yaml.Node) {
	if to.HeadComment == "" {
		to.HeadComment = from.HeadComment
	}
	if to.LineComment == "" {
		to.LineComment = from.LineComment
	}
	if to.FootComment == "" {
		to.FootComment = from.FootComment
	}
}

type yamlPair struct {
	key, value *yaml.Node
}

func mappingPairs(node *yaml.Node) []yamlPair {
	pairs := make([]yamlPair, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		pairs = append(pairs, yamlPair{key: node.Content[i], value: node.Content[i+1]})
	}
	return pairs
}

// carryMappingComments matches pairs by key. Keys present on both sides keep
// the order they had on disk; a key only in the new rendering is placed after
// the key that precedes it there. A key that was removed takes its own head and
// line comments with it, but its foot comment — which yaml.v3 also uses for a
// comment block trailing the whole mapping — moves to the surviving key before
// it, so a closing comment is not lost with the last entry.
func carryMappingComments(from, to *yaml.Node) {
	old := mappingPairs(from)
	fresh := mappingPairs(to)
	freshByKey := make(map[string]yamlPair, len(fresh))
	for _, pair := range fresh {
		freshByKey[pair.key.Value] = pair
	}
	oldKeys := make(map[string]bool, len(old))
	for _, pair := range old {
		oldKeys[pair.key.Value] = true
	}

	var ordered []yamlPair
	var orphanedFoot []string
	for _, pair := range old {
		match, ok := freshByKey[pair.key.Value]
		if !ok {
			if pair.key.FootComment != "" {
				orphanedFoot = append(orphanedFoot, pair.key.FootComment)
			}
			if pair.value.FootComment != "" {
				orphanedFoot = append(orphanedFoot, pair.value.FootComment)
			}
			continue
		}
		carryYAMLComments(pair.key, match.key)
		carryYAMLComments(pair.value, match.value)
		if len(orphanedFoot) > 0 {
			// A removed key's head/line comments go with it; its foot comments
			// attach to what now precedes them, or head what follows.
			if len(ordered) > 0 {
				appendFoot(ordered[len(ordered)-1].key, orphanedFoot)
			} else {
				match.key.HeadComment = joinComments(append(orphanedFoot, match.key.HeadComment))
			}
			orphanedFoot = nil
		}
		ordered = append(ordered, match)
	}
	if len(orphanedFoot) > 0 {
		if len(ordered) > 0 {
			appendFoot(ordered[len(ordered)-1].key, orphanedFoot)
		} else {
			to.FootComment = joinComments(append([]string{to.FootComment}, orphanedFoot...))
		}
	}

	// Insert the keys the file did not have, each after its predecessor in the
	// canonical rendering.
	for i, pair := range fresh {
		if oldKeys[pair.key.Value] {
			continue
		}
		at := 0
		if i > 0 {
			predecessor := fresh[i-1].key.Value
			for j, placed := range ordered {
				if placed.key.Value == predecessor {
					at = j + 1
					break
				}
			}
		}
		ordered = append(ordered, yamlPair{})
		copy(ordered[at+1:], ordered[at:])
		ordered[at] = pair
	}

	to.Content = to.Content[:0]
	for _, pair := range ordered {
		to.Content = append(to.Content, pair.key, pair.value)
	}
}

func appendFoot(node *yaml.Node, comments []string) {
	node.FootComment = joinComments(append([]string{node.FootComment}, comments...))
}

func joinComments(comments []string) string {
	var kept []string
	for _, comment := range comments {
		if comment != "" {
			kept = append(kept, comment)
		}
	}
	return strings.Join(kept, "\n")
}

// carrySequenceComments keeps the canonical order of a sequence — it is part
// of the content — and matches items by identity: a mapping by its `name`, a
// scalar by its value, and anything else by position.
func carrySequenceComments(from, to *yaml.Node) {
	used := make([]bool, len(from.Content))
	for i, item := range to.Content {
		match := -1
		if id, ok := sequenceItemIdentity(item); ok {
			for j, candidate := range from.Content {
				if used[j] {
					continue
				}
				if cid, ok := sequenceItemIdentity(candidate); ok && cid == id {
					match = j
					break
				}
			}
		} else if i < len(from.Content) && !used[i] {
			if _, ok := sequenceItemIdentity(from.Content[i]); !ok {
				match = i
			}
		}
		if match >= 0 {
			used[match] = true
			carryYAMLComments(from.Content[match], item)
		}
	}
}

func sequenceItemIdentity(node *yaml.Node) (string, bool) {
	switch node.Kind {
	case yaml.ScalarNode:
		return "scalar:" + node.Value, true
	case yaml.MappingNode:
		for _, pair := range mappingPairs(node) {
			if pair.key.Value == "name" && pair.value.Kind == yaml.ScalarNode {
				return "name:" + pair.value.Value, true
			}
		}
	}
	return "", false
}
