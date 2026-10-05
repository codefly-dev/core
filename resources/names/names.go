// Package names holds the name grammars core's models share, so a reader in
// one package and a validator in another cannot disagree about what a name is.
// It imports nothing: a wire model that must not link core's resource tree can
// still hold a value to the same grammar the resource model holds it to.
package names

import (
	"regexp"
	"strings"
)

// AllowAllModules is the allow-list wildcard entry that grants every module
// access to an internal endpoint.
const AllowAllModules = "*"

// modulePattern is what a module name may be, as the schema spells one.
var modulePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// IsModule reports whether value is a module name: lowercase alphanumerics and
// single dashes. A doubled dash is refused, so one name has one spelling.
func IsModule(value string) bool {
	return modulePattern.MatchString(value) && !strings.Contains(value, "--")
}

// IsAllowModulesEntry reports whether value is a legal allow-list entry: a
// module name, or the wildcard that names every module. An entry that names
// no module would make a list non-empty while granting nobody, which is the
// refusal an "exports to no module" check exists to raise.
func IsAllowModulesEntry(value string) bool {
	return value == AllowAllModules || IsModule(value)
}
