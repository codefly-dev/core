package responsepolicy

import (
	"strconv"

	"github.com/codefly-dev/core/provider/manifest"
)

// match is one concrete value a selector resolved to. Path is the concrete
// selector with every wildcard replaced by the matched index, so two elements
// of the same array never collide on one selector string.
type match struct {
	path  string
	value any
}

// evaluate resolves selector against the value tree. A selector that matches
// nothing returns an empty slice rather than an error: presence is a policy
// decision the caller makes, not a parse failure.
func evaluate(selector manifest.Selector, root any) ([]match, error) {
	tokens, err := manifest.ParseSelector(selector)
	if err != nil {
		return nil, err
	}
	return walk(tokens, root, "$"), nil
}

func walk(tokens []manifest.SelectorToken, value any, path string) []match {
	if len(tokens) == 0 {
		return []match{{path: path, value: value}}
	}
	token := tokens[0]
	rest := tokens[1:]
	switch token.Kind {
	case manifest.SelectorObjectKey:
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		child, present := object[token.Key]
		if !present {
			return nil
		}
		return walk(rest, child, path+"."+token.Key)
	case manifest.SelectorExactIndex:
		array, ok := value.([]any)
		if !ok || token.Index >= uint64(len(array)) {
			return nil
		}
		return walk(rest, array[token.Index], path+"["+strconv.FormatUint(token.Index, 10)+"]")
	case manifest.SelectorArrayWildcard:
		array, ok := value.([]any)
		if !ok {
			return nil
		}
		var matches []match
		for i, item := range array {
			matches = append(matches, walk(rest, item, path+"["+strconv.Itoa(i)+"]")...)
		}
		return matches
	default:
		return nil
	}
}
