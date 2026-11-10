package resources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func writeProtoNamesFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A contract as `codefly generate client` leaves it in a library.
func writeContract(t *testing.T, dir, file, pkg string, messages ...string) {
	t.Helper()
	fd := &descriptorpb.FileDescriptorProto{Name: proto.String(file), Package: proto.String(pkg)}
	for _, m := range messages {
		fd.MessageType = append(fd.MessageType, &descriptorpb.DescriptorProto{Name: proto.String(m)})
	}
	fd.Service = []*descriptorpb.ServiceDescriptorProto{{Name: proto.String("Service")}}
	wellKnown := &descriptorpb.FileDescriptorProto{Name: proto.String("google/api/annotations.proto"), Package: proto.String("google.api"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Http")}}}
	data, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{fd, wellKnown}})
	if err != nil {
		t.Fatal(err)
	}
	writeProtoNamesFile(t, filepath.Join(dir, "contract", "contract.binpb"), string(data))
}

// The scaffold gives every service proto/api.proto in package api. A service
// that depends on another's client then registers both in one binary, which
// Go refuses at start. The check names it from the sources.
func TestServiceProtoNameCollisions(t *testing.T) {
	own := t.TempDir()
	writeProtoNamesFile(t, filepath.Join(own, "api.proto"), "syntax = \"proto3\";\npackage api;\n\nmessage VersionRequest {\n  message Inner {}\n}\nmessage Call {}\nservice CallsService {}\n")
	writeProtoNamesFile(t, filepath.Join(own, "google", "api", "annotations.proto"), "syntax = \"proto3\";\npackage google.api;\nmessage Http {}\n")
	mine, err := ownProtoNames(own)
	if err != nil {
		t.Fatal(err)
	}
	if !mine.files["api.proto"] || mine.files["google/api/annotations.proto"] || !mine.names["api.VersionRequest"] || !mine.names["api.CallsService"] || mine.names["api.Inner"] {
		t.Fatalf("own names: files %v names %v", mine.files, mine.names)
	}

	same, moved := t.TempDir(), t.TempDir()
	writeContract(t, same, "api.proto", "api", "VersionRequest", "Record")
	writeContract(t, moved, "shop/v1/api.proto", "shop.v1", "VersionRequest", "Record")
	colliding, err := contractProtoNames(filepath.Join(same, "contract"))
	if err != nil {
		t.Fatal(err)
	}
	if got := sharedKeys(mine.files, colliding.files); len(got) != 1 || got[0] != "api.proto" {
		t.Fatalf("the same file path collides: %v", got)
	}
	if got := sharedKeys(mine.names, colliding.names); len(got) != 1 || got[0] != "api.VersionRequest" {
		t.Fatalf("the same full name collides, and google.* never does: %v", got)
	}
	versioned, err := contractProtoNames(filepath.Join(moved, "contract"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sharedKeys(mine.files, versioned.files))+len(sharedKeys(mine.names, versioned.names)) != 0 {
		t.Fatal("a contract in its own package and path collides with nothing")
	}
	// No proto directory, no contract: nothing declared, no error.
	if empty, err := ownProtoNames(filepath.Join(own, "nope")); err != nil || len(empty.files) != 0 {
		t.Fatalf("a missing directory: %v %v", empty, err)
	}
	// A service with no library dependency loads one set of names.
	if got, err := ServiceProtoNameCollisions(context.Background(), &Workspace{}, &Service{}); err != nil || got != nil {
		t.Fatalf("no dependency, no collision: %v %v", got, err)
	}
	described := DescribeProtoNameCollisions([]ProtoNameCollision{
		{What: ProtoCollisionFile, Name: "api.proto", Sources: [2]string{"a", "b"}},
		{What: ProtoCollisionName, Name: "api.A"}, {What: ProtoCollisionName, Name: "api.B"},
		{What: ProtoCollisionName, Name: "api.C"}, {What: ProtoCollisionName, Name: "api.D"}, {What: ProtoCollisionName, Name: "api.E"},
	})
	if !strings.Contains(described, `proto file "api.proto" is declared by a and by b`) || !strings.Contains(described, "and 2 more names") || strings.Contains(described, "api.D") {
		t.Fatalf("description: %s", described)
	}
}

// The whole check on a workspace laid out as codefly lays it: a service that
// keeps the scaffold's contract and depends on a client generated from
// another scaffolded contract.
func TestServiceProtoNameCollisionsInAWorkspace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeProtoNamesFile(t, filepath.Join(root, WorkspaceConfigurationName), "name: test\nlayout: modules\nmodules:\n    - name: phone\n")
	writeProtoNamesFile(t, filepath.Join(root, "modules", "phone", "module.codefly.yaml"), "kind: module\nname: phone\nservices:\n    - name: calls\n")
	serviceDir := filepath.Join(root, "modules", "phone", "services", "calls")
	writeProtoNamesFile(t, filepath.Join(serviceDir, "service.codefly.yaml"),
		"name: calls\nversion: 0.0.0\nagent:\n  kind: codefly:service\n  name: fixture\n  publisher: codefly.dev\n  version: 1.0.0\nlibrary-dependencies:\n    - name: shop-client\n      languages:\n        - go\n")
	writeProtoNamesFile(t, filepath.Join(serviceDir, "proto", "api.proto"), "syntax = \"proto3\";\npackage api;\nmessage VersionRequest {}\n")
	libDir := filepath.Join(root, "libraries", "shop-client")
	writeProtoNamesFile(t, filepath.Join(libDir, LibraryConfigurationName), "kind: library\nname: shop-client\nversion: 0.0.0\n")
	writeContract(t, libDir, "api.proto", "api", "VersionRequest", "Record")

	workspace, err := LoadWorkspaceFromDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	module, err := workspace.LoadModuleFromName(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	service, err := module.LoadServiceFromName(ctx, "calls")
	if err != nil {
		t.Fatal(err)
	}
	collisions, err := ServiceProtoNameCollisions(ctx, workspace, service)
	if err != nil {
		t.Fatal(err)
	}
	described := DescribeProtoNameCollisions(collisions)
	if len(collisions) != 2 || !strings.Contains(described, `proto file "api.proto" is declared by the service's own proto/ and by library shop-client`) || !strings.Contains(described, `"api.VersionRequest"`) {
		t.Fatalf("collisions: %s", described)
	}
	// The producer moved to its own package and path: nothing collides.
	if err := os.RemoveAll(filepath.Join(libDir, "contract")); err != nil {
		t.Fatal(err)
	}
	writeContract(t, libDir, "shop/v1/api.proto", "shop.v1", "VersionRequest", "Record")
	if collisions, err = ServiceProtoNameCollisions(ctx, workspace, service); err != nil || len(collisions) != 0 {
		t.Fatalf("after the move: %v %v", collisions, err)
	}
}
