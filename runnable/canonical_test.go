package runnable_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runnable"
)

const (
	fixtureDigestA = "sha256:" + "a1" + "00000000000000000000000000000000000000000000000000000000000000"
	fixtureDigestB = "sha256:" + "b2" + "00000000000000000000000000000000000000000000000000000000000000"
	fixtureDigestC = "sha256:" + "c3" + "00000000000000000000000000000000000000000000000000000000000000"
)

// canonicalFixture is built here rather than from a shared workspace fixture on
// purpose: the golden below must move when the normalization moves and at no
// other time. Reading testdata another package owns would let an edit there
// fail this test too, and regenerating the golden is the right answer to that
// and the catastrophic answer to a normalization change.
//
// The reference and one command argument carry `&`, `<` and `>` so the golden
// pins how they are emitted. Go's encoding/json would escape them to \uXXXX,
// which nothing outside Go reproduces.
func canonicalFixture() *basev0.RunnablePackage {
	return &basev0.RunnablePackage{
		Schema:   runnable.PackageSchemaV1,
		Identity: &basev0.RunnableIdentity{Name: "word-count", Module: "with-runnables", Workspace: "qualification", Version: "0.1.0"},
		Agent:    &basev0.Agent{Kind: basev0.Agent_RUNNABLE, Name: "python", Publisher: "codefly.dev", Version: "0.0.1"},
		Contract: &basev0.RunnableContract{
			Protocol: runnable.ProtocolV1,
			Input: &basev0.RunnableSchema{Fields: []*basev0.RunnableField{
				{Name: "text", Type: basev0.RunnableField_STRING},
				{Name: "options", Type: basev0.RunnableField_OBJECT, Optional: true, Fields: []*basev0.RunnableField{
					{Name: "case_sensitive", Type: basev0.RunnableField_BOOLEAN},
					{Name: "stop_words", Type: basev0.RunnableField_ARRAY, Nullable: true, Items: &basev0.RunnableField{Type: basev0.RunnableField_STRING}},
				}},
			}},
			Output: &basev0.RunnableSchema{Fields: []*basev0.RunnableField{
				{Name: "count", Type: basev0.RunnableField_INTEGER},
				{Name: "longest", Type: basev0.RunnableField_STRING, Nullable: true},
			}},
		},
		Execution: &basev0.RunnableExecution{
			Facilities: []*basev0.RunnableFacility{
				{Kind: basev0.RunnableFacility_KUBERNETES},
				{Kind: basev0.RunnableFacility_NATIVE},
			},
			Timeout:        durationpb.New(2 * time.Minute),
			Cancellation:   basev0.RunnableExecution_CANCELLATION_SIGNAL,
			Recovery:       basev0.RunnableExecution_RECOVERY_RECOMPUTE,
			MaxInputBytes:  65536,
			MaxOutputBytes: resources.DefaultRunnablePayloadBytes,
			MaxLogBytes:    resources.DefaultRunnableLogBytes,
		},
		Build: &basev0.RunnableBuild{
			Handler: &basev0.RunnableInputDigest{Path: "handler.py", Digest: fixtureDigestA},
			Inputs: []*basev0.RunnableInputDigest{
				{Path: "uv.lock", Digest: fixtureDigestB},
				{Path: "pyproject.toml", Digest: fixtureDigestC},
			},
			HarnessDigest:       fixtureDigestA,
			Toolchain:           "python-3.12.4",
			ConfigurationDigest: fixtureDigestB,
		},
		Artifacts: []*basev0.RunnableArtifact{
			{
				Kind:      basev0.RunnableArtifact_IMAGE,
				Platform:  "linux/amd64",
				Reference: "ghcr.io/example/word-count:0.1.0?tag=a&arch=<amd64>@" + fixtureDigestB,
				Digest:    fixtureDigestB,
			},
			{
				Kind:      basev0.RunnableArtifact_NATIVE,
				Platform:  "darwin/arm64",
				Reference: "word-count-0.1.0-darwin-arm64.tar.gz",
				Digest:    fixtureDigestA,
				Command:   []string{"sh", "-c", "prepare && exec python -m codefly_runnable.harness"},
			},
		},
		ServiceDependencies:                []*basev0.RunnableDependency{{Name: "store", Module: "with-runnables", Kind: "runtime", Endpoints: []string{"tcp"}}},
		WorkspaceConfigurationDependencies: []string{"openai", "artifact-store"},
	}
}

func preparedCanonicalFixture(t *testing.T) *basev0.RunnablePackage {
	t.Helper()
	prepared, err := runnable.PreparePackage(canonicalFixture())
	require.NoError(t, err)
	return prepared
}

// The canonical bytes are a contract, not an implementation detail: `codefly
// generate runnables` writes them to disk and diffs a committed file against a
// freshly derived one. A normalization change has to fail here, where it is one
// reviewed decision, rather than in every consuming repo at once.
//
// The golden file holds the canonical bytes exactly, with no trailing newline,
// so it is the contract rather than a rendering of it.
func TestCanonicalJSONIsPinned(t *testing.T) {
	golden, err := os.ReadFile("testdata/canonical/runnable-package.json")
	require.NoError(t, err)

	prepared := preparedCanonicalFixture(t)
	canonical, err := runnable.CanonicalJSON(prepared)
	require.NoError(t, err)
	require.Equal(t, string(golden), string(canonical))

	// `&` and `<` stay themselves; Go's HTML-escaping default is off.
	require.Contains(t, string(canonical), "?tag=a&arch=<amd64>")
	require.Contains(t, string(canonical), "prepare && exec python")
	// Go's default encoder escapes exactly this reference; the canonical form
	// must carry it verbatim instead.
	reference := prepared.GetArtifacts()[1].GetReference()
	htmlEscaped, err := json.Marshal(reference)
	require.NoError(t, err)
	require.NotEqual(t, strconv.Quote(reference), string(htmlEscaped))
	require.Contains(t, string(canonical), reference)

	// The bytes a consumer writes are the bytes core hashes: the digest the
	// pinned form carries is sha256 over the format identifier, a separator and
	// the same canonical form of the descriptor without it.
	undigested := proto.Clone(prepared).(*basev0.RunnablePackage)
	undigested.Digest = ""
	hashed, err := runnable.CanonicalJSON(undigested)
	require.NoError(t, err)
	hash := sha256.New()
	hash.Write([]byte(runnable.PackageDigestFormatV1))
	hash.Write([]byte{0})
	hash.Write(hashed)
	require.Equal(t, prepared.GetDigest(), hex.EncodeToString(hash.Sum(nil)))
}

// CanonicalJSON normalizes the encoding, not the message, so a descriptor that
// has not been through PreparePackage canonicalizes to stable bytes of an
// uncanonical descriptor. A consumer that wrote those would diff them against
// themselves forever while core registered a different digest.
func TestCanonicalJSONDoesNotCanonicalizeTheMessage(t *testing.T) {
	prepared := preparedCanonicalFixture(t)
	prepared.Digest = ""
	fromPrepared, err := runnable.CanonicalJSON(prepared)
	require.NoError(t, err)

	raw := canonicalFixture()
	fromRaw, err := runnable.CanonicalJSON(raw)
	require.NoError(t, err)

	require.NotEqual(t, string(fromPrepared), string(fromRaw))
	require.Equal(t, basev0.RunnableArtifact_NATIVE, prepared.GetArtifacts()[0].GetKind())
	require.Equal(t, basev0.RunnableArtifact_IMAGE, raw.GetArtifacts()[0].GetKind())
}

// A typed nil is the dangerous absent message: it canonicalizes to "{}" with no
// error, and a consumer writing that gets a file every later check agrees with.
func TestCanonicalJSONRejectsAbsentMessage(t *testing.T) {
	var absent *basev0.RunnablePackage
	_, err := runnable.CanonicalJSON(absent)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "message is required")

	_, err = runnable.CanonicalJSON(nil)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "message is required")

	// An empty message is not an absent one and still canonicalizes.
	empty, err := runnable.CanonicalJSON(&basev0.RunnablePackage{})
	require.NoError(t, err)
	require.Equal(t, "{}", string(empty))
}

func TestCanonicalJSONRejectsUnknownFields(t *testing.T) {
	pkg := preparedCanonicalFixture(t)
	pkg.GetBuild().ProtoReflect().SetUnknown([]byte{0xf8, 0x7f, 0x01})
	_, err := runnable.CanonicalJSON(pkg)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "outside its declared schema")
}
