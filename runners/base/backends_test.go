package base

import (
	"testing"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

func TestBackendSupportResolveBackends(t *testing.T) {
	tests := []struct {
		name            string
		support         BackendSupport
		nixInstalled    bool
		nixSupported    bool
		dockerInstalled bool
		want            []agentv0.Backend_Type
	}{
		{
			name:            "canonical preference order",
			support:         BackendSupport{Local: func() bool { return true }, Nix: true, Docker: true},
			nixInstalled:    true,
			nixSupported:    true,
			dockerInstalled: true,
			want:            []agentv0.Backend_Type{agentv0.Backend_LOCAL, agentv0.Backend_NIX, agentv0.Backend_DOCKER},
		},
		{
			name:            "host availability filters capabilities",
			support:         BackendSupport{Local: func() bool { return false }, Nix: true, Docker: true},
			nixInstalled:    false,
			nixSupported:    true,
			dockerInstalled: false,
		},
		{
			name:            "unsupported OS filters nix",
			support:         BackendSupport{Nix: true, Docker: true},
			nixInstalled:    true,
			nixSupported:    false,
			dockerInstalled: true,
			want:            []agentv0.Backend_Type{agentv0.Backend_DOCKER},
		},
		{
			name:            "undeclared backends stay disabled",
			support:         BackendSupport{},
			nixInstalled:    true,
			nixSupported:    true,
			dockerInstalled: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved := test.support.resolveBackends(
				func() bool { return test.nixInstalled },
				func() bool { return test.nixSupported },
				func() bool { return test.dockerInstalled },
			)
			got := make([]agentv0.Backend_Type, 0, len(resolved))
			for _, backend := range resolved {
				if backend == nil {
					t.Fatal("resolver returned a nil backend")
				}
				got = append(got, backend.Type)
			}
			if len(got) != len(test.want) {
				t.Fatalf("resolved backends = %v, want %v", got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("resolved backends = %v, want %v", got, test.want)
				}
			}
		})
	}
}
