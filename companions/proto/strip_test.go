package proto

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/languages"
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
	stripped := runStrip(t, python, set, "saas/accounts/v1/audit.proto")

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

// TestStripKeepsTypeDependencies covers the edges the strip must not cut when a
// library owns more than one self-referencing proto: the import between two kept
// target files, and the one into the other package whose type an rpc returns.
// Both ends are declared, so both survive and both edges stay.
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

	stripped := runStrip(t, python, set,
		"saas/accounts/v1/common.proto",
		"saas/accounts/v1/api_keys.proto",
		"saas/jobs/v1/jobs.proto",
	)

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
		t.Fatal("declared target saas/jobs/v1/jobs.proto was dropped")
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

// TestStripRefusesUndeclaredReference pins the ownership boundary: what the
// generated library contains is the caller's declaration, so a type reference
// into a file nobody declared stops generation by name instead of quietly
// pulling another contract's descriptors into this library.
func TestStripRefusesUndeclaredReference(t *testing.T) {
	python := pythonWithProtobuf(t)

	set := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{
			{
				Name:    proto.String("saas/jobs/v1/jobs.proto"),
				Package: proto.String("saas.jobs.v1"),
				MessageType: []*descriptorpb.DescriptorProto{
					{Name: proto.String("GetJobOperationsResponse")},
				},
			},
			{
				Name:       proto.String("saas/accounts/v1/api_keys.proto"),
				Package:    proto.String("saas.accounts.v1"),
				Dependency: []string{"saas/jobs/v1/jobs.proto"},
				MessageType: []*descriptorpb.DescriptorProto{
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

	output := runStripExpectingFailure(t, python, set, "saas/accounts/v1/api_keys.proto")

	for _, want := range []string{"saas/jobs/v1/jobs.proto", "not declared as targets"} {
		if !strings.Contains(output, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, output)
		}
	}
}

// TestStripRefusesGoogleapisCommonProtos guards the descriptor-pool collision
// the strip exists for: googleapis-common-protos registers google/rpc/code.proto
// at import, so a copy inside a generated library is a duplicate file name in
// the default pool. Declaring it cannot make it safe, so declaring it does not
// help.
func TestStripRefusesGoogleapisCommonProtos(t *testing.T) {
	python := pythonWithProtobuf(t)

	set := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{
			{
				Name:    proto.String("google/rpc/code.proto"),
				Package: proto.String("google.rpc"),
				EnumType: []*descriptorpb.EnumDescriptorProto{{
					Name:  proto.String("Code"),
					Value: []*descriptorpb.EnumValueDescriptorProto{{Name: proto.String("OK"), Number: proto.Int32(0)}},
				}},
			},
			{
				Name:       proto.String("saas/accounts/v1/api_keys.proto"),
				Package:    proto.String("saas.accounts.v1"),
				Dependency: []string{"google/rpc/code.proto"},
				MessageType: []*descriptorpb.DescriptorProto{{
					Name: proto.String("APIKey"),
					Field: []*descriptorpb.FieldDescriptorProto{{
						Name:     proto.String("status"),
						Number:   proto.Int32(1),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						TypeName: proto.String(".google.rpc.Code"),
					}},
				}},
			},
		},
	}

	output := runStripExpectingFailure(t, python, set,
		"saas/accounts/v1/api_keys.proto", "google/rpc/code.proto")

	for _, want := range []string{"google/rpc/code.proto", "googleapis-common-protos"} {
		if !strings.Contains(output, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, output)
		}
	}
}

func runStripExpectingFailure(t *testing.T, python string, set *descriptorpb.FileDescriptorSet, targets ...string) string {
	t.Helper()
	_, output, err := execStrip(t, python, set, targets)
	if err == nil {
		t.Fatalf("strip accepted a target set it should have refused:\n%s", output)
	}
	return output
}

func runStrip(t *testing.T, python string, set *descriptorpb.FileDescriptorSet, targets ...string) *descriptorpb.FileDescriptorSet {
	t.Helper()
	out, output, err := execStrip(t, python, set, targets)
	if err != nil {
		t.Fatalf("strip_custom_options.py: %v\n%s", err, output)
	}

	stripped := &descriptorpb.FileDescriptorSet{}
	blob, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err = proto.Unmarshal(blob, stripped); err != nil {
		t.Fatal(err)
	}
	return stripped
}

// execStrip writes the set to a temp dir, runs the real script over it, and
// reports where the output would be plus what the script said.
func execStrip(t *testing.T, python string, set *descriptorpb.FileDescriptorSet, targets []string) (string, string, error) {
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
	output, err := cmd.CombinedOutput()
	return out, string(output), err
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

// TestStripPreservesImportMarkers pins a coupling that spans two languages and is
// otherwise invisible. On the Python facade path the image handed to the strip is
// already marked by MarkForeignImports, and the strip re-serializes every
// FileDescriptorProto it keeps. It preserves the markers today only because it
// appends the parsed message; a rewrite that builds fresh descriptors field by
// field would drop them silently, and buf would resume generating
// google/protobuf/*_pb2.py into the facade — re-registering descriptors the
// protobuf runtime already owns, which is the collision the strip exists to
// prevent.
func TestStripPreservesImportMarkers(t *testing.T) {
	python := pythonWithProtobuf(t)

	set := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{
			{Name: proto.String("google/protobuf/timestamp.proto"), Package: proto.String("google.protobuf")},
			{
				Name:       proto.String("saas/accounts/v1/audit.proto"),
				Package:    proto.String("saas.accounts.v1"),
				Dependency: []string{"google/protobuf/timestamp.proto"},
			},
		},
	}
	source, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	marked, _, err := MarkForeignImports(source, languages.PYTHON)
	if err != nil {
		t.Fatal(err)
	}
	markedSet := &descriptorpb.FileDescriptorSet{}
	if err = proto.Unmarshal(marked, markedSet); err != nil {
		t.Fatal(err)
	}

	stripped := runStrip(t, python, markedSet, "saas/accounts/v1/audit.proto")

	var timestamp *descriptorpb.FileDescriptorProto
	for _, file := range stripped.GetFile() {
		if file.GetName() == "google/protobuf/timestamp.proto" {
			timestamp = file
		}
	}
	if timestamp == nil {
		t.Fatal("the strip dropped the well-known type the target depends on")
	}
	if !isMarkedAsImport(timestamp) {
		t.Fatal("the strip lost buf's is_import marker: buf would generate bindings for the well-known types again")
	}
}
