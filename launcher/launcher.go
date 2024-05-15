package launcher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"google.golang.org/grpc/credentials/insecure"

	"google.golang.org/protobuf/types/known/emptypb"

	"google.golang.org/grpc"

	v0 "github.com/codefly-dev/core/generated/go/cli/v0"
)

type Launcher struct {
	cmd            *exec.Cmd
	cli            v0.CLIClient
	runtimeContext *basev0.RuntimeContext
}

type Option struct {
	Debug bool
}

func New(ctx context.Context, opt *Option) (*Launcher, error) {
	args := []string{"run", "service"}
	if opt != nil {
		if opt.Debug {
			args = append(args, "-d")
		}
	}
	args = append(args, "--exclude-root", "--cli-server")
	cmd := exec.CommandContext(ctx, "codefly", args...)
	cmd.Stdout = os.Stdout // log stdout
	cmd.Stderr = os.Stderr // log stderr
	err := cmd.Start()
	if err != nil {
		return nil, err
	}
	port := 10000
	var conn *grpc.ClientConn
	wait := 5 * time.Second
	for {
		time.Sleep(time.Second)
		conn, err = grpc.Dial(fmt.Sprintf("localhost:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials()))
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
	l := &Launcher{cmd: cmd, cli: cli, runtimeContext: resources.NewRuntimeContextNative()}
	err = l.WaitForReady(ctx)
	if err != nil {
		return nil, err
	}
	err = l.SetEnvironment(ctx)
	if err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Launcher) WaitForReady(ctx context.Context) error {
	time.Sleep(time.Second)
	wait := 5 * time.Second
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

func (l *Launcher) SetEnvironment(ctx context.Context) error {
	w := wool.Get(ctx).In("codefly.SetEnvironment")
	svc, err := Service()
	if err != nil {
		return err
	}
	request := &v0.GetConfigurationRequest{
		Module:  svc.Module,
		Service: svc.Name,
	}
	resp, err := l.cli.GetDependenciesConfigurations(ctx, request)
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
			w.Focus("setting environment variable", wool.Field("key", k), wool.Field("value", v))
			err = os.Setenv(k, v)
		}
	}
	return nil
}

func (l *Launcher) Close(ctx context.Context) error {
	_, err := l.cli.StopFlow(ctx, &v0.StopFlowRequest{})
	err = l.cmd.Process.Kill()
	return err
}

func (l *Launcher) Destroy(ctx context.Context) error {
	_, err := l.cli.DestroyFlow(ctx, &v0.DestroyFlowRequest{})
	return err
}
