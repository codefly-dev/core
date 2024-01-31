package runners

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/docker/go-connections/nat"

	"github.com/codefly-dev/core/wool"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
)

type Docker struct {
	client *client.Client
	image  DockerImage
	option DockerRunOption

	mounts []mount.Mount

	envs []string
	cmd  []string

	port *DockerPort

	instance *DockerContainerInstance

	workingDir string
	silent     bool

	RunningContext context.Context
	Cancel         func()
}

type DockerRunOption struct {
	Location   string
	CustomExec bool
}

type DockerOption func(option *DockerRunOption)

type DockerPort struct {
	Host      string
	Container string
}

func DockerRunning(ctx context.Context) bool {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return false
	}
	_, err = cli.Ping(ctx)
	return err == nil
}

// NewDocker creates a new docker runner
func NewDocker(ctx context.Context, opts ...DockerOption) (*Docker, error) {
	option := DockerRunOption{}
	for _, o := range opts {
		o(&option)
	}
	w := wool.Get(ctx).In("NewDockerRunner")
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, w.Wrapf(err, "cannot createAndWait docker client")
	}
	var mounts []mount.Mount
	workingDir := "/codefly"
	if option.Location != "" {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: option.Location,
			Target: workingDir,
		})
	}
	return &Docker{
		client:     cli,
		option:     option,
		workingDir: workingDir,
		mounts:     mounts,
	}, nil
}

type DockerImage struct {
	Name string
	Tag  string
}

func (image *DockerImage) Image() string {
	if image.Tag == "" {
		return image.Name
	}
	return fmt.Sprintf("%s:%s", image.Name, image.Tag)
}

func (docker *Docker) Init(ctx context.Context, image DockerImage) error {
	w := wool.Get(ctx).In("Docker.Start")

	// Pull the image if needed
	err := docker.GetImage(ctx, image)
	if err != nil {
		return w.Wrapf(err, "cannot get image")
	}
	docker.image = image
	// Wait only if we have custom command
	wait := docker.option.CustomExec
	err = docker.create(ctx, wait)
	if err != nil {
		return w.Wrapf(err, "cannot create container")
	}
	return nil
}

func (docker *Docker) ImageExists(ctx context.Context, image DockerImage) (bool, error) {
	w := wool.Get(ctx).In("Docker.ImageExists")
	images, err := docker.client.ImageList(ctx, types.ImageListOptions{})
	if err != nil {
		return false, w.Wrapf(err, "cannot list images")
	}
	for i := range images {
		img := &images[i]
		for _, repoTag := range img.RepoTags {
			if repoTag == image.Image() {
				return true, nil
			}
		}
	}
	return false, nil
}

func (docker *Docker) GetImage(ctx context.Context, image DockerImage) error {
	w := wool.Get(ctx).In("Docker.GetImage")
	if exists, err := docker.ImageExists(ctx, image); err != nil {
		return w.Wrapf(err, "cannot check if image exists")
	} else if exists {
		w.Trace("found Docker image locally")
		return nil
	}
	w.Debug("pulling Docker image")
	out, err := docker.client.ImagePull(ctx, image.Image(), types.ImagePullOptions{})
	if err != nil {
		return w.Wrapf(err, "cannot pull image")
	}
	Forward(out, w)
	return nil
}

type DockerContainerInstance struct {
	container container.CreateResponse
}

func (docker *Docker) Run(ctx context.Context, cmds ...*Command) error {
	w := wool.Get(ctx).In("Docker.Run")
	for _, cmd := range cmds {
		err := docker.runWithCommand(ctx, cmd)
		if err != nil {
			return w.Wrapf(err, "cannot runWithCommand command: %s", cmd.AsSlice())
		}
		w.Info("success running", wool.Field("cmd", cmd.AsSlice()))
	}
	return nil
}

func (docker *Docker) Start() error {

	w := wool.Get(docker.RunningContext).In("Docker.Run")
	go func() {
		err := docker.run(docker.RunningContext)
		if err != nil {
			w.Error(err.Error())
		}
	}()
	return nil
}

func (docker *Docker) StartWithCommand(cmd *Command) error {
	w := wool.Get(docker.RunningContext).In("Docker.Run")
	go func() {
		err := docker.runWithCommand(docker.RunningContext, cmd)
		if err != nil {
			w.Error(err.Error())
		}
	}()
	return nil
}

func (docker *Docker) create(ctx context.Context, wait bool) error {
	w := wool.Get(ctx).In("Docker.createAndWait")
	containerConfig := &container.Config{
		Image:      docker.image.Image(),
		WorkingDir: docker.workingDir,
		Env:        docker.envs,
		Tty:        true,
	}
	if wait {
		containerConfig.Cmd = []string{"tail", "-f", "/dev/null"}
	}
	if len(docker.cmd) > 0 {
		containerConfig.Cmd = docker.cmd

	}
	if docker.port != nil {
		containerConfig.ExposedPorts = nat.PortSet{
			nat.Port(docker.port.Container + "/tcp"): struct{}{},
		}
	}

	hostConfig := &container.HostConfig{
		Mounts:     docker.mounts,
		AutoRemove: true,
	}
	if docker.port != nil {
		hostConfig.PortBindings = docker.portBindings()
	}

	// Create the container
	w.Trace("creating container")
	resp, err := docker.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, "")
	if err != nil {
		return w.Wrapf(err, "cannot createAndWait container")
	}
	w.Trace("created container", wool.Field("id", resp.ID))
	docker.instance = &DockerContainerInstance{
		container: resp,
	}
	return nil
}

func (docker *Docker) run(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.run")
	err := docker.client.ContainerStart(ctx, docker.instance.container.ID, container.StartOptions{})
	if err != nil {
		return w.Wrapf(err, "cannot start container")
	}
	if !docker.silent {
		options := container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: false}
		logReader, err := docker.client.ContainerLogs(ctx, docker.instance.container.ID, options)
		if err != nil {
			return w.Wrapf(err, "cannot get container logs")
		}
		defer logReader.Close()

		Forward(logReader, w)
	} else {
		_, _ = w.Forward([]byte("silent mode"))
	}
	return nil
}

func (docker *Docker) runWithCommand(ctx context.Context, cmd *Command) error {
	w := wool.Get(ctx).In("Docker.runWithCommand")
	w.WithLoglevel(cmd.LogLevel())
	execConfig := types.ExecConfig{
		Cmd:          cmd.AsSlice(),
		Env:          cmd.Envs(),
		AttachStdout: true,
		AttachStderr: true,
	}
	w.Debug("running", wool.Field("cmd", cmd.AsSlice()))
	w.Debug("creating exec")
	execResp, err := docker.client.ContainerExecCreate(ctx, docker.instance.container.ID, execConfig)
	if err != nil {
		return w.Wrapf(err, "cannot createAndWait exec")
	}

	// Attach to the exec instance to get the output streams
	w.Debug("attaching to exec")
	resp, err := docker.client.ContainerExecAttach(ctx, execResp.ID, types.ExecStartCheck{})
	if err != nil {
		return w.Wrapf(err, "cannot attach to exec")
	}
	Forward(resp.Reader, w)

	w.Debug("starting exec")
	err = docker.client.ContainerExecStart(ctx, execResp.ID, types.ExecStartCheck{})
	if err != nil {
		return w.Wrapf(err, "cannot start exec")
	}
	w.Debug("started exec", wool.Field("id", execResp.ID))

	// Get the logs
	Forward(resp.Reader, w)

	// Wait for the exec to finish and check the exit code
	w.Debug("waiting for exec to finish")
	inspect, err := docker.client.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return w.Wrapf(err, "cannot inspect exec")
	}

	if inspect.ExitCode != 0 {
		return fmt.Errorf("command failed with exit code: %d", inspect.ExitCode)
	}
	return nil
}

func Forward(reader io.Reader, writers ...io.Writer) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		for _, w := range writers {
			_, _ = w.Write([]byte(strings.TrimSpace(scanner.Text())))
		}
	}

	if err := scanner.Err(); err != nil {
		for _, w := range writers {
			_, _ = w.Write([]byte(strings.TrimSpace(err.Error())))
		}
	}
}

func WithWorkspace(location string) DockerOption {
	return func(option *DockerRunOption) {
		option.Location = location
	}
}

func WithCustomExec() DockerOption {
	return func(option *DockerRunOption) {
		option.CustomExec = true
	}
}

func (docker *Docker) WithPort(port DockerPort) {
	docker.port = &port
}

func (docker *Docker) WithEnvironmentVariables(envs ...string) {
	docker.envs = append(docker.envs, envs...)
}

func (docker *Docker) WithCommand(cmd ...string) {
	docker.cmd = cmd
}

func (docker *Docker) portBindings() nat.PortMap {
	return nat.PortMap{
		nat.Port(docker.port.Container + "/tcp"): []nat.PortBinding{
			{
				HostIP:   "0.0.0.0",
				HostPort: docker.port.Host,
			},
		},
	}
}

func (docker *Docker) Stop() error {
	if docker.Cancel != nil {
		docker.Cancel()
	}
	go func() {
		err := docker.client.ContainerStop(context.Background(), docker.instance.container.ID, container.StopOptions{})
		if err != nil {
			_ = docker.client.ContainerKill(context.Background(), docker.instance.container.ID, "SIGKILL")
		}
	}()
	return nil
}

func (docker *Docker) Silence() {
	docker.silent = true
}
