package runners

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"

	"github.com/docker/go-connections/nat"

	"github.com/codefly-dev/core/wool"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
)

type Docker struct {
	client *client.Client
	image  *configurations.DockerImage

	name string

	workDir string
	mounts  []mount.Mount

	envs []string
	cmd  []string

	portMapping *DockerPortMapping

	instance *DockerContainerInstance

	silent bool
	out    io.Writer
	reader io.ReadCloser

	outLock sync.Mutex
	wg      sync.WaitGroup
	ctx     context.Context
	running bool
}

type DockerPortMapping struct {
	Host      int
	Container int
}

func DockerEngineRunning(ctx context.Context) bool {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return false
	}
	_, err = cli.Ping(ctx)
	return err == nil
}

// NewDocker creates a new docker runner
func NewDocker(ctx context.Context) (*Docker, error) {
	w := wool.Get(ctx).In("NewDockerRunner")
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, w.Wrapf(err, "cannot createAndWait docker client")
	}
	return &Docker{
		client: cli,
		out:    w,
	}, nil
}

func (docker *Docker) WithMount(sourceDir string, targetDir string) *Docker {
	docker.mounts = append(docker.mounts, mount.Mount{
		Type:   mount.TypeBind,
		Source: sourceDir,
		Target: targetDir,
	})
	return docker
}

func (docker *Docker) WithName(name string) {
	docker.name = name
}

func (docker *Docker) WithOut(writer io.Writer) {
	docker.out = writer
}

func (docker *Docker) Init(ctx context.Context, image *configurations.DockerImage) error {
	w := wool.Get(ctx).In("Docker.Start")

	// Pull the image if needed
	err := docker.GetImage(ctx, image)
	if err != nil {
		return w.Wrapf(err, "cannot get image")
	}
	docker.image = image
	return nil
}

func (docker *Docker) ImageExists(ctx context.Context, image *configurations.DockerImage) (bool, error) {
	w := wool.Get(ctx).In("Docker.ImageExists")
	images, err := docker.client.ImageList(ctx, types.ImageListOptions{})
	if err != nil {
		return false, w.Wrapf(err, "cannot list images")
	}
	for i := range images {
		img := &images[i]
		for _, repoTag := range img.RepoTags {
			if repoTag == image.FullName() {
				return true, nil
			}
		}
	}
	return false, nil
}

func (docker *Docker) GetImage(ctx context.Context, image *configurations.DockerImage) error {
	w := wool.Get(ctx).In("Docker.GetImage")
	if exists, err := docker.ImageExists(ctx, image); err != nil {
		return w.Wrapf(err, "cannot check if image exists")
	} else if exists {
		w.Trace("found Docker image locally")
		return nil
	}
	w.Debug("pulling Docker image")
	out, err := docker.client.ImagePull(ctx, image.FullName(), types.ImagePullOptions{})
	if err != nil {
		return w.Wrapf(err, "cannot pull image")
	}

	docker.ForwardLogs(out)

	return nil
}

type DockerContainerInstance struct {
	container container.CreateResponse
}

// SetCommand to run
func (docker *Docker) SetCommand(bin string, args ...string) {
	cmd := []string{bin}
	cmd = append(cmd, args...)
	docker.cmd = cmd
}

func (docker *Docker) create(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.create")
	containerConfig := &container.Config{
		Image:      docker.image.FullName(),
		Env:        docker.envs,
		Tty:        true,
		WorkingDir: docker.workDir,
	}
	if len(docker.cmd) > 0 {
		containerConfig.Cmd = docker.cmd

	}
	if docker.portMapping != nil {
		containerConfig.ExposedPorts = nat.PortSet{
			nat.Port(fmt.Sprintf("%d/tcp", docker.portMapping.Container)): struct{}{},
		}
	}

	hostConfig := &container.HostConfig{
		Mounts:     docker.mounts,
		AutoRemove: true,
	}
	if docker.portMapping != nil {
		hostConfig.PortBindings = docker.portBindings()
	}

	// Create the container
	w.Trace("creating container")
	resp, err := docker.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, docker.name)
	if err != nil {
		return w.Wrapf(err, "cannot create container")
	}
	w.Trace("created container", wool.Field("id", resp.ID))
	docker.instance = &DockerContainerInstance{
		container: resp,
	}
	return nil
}

func (docker *Docker) Start(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.run")
	// Create
	docker.ctx = ctx
	err := docker.create(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot create container")
	}
	err = docker.client.ContainerStart(ctx, docker.instance.container.ID, container.StartOptions{})
	if err != nil {
		return w.Wrapf(err, "cannot start container")
	}
	docker.running = true
	if !docker.silent {
		options := container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: false}
		logReader, err := docker.client.ContainerLogs(ctx, docker.instance.container.ID, options)
		if err != nil {
			return w.Wrapf(err, "cannot get container logs")
		}
		docker.reader = logReader

		docker.ForwardLogs(logReader)
	} else {
		_, _ = w.Forward([]byte("silent mode"))
	}
	return nil
}

func (docker *Docker) ForwardLogs(reader io.Reader) {
	docker.wg.Add(1)
	scanner := bufio.NewScanner(reader)
	output := make(chan []byte)
	go func() {
		defer docker.wg.Done()
		for {
			select {
			case <-docker.ctx.Done():
				return
			default:
				for scanner.Scan() {
					output <- []byte(strings.TrimSpace(scanner.Text()))
				}
				//
				//if err := scanner.Err(); err != nil {
				//	output <- []byte(strings.TrimSpace(err.Error()))
				//}

			}
		}
	}()
	go func() {
		for out := range output {
			docker.outLock.Lock()
			_, _ = docker.out.Write(out)
			docker.outLock.Unlock()
		}
	}()
}

func (docker *Docker) WithPort(port DockerPortMapping) {
	docker.portMapping = &port
}

func (docker *Docker) WithEnvironmentVariables(envs ...string) {
	docker.envs = append(docker.envs, envs...)
}

func (docker *Docker) WithCommand(cmd ...string) {
	docker.cmd = cmd
}

func (docker *Docker) portBindings() nat.PortMap {
	return nat.PortMap{
		nat.Port(fmt.Sprintf("%d/tcp", docker.portMapping.Container)): []nat.PortBinding{
			{
				HostIP:   "0.0.0.0",
				HostPort: fmt.Sprintf("%d", docker.portMapping.Host),
			},
		},
	}
}

func (docker *Docker) Stop() error {
	if docker.reader != nil {
		defer func() {
			docker.reader.Close()
		}()
	}
	if docker.instance == nil || docker.instance.container.ID == "" {
		return nil
	}
	err := docker.client.ContainerStop(context.Background(), docker.instance.container.ID, container.StopOptions{Timeout: shared.Pointer(3)})
	if err != nil {
		_ = docker.client.ContainerKill(context.Background(), docker.instance.container.ID, "SIGKILL")
	}
	return nil
}

func (docker *Docker) Silence() {
	docker.silent = true
}

func (docker *Docker) WithWorkDir(dir string) {
	docker.workDir = dir

}

func (docker *Docker) Running() bool {
	return docker.running

}

//
//func (docker *Docker) Run() error {
//	// Start the container
//	err := docker.Start(docker.ctx)
//	if err != nil {
//		return 0, err
//	}
//
//	// Wait for the container to finish
//	resultC, errC := docker.client.ContainerWait(docker.ctx, docker.instance.container.ID, container.WaitConditionNotRunning)
//	select {
//	case err := <-errC:
//		if err != nil {
//			return 0, err
//		}
//	case result := <-resultC:
//		return result.StatusCode, nil
//	}
//}
