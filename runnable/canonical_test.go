package runnable_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/runnable"
)

// The canonical bytes are a contract, not an implementation detail: `codefly
// generate runnables` writes them to disk and diffs a committed file against a
// freshly derived one. A normalization change has to fail here, where it is one
// reviewed decision, rather than in every consuming repo at once.
func TestCanonicalJSONIsPinned(t *testing.T) {
	golden, err := os.ReadFile("testdata/canonical/runnable-package.json")
	require.NoError(t, err)
	expected := strings.TrimSuffix(string(golden), "\n")

	pkg := preparedPackage(t)
	canonical, err := runnable.CanonicalJSON(pkg)
	require.NoError(t, err)
	require.Equal(t, expected, string(canonical))

	// The bytes a consumer writes are the bytes core hashes: the digest the
	// pinned form carries is sha256 over the format identifier, a separator and
	// the same canonical form of the descriptor without it.
	undigested := proto.Clone(pkg).(*basev0.RunnablePackage)
	undigested.Digest = ""
	hashed, err := runnable.CanonicalJSON(undigested)
	require.NoError(t, err)
	hash := sha256.New()
	hash.Write([]byte(runnable.PackageDigestFormatV1))
	hash.Write([]byte{0})
	hash.Write(hashed)
	require.Equal(t, pkg.GetDigest(), hex.EncodeToString(hash.Sum(nil)))
}

func TestCanonicalJSONRejectsUnknownFields(t *testing.T) {
	pkg := preparedPackage(t)
	pkg.GetBuild().ProtoReflect().SetUnknown([]byte{0xf8, 0x7f, 0x01})
	_, err := runnable.CanonicalJSON(pkg)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "outside its declared schema")
}
