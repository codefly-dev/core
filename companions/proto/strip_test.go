package proto

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// TestStripCustomOptions runs the actual strip_custom_options.py against a
// descriptor set that imports both buf/validate/validate.proto and an
// org-shared options proto (saas/policy), and asserts that only the named
// target survives — every shared import, whatever its path, is dropped, and
// the target's field options are cleared. It needs a python with the protobuf
// runtime; without one it skips (this test is not Docker-gated).
//
// saas/policy is the case a path-prefix guess would have wrongly kept: it is
// neither a google well-known type nor under buf/, yet keeping it re-registers
// a shared descriptor into the pool. Only the caller's explicit target list
// gets this right.
func TestStripCustomOptions(t *testing.T) {
	python := pythonWithProtobuf(t)

	set := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{
			{Name: proto.String("buf/validate/validate.proto"), Package: proto.String("buf.validate")},
			{Name: proto.String("saas/policy/v1/options.proto"), Package: proto.String("saas.policy.v1")},
			{Name: proto.String("google/protobuf/timestamp.proto"), Package: proto.String("google.protobuf")},
			{
				Name:       proto.String("saas/accounts/v1/audit.proto"),
				Package:    proto.String("saas.accounts.v1"),
				Dependency: []string{"buf/validate/validate.proto", "saas/policy/v1/options.proto", "google/protobuf/timestamp.proto"},
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
		switch f.GetName() {
		case "buf/validate/validate.proto":
			t.Error("buf/validate/validate.proto survived the strip")
		case "saas/policy/v1/options.proto":
			t.Error("saas/policy/v1/options.proto (a shared, non-target proto) survived the strip")
		case "saas/accounts/v1/audit.proto":
			target = f
		}
	}
	if target == nil {
		t.Fatal("target file dropped by the strip")
	}
	for _, dep := range target.GetDependency() {
		if dep == "buf/validate/validate.proto" || dep == "saas/policy/v1/options.proto" {
			t.Errorf("target still depends on stripped file %s", dep)
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
