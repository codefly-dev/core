package manifest

import (
	"fmt"
	"strconv"
	"unicode"
)

type SelectorTokenKind string

const (
	SelectorObjectKey     SelectorTokenKind = "object-key"
	SelectorExactIndex    SelectorTokenKind = "exact-index"
	SelectorArrayWildcard SelectorTokenKind = "array-wildcard"
)

type SelectorToken struct {
	Kind  SelectorTokenKind
	Key   string
	Index uint64
}

func (s Selector) Validate() error {
	_, err := ParseSelector(s)
	return err
}

func ParseSelector(selector Selector) ([]SelectorToken, error) {
	if selector.Version != SelectorVersionV1 {
		return nil, fmt.Errorf("unsupported selector version %q", selector.Version)
	}
	if selector.Path == "" || selector.Path[0] != '$' {
		return nil, fmt.Errorf("selector must start with $")
	}
	if selector.Path == "$" {
		return nil, nil
	}
	var tokens []SelectorToken
	for offset := 1; offset < len(selector.Path); {
		switch selector.Path[offset] {
		case '.':
			offset++
			if offset == len(selector.Path) || selector.Path[offset] == '.' || selector.Path[offset] == '*' {
				return nil, fmt.Errorf("selector contains unsupported object wildcard or recursive descent")
			}
			start := offset
			for offset < len(selector.Path) && selectorKeyRune(rune(selector.Path[offset]), offset == start) {
				offset++
			}
			if start == offset {
				return nil, fmt.Errorf("selector contains an invalid object key")
			}
			tokens = append(tokens, SelectorToken{Kind: SelectorObjectKey, Key: selector.Path[start:offset]})
		case '[':
			end := offset + 1
			for end < len(selector.Path) && selector.Path[end] != ']' {
				end++
			}
			if end == len(selector.Path) {
				return nil, fmt.Errorf("selector contains an unterminated index")
			}
			value := selector.Path[offset+1 : end]
			if value == "*" {
				tokens = append(tokens, SelectorToken{Kind: SelectorArrayWildcard})
			} else {
				if value == "" || (len(value) > 1 && value[0] == '0') {
					return nil, fmt.Errorf("selector contains a non-canonical exact index")
				}
				index, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("selector contains an unsupported index %q", value)
				}
				tokens = append(tokens, SelectorToken{Kind: SelectorExactIndex, Index: index})
			}
			offset = end + 1
		default:
			return nil, fmt.Errorf("selector contains unsupported syntax at byte %d", offset)
		}
	}
	return tokens, nil
}

func selectorKeyRune(r rune, first bool) bool {
	if first {
		return unicode.IsLetter(r) || r == '_'
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}
