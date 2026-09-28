package solutionhost_test

import (
	"reflect"
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
	require.Equal(t, "codefly/solution-host-binding/v1", solutionhost.SchemaV1)
	require.Equal(t, "solution-host-binding.codefly.yaml", solutionhost.FileName)
}
