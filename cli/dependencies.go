package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codefly "github.com/codefly-dev/sdk-go"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"google.golang.org/grpc/credentials/insecure"

	"google.golang.org/protobuf/types/known/emptypb"

	"google.golang.org/grpc"

	v0 "github.com/codefly-dev/core/generated/go/codefly/cli/v0"
)

type Dependencies struct {
	cmd            *exec.Cmd
	cli            v0.CLIClient
	runtimeContext *basev0.RuntimeContext
}

type Option struct {
	Debug       bool
	Timeout     time.Duration
	NamingScope string
}

type OptionFunc func(*Option)

func WithDebug() OptionFunc {
	return func(o *Option) {
		o.Debug = true
	}
}

func WithTimeout(timeout time.Duration) OptionFunc {
	return func(o *Option) {
		o.Timeout = timeout
	}
}

func WithNamingScope(scope string) OptionFunc {
	return func(o *Option) {
		o.NamingScope = scope
	}

}

func WithDependencies(ctx context.Context, opts ...OptionFunc) (*Dependencies, error) {
	opt := &Option{
		Debug:   false,
		Timeout: 10 * time.Second,
	}
	for _, o := range opts {
		o(opt)
	}
	args := []string{"run", "service"}
	if opt.Debug {
		args = append(args, "-d")
	}
	if opt.NamingScope != "" {
		args = append(args, "--naming-scope", opt.NamingScope)
	}
	args = append(args, "--exclude-root", "--cli-server")
	cmd := exec.CommandContext(ctx, "codefly", args...)
	fmt.Println("CMD", args)
	cmd.Stdout = os.Stdout // log stdout
	cmd.Stderr = os.Stderr // log stderr
	err := cmd.Start()
	if err != nil {
		return nil, err
	}
	port := 10000
	var conn *grpc.ClientConn
	wait := opt.Timeout
	for {
		time.Sleep(time.Second)
		conn, err = grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err == nil {
			break
		}
		wait -= 500 * time.Millisecond
		if wait <= 0 {
			return nil, fmt.Errorf("timeout waiting for connection")
		}
		time.Sleep(500 * time.Millisecond)
	}
	cli := v0.NewCLIClient(conn)
	_, err = cli.Ping(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, err
	}
	l := &Dependencies{cmd: cmd, cli: cli, runtimeContext: resources.NewRuntimeContextNative()}
	err = l.WaitForReady(ctx, opt)
	if err != nil {
		return nil, err
	}
	err = l.SetEnvironment(ctx)
	if err != nil {
		return nil, err
	}
	_, err = codefly.Init(ctx)
	if err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Dependencies) WaitForReady(ctx context.Context, opt *Option) error {
	time.Sleep(time.Second)
	wait := opt.Timeout
	for {
		status, err := l.cli.GetFlowStatus(ctx, &emptypb.Empty{})
		if err != nil {
			return err
		}
		if status.Ready {
			break
		}
		wait -= 500 * time.Millisecond
		if wait <= 0 {
			return fmt.Errorf("timeout waiting for flow to be ready")
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

var runningService *resources.Service

func LoadService(ctx context.Context) error {
	w := wool.Get(ctx).In("codefly.LoadService")
	dir, errFind := resources.FindUp[resources.Service](ctx)
	if errFind != nil {
		return errFind
	}
	if dir != nil {
		svc, err := resources.LoadServiceFromDir(ctx, *dir)
		if err != nil {
			return err
		}
		w.Debug("loaded service", wool.Field("service", svc.Unique()))
		runningService = svc
		return nil
	}
	return w.NewError("no service found")
}

func Service() (*resources.Service, error) {
	if runningService == nil {
		err := LoadService(context.Background())
		if err != nil {
			return nil, err
		}
	}
	return runningService, nil
}

func (l *Dependencies) SetEnvironment(ctx context.Context) error {
	w := wool.Get(ctx).In("codefly.SetEnvironment")
	svc, err := Service()
	if err != nil {
		return err
	}
	// Setup Networking
	{
		networkAccess := resources.NetworkAccessFromRuntimeContext(l.runtimeContext)
		if networkAccess == nil {
			return w.NewError("no network access found")
		}
		req := &v0.GetNetworkMappingsRequest{Module: svc.Module, Service: svc.Name}

		resp, err := l.cli.GetDependenciesNetworkMappings(ctx, req)
		if err != nil {
			return w.Wrapf(err, "failed to get dependencies network mappings")
		}
		for _, np := range resp.NetworkMappings {
			inst := resources.FilterNetworkInstance(ctx, np.Instances, networkAccess)
			if inst == nil {
				return w.NewError("no network instance found")
			}
			env := resources.EndpointAsEnvironmentVariable(np.Endpoint, inst)
			k := env.Key
			v := fmt.Sprintf("%s", env.Value)
			w.Debug("setting environment variable", wool.Field("key", k), wool.Field("value", v))
			err = os.Setenv(k, v)
			if err != nil {
				return w.Wrapf(err, "failed to set environment variable")
			}

		}
	}
	// Setup Configuration
	{
		req := &v0.GetConfigurationRequest{
			Module:  svc.Module,
			Service: svc.Name,
		}
		resp, err := l.cli.GetDependenciesConfigurations(ctx, req)
		if err != nil {
			return w.Wrapf(err, "failed to get dependencies configurations")
		}
		dependenciesConfigurations := resources.FilterConfigurations(resp.Configurations, l.runtimeContext)
		for _, conf := range dependenciesConfigurations {
			envs := resources.ConfigurationAsEnvironmentVariables(conf, false)
			secrets := resources.ConfigurationAsEnvironmentVariables(conf, true)
			envs = append(envs, secrets...)
			for _, env := range envs {
				k := env.Key
				v := fmt.Sprintf("%s", env.Value)
				w.Debug("setting environment variable", wool.Field("key", k), wool.Field("value", v))
				err = os.Setenv(k, v)
				if err != nil {
					return w.Wrapf(err, "failed to set environment variable")
				}
			}
		}
	}
	return nil
}

func (l *Dependencies) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("codefly.Stop")
	_, err := l.cli.StopFlow(ctx, &v0.StopFlowRequest{})
	if err != nil {
		w.Warn("failed to stop flow", wool.Field("error", err))
	}
	err = l.cmd.Process.Kill()
	if err != nil {
		return w.Wrapf(err, "failed to kill process")
	}
	return err
}

func (l *Dependencies) Destroy(ctx context.Context) error {
	w := wool.Get(ctx).In("codefly.Stop")
	_, err := l.cli.DestroyFlow(ctx, &v0.DestroyFlowRequest{})
	if err != nil {
		w.Warn("failed to stop flow", wool.Field("error", err))
	}
	err = l.cmd.Process.Kill()
	if err != nil {
		return w.Wrapf(err, "failed to kill process")
	}
	return err
}
