package code

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/failures"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
)

// Executor is the narrow Code contract required by SourceTooling. Language
// plugins can embed SourceTooling and provide only an in-memory SourceFixer;
// core owns the repetitive Tooling-to-Code translation and failure mapping.
type Executor interface {
	Execute(context.Context, *codev0.CodeRequest) (*codev0.CodeResponse, error)
}

// SourceTooling implements the source authoring portion of Tooling over Code.
// Plugins with dependency, build, test, or lint behavior can embed it and add
// those methods; formatter-only plugins can register it directly.
type SourceTooling struct {
	toolingv0.UnimplementedToolingServer
	code Executor
}

func NewSourceTooling(code Executor) *SourceTooling {
	return &SourceTooling{code: code}
}

func (t *SourceTooling) Fix(ctx context.Context, req *toolingv0.FixRequest) (*toolingv0.FixResponse, error) {
	response, err := t.code.Execute(ctx, &codev0.CodeRequest{Operation: &codev0.CodeRequest_Fix{Fix: &codev0.FixRequest{
		File: req.GetFile(), Mode: req.GetMode(), DryRun: req.GetDryRun(),
	}}})
	if err != nil {
		return nil, fmt.Errorf("tooling fix: %w", err)
	}
	fix := response.GetFix()
	if fix == nil {
		return &toolingv0.FixResponse{Failure: failures.Ensure(response.GetFailure(), basev0.FailureCode_FAILURE_CODE_INTERNAL, "tooling.fix", "code service returned no fix result")}, nil
	}
	return &toolingv0.FixResponse{
		Success: fix.GetSuccess(), Content: fix.GetContent(), Actions: fix.GetActions(),
		Failure: failures.Clone(response.GetFailure()), Changed: fix.GetChanged(),
		BeforeSha256: fix.GetBeforeSha256(), AfterSha256: fix.GetAfterSha256(),
		Wrote: fix.GetWrote(), Output: fix.GetOutput(),
	}, nil
}

func (t *SourceTooling) ApplyEdit(ctx context.Context, req *toolingv0.ApplyEditRequest) (*toolingv0.ApplyEditResponse, error) {
	response, err := t.code.Execute(ctx, &codev0.CodeRequest{Operation: &codev0.CodeRequest_ApplyEdit{ApplyEdit: &codev0.ApplyEditRequest{
		File: req.GetFile(), Find: req.GetFind(), Replace: req.GetReplace(),
		FixMode: req.GetFixMode(), DryRun: req.GetDryRun(),
	}}})
	if err != nil {
		return nil, fmt.Errorf("tooling apply edit: %w", err)
	}
	edit := response.GetApplyEdit()
	if edit == nil {
		return &toolingv0.ApplyEditResponse{Failure: failures.Ensure(response.GetFailure(), basev0.FailureCode_FAILURE_CODE_INTERNAL, "tooling.apply-edit", "code service returned no apply-edit result")}, nil
	}
	return &toolingv0.ApplyEditResponse{
		Success: edit.GetSuccess(), Content: edit.GetContent(), Strategy: edit.GetStrategy(),
		FixActions: edit.GetFixActions(), Failure: failures.Clone(response.GetFailure()),
		Changed: edit.GetChanged(), BeforeSha256: edit.GetBeforeSha256(),
		AfterSha256: edit.GetAfterSha256(), Wrote: edit.GetWrote(), Output: edit.GetOutput(),
	}, nil
}

func (t *SourceTooling) GetProjectInfo(ctx context.Context, _ *toolingv0.GetProjectInfoRequest) (*toolingv0.GetProjectInfoResponse, error) {
	response, err := t.code.Execute(ctx, &codev0.CodeRequest{Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}}})
	if err != nil {
		return nil, fmt.Errorf("tooling get project info: %w", err)
	}
	info := response.GetGetProjectInfo()
	if info == nil {
		return &toolingv0.GetProjectInfoResponse{Failure: failures.Ensure(response.GetFailure(), basev0.FailureCode_FAILURE_CODE_INTERNAL, "tooling.get-project-info", "code service returned no project-info result")}, nil
	}
	return &toolingv0.GetProjectInfoResponse{
		Module: info.GetModule(), Language: info.GetLanguage(),
		LanguageVersion: info.GetLanguageVersion(), FileHashes: info.GetFileHashes(),
		Failure: failures.Clone(response.GetFailure()),
	}, nil
}
