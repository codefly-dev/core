package manager

import (
	"context"
	"os/exec"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
	"github.com/hashicorp/go-plugin"
)

type AgentContext interface {
	Key(p *configurations.Agent, unique string) string
	Default() plugin.Plugin
}

type Pluggable interface {
	ImplementationKind() string
	Path() (string, error)
	Name() string
	Unique() string
}

var inUse map[string]*plugin.Client

func init() {
	inUse = make(map[string]*plugin.Client)
}

func Cleanup(unique string) {
	logger := shared.NewLogger().With("agents.Cleanup<%s>", unique)
	if client, ok := inUse[unique]; ok {
		client.Kill()
		return
	}
	logger.Oops("cannot find agent client for <%s> in use", unique)
}

// Name is what the agent will be identified as: for clean up

func Load[P AgentContext, Instance any](ctx context.Context, p *configurations.Agent, unique string) (*Instance, error) {
	w := wool.Get(ctx).In("agents.Load", wool.Field("agent", p.Identifier()))
	if p == nil {
		return nil, w.NewError("agent cannot be nil")
	}
	bin, err := p.Path()
	if err != nil {
		return nil, w.Wrapf(err, "cannot compute agent path")
	}

	w.Trace("local", wool.Field("path", bin))
	// Already loaded or download
	if _, err := exec.LookPath(bin); err != nil {
		err := Download(ctx, p)
		if err != nil {
			return nil, w.Wrapf(err, "cannot download")
		}
	}
	w.Trace("loading", wool.Field("path", bin))

	var this P
	placeholder := this.Default()
	pluginMap := map[string]plugin.Plugin{this.Key(p, unique): placeholder}

	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  agents.HandshakeConfig,
		Plugins:          pluginMap,
		Cmd:              exec.Command(bin),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		Logger:           agents.LogHandler().Receiver,
	})
	w.Trace("loaded")
	inUse[unique] = client

	// Connect via gRPC
	grpcClient, err := client.Client()
	if err != nil {
		return nil, w.Wrapf(err, "cannot connect to gRPC client")
	}
	// Request the platform
	raw, err := grpcClient.Dispense(this.Key(p, unique))
	if err != nil {
		return nil, w.Wrapf(err, "cannot dispense agent")
	}
	u := raw.(*Instance)
	if u == nil {
		return nil, w.NewError("cannot cast agent")
	}
	return u, nil
}
