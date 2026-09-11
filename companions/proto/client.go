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
	// TargetFiles names the proto files the generated library will own. Required
	// with DescriptorSet when generating a Python Facade: the strip step keeps
	// exactly these and drops every import reached only through options
	// (buf.validate, google.api, org-shared options, ...), so that the generated
	// *_pb2 never registers a shared descriptor a sibling SDK also carries.
	// A file whose types these reference must be listed too — the strip refuses
	// an incomplete set by name rather than pulling the file in, because what
	// the library contains is this caller's decision, not the strip's. Ignored
	// for the Sources path (the sources are the targets) and for non-Python or
	// non-facade generation.
	TargetFiles []string
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
		targetFiles:   req.TargetFiles,
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
	targetFiles   []string
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

	// Mark before rendering the buf configuration: the go_package of every file
	// the image carries only as an import has to be pinned in buf.gen.yaml, or
	// managed mode rewrites it to this library's own path and the module's
	// bindings import a package nothing generated.
	var markedDescriptorSet []byte
	var goPackageOverrides map[string]string
	if len(spec.descriptorSet) > 0 {
		var merr error
		if markedDescriptorSet, goPackageOverrides, merr = MarkForeignImports(spec.descriptorSet, spec.language); merr != nil {
			return w.Wrapf(merr, "cannot mark foreign imports in descriptor set")
		}
	}

	if err = CreateBufConfiguration(ctx, tmpDir, spec.service, spec.language, spec.facade,
		WithGoPackageOverrides(goPackageOverrides)); err != nil {
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
		// buf takes every file of a plain FileDescriptorSet as a generation
		// target, imports included, so hand it the marked image: the foreign
		// namespaces are not ours to generate.
		if err = os.WriteFile(filepath.Join(tmpDir, "image.binpb"), markedDescriptorSet, 0600); err != nil {
			return w.Wrapf(err, "cannot write descriptor set")
		}
		input := "/workspace/image.binpb"
		// A Python facade must not register shared descriptors (buf.validate,
		// google.api, org-shared options, ...) into the global pool, so strip
		// the image down to the module's own files before generating the *_pb2
		// bindings. The caller names those files; guessing them (e.g. by path
		// prefix) silently keeps any shared proto that doesn't match the guess.
		if spec.language == languages.PYTHON && spec.facade.Facade {
			if len(spec.targetFiles) == 0 {
				return w.NewError("TargetFiles is required for a Python facade from a descriptor set: the strip step needs the module's own proto file names")
			}
			stripped := "/workspace/image.stripped.binpb"
			targets := spec.targetFiles
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

	if err = removeForeignOutput(ctx, spec.destination); err != nil {
		return w.Wrapf(err, "cannot remove stale foreign bindings")
	}

	name := fmt.Sprintf("proto-%s-%d-%s", spec.service, time.Now().UnixMilli(), spec.language)
	return runBuf(ctx, name, image, tmpDir, spec.destination, depUpdate, generateArgs, before)
}

// removeForeignOutput deletes the generated trees for the namespaces the
// generated library never owns. buf writes into destination but nothing empties
// it, so a library generated before those namespaces were carried as imports
// keeps its stale google/ and buf/validate/ bindings: the build stays broken (or
// the descriptors stay in the pool) even though the current run emits nothing
// there, and the fix looks like it did not work.
//
// Only the namespace roots that MarkForeignImports considers are removed, and
// they are removed before buf runs, so a caller that still owns protos under
// those paths — a Sources-path caller, or a TypeScript library that keeps its
// google/api and buf/validate copies — has them regenerated in the same run.
// Reclaiming them unconditionally is what keeps a namespace a contract has
// stopped importing from living in the destination forever.
func removeForeignOutput(ctx context.Context, destination string) error {
	w := wool.Get(ctx).In("removeForeignOutput", wool.DirField(destination))
	for _, ns := range foreignNamespaces {
		path := filepath.Join(destination, filepath.FromSlash(strings.TrimSuffix(ns.pathPrefix, "/")))
		if err := os.RemoveAll(path); err != nil {
			return w.Wrapf(err, "cannot remove %s", path)
		}
	}
	return nil
}
