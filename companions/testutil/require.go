package testutil

import (
	"context"
	"os/exec"
	"testing"

	"github.com/codefly-dev/core/companions/golang"
	"github.com/codefly-dev/core/companions/proto"
	runners "github.com/codefly-dev/core/runners/base"
)

// BuildCompanionsHint is the message to show when a companion image is missing.
const BuildCompanionsHint = "run ./companions/scripts/build_companions.sh from core/"

// RequireDocker marks the test as failed if Docker is not running.
func RequireDocker(t *testing.T, ctx context.Context) {
	t.Helper()
	if !runners.DockerEngineRunning(ctx) {
		t.Fatalf("Docker must be running for this test (%s)", BuildCompanionsHint)
	}
}

// RequireProtoImage skips the test if the proto companion image is not built.
func RequireProtoImage(t *testing.T, ctx context.Context) {
	t.Helper()
	RequireDocker(t, ctx)
	img, err := proto.CompanionImage(ctx)
	if err != nil {
		t.Skipf("cannot get proto companion image: %v (%s)", err, BuildCompanionsHint)
	}
	if img == nil {
		t.Skipf("proto companion image not configured (%s)", BuildCompanionsHint)
	}
	ref := img.Name + ":" + img.Tag
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", ref).Run(); err != nil {
		t.Skipf("proto companion image %s not built: %v (%s)", ref, err, BuildCompanionsHint)
	}
}

// RequireGoImage skips the test if the Go companion image is not built.
func RequireGoImage(t *testing.T, ctx context.Context) {
	t.Helper()
	RequireDocker(t, ctx)
	img, err := golang.CompanionImage(ctx)
	if err != nil {
		t.Skipf("cannot get go companion image: %v (%s)", err, BuildCompanionsHint)
	}
	if img == nil {
		t.Skipf("go companion image not configured (%s)", BuildCompanionsHint)
	}
	ref := img.Name + ":" + img.Tag
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", ref).Run(); err != nil {
		t.Skipf("go companion image %s not built: %v (%s)", ref, err, BuildCompanionsHint)
	}
}
