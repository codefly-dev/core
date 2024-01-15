package configurations

import "strings"

func Keying(s string) string {
	return strings.ToUpper(s)
}

func IdentifierKey(identifier string, app string, svc string) string {
	return "CODEFLY_" + identifier + "__" + Keying(app) + "__" + Keying(svc)
}
