// Package recoveryscope carries the container-recovery ownership identity from
// the process that projects it to the agents it spawns.
//
// It holds no Docker client on purpose. An agent validates and echoes the
// identity it inherited but never speaks to a daemon, and neither does a test
// binary that links the SDK only to boot its dependencies. Reading the marker
// through the Docker runner put github.com/docker/docker in the build graph of
// every such consumer, carrying advisories that no available bump clears.
package recoveryscope

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// EnvironmentVariable projects the ownership identity to child processes.
const EnvironmentVariable = "CODEFLY_CONTAINER_RECOVERY_SCOPE"

// Header acknowledges the ownership identity inherited by an agent. Older
// agents omit it and cannot promise scoped recovery.
const Header = "codefly-container-recovery-scope"

// markerVersion tags the marker's field layout. Two Core revisions gave the
// field after the exact scope different meanings — a recovery group in one, the
// durable namespace in the other — and a reader that guesses wrong stamps a
// container with ownership no sweep can ever match, leaking exactly what this
// recovery exists to collect. An untagged marker is therefore honored only for
// the one field every revision agreed on.
const markerVersion = "v2"

// Capture the launching parent before serving any requests. A parent dying
// during a request must not revoke the child's already inherited ownership.
// If it died before initialization, validation fails and creation is refused.
var launchingParentPID = os.Getppid()

// LaunchingParentPID is the parent captured at initialization, before any
// request could reparent this process.
func LaunchingParentPID() int { return launchingParentPID }

// Marker formats the projection pid writes for its children. The namespace is
// empty on a host with no durable identity.
func Marker(pid int, id, namespace string) string {
	return strings.Join([]string{strconv.Itoa(pid), markerVersion, id, namespace}, ":")
}

// Inherited validates the marker this process was launched with and returns the
// identity it carries. An absent marker yields an empty identity and no error.
func Inherited() (id string, namespace string, err error) {
	marker := os.Getenv(EnvironmentVariable)
	if marker == "" {
		return "", "", nil
	}
	owner, identity, ok := strings.Cut(marker, ":")
	if !ok {
		return "", "", fmt.Errorf("invalid container recovery marker")
	}
	pid, err := strconv.Atoi(owner)
	if err != nil || pid <= 1 || (pid != os.Getpid() && pid != launchingParentPID) {
		return "", "", fmt.Errorf("container recovery marker does not belong to this process or its launching parent")
	}
	fields := strings.Split(identity, ":")
	if fields[0] != markerVersion {
		// A marker written before the layout was tagged. Only the exact scope
		// had a single agreed meaning across those revisions, so refuse anything
		// trailing it rather than guess whether it is a group or a namespace.
		if len(fields) != 1 {
			return "", "", fmt.Errorf("untagged container recovery marker carries ambiguous fields")
		}
		if err := validDigest(fields[0]); err != nil {
			return "", "", fmt.Errorf("invalid container recovery identity")
		}
		return fields[0], "", nil
	}
	digests := fields[1:]
	if len(digests) != 2 {
		return "", "", fmt.Errorf("container recovery marker has %d fields, want scope and namespace", len(digests))
	}
	if err := validDigest(digests[0]); err != nil {
		return "", "", fmt.Errorf("invalid container recovery identity")
	}
	// The namespace is deliberately optional — a host with no durable identity
	// projects the exact scope alone — but must be a digest when present.
	if digests[1] != "" {
		if err := validDigest(digests[1]); err != nil {
			return "", "", fmt.Errorf("invalid container recovery namespace")
		}
	}
	return digests[0], digests[1], nil
}

// Acknowledgement is the identity an agent echoes over gRPC. It reports the
// complete inherited identity, including an empty namespace when the projecting
// host has no durable one: the caller compares it against what it projected, so
// conflating "agent did not understand" with "cross-scope recovery is
// unavailable here" would report a compatible agent as incompatible.
func Acknowledgement() string {
	id, namespace, err := Inherited()
	if err != nil || id == "" {
		return ""
	}
	return id + ":" + namespace
}

func validDigest(digest string) error {
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("not a sha256 digest")
	}
	return nil
}
