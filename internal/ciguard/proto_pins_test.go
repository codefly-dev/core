package ciguard

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Masterminds/semver"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"
)

// The external schemas proto/ imports reach this repository twice. buf resolves
// them from the BSR commits in proto/buf.lock when it generates the bindings;
// the Go runtime registers them from the modules go.mod pins when anything
// reads those bindings back. The two pins are independent, and the halves they
// feed are compared — a field option on one of our messages is serialized
// against the first and parsed against the second.
//
// Nothing moves them together. Dependabot's gomod ecosystem moves the go.mod
// side alone, and buf.lock is written only by `buf dep update`, which no check
// runs. They had already drifted apart when this guard was written: buf.lock
// stood at a protovalidate commit from April against a go.mod pin from August.

type bufLockFile struct {
	Deps []struct {
		Remote     string `yaml:"remote"`
		Owner      string `yaml:"owner"`
		Repository string `yaml:"repository"`
		Commit     string `yaml:"commit"`
	} `yaml:"deps"`
}

func loadBufLock(t *testing.T) bufLockFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "proto", "buf.lock"))
	require.NoError(t, err)
	var lock bufLockFile
	require.NoError(t, yaml.Unmarshal(raw, &lock))
	require.NotEmpty(t, lock.Deps, "proto/buf.lock lists no dependencies")
	return lock
}

func bufLockCommit(t *testing.T, owner, repository string) string {
	t.Helper()
	for _, dep := range loadBufLock(t).Deps {
		if dep.Owner == owner && dep.Repository == repository {
			return dep.Commit
		}
	}
	require.FailNow(t, "proto/buf.lock has no dependency on "+owner+"/"+repository)
	return ""
}

func goModRequirement(t *testing.T, path string) string {
	t.Helper()
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	require.NoError(t, err)
	parsed, err := modfile.Parse("go.mod", raw, nil)
	require.NoError(t, err)
	for _, req := range parsed.Require {
		if req.Mod.Path == path {
			return req.Mod.Version
		}
	}
	require.FailNow(t, "go.mod no longer requires "+path)
	return ""
}

// bsrGeneratedSDKCommit reads the BSR commit out of a buf.build generated-SDK
// module version. Those are v<plugin version>-<commit timestamp>-<the first 12
// hex digits of the BSR commit>.<revision>, so the commit in buf.lock — the
// full 32 digits — is identifiable from the Go side and only from here.
var bsrGeneratedSDKCommit = regexp.MustCompile(`-[0-9]{14}-([0-9a-f]{12})\.[0-9]+$`)

const protovalidateGeneratedSDK = "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go"

const protobufGo = "google.golang.org/protobuf"

// Buf and protobuf-go publish the upstream protobuf release that supplies
// their Well-Known Types, but neither version includes that release and neither
// module records the other's version. This table relates release pairs whose
// source metadata names the same upstream release: datawkt.Version in buf and
// protobufVersion in protobuf-go's integration_test.go. Record the first image
// shipping the compiler too: rebuilding an old tag locally can make the image
// test pass while publishing skips that tag and consumers retain the old buf.
var bufProtobufGoVersions = map[string]struct {
	protobufGoVersion   string
	minCompanionVersion string
}{
	"1.73.0": {"v1.36.12", "0.0.15"}, // protobuf 35.1
}

// Buf resolves google/protobuf imports from sources embedded in its binary;
// the Go runtime registers the same files from protobuf-go. A schema that uses
// editions or an option type defined by descriptor.proto is compiled against
// the first copy and read against the second, so the copies must come from the
// same upstream protobuf release.
func TestWellKnownTypePinsAgree(t *testing.T) {
	bufVersion := makefileBufVersion(t)
	protobufGoVersion := goModRequirement(t, protobufGo)

	required, ok := bufProtobufGoVersions[bufVersion]
	require.True(t, ok,
		"buf %s has no verified protobuf-go pairing. Read datawkt.Version in the "+
			"buf release and protobufVersion in protobuf-go's integration_test.go, then "+
			"add the pair only when both name the same upstream protobuf release, and record the new companion image version shipping it.",
		bufVersion)
	require.Equal(t, required.protobufGoVersion, protobufGoVersion,
		"buf %s embeds Well-Known Types paired with %s, but go.mod requires %s. "+
			"Buf compiles google/protobuf imports against the first copy and the Go "+
			"runtime registers the second. Choose releases whose source metadata names "+
			"the same upstream protobuf release, then record that verified pair in "+
			"bufProtobufGoVersions.",
		bufVersion, required.protobufGoVersion, protobufGoVersion)

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "companions", "proto", "info.codefly.yaml"))
	require.NoError(t, err)
	var image struct {
		Version string `yaml:"version"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &image))
	selected, err := semver.NewVersion(image.Version)
	require.NoError(t, err)
	minimum, err := semver.NewVersion(required.minCompanionVersion)
	require.NoError(t, err)
	require.False(t, selected.LessThan(minimum),
		"buf %s first ships in proto companion %s, but consumers still select %s; bump info.codefly.yaml and publish the new image, since existing tags are cached and publishing skips them",
		bufVersion, minimum, selected)
}

// buf.lock and go.mod must name the same protovalidate commit.
//
// go.mod leads, and that is forced rather than chosen: its version rises on its
// own. Dependabot proposes it, and minimum version selection raises it anyway
// whenever buf.build/go/protovalidate or gitlab.com/gitlab-org/api/client-go
// bumps, because both require this same module. A dependabot `ignore` would not
// hold it. buf.lock is the side that can only move deliberately, so it is the
// side that follows.
//
// When they disagree the observable symptom is not a build failure. Both halves
// still compile; what changes is that a descriptor compiled from proto/ stops
// matching the descriptor a .pb.go embeds, in the field options alone, and the
// obvious reading of that — stale bindings, regenerate — is wrong.
func TestProtovalidatePinsAgree(t *testing.T) {
	version := goModRequirement(t, protovalidateGeneratedSDK)
	encoded := bsrGeneratedSDKCommit.FindStringSubmatch(version)
	require.NotNil(t, encoded,
		"go.mod requires %s at %q, which carries no BSR commit. The generated-SDK "+
			"version scheme is what makes the two pins comparable at all; if buf has "+
			"changed it, this guard needs rewriting rather than deleting.",
		protovalidateGeneratedSDK, version)

	commit := bufLockCommit(t, "bufbuild", "protovalidate")
	require.True(t, strings.HasPrefix(commit, encoded[1]),
		"proto/buf.lock pins protovalidate at BSR commit %s while go.mod pins the "+
			"generated SDK built from %s.... buf generates the bindings against the "+
			"first and the Go runtime registers buf/validate/validate.proto from the "+
			"second, so our own field options are written against one schema and read "+
			"against the other. Run `make buf-dep-update`; if that lands buf.lock ahead "+
			"of go.mod, bring go.mod up to it with `go get %s@latest && go mod tidy`.",
		commit, encoded[1], protovalidateGeneratedSDK)
}

// bufDependencyPins records, for every dependency in proto/buf.lock, what keeps
// its BSR commit in step with the Go module carrying the same descriptors.
var bufDependencyPins = map[string]string{
	"bufbuild/protovalidate": "TestProtovalidatePinsAgree holds it equal to the " +
		"generated-SDK version in go.mod",

	// googleapis has no counterpart to hold it against. buf.lock names a BSR
	// snapshot of github.com/googleapis/googleapis; go.mod takes the same
	// descriptors from google.golang.org/genproto, which Google generates from
	// that repository and publishes on its own schedule under its own commit
	// ids. The two sides share an upstream and nothing else — there is no
	// identifier to compare, so an equality assertion cannot be written here.
	//
	// They agree today: buf.build/googleapis/googleapis at the commit in
	// buf.lock and the descriptors genproto registers are byte-identical for
	// all four files proto/ imports (google/api/annotations.proto,
	// google/api/http.proto, google/rpc/code.proto, google/rpc/status.proto),
	// checked by building the BSR module and comparing against
	// protoregistry.GlobalFiles. These are frozen surfaces, so they agree
	// whenever neither publisher's snapshot straddles a change to them — which
	// is a property of what googleapis does, not something this repository
	// controls. If one of them ever does move, the Go side has to become the
	// single source: generation would resolve these imports from the same
	// descriptors the runtime registers rather than from BSR.
	"googleapis/googleapis": "resolved from google.golang.org/genproto on the Go " +
		"side, which shares no commit id with BSR",
}

// A dependency added to proto/buf.lock with no entry above is a third pin
// nothing keeps in step, arriving the same silent way the protovalidate one
// did. The rule is the deliverable here, not the assertion: deciding what holds
// a new schema dependency together is what this map forces.
func TestEveryBufDependencyHasAPinRule(t *testing.T) {
	var deps []string
	for _, dep := range loadBufLock(t).Deps {
		require.Equal(t, "buf.build", dep.Remote,
			"proto/buf.lock depends on %s/%s from remote %q. Only buf.build commits "+
				"carry the generated-SDK version scheme these rules read.",
			dep.Owner, dep.Repository, dep.Remote)
		deps = append(deps, dep.Owner+"/"+dep.Repository)
	}

	var ruled []string
	for name := range bufDependencyPins {
		ruled = append(ruled, name)
	}
	sort.Strings(deps)
	sort.Strings(ruled)

	require.Equal(t, ruled, deps,
		"proto/buf.lock and bufDependencyPins disagree on which external schemas "+
			"this module depends on. Every one of them is pinned a second time by the "+
			"Go module that registers its descriptors, and buf.lock moves only when "+
			"someone runs `buf dep update`. Add the dependency to bufDependencyPins "+
			"with what holds the two pins together — an assertion where the two "+
			"identifiers are comparable, and why they are not where they aren't.")
}
