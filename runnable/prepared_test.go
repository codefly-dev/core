package runnable_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/runnable"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// publishedContract is a real published contract: runnable.proto and its
// imports, each file carrying SourceCodeInfo as `codefly generate contracts`
// leaves it.
func publishedContract(t *testing.T) []byte {
	t.Helper()
	set := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	var visit func(protoreflect.FileDescriptor)
	visit = func(file protoreflect.FileDescriptor) {
		if seen[file.Path()] {
			return
		}
		seen[file.Path()] = true
		imports := file.Imports()
		for i := 0; i < imports.Len(); i++ {
			visit(imports.Get(i).FileDescriptor)
		}
		fileProto := protodesc.ToFileDescriptorProto(file)
		fileProto.SourceCodeInfo = &descriptorpb.SourceCodeInfo{Location: []*descriptorpb.SourceCodeInfo_Location{{
			Path: []int32{4, 0}, Span: []int32{1, 0, 10}, LeadingComments: proto.String(strings.Repeat("a comment no caller needs ", 64)),
		}}}
		set.File = append(set.File, fileProto)
	}
	visit(basev0.File_codefly_base_v0_runnable_proto)
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(set)
	require.NoError(t, err)
	return raw
}

func TestLeanDescriptorSetIsStableAndCarriesNoSourceInfo(t *testing.T) {
	contract := publishedContract(t)
	first, err := runnable.LeanDescriptorSet(contract)
	require.NoError(t, err)
	second, err := runnable.LeanDescriptorSet(contract)
	require.NoError(t, err)
	require.Equal(t, first, second, "equal contracts must derive equal bytes")
	require.Equal(t, runnable.DescriptorSetDigest(first), runnable.DescriptorSetDigest(second))
	require.Less(t, len(first), len(contract))

	set := &descriptorpb.FileDescriptorSet{}
	require.NoError(t, proto.Unmarshal(first, set))
	for _, file := range set.GetFile() {
		require.Nil(t, file.GetSourceCodeInfo(), file.GetName())
	}
	// Every file of the contract is kept, in order: the set serves every
	// operation on the endpoint, not one method's closure.
	source := &descriptorpb.FileDescriptorSet{}
	require.NoError(t, proto.Unmarshal(contract, source))
	require.Len(t, set.GetFile(), len(source.GetFile()))
	for i := range source.GetFile() {
		require.Equal(t, source.GetFile()[i].GetName(), set.GetFile()[i].GetName())
	}
	// Leaning a lean set is the identity: the delivered set is a fixed point.
	again, err := runnable.LeanDescriptorSet(first)
	require.NoError(t, err)
	require.Equal(t, first, again)
}

func TestLeanDescriptorSetRefusesWhatIsNotAContract(t *testing.T) {
	for name, raw := range map[string][]byte{"empty": nil, "garbage": []byte("\xff\xff\xff"), "no file": {}} {
		_, err := runnable.LeanDescriptorSet(raw)
		require.ErrorIs(t, err, runnable.ErrInvalid, name)
	}
}

func TestDescriptorSetKeyIsDerivedFromTheDigest(t *testing.T) {
	digest := runnable.DescriptorSetDigest([]byte("x"))
	key, err := runnable.DescriptorSetKey(digest)
	require.NoError(t, err)
	require.Equal(t, "DESCRIPTOR_SET__"+strings.ToUpper(strings.TrimPrefix(digest, "sha256:")), key)
	for _, bad := range []string{"", "sha256:ABC", strings.ToUpper(digest), "md5:" + strings.TrimPrefix(digest, "sha256:")} {
		_, err := runnable.DescriptorSetKey(bad)
		require.ErrorIs(t, err, runnable.ErrInvalid, bad)
	}
}

func TestResolveDescriptorSetVerifiesTheDigest(t *testing.T) {
	set, err := runnable.LeanDescriptorSet(publishedContract(t))
	require.NoError(t, err)
	reference := &runnable.DescriptorSetReference{Digest: runnable.DescriptorSetDigest(set), Contract: runnable.DescriptorSetDigest([]byte("contract"))}
	key, err := reference.Key()
	require.NoError(t, err)
	group := map[string]string{key: runnable.EncodeDescriptorSet(set)}
	lookup := func(key string) (string, error) {
		value, ok := group[key]
		if !ok {
			return "", fmt.Errorf("no value for %s", key)
		}
		return value, nil
	}

	resolved, err := runnable.ResolveDescriptorSet(reference, lookup)
	require.NoError(t, err)
	require.Equal(t, set, resolved)

	// Any other bytes under the key are refused: a stale group, a set from
	// another generation, a truncated file.
	other := append([]byte(nil), set...)
	other = other[:len(other)-1]
	group[key] = runnable.EncodeDescriptorSet(other)
	_, err = runnable.ResolveDescriptorSet(reference, lookup)
	require.ErrorIs(t, err, runnable.ErrDescriptorSetMismatch)
	require.Contains(t, err.Error(), key)

	group[key] = "not base64!"
	_, err = runnable.ResolveDescriptorSet(reference, lookup)
	require.ErrorIs(t, err, runnable.ErrInvalid)

	delete(group, key)
	_, err = runnable.ResolveDescriptorSet(reference, lookup)
	require.Error(t, err)
	require.Contains(t, err.Error(), key)

	// Bytes matching their digest but not a descriptor set are still refused.
	junk := []byte("\xff\xff\xff")
	junkReference := &runnable.DescriptorSetReference{Digest: runnable.DescriptorSetDigest(junk), Contract: reference.Contract}
	junkKey, err := junkReference.Key()
	require.NoError(t, err)
	group[junkKey] = base64.StdEncoding.EncodeToString(junk)
	_, err = runnable.ResolveDescriptorSet(junkReference, lookup)
	require.ErrorIs(t, err, runnable.ErrInvalid)
}

func preparedValue(extra string) string {
	digest := runnable.DescriptorSetDigest([]byte("set"))
	contract := runnable.DescriptorSetDigest([]byte("contract"))
	return `{"schema":"` + runnable.PreparedSchemaV2 + `","package":{"schema":"p"},"binding":{"schema":"b"},"operation":{"method":"/a.B/C"},` +
		`"descriptor_set":{"digest":"` + digest + `","contract":"` + contract + `"}` + extra + `}`
}

func TestDecodePreparedReadsTheSharedForm(t *testing.T) {
	prepared, err := runnable.DecodePrepared([]byte(preparedValue("")))
	require.NoError(t, err)
	require.Equal(t, runnable.PreparedSchemaV2, prepared.Schema)
	require.Equal(t, runnable.DescriptorSetDigest([]byte("set")), prepared.DescriptorSet.Digest)
	require.JSONEq(t, `{"method":"/a.B/C"}`, string(prepared.Operation))
}

func TestDecodePreparedRefusesTheEmbeddedForm(t *testing.T) {
	// The form every value had before v2: no schema, descriptors inline.
	embedded := `{"package":{},"binding":{},"operation":{"method":"/a.B/C"},"descriptors":"AAAA"}`
	_, err := runnable.DecodePrepared([]byte(embedded))
	require.ErrorIs(t, err, runnable.ErrEmbeddedDescriptors)
	require.Contains(t, err.Error(), "codefly generate runnable-bindings")

	// Inline descriptors beside a v2 schema are still two sources for one fact.
	_, err = runnable.DecodePrepared([]byte(preparedValue(`,"descriptors":"AAAA"`)))
	require.ErrorIs(t, err, runnable.ErrEmbeddedDescriptors)
}

func TestDecodePreparedIsStrict(t *testing.T) {
	cases := map[string]string{
		"unknown field":    preparedValue(`,"extra":1`),
		"trailing":         preparedValue("") + `{}`,
		"other schema":     strings.Replace(preparedValue(""), runnable.PreparedSchemaV2, "codefly.runnable-prepared/v3", 1),
		"no operation":     strings.Replace(preparedValue(""), `"operation":{"method":"/a.B/C"}`, `"operation":null`, 1),
		"malformed digest": strings.Replace(preparedValue(""), "sha256:", "sha256:X", 1),
		"not an object":    `[]`,
		"oversized":        `{"schema":"` + strings.Repeat("x", runnable.MaxPreparedBytes) + `"}`,
	}
	for name, value := range cases {
		_, err := runnable.DecodePrepared([]byte(value))
		require.Error(t, err, name)
		require.False(t, errors.Is(err, runnable.ErrEmbeddedDescriptors), name)
	}
}

// pinnedContract is a fixed, hand-declared contract, so the digest pinned
// below moves only when the derivation does, never when a core proto does.
func pinnedContract(t *testing.T) []byte {
	t.Helper()
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:    proto.String("acme/items/v1/items.proto"),
		Package: proto.String("acme.items.v1"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Item"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name: proto.String("id"), Number: proto.Int32(1), JsonName: proto.String("id"),
				Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			}},
		}},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name:   proto.String("Items"),
			Method: []*descriptorpb.MethodDescriptorProto{{Name: proto.String("Get"), InputType: proto.String(".acme.items.v1.Item"), OutputType: proto.String(".acme.items.v1.Item")}},
		}},
		SourceCodeInfo: &descriptorpb.SourceCodeInfo{Location: []*descriptorpb.SourceCodeInfo_Location{{
			Path: []int32{6, 0}, Span: []int32{3, 0, 5, 1}, LeadingComments: proto.String(" Items serves items.\n"),
		}}},
	}}}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(set)
	require.NoError(t, err)
	return raw
}

// The delivered set's digest is what every prepared value references and what
// a worker verifies, so the derivation is pinned: a change to it is one
// reviewed decision that regenerates every runnable-bindings group, never an
// incidental one.
func TestLeanDescriptorSetDigestIsPinned(t *testing.T) {
	lean, err := runnable.LeanDescriptorSet(pinnedContract(t))
	require.NoError(t, err)
	require.Equal(t, pinnedLeanDigest, runnable.DescriptorSetDigest(lean))
}

const pinnedLeanDigest = "sha256:a8fbb399dd8f414c8bca27f35f81907b9787b53ceed57c3027e8a28dcc3e0044"
