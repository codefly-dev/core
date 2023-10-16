package shared

import "strings"

func HandleError(err error) {
	if err != nil {
		panic(err)
	}
}

func ParseError(s string) error {
	if msg, ok := strings.CutPrefix(s, "OutError: "); ok {
		return NewOutputError(msg)
	}
	panic("not implemented")
}
