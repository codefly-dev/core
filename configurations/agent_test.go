package configurations_test

import (
	"embed"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
)

func TestAgentParse(t *testing.T) {
	tcs := []struct {
		name string
		in   string
		out  *configurations.Agent
	}{
		{name: "identifier only", in: "go-grpc", out: &configurations.Agent{Kind: configurations.AgentService, Publisher: "codefly.ai", Identifier: "go-grpc", Version: "latest"}},
		{name: "identifier with publisher", in: "go-grpc:0.0.0", out: &configurations.Agent{Kind: configurations.AgentService, Publisher: "codefly.ai", Identifier: "go-grpc", Version: "0.0.0"}},
		{name: "full specification", in: "codefly.ai/go-grpc:0.0.0", out: &configurations.Agent{Kind: configurations.AgentService, Publisher: "codefly.ai", Identifier: "go-grpc", Version: "0.0.0"}},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			p, err := configurations.ParseAgent(configurations.AgentService, tc.in)
			if err != nil && !shared.IsUserWarning(err) {
				t.Fatal(err)
			}
			if p.Kind != tc.out.Kind {
				t.Fatalf("expected kind %s, got %s", tc.out.Kind, p.Kind)
			}
			if p.Publisher != tc.out.Publisher {
				t.Fatalf("expected publisher %s, got %s", tc.out.Publisher, p.Publisher)
			}
			if p.Identifier != tc.out.Identifier {
				t.Fatalf("expected identifier %s, got %s", tc.out.Identifier, p.Identifier)
			}
			if p.Version != tc.out.Version {
				t.Fatalf("expected version %s, got %s", tc.out.Version, p.Version)
			}
		})
	}
}

func TestAgentLoadDir(t *testing.T) {
	p := configurations.LoadAgentConfiguration(shared.NewDirReader().At("testdata"))
	assert.Equal(t, "codefly.ai", p.Publisher)
	assert.Equal(t, "go", p.Identifier)
	assert.Equal(t, "0.0.0", p.Version)

	patch, err := p.Patch()
	assert.NoError(t, err)
	assert.Equal(t, "0.0.1", patch.Version)
}

func TestAgentLoadEmbed(t *testing.T) {
	p := configurations.LoadAgentConfiguration(shared.Embed(info).At("testdata"))
	assert.Equal(t, "codefly.ai", p.Publisher)
}

//go:embed testdata/agent.codefly.yaml
var info embed.FS