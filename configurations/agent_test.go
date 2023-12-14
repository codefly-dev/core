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
		{name: "identifier only", in: "go-grpc", out: &configurations.Agent{Kind: configurations.ServiceAgent, Publisher: "codefly.dev", Name: "go-grpc", Version: "latest"}},
		{name: "identifier with publisher", in: "go-grpc:0.0.0", out: &configurations.Agent{Kind: configurations.ServiceAgent, Publisher: "codefly.dev", Name: "go-grpc", Version: "0.0.0"}},
		{name: "full specification", in: "codefly.dev/go-grpc:0.0.0", out: &configurations.Agent{Kind: configurations.ServiceAgent, Publisher: "codefly.dev", Name: "go-grpc", Version: "0.0.0"}},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			p, err := configurations.ParseAgent(ctx, configurations.ServiceAgent, tc.in)
			if err != nil && !shared.IsUserWarning(err) {
				t.Fatal(err)
			}

			if *shared.Must(configurations.AgentKindFromProto(p.Kind)) != tc.out.Kind {
				t.Fatalf("expected kind %s, got %s", tc.out.Kind, p.Kind)
			}
			if p.Publisher != tc.out.Publisher {
				t.Fatalf("expected publisher %s, got %s", tc.out.Publisher, p.Publisher)
			}
			if p.Name != tc.out.Name {
				t.Fatalf("expected identifier %s, got %s", tc.out.Name, p.Name)
			}
			if p.Version != tc.out.Version {
				t.Fatalf("expected version %s, got %s", tc.out.Version, p.Version)
			}
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
