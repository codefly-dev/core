package launcher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/codefly-dev/core/configurations"

	"google.golang.org/protobuf/types/known/emptypb"

	"google.golang.org/grpc"

	v0 "github.com/codefly-dev/core/generated/go/cli/v0"
)

type Launcher struct {
	cmd  *exec.Cmd
	port int
	cli  v0.CLIClient
}

func LaunchUpTo(ctx context.Context) (*Launcher, error) {
	cmd := exec.CommandContext(ctx, "codefly", "run", "service", "--exclude-root", "--server")
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
		conn, err = grpc.Dial(fmt.Sprintf("localhost:%d", port), grpc.WithInsecure())
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
	return &Launcher{cmd: cmd, cli: cli}, nil
}

func (l *Launcher) WaitForReady(ctx context.Context) error {
	time.Sleep(time.Second)
	wait := 5 * time.Second
	for {
		status, err := l.cli.GetFlowStatus(ctx, &emptypb.Empty{})
		if err != nil {
			return err
		}
		if err == nil && status.Ready {
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

func (l *Launcher) GetServiceInformationProvider(ctx context.Context, app string, svc string, name string, key string) (string, error) {
	infos, err := l.cli.GetServiceProviderInformation(ctx, &v0.GetServiceProviderInfoRequest{
		Application: app,
		Service:     svc,
	})
	if err != nil {
		return "", err
	}
	// Find name, key
	info, err := configurations.FindServiceProvider(configurations.ServiceUnique(app, svc), name, infos.ProviderInfos)
	if err != nil {
		return "", err
	}
	if val, ok := info.Data[key]; ok {
		return val, nil
	}
	return "", fmt.Errorf("couldn't find key")
}

func (l *Launcher) Close(ctx context.Context) error {
	_, err := l.cli.StopFlow(ctx, &emptypb.Empty{})
	return err
}
