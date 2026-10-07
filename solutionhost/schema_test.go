package solutionhost_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

// "No secret value is ever embedded; the document names identities, never
// credentials" is a property of the schema, not of any one document, so it is
// held here rather than remembered. The day someone adds `token:` to make a
// host boot, this fails instead of shipping a delivery repository full of
// credentials in git.
func TestTheSchemaHasNoFieldACredentialCouldBeWrittenInto(t *testing.T) {
	forbidden := []string{"secret", "token", "password", "passphrase", "credential", "privatekey", "apikey", "certificate", "bearer"}

	var walk func(t *testing.T, value reflect.Type, path string, seen map[reflect.Type]bool)
	walk = func(t *testing.T, value reflect.Type, path string, seen map[reflect.Type]bool) {
		for value.Kind() == reflect.Pointer || value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
			value = value.Elem()
		}
		if value.Kind() != reflect.Struct || seen[value] {
			return
		}
		seen[value] = true
		for index := range value.NumField() {
			field := value.Field(index)
			name := strings.ToLower(field.Name)
			for _, word := range forbidden {
				require.NotContainsf(t, name, word, "%s.%s: this document names identities, never credentials", path, field.Name)
			}
			walk(t, field.Type, path+"."+field.Name, seen)
		}
	}
	walk(t, reflect.TypeOf(solutionhost.SolutionHostBinding{}), "SolutionHostBinding", map[reflect.Type]bool{})
}

// Every field is serialized under both tags, so the document a host reads as
// YAML and the canonical bytes it digests describe the same record.
func TestEveryFieldIsTaggedForBothEncodings(t *testing.T) {
	var walk func(t *testing.T, value reflect.Type, path string, seen map[reflect.Type]bool)
	walk = func(t *testing.T, value reflect.Type, path string, seen map[reflect.Type]bool) {
		for value.Kind() == reflect.Pointer || value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
			value = value.Elem()
		}
		if value.Kind() != reflect.Struct || seen[value] {
			return
		}
		seen[value] = true
		for index := range value.NumField() {
			field := value.Field(index)
			for _, tag := range []string{"yaml", "json"} {
				name, _, _ := strings.Cut(field.Tag.Get(tag), ",")
				require.NotEmptyf(t, name, "%s.%s has no %s tag", path, field.Name, tag)
				require.Equalf(t, strings.ToLower(name), name, "%s.%s %s tag is not lowercase", path, field.Name, tag)
			}
			walk(t, field.Type, path+"."+field.Name, seen)
		}
	}
	walk(t, reflect.TypeOf(solutionhost.SolutionHostBinding{}), "SolutionHostBinding", map[reflect.Type]bool{})
}

// v1 is the only schema this Core reads, and the constant is the contract three
// repositories pin to. Changing it is a version step, never an edit.
func TestSchemaConstantIsTheVersionedName(t *testing.T) {
	require.Equal(t, "codefly/solution-host-binding/v2", solutionhost.SchemaPresenceV2)
	require.Equal(t, "codefly/solution-authority/v1", solutionhost.SchemaAuthorityV1)
	require.Equal(t, "codefly/solution-host-signed/v1", solutionhost.SchemaSignedV1)
	require.Equal(t, "solution-host-binding.codefly.yaml", solutionhost.FileName)
	require.Equal(t, "solution-authority.codefly.yaml", solutionhost.AuthorityFileName)
}

// The shipped fixture bytes are pinned by one digest, so no fixture changes
// without someone deliberately re-pinning it.
//
// This exists because the fixtures leaked a real deployment coordinate, a
// customer registry path and a product principal name, and nothing noticed.
// They are shipped to other repositories and quoted in documentation, so a name
// in one travels much further than a name in a comment.
//
// Two designs were tried and discarded before this one, and the reasons are
// worth recording because both are tempting:
//
//   - A DENYLIST of real names made the test enforcing neutrality the largest
//     concentration of real names in the repository, and needed a
//     self-exemption so it would not fail itself. The self-exemption was the
//     tell.
//   - An ALLOWLIST of neutral vocabulary cannot cover the explanatory prose each
//     fixture carries, so it either constrains English to forty words or stops
//     reading the comments — where a name leaks just as well.
//
// A digest is not a semantic check and does not know what a product name is.
// What it does is make every fixture edit fail until a human looks at the diff
// and re-pins it, which is the step that was missing. It is the same mechanism,
// for the same reason, as the canonical-encoding pins in regression_test.go:
// changing the constant is a deliberate act with a review behind it, never a
// side effect of an edit.
func TestShippedFixtureBytesArePinned(t *testing.T) {
	files, err := filepath.Glob("testdata/*/*.codefly.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	sort.Strings(files)

	// Path and content, so renaming, adding or removing a fixture moves the
	// digest as surely as editing one does.
	sum := sha256.New()
	for _, file := range files {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		sum.Write([]byte(file))
		sum.Write([]byte{0})
		sum.Write(data)
		sum.Write([]byte{0})
	}
	require.Equal(t, shippedFixtureDigest, "sha256:"+hex.EncodeToString(sum.Sum(nil)),
		"a shipped fixture changed. Re-read the diff before re-pinning this: these documents go to other "+
			"repositories and into documentation, and the reason this pin exists is that a real deployment "+
			"coordinate, a customer registry path and a product name once arrived in them unnoticed.")
}

// shippedFixtureDigest covers every document under testdata, by path and
// content. See TestShippedFixtureBytesArePinned.
const shippedFixtureDigest = "sha256:f411bf41209f5202cbbd7475b502251935f6b88870d14da1eb0dee2c8da9dca4"
