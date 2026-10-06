// Package names holds the name grammars core's models share, so a reader in
// one package and a validator in another cannot disagree about what a name is.
// It imports nothing: a wire model that must not link core's resource tree can
// still hold a value to the same grammar the resource model holds it to.
package names

import (
	"regexp"
	"strings"
)

// modulePattern is what a module name may be, as the schema spells one.
var modulePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// IsModule reports whether value is a module name: lowercase alphanumerics and
// single dashes. A doubled dash is refused, so one name has one spelling.
func IsModule(value string) bool {
	return modulePattern.MatchString(value) && !strings.Contains(value, "--")
}
