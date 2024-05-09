package manager

import (
	"context"
	"os/exec"
	"strconv"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/hashicorp/go-plugin"
)

type AgentContext interface {
	Key(p *resources.Agent, unique string) string
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
	if client, ok := inUse[unique]; ok {
		client.Kill()
		return
	}
}

type ProcessInfo struct {
	PID int
}

func Load[P AgentContext, Instance any](ctx context.Context, p *resources.Agent, unique string) (*Instance, *ProcessInfo, error) {
	w := wool.Get(ctx).In("agents.Load", wool.Field("agent", p.Identifier()))

	if p == nil {
		return nil, nil, w.NewError("agent cannot be nil")
	}
	bin, err := p.Path()
	if err != nil {
		return nil, nil, w.Wrapf(err, "cannot compute agent path")
	}

	// Already loaded or download
	if _, err := exec.LookPath(bin); err != nil {
		err := Download(ctx, p)
		if err != nil {
			return nil, nil, w.Wrapf(err, "cannot download")
		}
	}

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
	w.Trace("loaded", wool.PathField(bin), wool.Field("context", this.Key(p, unique)))
	inUse[unique] = client

	// Connect via gRPC
	grpcClient, err := client.Client()
	if err != nil {
		return nil, nil, w.Wrapf(err, "cannot connect to gRPC client")
	}

	// Request the platform
	raw, err := grpcClient.Dispense(this.Key(p, unique))
	if err != nil {
		return nil, nil, w.Wrapf(err, "cannot dispense agent")
	}
	u := raw.(*Instance)
	if u == nil {
		return nil, nil, w.NewError("cannot cast agent")
	}
	var proc *ProcessInfo
	if pid, err := strconv.Atoi(client.ID()); err == nil {
		proc = &ProcessInfo{PID: pid}
	}
	return u, proc, nil
}
