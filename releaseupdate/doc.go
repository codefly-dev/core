// Package releaseupdate discovers and applies verified releases for binaries
// that a caller explicitly identifies as directly owned.
//
// Discovery is read-only with respect to an installation. It projects GitHub
// releases into provider-neutral types, applies stable or beta channel policy,
// selects a named GoReleaser-compatible OS/architecture asset, and persists
// HTTP validators and release metadata only through a caller-supplied Store.
//
// Direct updates require a pinned publisher certificate, a signed checksum
// manifest, and a matching artifact checksum. Downloads and decompression are
// bounded before a verified executable can be staged. Apply uses a private,
// no-replace filesystem transaction with rollback.
//
// Callers retain ownership of check cadence, cache persistence, install-source
// detection, user consent and rendering, package-manager and application-bundle
// behavior, daemon supervision, restart/readiness policy, and telemetry. In
// particular, this package never infers mutation authority from the running
// executable path and never stops or restarts a service.
package releaseupdate
