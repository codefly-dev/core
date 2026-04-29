// Package shared collects small, dependency-free utilities used across
// the codefly stack: case conversions, file & path helpers, the Must
// pattern, generics helpers, and concurrency-safe writers used in tests
// and runners.
//
// Nothing in here imports other codefly packages — it sits at the bottom
// of the import graph so any package can depend on it.
package shared
