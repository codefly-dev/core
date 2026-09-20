package resources_test

import (
	"context"
	"embed"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
)

func TestAgentIdentityBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, registration := range resources.AgentKindRegistry() {
		t.Run(string(registration.Resource), func(t *testing.T) {
			for _, component := range []string{"publisher", "name", "version"} {
				for _, invalid := range []string{"", ".", "..", "../outside", "../../../outside", "nested/name", "/outside", `..\outside`, `C:\outside`, "name:version", "name?query", "name#fragment", "name\x00", "name\n", "name "} {
					t.Run(component+"/"+invalid, func(t *testing.T) {
						a := &resources.Agent{Kind: registration.Resource, Publisher: "example.test", Name: "widget", Version: "1.0.0"}
						switch component {
						case "publisher":
							a.Publisher = invalid
						case "name":
							a.Name = invalid
						case "version":
							a.Version = invalid
						}
						_, err := resources.AgentFromProto(&basev0.Agent{Kind: registration.ProtoKind, Publisher: a.Publisher, Name: a.Name, Version: a.Version})
						require.Error(t, err)
						location, err := a.Path(ctx)
						require.Error(t, err)
						require.Empty(t, location)
						_, err = resources.ParseAgent(ctx, registration.Resource, a.Identifier())
						require.Error(t, err)
					})
				}
			}
			for _, version := range []string{"1.0.0", "v2.3.4-rc.1+build.7", "latest", "custom_release-42"} {
				a, err := resources.ParseAgent(ctx, registration.Resource, "independent.example/widget-kit:"+version)
				require.NoError(t, err)
				location, err := a.Path(ctx)
				if !registration.Operations.Load {
					require.Error(t, err)
					continue
				}
				require.NoError(t, err)
				root := filepath.Join(resources.AgentBase(ctx), "agents", registration.InstallSubdirectory)
				relative, err := filepath.Rel(root, location)
				require.NoError(t, err)
				require.Equal(t, filepath.Join("independent.example", "widget-kit__"+version), relative)
			}
		})
	}
}

func TestAgentCacheSeparatorCannotAppearInVersion(t *testing.T) {
	ctx := t.Context()
	for _, registration := range resources.AgentKindRegistry() {
		for _, version := range []string{"1__2", "1___2", "1__", "1.0.0+build__2"} {
			a := &resources.Agent{Kind: registration.Resource, Publisher: "example.test", Name: "widget", Version: version}
			_, err := a.Proto()
			require.ErrorContains(t, err, "cache separator")
			_, err = a.Path(ctx)
			require.ErrorContains(t, err, "cache separator")
			_, err = resources.ParseAgent(ctx, registration.Resource, a.Identifier())
			require.ErrorContains(t, err, "cache separator")
		}
		for _, name := range []string{"widget", "widget_", "widget__1", "widget___1"} {
			for _, version := range []string{"2", "1_2", "1.0.0+build.2"} {
				_, err := resources.ParseAgent(ctx, registration.Resource, "example.test/"+name+":"+version)
				require.NoError(t, err)
			}
		}
	}
}

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
