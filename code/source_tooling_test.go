package code

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
)

type sourceToolingExecutor func(context.Context, *codev0.CodeRequest) (*codev0.CodeResponse, error)

func (f sourceToolingExecutor) Execute(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	return f(ctx, req)
}

func TestSourceToolingPreservesFixContract(t *testing.T) {
	tooling := NewSourceTooling(sourceToolingExecutor(func(_ context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
		fix := req.GetFix()
		if fix.GetMode() != basev0.FixMode_FIX_MODE_AGGRESSIVE || !fix.GetDryRun() {
			t.Fatalf("request = %+v", fix)
		}
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_Fix{Fix: &codev0.FixResponse{
			Success: true, Content: "fixed", Actions: []string{"formatter"}, Changed: true,
			BeforeSha256: "before", AfterSha256: "after", Output: "diagnostic",
		}}}, nil
	}))

	response, err := tooling.Fix(context.Background(), &toolingv0.FixRequest{
		File: "main.rs", Mode: basev0.FixMode_FIX_MODE_AGGRESSIVE, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.GetSuccess() || !response.GetChanged() || response.GetAfterSha256() != "after" || response.GetOutput() != "diagnostic" {
		t.Fatalf("response = %+v", response)
	}
}
