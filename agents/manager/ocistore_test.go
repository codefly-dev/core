package manager

import (
	"testing"

	"github.com/codefly-dev/core/resources"
)

// TestOCIStore_RepoPath locks the OCI repository-path contract. This MUST
// match the path the publisher tooling pushes to (`oras push
// {registry}/{repoPath}:{tag}`). The path is now kind-qualified
// (`{kind}-{name}`), so changing the format silently breaks every pull.
func TestOCIStore_RepoPath(t *testing.T) {
	s := NewOCIStore("registry.example.com", "https", nil)
	cases := []struct {
		agent *resources.Agent
		want  string
	}{
		{
			agent: &resources.Agent{Kind: "codefly:service", Publisher: "codefly.dev", Name: "go-grpc", Version: "0.0.147"},
			want:  "agents/codefly.dev/service-go-grpc",
		},
		{
			agent: &resources.Agent{Kind: "codefly:module", Publisher: "codefly.dev", Name: "user-management", Version: "1.2.3"},
			want:  "agents/codefly.dev/module-user-management",
		},
	}
	for _, tc := range cases {
		got, err := s.repoPath(tc.agent)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("repoPath(%+v) = %q, want %q", tc.agent, got, tc.want)
		}
	}
	if _, err := s.repoPath(&resources.Agent{Kind: "service", Name: "bare", Version: "0.1.0"}); err == nil {
		t.Fatal("unregistered kind must fail closed")
	}
}
