package resources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// ProtoNameCollision is one protobuf name two sources of a service declare.
//
// A service's binary loads its own protobuf bindings and those of every
// client library it depends on, and a protobuf runtime keeps one registry
// per process: two files registered under one path, or two declarations with
// one full name, are refused. In Go that is a panic at start
// (`proto: file "api.proto" is already registered`), before any code of the
// service runs. Nothing at build time says so, which is why it is checked
// here, from the sources, before a run, a lint or a test.
type ProtoNameCollision struct {
	// What collides: ProtoCollisionFile (a proto file path) or
	// ProtoCollisionName (a message, enum or service full name).
	What string
	// Name is the path (api.proto) or the full name (api.VersionRequest).
	Name string
	// Sources are the two that declare it, as protoSource.label names them.
	Sources [2]string
}

const (
	ProtoCollisionFile = "file"
	ProtoCollisionName = "name"
)

func (c ProtoNameCollision) String() string {
	if c.What == ProtoCollisionFile {
		return fmt.Sprintf("proto file %q is declared by %s and by %s", c.Name, c.Sources[0], c.Sources[1])
	}
	return fmt.Sprintf("%q is declared by %s and by %s", c.Name, c.Sources[0], c.Sources[1])
}

// protoSource is the protobuf names one origin declares.
type protoSource struct {
	label string
	files map[string]bool
	names map[string]bool
}

// ServiceProtoNameCollisions lists the protobuf names a service's binary
// would register twice: between its own proto files (proto/ in its
// directory) and the contract of each client library it depends on, and
// between two such libraries. Well-known and vendored packages (google.*,
// buf.*, grpc.*) are shared by design and never reported.
func ServiceProtoNameCollisions(ctx context.Context, workspace *Workspace, service *Service) ([]ProtoNameCollision, error) {
	if workspace == nil || service == nil || len(service.LibraryDependencies) == 0 {
		return nil, nil
	}
	var sources []protoSource
	own, err := ownProtoNames(filepath.Join(service.Dir(), "proto"))
	if err != nil {
		return nil, fmt.Errorf("cannot read the proto files of %s: %w", service.Name, err)
	}
	own.label = "the service's own proto/"
	sources = append(sources, own)
	for _, dep := range service.LibraryDependencies {
		library, err := workspace.LoadLibraryFromName(ctx, dep.Name)
		if err != nil {
			// A library that cannot be loaded is another check's finding.
			continue
		}
		contract, err := contractProtoNames(filepath.Join(library.Dir(), "contract"))
		if err != nil {
			return nil, fmt.Errorf("cannot read the contract of library %s: %w", dep.Name, err)
		}
		contract.label = "library " + dep.Name
		sources = append(sources, contract)
	}
	var out []ProtoNameCollision
	for i := range sources {
		for j := i + 1; j < len(sources); j++ {
			a, b := sources[i], sources[j]
			for _, file := range sharedKeys(a.files, b.files) {
				out = append(out, ProtoNameCollision{What: ProtoCollisionFile, Name: file, Sources: [2]string{a.label, b.label}})
			}
			for _, name := range sharedKeys(a.names, b.names) {
				out = append(out, ProtoNameCollision{What: ProtoCollisionName, Name: name, Sources: [2]string{a.label, b.label}})
			}
		}
	}
	return out, nil
}

// DescribeProtoNameCollisions is the collisions as one message: every file,
// and the first few names (a shared package collides on each of its
// declarations, and listing forty of them says nothing more).
func DescribeProtoNameCollisions(collisions []ProtoNameCollision) string {
	var lines []string
	names := 0
	for _, c := range collisions {
		if c.What == ProtoCollisionName {
			if names++; names > 3 {
				continue
			}
		}
		lines = append(lines, c.String())
	}
	if names > 3 {
		lines = append(lines, fmt.Sprintf("and %d more names", names-3))
	}
	return strings.Join(lines, "; ")
}

func sharedKeys(a, b map[string]bool) []string {
	var out []string
	for key := range a {
		if b[key] {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// sharedProtoPackage reports a package every binary shares by design.
func sharedProtoPackage(pkg string) bool {
	for _, prefix := range []string{"google.", "buf.", "grpc.", "codefly."} {
		if strings.HasPrefix(pkg+".", prefix) {
			return true
		}
	}
	return false
}

var (
	protoPackageLine     = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z_][\w.]*)\s*;`)
	protoDeclarationLine = regexp.MustCompile(`(?m)^(?:message|enum|service)\s+([A-Za-z_]\w*)`)
)

// ownProtoNames reads a service's proto sources: each file's path under the
// proto root (what the generated code registers it as) and its top-level
// declarations. A missing directory declares nothing.
func ownProtoNames(root string) (protoSource, error) {
	source := protoSource{files: map[string]bool{}, names: map[string]bool{}}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) && path == root {
				return filepath.SkipAll
			}
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".proto" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		pkg := ""
		if m := protoPackageLine.FindSubmatch(content); m != nil {
			pkg = string(m[1])
		}
		if sharedProtoPackage(pkg) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		source.files[filepath.ToSlash(rel)] = true
		for _, m := range protoDeclarationLine.FindAllSubmatch(content, -1) {
			source.names[fullProtoName(pkg, string(m[1]))] = true
		}
		return nil
	})
	return source, err
}

// contractProtoNames reads a client library's contract: every serialized
// FileDescriptorSet (contract.binpb) under its contract directory.
func contractProtoNames(root string) (protoSource, error) {
	source := protoSource{files: map[string]bool{}, names: map[string]bool{}}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) && path == root {
				return filepath.SkipAll
			}
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".binpb" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var set descriptorpb.FileDescriptorSet
		if err := proto.Unmarshal(content, &set); err != nil {
			return fmt.Errorf("%s is not a file descriptor set: %w", path, err)
		}
		for _, file := range set.GetFile() {
			pkg := file.GetPackage()
			if sharedProtoPackage(pkg) {
				continue
			}
			source.files[file.GetName()] = true
			for _, m := range file.GetMessageType() {
				source.names[fullProtoName(pkg, m.GetName())] = true
			}
			for _, e := range file.GetEnumType() {
				source.names[fullProtoName(pkg, e.GetName())] = true
			}
			for _, s := range file.GetService() {
				source.names[fullProtoName(pkg, s.GetName())] = true
			}
		}
		return nil
	})
	return source, err
}

func fullProtoName(pkg, name string) string {
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}
