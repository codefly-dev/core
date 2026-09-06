package proto

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestNonSharedFiles(t *testing.T) {
	set := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{
			{Name: proto.String("buf/validate/validate.proto")},
			{Name: proto.String("google/protobuf/timestamp.proto")},
			{Name: proto.String("google/api/http.proto")},
			{Name: proto.String("saas/policy/v1/options.proto")},
			{Name: proto.String("saas/accounts/v1/audit.proto")},
		},
	}
	blob, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	got, err := nonSharedFiles(blob)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"saas/policy/v1/options.proto", "saas/accounts/v1/audit.proto"}
	if len(got) != len(want) {
		t.Fatalf("nonSharedFiles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("nonSharedFiles = %v, want %v", got, want)
		}
	}
}

// TestStripCustomOptions runs the actual strip_custom_options.py against a
// descriptor set that imports buf/validate/validate.proto and asserts the
// shared file and the field options are gone. It needs a python with the
// protobuf runtime; without one it skips (this test is not Docker-gated).
func TestStripCustomOptions(t *testing.T) {
	python := pythonWithProtobuf(t)

	set := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{
			{Name: proto.String("buf/validate/validate.proto"), Package: proto.String("buf.validate")},
			{Name: proto.String("google/protobuf/timestamp.proto"), Package: proto.String("google.protobuf")},
			{
				Name:       proto.String("saas/accounts/v1/audit.proto"),
				Package:    proto.String("saas.accounts.v1"),
				Dependency: []string{"buf/validate/validate.proto", "google/protobuf/timestamp.proto"},
				MessageType: []*descriptorpb.DescriptorProto{{
					Name: proto.String("Req"),
					Field: []*descriptorpb.FieldDescriptorProto{{
						Name:    proto.String("page_size"),
						Number:  proto.Int32(1),
						Type:    descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
						Label:   descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Options: &descriptorpb.FieldOptions{Deprecated: proto.Bool(true)},
					}},
				}},
			},
		},
	}
	blob, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "in.binpb")
	out := filepath.Join(dir, "out.binpb")
	if err := os.WriteFile(in, blob, 0600); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join("facades", "python", "strip_custom_options.py")
	cmd := exec.Command(python, script, in, out, "saas/accounts/v1/audit.proto")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("strip_custom_options.py: %v\n%s", err, output)
	}

	stripped := &descriptorpb.FileDescriptorSet{}
	blob, err = os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(blob, stripped); err != nil {
		t.Fatal(err)
	}

	var target *descriptorpb.FileDescriptorProto
	for _, f := range stripped.GetFile() {
		if f.GetName() == "buf/validate/validate.proto" {
			t.Error("buf/validate/validate.proto survived the strip")
		}
		if f.GetName() == "saas/accounts/v1/audit.proto" {
			target = f
		}
	}
	if target == nil {
		t.Fatal("target file dropped by the strip")
	}
	for _, dep := range target.GetDependency() {
		if dep == "buf/validate/validate.proto" {
			t.Error("target still depends on buf/validate/validate.proto")
		}
	}
	if target.GetMessageType()[0].GetField()[0].Options != nil {
		t.Error("field options were not cleared")
	}
}

func pythonWithProtobuf(t *testing.T) string {
	t.Helper()
	candidates := []string{os.Getenv("CODEFLY_FACADE_PYTHON"), "python3", "python"}
	for _, python := range candidates {
		if python == "" {
			continue
		}
		if err := exec.Command(python, "-c", "import google.protobuf").Run(); err == nil {
			return python
		}
	}
	t.Skip("no python with the protobuf runtime available")
	return ""
}
