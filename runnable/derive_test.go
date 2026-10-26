package runnable_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/runnable"
)

// hopPolicyFile is authority.hop.v1's policy.proto as a module's contract
// ships it: core links no Go type for it and reads it from the descriptors.
func hopPolicyFile(t *testing.T) protoreflect.FileDescriptor {
	t.Helper()
	label := descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:       proto.String("authority/hop/v1/policy.proto"),
		Package:    proto.String("authority.hop.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/descriptor.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			message("Policy", repeated(named("requires", 1, tMessage, ".authority.hop.v1.Requirement")), scalar("any_checked_caller", 2, tBool)),
			{Name: proto.String("Requirement"), Field: []*descriptorpb.FieldDescriptorProto{
				scalar("resource_kind", 1, tString), {Name: proto.String("actions"), Number: proto.Int32(2), Label: label, Type: tString.Enum(), JsonName: proto.String("actions")}}},
		},
		Extension: []*descriptorpb.FieldDescriptorProto{{
			Name: proto.String("policy"), Number: proto.Int32(51100), Type: tMessage.Enum(),
			TypeName: proto.String(".authority.hop.v1.Policy"), Extendee: proto.String(".google.protobuf.MethodOptions"),
			JsonName: proto.String("policy"),
		}},
	}, protoregistry.GlobalFiles)
	require.NoError(t, err)
	return file
}

// requirement is one (resource_kind, actions) entry of a hop policy.
type requirement struct {
	kind    string
	actions []string
}

// hopMethod is an rpc carrying only authority.hop.v1.policy, encoded as the
// unknown bytes a reader without the Go type sees.
func hopMethod(t *testing.T, policyFile protoreflect.FileDescriptor, name string, anyChecked bool, requires ...requirement) *descriptorpb.MethodDescriptorProto {
	t.Helper()
	policyType := dynamicpb.NewMessageType(policyFile.Messages().ByName("Policy"))
	policy := policyType.New()
	requiresField := policy.Descriptor().Fields().ByName("requires")
	list := policy.Mutable(requiresField).List()
	for _, r := range requires {
		entry := list.NewElement().Message()
		entry.Set(entry.Descriptor().Fields().ByName("resource_kind"), protoreflect.ValueOfString(r.kind))
		actions := entry.Mutable(entry.Descriptor().Fields().ByName("actions")).List()
		for _, action := range r.actions {
			actions.Append(protoreflect.ValueOfString(action))
		}
		list.Append(protoreflect.ValueOfMessage(entry))
	}
	if anyChecked {
		policy.Set(policy.Descriptor().Fields().ByName("any_checked_caller"), protoreflect.ValueOfBool(true))
	}
	options := &descriptorpb.MethodOptions{}
	proto.SetExtension(options, dynamicpb.NewExtensionType(policyFile.Extensions().ByName("policy")), policy.Interface())
	raw, err := proto.Marshal(options)
	require.NoError(t, err)
	unknown := &descriptorpb.MethodOptions{}
	require.NoError(t, proto.Unmarshal(raw, unknown))
	method := operationMethod(name, ".documents.ingest.v1.ApplyTextRequest", ".documents.ingest.v1.ApplyTextResponse", nil)
	method.Options = unknown
	return method
}

func hopFiles(t *testing.T, methods ...*descriptorpb.MethodDescriptorProto) *protoregistry.Files {
	t.Helper()
	policyFile := hopPolicyFile(t)
	spec := ingestion(nil, methods...)
	spec.imports = append(spec.imports, "authority/hop/v1/policy.proto")
	resolver := &protoregistry.Files{}
	require.NoError(t, resolver.RegisterFile(policyFile))
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		_ = resolver.RegisterFile(fd)
		return true
	})
	file, err := protodesc.NewFile(spec.proto(), resolver)
	require.NoError(t, err)
	files := &protoregistry.Files{}
	require.NoError(t, files.RegisterFile(policyFile))
	require.NoError(t, files.RegisterFile(file))
	return files
}

// A method declaring only its authority policy is an operation and a tool:
// the owner adds nothing. A read recomputes with no receipt at all.
func TestAMethodDeclaringOnlyItsAuthorityPolicyIsAReadTool(t *testing.T) {
	files := hopFiles(t, hopMethod(t, hopPolicyFile(t), "ApplyText", false, requirement{"documents", []string{"read"}}))
	pkg, spec, err := runnable.PackageFromMethod(files, ingestLocation(), ingestOwner(), applyText)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableExecution_RECOVERY_RECOMPUTE, pkg.GetExecution().GetRecovery())
	require.Equal(t, basev0.RunnableExecution_RECOVERY_RECOMPUTE, spec.Recovery)
	require.Empty(t, spec.LookupScopes)
	require.Empty(t, spec.LookupMethod)
	require.Equal(t, "documents", spec.Audience, "the audience defaults to the owner's module")
	require.True(t, proto.Equal(&basev0.WorkScopeV1{ResourceKind: "documents", Actions: []string{"read"}}, spec.InvokeScopes[0]))
	require.Equal(t, runnablev0.ToolExposure_EFFECT_READ_ONLY, spec.Tool.GetEffect())
	require.Equal(t, "ApplyText", spec.Tool.GetName())

	// The policy delivers: a prepared binding carries it and presents it as a
	// read-only tool, with no receipt lookup to wire.
	tool, err := runnable.ToolFromPrepared(preparedFor(t, spec))
	require.NoError(t, err)
	require.Equal(t, runnablev0.ToolExposure_EFFECT_READ_ONLY, tool.GetEffect())
}

// Anything that is not a read is a mutation resolved by its receipt, read
// through the SDK's generic lookup under read on the kinds it writes, and
// attempted once.
func TestAMethodWhosePolicyWritesIsAMutationToolResolvedByItsReceipt(t *testing.T) {
	files := hopFiles(t, hopMethod(t, hopPolicyFile(t), "ApplyText", false,
		requirement{"documents", []string{"write"}}, requirement{"documents.collection", []string{"read"}}))
	pkg, spec, err := runnable.PackageFromMethod(files, ingestLocation(), ingestOwner(), applyText)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableExecution_RECOVERY_RECEIPT, pkg.GetExecution().GetRecovery())
	require.Equal(t, runnablev0.ToolExposure_EFFECT_MUTATION, spec.Tool.GetEffect())
	require.Empty(t, spec.LookupMethod, "the SDK's generic receipt lookup answers")
	require.EqualValues(t, 1, spec.MaxAttempts)
	require.Empty(t, spec.RetryableCodes)
	require.Equal(t, []string{"write", "read"}, spec.InvokeScopes[0].GetActions())
	require.Len(t, spec.LookupScopes, 2)
	for _, lookup := range spec.LookupScopes {
		require.Equal(t, []string{runnable.ReadOnlyScopeAction}, lookup.GetActions())
	}
	tool, err := runnable.ToolFromPrepared(preparedFor(t, spec))
	require.NoError(t, err)
	require.Equal(t, runnablev0.ToolExposure_EFFECT_MUTATION, tool.GetEffect())
}

func TestMethodsADefaultDerivationLeavesAlone(t *testing.T) {
	policyFile := hopPolicyFile(t)
	excluded := hopMethod(t, policyFile, "ApplyText", false, requirement{"documents", []string{"read"}})
	proto.SetExtension(excluded.Options, runnablev0.E_Exclude, true)
	_, _, err := runnable.PackageFromMethod(hopFiles(t, excluded), ingestLocation(), ingestOwner(), applyText)
	require.ErrorIs(t, err, runnable.ErrNotAnOperation)

	both := hopMethod(t, policyFile, "ApplyText", false, requirement{"documents", []string{"read"}})
	proto.SetExtension(both.Options, runnablev0.E_Exclude, true)
	proto.SetExtension(both.Options, runnablev0.E_Operation, declaredOperation())
	_, _, err = runnable.PackageFromMethod(hopFiles(t, both), ingestLocation(), ingestOwner(), applyText)
	require.ErrorIs(t, err, runnable.ErrInvalid)

	_, _, err = runnable.PackageFromMethod(hopFiles(t, hopMethod(t, policyFile, "ApplyText", true)), ingestLocation(), ingestOwner(), applyText)
	require.ErrorIs(t, err, runnable.ErrNotDerivable, "a policy requiring nothing has no authority to mint from")

	streaming := hopMethod(t, policyFile, "ApplyText", false, requirement{"documents", []string{"read"}})
	streaming.ServerStreaming = proto.Bool(true)
	_, _, err = runnable.PackageFromMethod(hopFiles(t, streaming), ingestLocation(), ingestOwner(), applyText)
	require.ErrorIs(t, err, runnable.ErrNotDerivable)

	plain := operationMethod("ApplyText", ".documents.ingest.v1.ApplyTextRequest", ".documents.ingest.v1.ApplyTextResponse", nil)
	_, _, err = runnable.PackageFromMethod(hopFiles(t, plain), ingestLocation(), ingestOwner(), applyText)
	require.ErrorIs(t, err, runnable.ErrNotAnOperation)
}

// An explicit option still wins, and is presented as a tool from the method
// with its effect from the policy beside it.
func TestAnExplicitOperationIsPresentedFromItsMethod(t *testing.T) {
	_, spec, err := runnable.PackageFromMethod(ingestionFiles(t, declaredOperation()), ingestLocation(), ingestOwner(), applyText)
	require.NoError(t, err)
	require.Equal(t, "ApplyText", spec.Tool.GetName())
	require.Equal(t, runnablev0.ToolExposure_EFFECT_MUTATION, spec.Tool.GetEffect(), "no policy is never read-only")
	require.Equal(t, basev0.RunnableExecution_RECOVERY_UNKNOWN, spec.Recovery)
	require.Equal(t, basev0.RunnableExecution_RECOVERY_RECEIPT, spec.EffectiveRecovery())
}

func TestARecomputedOperationCarriesNoReceipt(t *testing.T) {
	for name, change := range map[string]func(*runnablev0.Operation){
		"lookup scopes": func(o *runnablev0.Operation) {},
		"lookup method": func(o *runnablev0.Operation) { o.LookupScopes = nil },
		"submit": func(o *runnablev0.Operation) {
			o.LookupScopes, o.LookupMethod = nil, ""
			o.Completion = basev0.RunnableExecution_COMPLETION_SUBMIT
		},
		"mutation tool": func(o *runnablev0.Operation) {
			o.LookupScopes, o.LookupMethod = nil, ""
			o.Tool = &runnablev0.ToolExposure{Name: "apply", Description: "Applies.", Effect: runnablev0.ToolExposure_EFFECT_MUTATION}
		},
	} {
		t.Run(name, func(t *testing.T) {
			declared := declaredOperation()
			declared.Recovery = basev0.RunnableExecution_RECOVERY_RECOMPUTE
			change(declared)
			_, _, err := runnable.PackageFromMethod(ingestionFiles(t, declared), ingestLocation(), ingestOwner(), applyText)
			require.ErrorIs(t, err, runnable.ErrInvalid)
		})
	}
	declared := declaredOperation()
	declared.Recovery = basev0.RunnableExecution_RECOVERY_RECOMPUTE
	declared.LookupScopes, declared.LookupMethod = nil, ""
	pkg, spec, err := runnable.PackageFromMethod(ingestionFiles(t, declared), ingestLocation(), ingestOwner(), applyText)
	require.NoError(t, err)
	require.Equal(t, basev0.RunnableExecution_RECOVERY_RECOMPUTE, pkg.GetExecution().GetRecovery())
	require.Equal(t, runnablev0.ToolExposure_EFFECT_READ_ONLY, spec.Tool.GetEffect())
}

// preparedFor delivers a derived spec the way a composition does.
func preparedFor(t *testing.T, spec *runnable.OperationSpec) *runnablev0.PreparedBinding {
	t.Helper()
	binding := bindingFor(spec)
	binding.Schema = runnable.PreparedSchemaV3
	encoded, err := runnable.EncodePrepared(binding)
	require.NoError(t, err)
	delivered, err := runnable.DecodePrepared(encoded)
	require.NoError(t, err)
	return delivered
}
