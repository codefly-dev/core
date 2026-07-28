# Verified release discovery and direct updates

The `releaseupdate` package is a product-neutral boundary for Codefly binaries.
It separates read-only release discovery from the narrower authority to replace
one explicitly owned direct binary.

## Architecture

Discovery starts with a `Request` containing the current semantic version,
stable or beta channel, target platform, GoReleaser artifact name,
caller-detected install kind, and an explicit downgrade policy.
`GitHubChecker.Check`:

1. loads provider-neutral releases and HTTP validators through the caller's
   `Store`;
2. sends `If-None-Match` and `If-Modified-Since` when available;
3. follows the complete bounded GitHub release collection and maps it into
   `Release` and `Asset` values using authenticated release-asset API URLs;
4. orders compatible releases semantically, then delegates GoReleaser
   OS/architecture matching within each release to `go-selfupdate`, including
   the documented Darwin `all` fallback; and
5. returns a `Status` without reading or changing an installed executable.

A `304 Not Modified` result is recomputed against the request's current
version. Primary and secondary GitHub `403` or `429` rate limits return the last
successful status with `Status.Stale` set and a typed `RateLimitError`, so
callers retain useful state without mistaking it for a fresh success. Fresh
metadata is committed to the caller's store only after selection succeeds.

The cache contract stores release metadata, `ETag`, and `Last-Modified`.
Persistence location, format, locking, retention, and privacy policy belong to
the caller.

## Direct installation

`NewInstaller` requires:

- a pinned ECDSA publisher certificate;
- the exact signed checksum-manifest filename;
- an exact allowlist of HTTPS asset-API and redirect hosts; and
- optional artifact, metadata, expanded-archive, and executable size bounds.

`StageAndVerify` accepts the typed `Status` returned by discovery and only
permits an available `InstallKindDirect` release. It revalidates stable/beta and
explicit downgrade policy, rejecting Homebrew, Tauri, application-bundle,
managed, and unknown ownership before issuing a download. The operation
verifies the pinned signature over the checksum manifest before downloading the
artifact, then verifies the artifact entry from that manifest. It rejects
unsafe archive paths and non-regular entries, bounds both archive expansion and
the selected executable, and checks ELF or Mach-O architecture before returning
a mode-`0600` `StagedUpdate`.

`StagedUpdate.Apply` rechecks that both the destination identity and staged
bytes are unchanged. It writes an exact-mode replacement inside a unique
private transaction directory, captures and verifies the prior executable,
installs with a no-replace hard link, and restores the prior executable when
installation fails. `ApplyResult` distinguishes a committed replacement from a
failed transaction and identifies any prior executable or transaction directory
that still needs cleanup.

A checksum beside an artifact is not treated as publisher authentication. The
checksum manifest must first verify against the pinned publisher certificate.
HTTP errors omit request URLs, and the discovery adapter redacts URLs before
calling upstream selection, so bearer tokens and signed query parameters are
not included in returned errors or upstream logs.

## Caller authority

Core does not decide:

- whether or when to check;
- where cache data is persisted;
- how install ownership is detected;
- whether a user consents to an update;
- how status or errors are rendered;
- how Homebrew, Tauri, application bundles, or managed deployments update;
- whether a daemon is stopped, restarted, readiness-gated, or rolled back; or
- what telemetry is recorded.

Resolving the running executable is not proof of ownership. Callers must supply
an absolute destination and `InstallKindDirect`; the package never derives
either from `os.Executable`.

Production integration is tracked in the owning repositories by
`codefly-dev/cli#147` and `codefly-dev/mind#211`.

## Upstream dependency decision

The package wraps `github.com/creativeprojects/go-selfupdate` v1.6.0 rather
than reproducing its GoReleaser naming, extraction, and signature/checksum
validation mechanics. Core retains semantic ordering and the filesystem
transaction because those contracts require product identity, cross-process
isolation, exact permissions, and typed post-commit results that the upstream
high-level update path does not expose. The root upstream package includes
GitHub, Gitea, and GitLab providers, so its impact was measured before
acceptance.

On 2026-07-28, using Go 1.26.5 on Darwin/arm64, two minimal commands were built
with `-trimpath -ldflags='-s -w'`. The baseline command contained an empty
`main`; the second called `selfupdate.NewUpdater(selfupdate.Config{})`.

| Probe | Stripped bytes | Non-standard compiled packages |
| --- | ---: | ---: |
| Baseline | 1,161,906 | 1 |
| Import `go-selfupdate` | 7,127,874 | 45 |
| Delta | 5,965,968 | 44 |

Adding v1.6.0 introduced the Gitea and GitLab clients, the v86 GitHub client,
`semver/v3`, `xz`, and their supporting modules to Core's module graph. The
Core-package option was retained because it is the shared contract established
by this repository, keeps downstream product behavior on one maintained
mechanism, and does not link into binaries that do not import
`releaseupdate`. The binary cost remains visible here so a later module split
can be evaluated from recorded evidence instead of assumption.

`govulncheck` also reports `GO-2026-5932` because the monolithic upstream
package imports the unmaintained `golang.org/x/crypto/openpgp` implementation.
There is no fixed version. This package configures only the upstream ECDSA
validator and has no PGP call path, but the package-initialization finding and
upstream risk remain visible rather than being represented as fixed.

## Network qualification

`releaseupdate/testdata/goreleaser-releases.json` is a trimmed recording of
immutable GitHub release IDs `360083337` (`v2.17.1`) and `360063936`
(`v2.18.0-83f4c19a-nightly`) from `goreleaser/goreleaser`, captured through the
GitHub API on 2026-07-28. Tests replay the real tags, release flags, asset IDs,
names, sizes, authenticated asset API URLs, and immutable browser-download URLs
through the production HTTP decoder. The recording covers current/latest
comparison, stable prerelease exclusion, all four required Darwin/Linux
architectures, x86_64 aliases, and the upstream-documented universal-mac
fallback without making CI depend on a mutable network.
