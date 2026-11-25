package code

import (
	"context"
	"testing"

	basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"
	codev1 "github.com/codefly-dev/core/generated/go/codefly/services/code/v1"
	toolingv1 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v1"
)

type sourceToolingExecutor func(context.Context, *codev1.CodeRequest) (*codev1.CodeResponse, error)

func (f sourceToolingExecutor) Execute(ctx context.Context, req *codev1.CodeRequest) (*codev1.CodeResponse, error) {
	return f(ctx, req)
}

func TestSourceToolingPreservesFixContract(t *testing.T) {
	tooling := NewSourceTooling(sourceToolingExecutor(func(_ context.Context, req *codev1.CodeRequest) (*codev1.CodeResponse, error) {
		fix := req.GetFix()
		if fix.GetMode() != basev1.FixMode_FIX_MODE_AGGRESSIVE || !fix.GetDryRun() {
			t.Fatalf("request = %+v", fix)
		}
		return &codev1.CodeResponse{Result: &codev1.CodeResponse_Fix{Fix: &codev1.FixResponse{
			Success: true, Content: "fixed", Actions: []string{"formatter"}, Changed: true,
			BeforeSha256: "before", AfterSha256: "after", Output: "diagnostic",
			BeforeSizeBytes: 6, AfterSizeBytes: 5,
		}}}, nil
	}))

	response, err := tooling.Fix(context.Background(), &toolingv1.FixRequest{
		File: "main.rs", Mode: basev1.FixMode_FIX_MODE_AGGRESSIVE, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.GetSuccess() || !response.GetChanged() || response.GetAfterSha256() != "after" || response.GetOutput() != "diagnostic" || response.GetBeforeSizeBytes() != 6 || response.GetAfterSizeBytes() != 5 {
		t.Fatalf("response = %+v", response)
	}
}
