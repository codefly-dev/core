package docker

import (
	"strings"
	"testing"
)

func TestIsValidDockerImageName(t *testing.T) {
	tests := []struct {
		imageName string
		valid     bool
	}{
		{"examples/counter-go-grpc-nextjs-postgres/backend", true},
	}
	for _, tt := range tests {
		t.Run(tt.imageName, func(t *testing.T) {
			if got := IsValidDockerImageName(tt.imageName); got != tt.valid {
				t.Errorf("IsValidDockerImageName() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestBuildDiagnosticsPreservesCauseAndBoundsOutput(t *testing.T) {
	diagnostics := newBuildDiagnostics(2, 120)
	diagnostics.Add("npm error package-lock is out of sync\n" + strings.Repeat("usage ", 50))
	diagnostics.Add("The command returned a non-zero code")
	got := diagnostics.String()
	if !strings.Contains(got, "package-lock is out of sync") || !strings.Contains(got, "non-zero code") {
		t.Fatalf("diagnostics lost actionable context: %q", got)
	}
	if len(got) > 120 {
		t.Fatalf("diagnostics length = %d, want at most 120", len(got))
	}
}
