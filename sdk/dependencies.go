package sdk

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	v0 "github.com/codefly-dev/core/generated/go/codefly/cli/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Dependencies manages a running set of codefly-managed service dependencies.
type Dependencies struct {
	cmd            *exec.Cmd
	cli            v0.CLIClient
	runtimeContext *basev0.RuntimeContext
}

type Option struct {
	Debug       bool
	Timeout     time.Duration
	NamingScope string
	Silents     []string
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

func WithSilence(uniques ...string) OptionFunc {
	return func(o *Option) {
		o.Silents = uniques
	}
}

// WithDependencies starts all dependencies declared in the current service's
// service.codefly.yaml using the codefly CLI. This handles arbitrarily deep
// dependency graphs — the CLI resolves and starts everything in order.
//
// Connection strings are injected as environment variables (the standard
// codefly pattern). Use Connection() or os.Getenv() to retrieve them.
//
// Usage:
//
//	deps, err := sdk.WithDependencies(ctx)
//	deps, err := sdk.WithDependencies(ctx, sdk.WithDebug())
//	deps, err := sdk.WithDependencies(ctx, sdk.WithTimeout(30*time.Second))
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
	if len(opt.Silents) > 0 {
		args = append(args, "--silent", strings.Join(opt.Silents, ","))
	}
	args = append(args, "--exclude-root", "--cli-server")
	cmd := exec.CommandContext(ctx, "codefly", args...)
	fmt.Println("Running command", cmd.String())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
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
	runtimeContext := resources.RuntimeContextFromEnv()
	l := &Dependencies{cmd: cmd, cli: cli, runtimeContext: runtimeContext}
	err = l.WaitForReady(ctx, opt)
	if err != nil {
		return nil, err
	}
	err = l.SetEnvironment(ctx)
	if err != nil {
		return nil, err
	}
	return l, nil
}

// Connection returns a connection string from environment variables.
func Connection(service, name string) string {
	patterns := []string{
		fmt.Sprintf("CODEFLY__SERVICE_%s__%s__CONNECTION", normalize(service), normalize(name)),
		fmt.Sprintf("CODEFLY__%s__%s__CONNECTION", normalize(service), normalize(name)),
	}
	for _, p := range patterns {
		if v := os.Getenv(p); v != "" {
			return v
		}
	}
	return ""
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

var runningModule *resources.Module
var runningService *resources.Service

func Service() (*resources.Service, error) {
	if runningService == nil {
		mod, svc, err := resources.LoadModuleAndServiceFromCurrentPath(context.Background())
		if err != nil {
			return nil, err
		}
		runningService = svc
		runningModule = mod
	}
	return runningService, nil
}

func Module() (*resources.Module, error) {
	if runningModule == nil {
		ctx := context.Background()
		workspace, err := resources.FindWorkspaceUp(ctx)
		if err != nil {
			return nil, err
		}
		if workspace.Layout == resources.LayoutKindFlat {
			module, err := workspace.LoadModuleFromName(ctx, workspace.Name)
			if err != nil {
				return nil, err
			}
			runningModule = module
		} else {
			mod, err := resources.LoadModuleFromCurrentPath(ctx)
			if err != nil {
				return nil, err
			}
			runningModule = mod
		}
	}
	return runningModule, nil
}

func Inject(env *resources.EnvironmentVariable) {
	os.Setenv(env.Key, fmt.Sprintf("%v", env.Value))
}

func (l *Dependencies) SetEnvironment(ctx context.Context) error {
	w := wool.Get(ctx).In("sdk.SetEnvironment")
	svc, err := Service()
	if err != nil {
		return err
	}
	mod, err := Module()
	if err != nil {
		return err
	}

	var envs []*resources.EnvironmentVariable
	envs = append(envs,
		resources.ServiceAsEnvironmentVariable(svc.Name),
		resources.ModuleAsEnvironmentVariable(mod.Name),
		resources.VersionAsEnvironmentVariable(svc.Version))
	for _, env := range envs {
		Inject(env)
	}
	// Setup Networking
	{
		networkAccess := resources.NetworkAccessFromRuntimeContext(l.runtimeContext)
		if networkAccess == nil {
			return w.NewError("no network access found")
		}
		req := &v0.GetNetworkMappingsRequest{Module: mod.Name, Service: svc.Name}

		resp, err := l.cli.GetDependenciesNetworkMappings(ctx, req)
		if err != nil {
			return w.Wrapf(err, "failed to get dependencies network mappings")
		}
		for _, np := range resp.NetworkMappings {
			inst := resources.FilterNetworkInstance(ctx, np.Instances, networkAccess)
			if inst == nil {
				return w.NewError("no network instance found")
			}
			access := &resources.EndpointAccess{
				Endpoint:        np.Endpoint,
				NetworkInstance: inst,
			}
			Inject(resources.EndpointAsEnvironmentVariable(access))
		}
	}
	// Setup Configuration
	{
		req := &v0.GetConfigurationRequest{
			Module:  mod.Name,
			Service: svc.Name,
		}
		resp, err := l.cli.GetConfiguration(ctx, req)
		if err != nil {
			return w.Wrapf(err, "failed to get configuration")
		}
		conf := resp.Configuration
		if conf != nil {
			envs := resources.ConfigurationAsEnvironmentVariables(conf, false)
			secrets := resources.ConfigurationAsEnvironmentVariables(conf, true)
			envs = append(envs, secrets...)
			for _, env := range envs {
				Inject(env)
			}
		}
	}
	// Setup Dependencies Configurations
	{
		req := &v0.GetConfigurationRequest{
			Module:  mod.Name,
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
				Inject(env)
			}
		}
	}
	return nil
}

// Stop gracefully stops all running dependencies.
func (l *Dependencies) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("sdk.Stop")
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

// Destroy tears down all running dependencies.
func (l *Dependencies) Destroy(ctx context.Context) error {
	w := wool.Get(ctx).In("sdk.Destroy")
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

func normalize(s string) string {
	s = strings.ToUpper(s)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, "/", "__")
	return s
}
