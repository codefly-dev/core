package failures_test

import (
	"testing"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestCodeUsesOneCanonicalFailureEnvelope(t *testing.T) {
	response := (&codev0.CodeResponse{}).ProtoReflect().Descriptor()
	assertFailureField(t, response)

	results := []proto.Message{
		&codev0.WriteFileResponse{},
		&codev0.FixResponse{},
		&codev0.ApplyEditResponse{},
		&codev0.DeleteFileResponse{},
		&codev0.MoveFileResponse{},
		&codev0.CreateFileResponse{},
		&codev0.ListDependenciesResponse{},
		&codev0.AddDependencyResponse{},
		&codev0.RemoveDependencyResponse{},
		&codev0.GetProjectInfoResponse{},
		&codev0.GitLogResponse{},
		&codev0.GitDiffResponse{},
		&codev0.GitShowResponse{},
		&codev0.GitBlameResponse{},
		&codev0.ShellExecResponse{},
	}
	for _, result := range results {
		descriptor := result.ProtoReflect().Descriptor()
		if descriptor.Fields().ByName("error") != nil || descriptor.Fields().ByName("failure") != nil {
			t.Fatalf("%s reintroduced a nested failure channel", descriptor.FullName())
		}
	}

	service := codev0.File_codefly_services_code_v0_code_proto.Services().ByName("Code")
	if service.Methods().Len() != 1 || service.Methods().Get(0).Name() != "Execute" {
		t.Fatalf("Code RPC surface = %v, want only Execute", methodNames(service.Methods()))
	}
}

func TestEveryToolingResponseUsesCanonicalFailure(t *testing.T) {
	responses := []proto.Message{
		&toolingv0.FixResponse{},
		&toolingv0.ApplyEditResponse{},
		&toolingv0.ListDependenciesResponse{},
		&toolingv0.AddDependencyResponse{},
		&toolingv0.RemoveDependencyResponse{},
		&toolingv0.GetProjectInfoResponse{},
		&toolingv0.BuildResponse{},
		&toolingv0.TestResponse{},
		&toolingv0.LintResponse{},
	}
	for _, response := range responses {
		descriptor := response.ProtoReflect().Descriptor()
		if descriptor.Fields().ByName("error") != nil {
			t.Fatalf("%s retains legacy string error", descriptor.FullName())
		}
		assertFailureField(t, descriptor)
	}
}

func assertFailureField(t *testing.T, descriptor protoreflect.MessageDescriptor) {
	t.Helper()
	field := descriptor.Fields().ByName("failure")
	if field == nil || field.Message() == nil || field.Message().FullName() != "codefly.base.v0.Failure" {
		t.Fatalf("%s.failure is not codefly.base.v0.Failure", descriptor.FullName())
	}
}

func methodNames(methods protoreflect.MethodDescriptors) []protoreflect.Name {
	names := make([]protoreflect.Name, 0, methods.Len())
	for index := 0; index < methods.Len(); index++ {
		names = append(names, methods.Get(index).Name())
	}
	return names
}
