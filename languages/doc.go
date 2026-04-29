// Package languages defines the language and runtime constants codefly
// understands (GO, PYTHON, TYPESCRIPT, RUST, …) and the runtime kinds
// that agents declare as prerequisites.
//
// These constants are surfaced in service.codefly.yaml, drive
// language-specific code generation, and are matched against
// CheckForRuntimes to warn early when a required toolchain is missing.
package languages
