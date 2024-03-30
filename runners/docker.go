package runners

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"time"

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

	containerName string

	workDir string
	mounts  []mount.Mount

	envs []string
	cmd  []string

	portMapping *DockerPortMapping

	instance *DockerContainerInstance

	persist bool
	silent  bool
	out     io.Writer
	reader  io.ReadCloser

	outLock sync.Mutex
	wg      sync.WaitGroup
	ctx     context.Context
	running bool
}

type DockerPortMapping struct {
	Host      uint16
	Container uint16
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
func NewDocker(ctx context.Context, image *configurations.DockerImage) (*Docker, error) {
	w := wool.Get(ctx).In("NewDockerRunner")
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, w.Wrapf(err, "cannot createAndWait docker client")
	}
	return &Docker{
		client: cli,
		out:    w,
		image:  image,
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
	docker.containerName = fmt.Sprintf("codefly-%s", strings.ReplaceAll(name, "/", "-"))
}

func (docker *Docker) WithPersistence() {
	docker.persist = true
}

func (docker *Docker) WithSilence() {
	docker.silent = true
}

func (docker *Docker) WithWorkDir(dir string) {
	docker.workDir = dir
}

func (docker *Docker) Running() bool {
	return docker.running
}

func (docker *Docker) WithOut(writer io.Writer) {
	docker.out = writer
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

func (docker *Docker) Init(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.Start")
	docker.ctx = ctx
	// Pull the image if needed
	err := docker.GetImage(ctx, docker.image)
	if err != nil {
		return w.Wrapf(err, "cannot get image")
	}
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

type ProgressDetail struct {
	Current int `json:"current"`
	Total   int `json:"total"`
}

type DockerPullResponse struct {
	ID             string         `json:"id"`
	Status         string         `json:"status"`
	ProgressDetail ProgressDetail `json:"progressDetail"`
}

func PrintDownloadPercentage(reader io.ReadCloser, out io.Writer) {
	scanner := bufio.NewScanner(reader)
	scanner.Split(bufio.ScanLines)

	progressMap := make(map[string]DockerPullResponse)

	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for range ticker.C {
			var totalCurrent int
			for _, progress := range progressMap {
				totalCurrent += progress.ProgressDetail.Current
			}
			totalCurrentMB := float64(totalCurrent) / 1024 / 1024
			_, _ = out.Write([]byte(fmt.Sprintf("Downloaded: %.2f MB", totalCurrentMB)))
		}
	}()

	for scanner.Scan() {
		line := scanner.Text()
		var pullResponse DockerPullResponse
		_ = json.Unmarshal([]byte(line), &pullResponse)
		progressMap[pullResponse.ID] = pullResponse
	}

	ticker.Stop()
}

func (docker *Docker) GetImage(ctx context.Context, image *configurations.DockerImage) error {
	w := wool.Get(ctx).In("Docker.GetImage")
	if exists, err := docker.ImageExists(ctx, image); err != nil {
		return w.Wrapf(err, "cannot check if image exists")
	} else if exists {
		w.Trace("found Docker image locally")
		return nil
	}
	_, _ = w.Forward([]byte(fmt.Sprintf("pulling Docker image %s. Will show progress every 5 seconds.", image.FullName())))
	progress, err := docker.client.ImagePull(ctx, image.FullName(), types.ImagePullOptions{})
	if err != nil {
		return w.Wrapf(err, "cannot pull image")
	}

	PrintDownloadPercentage(progress, docker.out)

	// Wait for the image pull operation to be completed
	if _, err := io.Copy(io.Discard, progress); err != nil {
		return w.Wrapf(err, "error while waiting for image pull operation to be completed")
	}
	_, _ = w.Forward([]byte("Docker image pulled."))
	w.Debug("done pulling")
	return nil
}

type DockerContainerInstance struct {
	ID string
}

// SetCommand to run
func (docker *Docker) SetCommand(bin string, args ...string) {
	cmd := []string{bin}
	cmd = append(cmd, args...)
	docker.cmd = cmd
}

func (docker *Docker) IsRunningContainer(ctx context.Context) (bool, error) {
	// List all containers
	containers, err := docker.client.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return false, err
	}

	// Check if a container with the given name is running
	for i := range containers {
		c := containers[i]
		for _, name := range c.Names {
			if name == "/"+docker.containerName {
				docker.instance = &DockerContainerInstance{
					ID: c.ID,
				}
				return true, nil
			}
		}
	}
	return false, nil
}

func (docker *Docker) start(ctx context.Context) error {
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
	// Set network mode to "host" only for Linux builds
	if runtime.GOOS == "linux" {
		hostConfig.NetworkMode = container.NetworkMode("host")
	}
	if docker.portMapping != nil {
		hostConfig.PortBindings = docker.portBindings()
	}
	w.Debug("creating container", wool.Field("config", containerConfig.ExposedPorts), wool.Field("hostConfig", hostConfig.PortBindings))
	// Create the container
	var containerName string
	if docker.persist {
		containerName = docker.containerName
	}
	resp, err := docker.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, containerName)
	if err != nil {
		return w.Wrapf(err, "cannot create container")
	}

	docker.instance = &DockerContainerInstance{
		ID: resp.ID,
	}
	err = docker.client.ContainerStart(ctx, docker.instance.ID, container.StartOptions{})
	if err != nil {
		return w.Wrapf(err, "cannot start container")
	}
	docker.running = true
	return nil
}

func (docker *Docker) Start(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.start")
	create := true
	// Look up the container name only if we persist
	if docker.containerName != "" && docker.persist {
		ok, err := docker.IsRunningContainer(ctx)
		if err != nil {
			return w.Wrapf(err, "cannot check if container is running")
		}
		if ok {
			create = false
		}
	}

	// Create
	if create {
		docker.ctx = ctx
		err := docker.start(ctx)
		if err != nil {
			return w.Wrapf(err, "cannot start container")
		}
	}

	if !docker.silent {
		w.Debug("instance", wool.Field("intance", docker.instance))
		options := container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: false}
		logReader, err := docker.client.ContainerLogs(ctx, docker.instance.ID, options)
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

func (docker *Docker) Run(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.run")
	err := docker.start(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot start container")
	}
	if !docker.silent {
		w.Debug("instance", wool.Field("intance", docker.instance))
		options := container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: false}
		logReader, err := docker.client.ContainerLogs(ctx, docker.instance.ID, options)
		if err != nil {
			return w.Wrapf(err, "cannot get container logs")
		}
		docker.reader = logReader

		docker.ForwardLogs(logReader)
	}
	return docker.WaitForStop()
}

func (docker *Docker) WaitForStop() error {
	if docker.instance == nil || docker.instance.ID == "" {
		return fmt.Errorf("no running instance to wait for")
	}

	resultC, errC := docker.client.ContainerWait(context.Background(), docker.instance.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errC:
		if err != nil {
			return err
		}
	case result := <-resultC:
		statusCode := result.StatusCode
		if statusCode != 0 {
			return fmt.Errorf("container exited with status %d", statusCode)
		}
		return nil
	}
	return nil
}

func (docker *Docker) ForwardLogs(reader io.Reader) {
	if docker.out == nil {
		return
	}
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
				if err := scanner.Err(); err != nil {
					output <- []byte(strings.TrimSpace(err.Error()))
				}

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
	if docker == nil {
		return nil
	}
	if docker.reader != nil {
		defer func() {
			docker.reader.Close()
		}()
	}
	if docker.persist {
		return nil
	}
	if docker.instance == nil || docker.instance.ID == "" {
		return nil
	}
	err := docker.client.ContainerStop(context.Background(), docker.instance.ID, container.StopOptions{Timeout: shared.Pointer(3)})
	if err != nil {
		_ = docker.client.ContainerKill(context.Background(), docker.instance.ID, "SIGKILL")
	}
	return nil
}

// KillAll kills all Docker containers started by codefly: container name = /codefly-...
func (docker *Docker) KillAll(ctx context.Context) error {
	containers, err := docker.client.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return err
	}
	for i := range containers {
		c := containers[i]
		if strings.HasPrefix(c.Names[0], "/codefly-") {
			err := docker.client.ContainerStop(ctx, c.ID, container.StopOptions{})
			if err != nil {
				return err
			}
		}
	}
	return nil
}
