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

// TestStripKeepsTypeDependencies covers the two edges the strip must not cut
// when a module owns more than one self-referencing proto: the import between
// two kept target files, and the file another package's rpc type lives in — the
// latter is not a target, but without its bindings the descriptor is invalid and
// the facade has no module for the response type.
func TestStripKeepsTypeDependencies(t *testing.T) {
	python := pythonWithProtobuf(t)

	set := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{
			{Name: proto.String("buf/validate/validate.proto"), Package: proto.String("buf.validate")},
			{
				Name:    proto.String("saas/jobs/v1/jobs.proto"),
				Package: proto.String("saas.jobs.v1"),
				MessageType: []*descriptorpb.DescriptorProto{
					{Name: proto.String("GetJobOperationsResponse")},
				},
			},
			{
				Name:       proto.String("saas/accounts/v1/common.proto"),
				Package:    proto.String("saas.accounts.v1"),
				Dependency: []string{"buf/validate/validate.proto"},
				EnumType: []*descriptorpb.EnumDescriptorProto{{
					Name:  proto.String("Permission"),
					Value: []*descriptorpb.EnumValueDescriptorProto{{Name: proto.String("PERMISSION_UNSPECIFIED"), Number: proto.Int32(0)}},
				}},
			},
			{
				Name:    proto.String("saas/accounts/v1/api_keys.proto"),
				Package: proto.String("saas.accounts.v1"),
				Dependency: []string{
					"buf/validate/validate.proto",
					"saas/accounts/v1/common.proto",
					"saas/jobs/v1/jobs.proto",
				},
				MessageType: []*descriptorpb.DescriptorProto{
					{
						Name: proto.String("APIKey"),
						Field: []*descriptorpb.FieldDescriptorProto{{
							Name:     proto.String("scopes"),
							Number:   proto.Int32(1),
							Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
							Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
							TypeName: proto.String(".saas.accounts.v1.Permission"),
						}},
					},
					{Name: proto.String("ListJobsRequest")},
				},
				Service: []*descriptorpb.ServiceDescriptorProto{{
					Name: proto.String("APIKeyService"),
					Method: []*descriptorpb.MethodDescriptorProto{{
						Name:       proto.String("ListJobs"),
						InputType:  proto.String(".saas.accounts.v1.ListJobsRequest"),
						OutputType: proto.String(".saas.jobs.v1.GetJobOperationsResponse"),
					}},
				}},
			},
		},
	}

	stripped := runStrip(t, python, set, "saas/accounts/v1/common.proto", "saas/accounts/v1/api_keys.proto")

	kept := map[string]*descriptorpb.FileDescriptorProto{}
	var order []string
	for _, f := range stripped.GetFile() {
		kept[f.GetName()] = f
		order = append(order, f.GetName())
	}
	if _, ok := kept["buf/validate/validate.proto"]; ok {
		t.Error("buf/validate/validate.proto survived the strip")
	}
	if _, ok := kept["saas/jobs/v1/jobs.proto"]; !ok {
		t.Fatal("saas/jobs/v1/jobs.proto dropped: an rpc response type lives there")
	}
	apiKeys := kept["saas/accounts/v1/api_keys.proto"]
	if apiKeys == nil {
		t.Fatal("target file dropped by the strip")
	}
	deps := map[string]bool{}
	for _, dep := range apiKeys.GetDependency() {
		deps[dep] = true
	}
	for _, want := range []string{"saas/accounts/v1/common.proto", "saas/jobs/v1/jobs.proto"} {
		if !deps[want] {
			t.Errorf("import edge on %s was dropped", want)
		}
	}
	if deps["buf/validate/validate.proto"] {
		t.Error("target still depends on stripped file buf/validate/validate.proto")
	}
	position := map[string]int{}
	for i, name := range order {
		position[name] = i
	}
	for _, dep := range apiKeys.GetDependency() {
		if position[dep] > position["saas/accounts/v1/api_keys.proto"] {
			t.Errorf("%s appears after the file that imports it", dep)
		}
	}
}

func runStrip(t *testing.T, python string, set *descriptorpb.FileDescriptorSet, targets ...string) *descriptorpb.FileDescriptorSet {
	t.Helper()
	blob, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "in.binpb")
	out := filepath.Join(dir, "out.binpb")
	if err = os.WriteFile(in, blob, 0600); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join("facades", "python", "strip_custom_options.py")
	cmd := exec.Command(python, append([]string{script, in, out}, targets...)...)
	if output, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
		t.Fatalf("strip_custom_options.py: %v\n%s", cmdErr, output)
	}

	stripped := &descriptorpb.FileDescriptorSet{}
	if blob, err = os.ReadFile(out); err != nil {
		t.Fatal(err)
	}
	if err = proto.Unmarshal(blob, stripped); err != nil {
		t.Fatal(err)
	}
	return stripped
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
