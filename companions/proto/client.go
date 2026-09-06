// Package proto drives the proto companion: it turns proto sources or a
// descriptor set into generated client code (message bindings, connect stubs,
// gRPC stubs, OpenAPI types, and gateway-bound facades) by running buf inside
// the companion image.
package proto

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/runners/companion"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// ClientRequest describes a single client-generation run. Exactly one of
// DescriptorSet or Sources must be set.
type ClientRequest struct {
	Language    languages.Language
	Destination string
	// Module is the facade entry-point name; empty derives it from the proto
	// package. It also scopes the generated Go package path.
	Module string
	// Services optionally restricts the facade to a subset of services.
	Services []string
	// Facade turns on the gateway-bound client facade plugin.
	Facade bool

	// DescriptorSet is a serialized google.protobuf.FileDescriptorSet — the
	// contract a module package carries. buf accepts it as a generation input.
	DescriptorSet []byte
	// Sources are .proto files written to the temp dir — today's
	// GenerateGRPC path. Each carries its repo-relative path, not just content:
	// the path decides the generated module/import names, so it must match the
	// descriptor set's file names for the two inputs to produce identical
	// output.
	Sources []Source
}

// Source is a .proto file to generate from: its repo-relative path (e.g.
// "saas/accounts/v1/audit.proto") and its source text.
type Source struct {
	Path    string
	Content []byte
}

// GenerateClient runs buf in the proto companion to generate client code —
// message bindings, connect stubs, and, when Facade is set, a gateway-bound
// facade — from either a descriptor set or proto sources.
func GenerateClient(ctx context.Context, req ClientRequest) error {
	w := wool.Get(ctx).In("generateClient", wool.Field("destination", req.Destination))
	if (len(req.DescriptorSet) == 0) == (len(req.Sources) == 0) {
		return w.NewError("exactly one of DescriptorSet or Sources must be set")
	}

	service := req.Module
	if service == "" {
		service = "client"
	}
	spec := clientSpec{
		language:      req.Language,
		destination:   req.Destination,
		service:       service,
		descriptorSet: req.DescriptorSet,
		facade: FacadeOptions{
			Facade:   req.Facade,
			Services: req.Services,
			Module:   req.Module,
		},
		sources: req.Sources,
	}
	return generateClient(ctx, spec)
}

// clientSpec is the resolved, internal form of a generation run shared by
// GenerateClient and GenerateGRPC.
type clientSpec struct {
	language      languages.Language
	destination   string
	service       string
	sources       []Source
	descriptorSet []byte
	facade        FacadeOptions
}

func generateClient(ctx context.Context, spec clientSpec) error {
	w := wool.Get(ctx).In("generateClient", wool.DirField(spec.destination))

	image, err := CompanionImage(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot get companion image")
	}

	tmpDir, err := os.MkdirTemp("", "proto")
	if err != nil {
		return w.Wrapf(err, "cannot create tmp dir")
	}
	defer func(path string) {
		if rmErr := os.RemoveAll(path); rmErr != nil {
			w.Error("cannot remove tmp dir", wool.Field("path", path))
		}
	}(tmpDir)

	if err = CreateBufConfiguration(ctx, tmpDir, spec.service, spec.language, spec.facade); err != nil {
		return w.Wrapf(err, "cannot create buf configuration")
	}

	for _, src := range spec.sources {
		dest := filepath.Join(tmpDir, filepath.FromSlash(src.Path))
		if err = os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
			return w.Wrapf(err, "cannot create proto directory")
		}
		if err = os.WriteFile(dest, src.Content, 0600); err != nil {
			return w.Wrapf(err, "cannot write proto file")
		}
	}

	var before func(context.Context, companion.CompanionRunner) error
	generateArgs := []string{"generate"}
	depUpdate := len(spec.sources) > 0

	if len(spec.descriptorSet) > 0 {
		if err = os.WriteFile(filepath.Join(tmpDir, "image.binpb"), spec.descriptorSet, 0600); err != nil {
			return w.Wrapf(err, "cannot write descriptor set")
		}
		input := "/workspace/image.binpb"
		// A Python facade must not register shared descriptors (buf.validate,
		// google.api, ...) into the global pool, so strip them from the image
		// before generating the *_pb2 bindings.
		if spec.language == languages.PYTHON && spec.facade.Facade {
			targets, terr := nonSharedFiles(spec.descriptorSet)
			if terr != nil {
				return w.Wrapf(terr, "cannot inspect descriptor set")
			}
			stripped := "/workspace/image.stripped.binpb"
			before = func(ctx context.Context, runner companion.CompanionRunner) error {
				args := append([]string{"/workspace/image.binpb", stripped}, targets...)
				proc, perr := runner.NewProcess("codefly-proto-strip-options", args...)
				if perr != nil {
					return perr
				}
				return proc.Run(ctx)
			}
			input = stripped
		}
		generateArgs = append(generateArgs, input, "--template", "buf.gen.yaml")
	}

	if _, err = shared.CheckDirectoryOrCreate(ctx, spec.destination); err != nil {
		return w.Wrapf(err, "cannot create destination")
	}

	name := fmt.Sprintf("proto-%s-%d-%s", spec.service, time.Now().UnixMilli(), spec.language)
	return runBuf(ctx, name, image, tmpDir, spec.destination, depUpdate, generateArgs, before)
}

// sharedFilePrefixes are the proto files a generated client must never embed
// into its own descriptor pool — well-known types come from the runtime, and
// validation/annotation options are shared descriptors.
var sharedFilePrefixes = []string{"google/", "buf/", "gogoproto/", "grpc/"}

// nonSharedFiles returns the module's own proto files in a descriptor set — the
// strip targets — by dropping the shared dependency files.
func nonSharedFiles(descriptorSet []byte) ([]string, error) {
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(descriptorSet, &set); err != nil {
		return nil, err
	}
	var files []string
	for _, f := range set.GetFile() {
		name := f.GetName()
		shared := false
		for _, prefix := range sharedFilePrefixes {
			if strings.HasPrefix(name, prefix) {
				shared = true
				break
			}
		}
		if !shared {
			files = append(files, name)
		}
	}
	return files, nil
}
