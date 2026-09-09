// Package version exposes the current codefly build version and is the
// single source of truth used by both the CLI (--version) and the
// embedded agents.
//
// info.codefly.yaml is embedded (see version.go) and is authoritative in
// EVERY build, releases included — there is no -ldflags override. The value
// is a hard gate, not a label: composition/contracts.go checks it against
// each package's minimum-codefly-version, so a file that lags the published
// tag makes the tool reject packages it can actually run.
//
// Because of that, the file and the git tag must agree. scripts/check_version_tag.sh
// fails CI when the file falls behind the tags, and .github/workflows/version-tag.yml
// cuts the tag FROM this file once CI is green — so bumping this file is how a
// release is made, the same way companions/<name>/info.codefly.yaml drives a
// companion image publish.
package version
