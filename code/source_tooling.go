package code

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/failures"
	basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"
	codev1 "github.com/codefly-dev/core/generated/go/codefly/services/code/v1"
	toolingv1 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v1"
)

// Executor is the narrow Code contract required by SourceTooling. Language
// plugins can embed SourceTooling and provide only an in-memory SourceFixer;
// core owns the repetitive Tooling-to-Code translation and failure mapping.
type Executor interface {
	Execute(context.Context, *codev1.CodeRequest) (*codev1.CodeResponse, error)
}

// SourceTooling implements the source authoring portion of Tooling over Code.
// Plugins with dependency, build, test, or lint behavior can embed it and add
// those methods; formatter-only plugins can register it directly.
type SourceTooling struct {
	toolingv1.UnimplementedToolingServer
	code Executor
}

func NewSourceTooling(code Executor) *SourceTooling {
	return &SourceTooling{code: code}
}

func (t *SourceTooling) Fix(ctx context.Context, req *toolingv1.FixRequest) (*toolingv1.FixResponse, error) {
	response, err := t.code.Execute(ctx, &codev1.CodeRequest{Operation: &codev1.CodeRequest_Fix{Fix: &codev1.FixRequest{
		File: req.GetFile(), Mode: req.GetMode(), DryRun: req.GetDryRun(),
	}}})
	if err != nil {
		return nil, fmt.Errorf("tooling fix: %w", err)
	}
	fix := response.GetFix()
	if fix == nil {
		return &toolingv1.FixResponse{Failure: failures.Ensure(response.GetFailure(), basev1.FailureCode_FAILURE_CODE_INTERNAL, "tooling.fix", "code service returned no fix result")}, nil
	}
	return &toolingv1.FixResponse{
		Success: fix.GetSuccess(), Content: fix.GetContent(), Actions: fix.GetActions(),
		Failure: failures.Clone(response.GetFailure()), Changed: fix.GetChanged(),
		BeforeSha256: fix.GetBeforeSha256(), AfterSha256: fix.GetAfterSha256(),
		Wrote: fix.GetWrote(), Output: fix.GetOutput(),
		BeforeSizeBytes: fix.GetBeforeSizeBytes(), AfterSizeBytes: fix.GetAfterSizeBytes(),
	}, nil
}

func (t *SourceTooling) ApplyEdit(ctx context.Context, req *toolingv1.ApplyEditRequest) (*toolingv1.ApplyEditResponse, error) {
	response, err := t.code.Execute(ctx, &codev1.CodeRequest{Operation: &codev1.CodeRequest_ApplyEdit{ApplyEdit: &codev1.ApplyEditRequest{
		File: req.GetFile(), Find: req.GetFind(), Replace: req.GetReplace(),
		FixMode: req.GetFixMode(), DryRun: req.GetDryRun(),
	}}})
	if err != nil {
		return nil, fmt.Errorf("tooling apply edit: %w", err)
	}
	edit := response.GetApplyEdit()
	if edit == nil {
		return &toolingv1.ApplyEditResponse{Failure: failures.Ensure(response.GetFailure(), basev1.FailureCode_FAILURE_CODE_INTERNAL, "tooling.apply-edit", "code service returned no apply-edit result")}, nil
	}
	return &toolingv1.ApplyEditResponse{
		Success: edit.GetSuccess(), Content: edit.GetContent(), Strategy: edit.GetStrategy(),
		FixActions: edit.GetFixActions(), Failure: failures.Clone(response.GetFailure()),
		Changed: edit.GetChanged(), BeforeSha256: edit.GetBeforeSha256(),
		AfterSha256: edit.GetAfterSha256(), Wrote: edit.GetWrote(), Output: edit.GetOutput(),
		BeforeSizeBytes: edit.GetBeforeSizeBytes(), AfterSizeBytes: edit.GetAfterSizeBytes(),
	}, nil
}

func (t *SourceTooling) GetProjectInfo(ctx context.Context, _ *toolingv1.GetProjectInfoRequest) (*toolingv1.GetProjectInfoResponse, error) {
	response, err := t.code.Execute(ctx, &codev1.CodeRequest{Operation: &codev1.CodeRequest_GetProjectInfo{GetProjectInfo: &codev1.GetProjectInfoRequest{}}})
	if err != nil {
		return nil, fmt.Errorf("tooling get project info: %w", err)
	}
	info := response.GetGetProjectInfo()
	if info == nil {
		return &toolingv1.GetProjectInfoResponse{Failure: failures.Ensure(response.GetFailure(), basev1.FailureCode_FAILURE_CODE_INTERNAL, "tooling.get-project-info", "code service returned no project-info result")}, nil
	}
	packages := make([]*toolingv1.PackageInfo, 0, len(info.GetPackages()))
	for _, pkg := range info.GetPackages() {
		packages = append(packages, &toolingv1.PackageInfo{
			Name: pkg.GetName(), RelativePath: pkg.GetRelativePath(), Files: append([]string(nil), pkg.GetFiles()...),
			Imports: append([]string(nil), pkg.GetImports()...), Doc: pkg.GetDoc(),
		})
	}
	dependencies := make([]*toolingv1.Dependency, 0, len(info.GetDependencies()))
	for _, dependency := range info.GetDependencies() {
		dependencies = append(dependencies, &toolingv1.Dependency{
			Name: dependency.GetName(), Version: dependency.GetVersion(), Direct: dependency.GetDirect(),
		})
	}
	sourceFiles := make([]*toolingv1.SourceFileInfo, 0, len(info.GetSourceFiles()))
	for _, file := range info.GetSourceFiles() {
		sourceFiles = append(sourceFiles, &toolingv1.SourceFileInfo{Path: file.GetPath(), Imports: append([]string(nil), file.GetImports()...)})
	}
	return &toolingv1.GetProjectInfoResponse{
		Module: info.GetModule(), Language: info.GetLanguage(),
		LanguageVersion: info.GetLanguageVersion(), Packages: packages, Dependencies: dependencies,
		FileHashes: info.GetFileHashes(), SourceFiles: sourceFiles, Failure: failures.Clone(response.GetFailure()),
	}, nil
}

// GetSemanticIndex delegates project inspection to Code and converts only the
// transport envelope. The shared semantic messages are not copied, parsed, or
// interpreted by the Tooling adapter.
func (t *SourceTooling) GetSemanticIndex(ctx context.Context, _ *toolingv1.GetSemanticIndexRequest) (*toolingv1.GetSemanticIndexResponse, error) {
	response, err := t.code.Execute(ctx, &codev1.CodeRequest{Operation: &codev1.CodeRequest_GetSemanticIndex{GetSemanticIndex: &codev1.GetSemanticIndexRequest{}}})
	if err != nil {
		return nil, fmt.Errorf("tooling get semantic index: %w", err)
	}
	index := response.GetGetSemanticIndex()
	if index == nil {
		return &toolingv1.GetSemanticIndexResponse{Failure: failures.Ensure(response.GetFailure(), basev1.FailureCode_FAILURE_CODE_INTERNAL, "tooling.get-semantic-index", "code service returned no semantic index")}, nil
	}
	return &toolingv1.GetSemanticIndexResponse{Index: index, Failure: failures.Clone(response.GetFailure())}, nil
}

// GetInstructionIndex delegates document discovery and Markdown parsing to
// Code. Tooling translates only the envelope; typed records remain unchanged.
func (t *SourceTooling) GetInstructionIndex(ctx context.Context, _ *toolingv1.GetInstructionIndexRequest) (*toolingv1.GetInstructionIndexResponse, error) {
	response, err := t.code.Execute(ctx, &codev1.CodeRequest{Operation: &codev1.CodeRequest_GetInstructionIndex{GetInstructionIndex: &codev1.GetInstructionIndexRequest{}}})
	if err != nil {
		return nil, fmt.Errorf("tooling get instruction index: %w", err)
	}
	index := response.GetGetInstructionIndex()
	if index == nil {
		return &toolingv1.GetInstructionIndexResponse{Failure: failures.Ensure(response.GetFailure(), basev1.FailureCode_FAILURE_CODE_INTERNAL, "tooling.get-instruction-index", "code service returned no instruction index")}, nil
	}
	return &toolingv1.GetInstructionIndexResponse{Index: index, Failure: failures.Clone(response.GetFailure())}, nil
}
