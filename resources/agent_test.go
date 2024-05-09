package resources_test

import (
	"context"
	"embed"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
)

func TestAgentParse(t *testing.T) {
	ctx := context.Background()
	tcs := []struct {
		name string
		in   string
		out  *resources.Agent
	}{
		{name: "empty", in: "", out: nil},
		{name: "identifier only", in: "go-grpc", out: &resources.Agent{Kind: resources.ServiceAgent, Publisher: "codefly.dev", Name: "go-grpc", Version: "latest"}},
		{name: "identifier with publisher", in: "go-grpc:0.0.0", out: &resources.Agent{Kind: resources.ServiceAgent, Publisher: "codefly.dev", Name: "go-grpc", Version: "0.0.0"}},
		{name: "full specification", in: "codefly.dev/go-grpc:0.0.0", out: &resources.Agent{Kind: resources.ServiceAgent, Publisher: "codefly.dev", Name: "go-grpc", Version: "0.0.0"}},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			agent, err := resources.ParseAgent(ctx, resources.ServiceAgent, tc.in)
			if agent == nil {
				require.Error(t, err)
				return
			}
			require.Equal(t, tc.out.Kind, agent.Kind)
			require.Equal(t, tc.out.Publisher, agent.Publisher)
			require.Equal(t, tc.out.Name, agent.Name)
			require.Equal(t, tc.out.Version, agent.Version)
		})
	}
}

func TestAgentLoadDir(t *testing.T) {
	p, err := resources.LoadFromFs[resources.Agent](shared.NewDirReader().At("testdata"))
	require.NoError(t, err)
	require.Equal(t, "codefly.dev", p.Publisher)
	require.Equal(t, "go", p.Name)
	require.Equal(t, "0.0.0", p.Version)

	patch, err := p.Patch()
	require.NoError(t, err)
	require.Equal(t, "0.0.1", patch.Version)
}

func TestAgentLoadEmbed(t *testing.T) {
	p, err := resources.LoadFromFs[resources.Agent](shared.Embed(info).At("testdata"))
	require.NoError(t, err)
	require.Equal(t, "codefly.dev", p.Publisher)
}

//go:embed testdata/agent.codefly.yaml
var info embed.FS
