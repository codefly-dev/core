package failures_test

import (
	"testing"

	codev1 "github.com/codefly-dev/core/generated/go/codefly/services/code/v1"
	toolingv1 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestCodeUsesOneCanonicalFailureEnvelope(t *testing.T) {
	response := (&codev1.CodeResponse{}).ProtoReflect().Descriptor()
	assertFailureField(t, response)

	results := []proto.Message{
		&codev1.WriteFileResponse{},
		&codev1.FixResponse{},
		&codev1.ApplyEditResponse{},
		&codev1.DeleteFileResponse{},
		&codev1.MoveFileResponse{},
		&codev1.CreateFileResponse{},
		&codev1.ListDependenciesResponse{},
		&codev1.AddDependencyResponse{},
		&codev1.RemoveDependencyResponse{},
		&codev1.GetProjectInfoResponse{},
		&codev1.GitLogResponse{},
		&codev1.GitDiffResponse{},
		&codev1.GitShowResponse{},
		&codev1.GitBlameResponse{},
		&codev1.ShellExecResponse{},
	}
	for _, result := range results {
		descriptor := result.ProtoReflect().Descriptor()
		if descriptor.Fields().ByName("error") != nil || descriptor.Fields().ByName("failure") != nil {
			t.Fatalf("%s reintroduced a nested failure channel", descriptor.FullName())
		}
	}

	service := codev1.File_codefly_services_code_v0_code_proto.Services().ByName("Code")
	if service.Methods().Len() != 1 || service.Methods().Get(0).Name() != "Execute" {
		t.Fatalf("Code RPC surface = %v, want only Execute", methodNames(service.Methods()))
	}
}

func TestEveryToolingResponseUsesCanonicalFailure(t *testing.T) {
	responses := []proto.Message{
		&toolingv1.FixResponse{},
		&toolingv1.ApplyEditResponse{},
		&toolingv1.ListDependenciesResponse{},
		&toolingv1.AddDependencyResponse{},
		&toolingv1.RemoveDependencyResponse{},
		&toolingv1.GetProjectInfoResponse{},
		&toolingv1.BuildResponse{},
		&toolingv1.TestResponse{},
		&toolingv1.LintResponse{},
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
	if field == nil || field.Message() == nil || field.Message().FullName() != "codefly.base.v1.Failure" {
		t.Fatalf("%s.failure is not codefly.base.v1.Failure", descriptor.FullName())
	}
}

func methodNames(methods protoreflect.MethodDescriptors) []protoreflect.Name {
	names := make([]protoreflect.Name, 0, methods.Len())
	for index := 0; index < methods.Len(); index++ {
		names = append(names, methods.Get(index).Name())
	}
	return names
}
