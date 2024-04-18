package base

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/pkg/stdcopy"

	"github.com/docker/docker/api/types"

	"github.com/codefly-dev/core/shared"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/go-connections/nat"

	"github.com/codefly-dev/core/configurations"

	"github.com/codefly-dev/core/wool"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
)

type DockerEnvironment struct {
	client *client.Client
	image  *configurations.DockerImage

	// Name of the environment
	name string

	localDir   string
	workingDir string

	// Override default cmd of the Docker Image
	cmd []string
	// or "pause": don't run the cmd
	pause bool

	mounts []mount.Mount

	envs []string

	portMappings []*DockerPortMapping

	instance *DockerContainerInstance

	out    io.Writer
	reader io.ReadCloser

	firstInit bool
}

func (docker *DockerEnvironment) WithEnvironmentVariables(envs ...configurations.EnvironmentVariable) {
	docker.envs = append(docker.envs, configurations.EnvironmentVariableAsStrings(envs)...)
}

// NewDockerEnvironment creates a new docker runner
func NewDockerEnvironment(ctx context.Context, image *configurations.DockerImage, dir string, name string) (*DockerEnvironment, error) {
	w := wool.Get(ctx).In("NewDockerRunner")
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, w.Wrapf(err, "cannot create docker client")
	}
	env := &DockerEnvironment{
		firstInit:  true,
		client:     cli,
		out:        w,
		image:      image,
		name:       ContainerName(name),
		workingDir: "/codefly",
	}
	// Will mount the local directory on /codefly the workDir
	env.WithDir(dir)
	// Pull the image if needed
	err = env.GetImageIfNotPresent(ctx, image)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get image")
	}
	return env, nil
}

// NewDockerHeadlessEnvironment creates a new docker runner
func NewDockerHeadlessEnvironment(ctx context.Context, image *configurations.DockerImage, name string) (*DockerEnvironment, error) {
	w := wool.Get(ctx).In("NewDockerRunner")
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, w.Wrapf(err, "cannot create docker client")
	}
	env := &DockerEnvironment{
		firstInit:  true,
		client:     cli,
		out:        w,
		image:      image,
		name:       ContainerName(name),
		workingDir: "/codefly",
	}
	// Pull the image if needed
	err = env.GetImageIfNotPresent(ctx, image)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get image")
	}
	return env, nil
}

func (docker *DockerEnvironment) ContainerID() (string, error) {
	if docker.instance == nil {
		return "", fmt.Errorf("no running container")
	}
	return docker.instance.ID, nil
}

func (docker *DockerEnvironment) WithMount(sourceDir string, targetDir string) {
	docker.mounts = append(docker.mounts, mount.Mount{
		Type:   mount.TypeBind,
		Source: sourceDir,
		Target: targetDir,
	})
}

func (docker *DockerEnvironment) WithPause() {
	docker.pause = true
}

func ContainerName(name string) string {
	return fmt.Sprintf("codefly-%s", strings.ReplaceAll(name, "/", "-"))
}

func (docker *DockerEnvironment) WithDir(dir string) {
	docker.localDir = dir
	docker.WithMount(dir, docker.workingDir)
}

func (docker *DockerEnvironment) Init(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.Start")
	// Get the container
	err := docker.GetContainer(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot create container")
	}
	docker.firstInit = false
	return nil
}

func (docker *DockerEnvironment) IsContainerPresent(ctx context.Context) (bool, error) {
	w := wool.Get(ctx).In("Docker.IsContainerPresent")
	// List all containers
	containers, err := docker.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return false, err
	}

	// Check if a container with the given name is running
	for i := range containers {
		c := containers[i]
		for _, name := range c.Names {
			if name == "/"+docker.name {
				docker.instance = &DockerContainerInstance{
					ID: c.ID,
				}
				w.Debug("container found", wool.Field("id", c.ID))
				return true, nil
			}
		}
	}
	return false, nil
}

func (docker *DockerEnvironment) GetContainer(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.GetContainer")
	w.Debug("getting container", wool.Field("image", docker.image.FullName()), wool.Field("name", docker.name))

	exists, err := docker.IsContainerPresent(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot check if container is running")
	}
	if exists {
		w.Debug("container found", wool.Field("id", docker.instance.ID))
		// make sure it's running
		inspect, err := docker.client.ContainerInspect(ctx, docker.instance.ID)
		if err != nil {
			return w.Wrapf(err, "cannot inspect container")
		}
		if inspect.State.Running {
			return nil
		}
		w.Debug("container was found but not running: starting it again")
		err = docker.startContainer(ctx, docker.instance.ID)
		if err != nil {
			return w.Wrapf(err, "cannot start container")
		}
		w.Debug("container should be running now")
		return nil
	}

	containerConfig := &container.Config{
		Image:        docker.image.FullName(),
		Env:          docker.envs,
		Tty:          true,
		WorkingDir:   docker.workingDir,
		ExposedPorts: docker.exposedPortSet(),
	}
	if len(docker.cmd) > 0 {
		containerConfig.Cmd = docker.cmd
	} else if docker.pause {
		containerConfig.Cmd = []string{"sleep", "infinity"}
	}

	hostConfig := &container.HostConfig{
		Mounts:       docker.mounts,
		AutoRemove:   false,
		PortBindings: docker.portBindings(),
	}

	// Add extra host only for Linux
	if runtime.GOOS == "linux" {
		hostConfig.ExtraHosts = []string{"host.docker.internal:172.17.0.1"}
	}
	w.Debug("creating container",
		wool.Field("config", containerConfig.ExposedPorts),
		wool.Field("host config", hostConfig.PortBindings))

	// Create the container
	resp, err := docker.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, docker.name)
	if err != nil {
		return w.Wrapf(err, "cannot create container")
	}
	w.Debug("created container", wool.Field("id", resp.ID))

	docker.instance = &DockerContainerInstance{
		ID: resp.ID,
	}

	// Start the container
	err = docker.startContainer(ctx, resp.ID)
	if err != nil {
		return w.Wrapf(err, "cannot start container")
	}

	// Get the logs
	options := container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: false}
	logReader, err := docker.client.ContainerLogs(ctx, docker.instance.ID, options)
	if err != nil {
		return w.Wrapf(err, "cannot get container logs")
	}
	docker.reader = logReader
	Forward(ctx, logReader, docker.out)

	return nil
}

func (docker *DockerEnvironment) startContainer(ctx context.Context, containerID string) error {
	w := wool.Get(ctx).In("Docker.startContainer")
	err := docker.client.ContainerStart(ctx, containerID, container.StartOptions{})
	if err != nil {
		return w.Wrapf(err, "cannot start container")
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		inspect, err := docker.client.ContainerInspect(ctx, containerID)
		if err != nil {
			return err
		}

		if inspect.State.Running {
			break
		}
		w.Debug("container not running yet", wool.Field("status", inspect.State.Status))
		// If the container is not running, wait for a while before checking again
		time.Sleep(time.Second)
		if time.Now().After(deadline) {
			return w.NewError("container did not start in time")
		}
	}
	w.Debug("container running")
	return nil
}
func (docker *DockerEnvironment) exposedPortSet() nat.PortSet {
	set := nat.PortSet{}
	for _, portMapping := range docker.portMappings {
		containerPort := nat.Port(fmt.Sprintf("%d/tcp", portMapping.Container))
		set[containerPort] = struct{}{}
	}
	return set
}

func (docker *DockerEnvironment) portBindings() nat.PortMap {
	portMap := nat.PortMap{}
	for _, portMapping := range docker.portMappings {
		port := nat.Port(fmt.Sprintf("%d/tcp", portMapping.Container))
		portMap[port] = []nat.PortBinding{
			{
				HostIP:   "0.0.0.0",
				HostPort: fmt.Sprintf("%d", portMapping.Host),
			},
		}
	}
	return portMap
}

func (docker *DockerEnvironment) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.Stop")
	if docker.instance == nil || docker.instance.ID == "" {
		return nil
	}
	err := docker.client.ContainerStop(context.Background(), docker.instance.ID, container.StopOptions{Timeout: shared.Pointer(3)})
	if err != nil {
		return w.Wrapf(err, "cannot stop container")
	}
	return nil
}

func (docker *DockerEnvironment) Shutdown(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.Shutdown")
	exists, err := docker.IsContainerPresent(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot check if container is running")
	}
	if exists {
		err = docker.Stop(ctx)
		if err != nil {
			return w.Wrapf(err, "cannot stop container")
		}
		err = docker.remove()
		if err != nil {
			return w.Wrapf(err, "cannot remove container")

		}
	}
	return nil
}

func (docker *DockerEnvironment) Clear(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.Clear")
	err := docker.Shutdown(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot shutdown")
	}
	return nil
}

func (docker *DockerEnvironment) remove() error {
	if docker.instance == nil || docker.instance.ID == "" {
		return nil
	}
	err := docker.client.ContainerRemove(context.Background(), docker.instance.ID, container.RemoveOptions{Force: true})
	if err != nil {
		return err
	}
	return nil
}

/*
DockerProc is a process running inside a Docker container
*/
type DockerProc struct {
	env    *DockerEnvironment
	output io.Writer
	cmd    []string

	envs []string
}

func (proc *DockerProc) WithEnvironmentVariables(envs ...configurations.EnvironmentVariable) {
	proc.envs = append(proc.envs, configurations.EnvironmentVariableAsStrings(envs)...)
}

func (docker *DockerEnvironment) NewProcess(bin string, args ...string) (Proc, error) {
	cmd := append([]string{bin}, args...)
	return &DockerProc{env: docker, cmd: cmd, output: docker.out}, nil
}

func (proc *DockerProc) Run(ctx context.Context) error {
	w := wool.Get(ctx).In("DockerProc.Run")
	w.Debug("running process", wool.Field("cmd", proc.cmd))
	err := proc.start(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot start process")
	}
	// Periodically check if the process with docker.execPID is still running
	for {
		// Sleep for a bit before checking again
		time.Sleep(time.Second)
		// Check if the process is still active
		active, err := proc.isProcessActive(ctx)
		if err != nil {
			return w.Wrapf(err, "error checking process status")
		}
		if !active {
			w.Debug("process has exited")
			break
		}
	}
	w.Debug("done")
	return nil
}
func (proc *DockerProc) FindPid(ctx context.Context, command []string) (int, error) {
	// Construct the command to execute 'ps' inside the container
	psCmd := []string{"ps", "aux"}
	execConfig := types.ExecConfig{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          psCmd,
	}

	// Create an exec instance inside the container to run the command
	execIDResp, err := proc.env.client.ContainerExecCreate(ctx, proc.env.instance.ID, execConfig)
	if err != nil {
		return 0, fmt.Errorf("cannot create exec to list processes: %w", err)
	}

	// Attach to the exec instance to capture the output
	execAttachResp, err := proc.env.client.ContainerExecAttach(ctx, execIDResp.ID, types.ExecStartCheck{})
	if err != nil {
		return 0, fmt.Errorf("cannot attach to exec for listing processes: %w", err)
	}
	defer execAttachResp.Close()

	// Capture and process the command output
	var outBuf, errBuf strings.Builder
	if _, err := stdcopy.StdCopy(&outBuf, &errBuf, execAttachResp.Reader); err != nil {
		return 0, fmt.Errorf("error reading output from exec: %w", err)
	}
	// Parse the output from 'ps' to find the process by command
	lines := strings.Split(outBuf.String(), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 4 { // Ensure there are enough fields for PID and CMD
			cmdPart := strings.Join(fields[3:], " ")
			if strings.Join(command, " ") == cmdPart {
				pid, err := strconv.Atoi(fields[0])
				if err != nil {
					return 0, fmt.Errorf("error converting PID to int: %w", err)
				}
				return pid, nil
			}
		}
	}
	return 0, nil
}

// isProcessActive checks if a process with the given PID is still running in the container.
func (proc *DockerProc) isProcessActive(ctx context.Context) (bool, error) {
	pid, err := proc.FindPid(ctx, proc.cmd)
	if err != nil {
		return false, err
	}
	return pid != 0, nil

}

func (proc *DockerProc) Start(ctx context.Context) error {
	w := wool.Get(ctx).In("DockerProc.Start")
	w.Debug("starting process", wool.Field("cmd", proc.cmd))
	return proc.start(ctx)
}

func (proc *DockerProc) start(ctx context.Context) error {
	w := wool.Get(ctx).In("DockerProc.start")
	// Ensure the container is running
	err := proc.env.GetContainer(ctx)
	if err != nil {
		return err
	}

	// Create an exec configuration
	execConfig := types.ExecConfig{
		AttachStdout: true,
		AttachStderr: true,
		Env:          proc.envs,
		Cmd:          proc.cmd,
	}
	w.Debug("creating exec", wool.Field("cmd", proc.cmd))
	// Create an exec instance
	execIDResp, err := proc.env.client.ContainerExecCreate(ctx, proc.env.instance.ID, execConfig)
	if err != nil {
		return err
	}
	// Start the exec instance
	execStartCheck := types.ExecStartCheck{
		Detach: false,
		Tty:    false,
	}
	execResp, err := proc.env.client.ContainerExecAttach(ctx, execIDResp.ID, execStartCheck)
	if err != nil {
		return err
	}

	go func() {
		defer execResp.Close()

		// Wrap the reader with stdcopy to demultiplex stdout and stderr
		stdOutWriter := newCustomWriter(proc.output)
		_, err := stdcopy.StdCopy(stdOutWriter, stdOutWriter, execResp.Reader)
		if err != nil {
			w.Error("cannot copy output", wool.ErrField(err))
		}
	}()
	return nil
}

func (proc *DockerProc) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("DockerProc.Stop")
	pid, err := proc.FindPid(ctx, proc.cmd)
	if err != nil {
		return err
	}

	w.Debug("killing process", wool.Field("pid", pid), wool.Field("cmd", proc.cmd))

	killCmd := []string{"sh", "-c", fmt.Sprintf("kill %d", pid)}
	execConfig := types.ExecConfig{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          killCmd,
	}
	execIDResp, err := proc.env.client.ContainerExecCreate(ctx, proc.env.instance.ID, execConfig)
	if err != nil {
		return w.Wrapf(err, "cannot create exec to kill")
	}

	execStartCheck := types.ExecStartCheck{Detach: false, Tty: false}
	execResp, err := proc.env.client.ContainerExecAttach(ctx, execIDResp.ID, execStartCheck)
	if err != nil {
		return w.Wrapf(err, "cannot kill process")
	}

	// Capture and log the output from the exec command, which might include error messages from 'kill'
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, execResp.Reader); err != nil {
		w.Error("error capturing output from kill command", wool.ErrField(err))
		// Depending on the severity, decide whether to return an error
	}

	// Log the outputs for diagnostics
	if stdout.Len() > 0 {
		w.Debug("stdout from kill command", wool.Field("output", stdout.String()))
	}
	if stderr.Len() > 0 {
		w.Debug("stderr from kill command", wool.Field("output", stderr.String()))
	}
	w.Debug("killed process")
	// Process is killed; you might want to handle output or errors
	return nil
}

// customWriter wraps an io.Writer to process the output line by line.
type customWriter struct {
	writer io.Writer
}

func newCustomWriter(w io.Writer) *customWriter {
	return &customWriter{writer: w}
}

func (cw *customWriter) Write(p []byte) (n int, err error) {
	scanner := bufio.NewScanner(strings.NewReader(string(p)))
	for scanner.Scan() {
		line := scanner.Text() // This gives you the line without the newline character
		// Now you can process the line as needed and write it to the original output
		_, err := cw.writer.Write([]byte(line))
		if err != nil {
			return 0, err
		}
	}
	// Return the original length and no error to satisfy the Writer interface,
	// indicating all data was processed.
	return len(p), nil
}

func (proc *DockerProc) WithOutput(output io.Writer) {
	proc.output = output
}

func (docker *DockerEnvironment) ImageExists(ctx context.Context, imag *configurations.DockerImage) (bool, error) {
	w := wool.Get(ctx).In("Docker.ImageExists")
	images, err := docker.client.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return false, w.Wrapf(err, "cannot list images")
	}
	for i := range images {
		img := &images[i]
		for _, repoTag := range img.RepoTags {
			if repoTag == imag.FullName() {
				return true, nil
			}
		}
	}
	return false, nil
}

func (docker *DockerEnvironment) GetImageIfNotPresent(ctx context.Context, imag *configurations.DockerImage) error {
	w := wool.Get(ctx).In("Docker.GetImageIfNotPresent")
	if exists, err := docker.ImageExists(ctx, imag); err != nil {
		return w.Wrapf(err, "cannot check if image exists")
	} else if exists {
		w.Trace("found Docker image locally")
		return nil
	}
	_, _ = w.Forward([]byte(fmt.Sprintf("pulling Docker image %s. Will show progress every 5 seconds.", imag.FullName())))
	progress, err := docker.client.ImagePull(ctx, imag.FullName(), image.PullOptions{})
	if err != nil {
		return w.Wrapf(err, "cannot pull image: %s", imag.FullName())
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

func (docker *DockerEnvironment) WithPort(ctx context.Context, port uint16) {
	docker.WithPortMapping(ctx, port, port)

}

func (docker *DockerEnvironment) WithPortMapping(ctx context.Context, local uint16, container uint16) {
	w := wool.Get(ctx).In("WithPort")
	w.Debug("setting port", wool.Field("local", local), wool.Field("container", container))
	docker.portMappings = append(docker.portMappings, &DockerPortMapping{
		Host:      local,
		Container: container,
	})
}

func (docker *DockerEnvironment) WithOutput(logger io.Writer) {
	docker.out = logger
}

func (docker *DockerEnvironment) WithCommand(cmd ...string) {
	docker.cmd = cmd
}

func (docker *DockerEnvironment) WithWorkDir(dir string) {
	docker.workingDir = dir
}
