package configurations_test

import (
	"embed"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
)

func TestAgentParse(t *testing.T) {
	ctx := shared.NewContext()
	tcs := []struct {
		name string
		in   string
		out  *configurations.Agent
	}{
		{name: "empty", in: "", out: nil},
		{name: "identifier only", in: "go-grpc", out: &configurations.Agent{Kind: configurations.ServiceAgent, Publisher: "codefly.dev", Name: "go-grpc", Version: "latest"}},
		{name: "identifier with publisher", in: "go-grpc:0.0.0", out: &configurations.Agent{Kind: configurations.ServiceAgent, Publisher: "codefly.dev", Name: "go-grpc", Version: "0.0.0"}},
		{name: "full specification", in: "codefly.dev/go-grpc:0.0.0", out: &configurations.Agent{Kind: configurations.ServiceAgent, Publisher: "codefly.dev", Name: "go-grpc", Version: "0.0.0"}},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			agent, err := configurations.ParseAgent(ctx, configurations.ServiceAgent, tc.in)
			if agent == nil {
				assert.Error(t, err)
				return
			}
			assert.Equal(t, tc.out.Kind, agent.Kind)
			assert.Equal(t, tc.out.Publisher, agent.Publisher)
			assert.Equal(t, tc.out.Name, agent.Name)
			assert.Equal(t, tc.out.Version, agent.Version)
		})
	}
}

func TestAgentLoadDir(t *testing.T) {
	p, err := configurations.LoadFromFs[configurations.Agent](shared.NewDirReader().At("testdata"))
	assert.NoError(t, err)
	assert.Equal(t, "codefly.dev", p.Publisher)
	assert.Equal(t, "go", p.Name)
	assert.Equal(t, "0.0.0", p.Version)

	patch, err := p.Patch()
	assert.NoError(t, err)
	assert.Equal(t, "0.0.1", patch.Version)
}

func TestAgentLoadEmbed(t *testing.T) {
	p, err := configurations.LoadFromFs[configurations.Agent](shared.Embed(info).At("testdata"))
	assert.NoError(t, err)
	assert.Equal(t, "codefly.dev", p.Publisher)
}

//go:embed testdata/agent.codefly.yaml
var info embed.FS
