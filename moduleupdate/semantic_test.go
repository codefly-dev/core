package moduleupdate

import (
	"crypto/sha256"
	"fmt"
	"testing"

	updatev0 "github.com/codefly-dev/core/generated/go/codefly/update/v0"
	"github.com/codefly-dev/core/runnable"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestProtobufSourceCompatibilityPreservesOldSnapshotIdentities(t *testing.T) {
	old := &descriptorpb.FileDescriptorProto{Name: proto.String("wire.proto"), Package: proto.String("wire"), Syntax: proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Value"), Field: []*descriptorpb.FieldDescriptorProto{{Name: proto.String("name"), JsonName: proto.String("name"), Number: proto.Int32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()}}}},
	}
	for _, scenario := range []string{"optional field", "incompatible wire type", "removed field", "unsupported validation", "tampered source", "missing source"} {
		t.Run(scenario, func(t *testing.T) {
			next := proto.Clone(old).(*descriptorpb.FileDescriptorProto)
			switch scenario {
			case "incompatible wire type":
				next.MessageType[0].Field[0].Type = descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum()
			case "removed field":
				next.MessageType[0].Field = nil
			case "unsupported validation":
				next.MessageType[0].Field[0].Options = &descriptorpb.FieldOptions{Deprecated: proto.Bool(true)}
			default:
				next.MessageType[0].Field = append(next.MessageType[0].Field, &descriptorpb.FieldDescriptorProto{Name: proto.String("enabled"), Number: proto.Int32(2), Type: descriptorpb.FieldDescriptorProto_TYPE_BOOL.Enum(), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()})
			}
			before, beforeSource := protobufEvidence(t, old, "1.0.0")
			after, afterSource := protobufEvidence(t, next, "1.5.0")
			diff, err := BuildReleaseDiff(before, after)
			require.NoError(t, err)
			pin := &updatev0.ConsumerPin{SchemaVersion: 1, Consumer: "product", Module: before.Module, Version: before.Version, SnapshotDigest: before.Digest, UsageComplete: true}
			for _, item := range before.Items {
				pin.Uses = append(pin.Uses, &updatev0.ContractUse{Item: item.Id, Digest: item.Digest, Dependencies: item.Dependencies})
			}
			if scenario == "tampered source" {
				afterSource["type"] = beforeSource["type"]
			}
			if scenario == "missing source" {
				afterSource = nil
			}
			prepared, err := PrepareReleaseDiffWithSources(diff, beforeSource, afterSource)
			if scenario == "tampered source" {
				require.ErrorContains(t, err, "snapshot digest")
				return
			}
			require.NoError(t, err)
			require.Equal(t, before.Digest, prepared.diff.Before.Digest)
			require.Equal(t, after.Digest, prepared.diff.After.Digest)
			result := prepared.Evaluate(pin)
			classification := ClassifyContractChangeWithSources(diff, beforeSource, afterSource)
			switch scenario {
			case "optional field":
				require.Equal(t, updatev0.Verdict_VERDICT_NEW_CAPABILITY, result.Verdict)
				require.Equal(t, ChangeMinor, classification.Level)
			case "incompatible wire type", "removed field":
				require.Equal(t, updatev0.Verdict_VERDICT_BREAKING, result.Verdict)
				require.Equal(t, ChangeMajor, classification.Level)
			default:
				require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, result.Verdict)
				require.Equal(t, ChangeUndetermined, classification.Level)
			}
			oldFile, err := protodesc.NewFile(old, nil)
			require.NoError(t, err)
			newFile, err := protodesc.NewFile(next, nil)
			require.NoError(t, err)
			message := dynamicpb.NewMessage(oldFile.Messages().Get(0))
			field := oldFile.Messages().Get(0).Fields().ByNumber(1)
			message.Set(field, protoreflect.ValueOfString("retained"))
			wire, err := proto.Marshal(message)
			require.NoError(t, err)
			decoded := dynamicpb.NewMessage(newFile.Messages().Get(0))
			require.NoError(t, proto.Unmarshal(wire, decoded))
			if scenario == "optional field" {
				require.Equal(t, "retained", decoded.Get(newFile.Messages().Get(0).Fields().ByNumber(1)).String())
			}
			if scenario == "incompatible wire type" {
				require.Equal(t, int64(0), decoded.Get(newFile.Messages().Get(0).Fields().ByNumber(1)).Int())
				require.NotEmpty(t, decoded.GetUnknown())
			}
		})
	}
}

func protobufEvidence(t *testing.T, file *descriptorpb.FileDescriptorProto, version string) (*updatev0.ContractSnapshot, map[string]ContractSource) {
	t.Helper()
	data, err := runnable.CanonicalJSON(file)
	require.NoError(t, err)
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	method := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("stable method")))
	snapshot, err := PrepareSnapshot(&updatev0.ContractSnapshot{SchemaVersion: 1, Module: "example/module", Version: version, Complete: true, Items: []*updatev0.ContractItem{{Id: "type", Digest: digest}, {Id: "rpc", Digest: method, Dependencies: []string{"type"}}}})
	require.NoError(t, err)
	return snapshot, map[string]ContractSource{"type": {Format: SourceProtobuf, Content: data}}
}

func TestOpenAPISourcePreservesExactNumericRequirements(t *testing.T) {
	for _, format := range []string{SourceOpenAPIOperation, SourceOpenAPISchema} {
		t.Run(format, func(t *testing.T) {
			old := `{"type":"integer","maximum":9007199254740993}`
			next := `{"type":"integer","maximum":9007199254740992}`
			if format == SourceOpenAPIOperation {
				old = `[{}, {"parameters":[{"name":"count","in":"query","schema":` + old + `}]}]`
				next = `[{}, {"parameters":[{"name":"count","in":"query","schema":` + next + `}]}]`
			}
			evidence := func(content, version string) (*updatev0.ContractSnapshot, map[string]ContractSource) {
				source := ContractSource{Format: format, Content: []byte(content)}
				snapshot, err := PrepareSnapshot(&updatev0.ContractSnapshot{SchemaVersion: 1, Module: "example/module", Version: version, Complete: true,
					Items: []*updatev0.ContractItem{{Id: "contract", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(source.Content))}},
				})
				require.NoError(t, err)
				return snapshot, map[string]ContractSource{"contract": source}
			}
			before, beforeSource := evidence(old, "1.0.0")
			after, afterSource := evidence(next, "1.1.0")
			diff, err := BuildReleaseDiff(before, after)
			require.NoError(t, err)
			prepared, err := PrepareReleaseDiffWithSources(diff, beforeSource, afterSource)
			require.NoError(t, err)
			pin := &updatev0.ConsumerPin{SchemaVersion: 1, Consumer: "product", Module: before.Module, Version: before.Version, SnapshotDigest: before.Digest, UsageComplete: true,
				Uses: []*updatev0.ContractUse{{Item: "contract", Digest: before.Items[0].Digest}},
			}
			require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, prepared.Evaluate(pin).Verdict)
			require.Equal(t, ChangeUndetermined, ClassifyContractChangeWithSources(diff, beforeSource, afterSource).Level)
		})
	}
}
