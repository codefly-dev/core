// Package testselection validates and acknowledges language-neutral test
// selections at the Codefly runtime boundary.
//
// Runtime plugins translate a validated selection to their native runner. The
// caller never supplies a command, regex, or framework-specific selector
// grammar. A scoped response must acknowledge the exact request so clients can
// fail closed when connected to a peer that does not implement typed
// selection.
package testselection
