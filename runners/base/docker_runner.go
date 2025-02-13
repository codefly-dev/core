package base

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/pkg/stdcopy"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/go-connections/nat"

	"github.com/codefly-dev/core/wool"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
)

type DockerEnvironment struct {
	client *client.Client
	image  *resources.DockerImage
	name   string

	localDir   string
	workingDir string

	cmd   []string
	pause bool

	mounts       []mount.Mount
	envs         []*resources.EnvironmentVariable
	portMappings []*DockerPortMapping

	instance *DockerContainerInstance
	out      io.Writer
	reader   io.ReadCloser

	running   bool
	firstInit bool
}

var _ RunnerEnvironment = &DockerEnvironment{}

// NewDockerEnvironment creates a new docker environment
func NewDockerEnvironment(ctx context.Context, image *resources.DockerImage, dir string, name string) (*DockerEnvironment, error) {
	w := wool.Get(ctx).In("NewDockerEnvironment")
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, w.Wrapf(err, "cannot create docker client")
	}

	env := &DockerEnvironment{
		client:     cli,
		out:        w,
		image:      image,
		name:       ContainerName(name),
		workingDir: dir,
	}

	env.WithDir(dir)
	if err := env.GetImageIfNotPresent(ctx, image); err != nil {
		return nil, w.Wrapf(err, "cannot get image")
	}

	return env, nil
}

// NewDockerHeadlessEnvironment creates a new docker runner
func NewDockerHeadlessEnvironment(ctx context.Context, image *resources.DockerImage, name string) (*DockerEnvironment, error) {
	w := wool.Get(ctx).In("NewDockerRunner")
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, w.Wrapf(err, "cannot create docker client")
	}
	env := &DockerEnvironment{
		firstInit: true,
		client:    cli,
		out:       w,
		image:     image,
		name:      ContainerName(name),
	}
	// Pull the image if needed
	err = env.GetImageIfNotPresent(ctx, image)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get image")
	}
	return env, nil
}

func (docker *DockerEnvironment) Init(ctx context.Context) error {
	return docker.GetContainer(ctx)
}

func (docker *DockerEnvironment) GetContainer(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.GetContainer")

	exists, err := docker.IsContainerPresent(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot check if container is present")
	}
	defer func() {
		logContext := context.Background()
		err := docker.GetLogs(logContext)
		if err != nil {
			w.Error("cannot get logs", wool.ErrField(err))
		}
	}()

	if exists {
		return docker.ensureContainerRunning(ctx)
	}

	return docker.createAndStartContainer(ctx)
}

func (docker *DockerEnvironment) ensureContainerRunning(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.ensureContainerRunning")

	inspect, err := docker.client.ContainerInspect(ctx, docker.instance.ID)
	if err != nil {
		return w.Wrapf(err, "cannot inspect container")
	}

	if inspect.State.Running {
		docker.running = true
		return nil
	}

	w.Debug("container found but not running: starting it again")
	if err := docker.startContainer(ctx, docker.instance.ID); err != nil {
		return w.Wrapf(err, "cannot start container")
	}

	docker.running = true
	return nil
}

func generateDockerCreateCommand(containerConfig *container.Config, hostConfig *container.HostConfig, containerName string) string {
	var cmd []string
	cmd = append(cmd, "docker", "create")

	// Add environment variables
	for _, env := range containerConfig.Env {
		cmd = append(cmd, "--env", env)
	}

	// Add working directory
	if containerConfig.WorkingDir != "" {
		cmd = append(cmd, "--workdir", containerConfig.WorkingDir)
	}

	// Add exposed ports
	for port := range containerConfig.ExposedPorts {
		cmd = append(cmd, "--expose", string(port))
	}

	// Add mounts
	for _, mount := range hostConfig.Mounts {
		cmd = append(cmd, "--mount", fmt.Sprintf("type=%s,source=%s,target=%s", mount.Type, mount.Source, mount.Target))
	}

	// Add port bindings
	for port, bindings := range hostConfig.PortBindings {
		for _, binding := range bindings {
			cmd = append(cmd, "--publish", fmt.Sprintf("%s:%s", binding.HostPort, port.Port()))
		}
	}

	// Add command
	if len(containerConfig.Cmd) > 0 {
		cmd = append(cmd, containerConfig.Cmd...)
	}

	// Add container name
	if containerName != "" {
		cmd = append(cmd, "--name", containerName)
	}

	// Add image
	cmd = append(cmd, containerConfig.Image)

	return strings.Join(cmd, " ")
}

func (docker *DockerEnvironment) createAndStartContainer(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.createAndStartContainer")

	containerConfig := docker.createContainerConfig(ctx)
	hostConfig := docker.createHostConfig(ctx)

	w.Debug("create container config",
		wool.Field("CLI equivalent", generateDockerCreateCommand(containerConfig, hostConfig, docker.name)),
		wool.Field("container", containerConfig),
		wool.Field("host", hostConfig),
	)

	resp, err := docker.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, docker.name)
	if err != nil {
		return w.Wrapf(err, "cannot create container")
	}

	docker.instance = &DockerContainerInstance{ID: resp.ID}
	w.Debug("created container", wool.Field("id", resp.ID))

	if err := docker.startContainer(ctx, resp.ID); err != nil {
		return w.Wrapf(err, "cannot start container")
	}

	docker.running = true
	return nil
}

func (docker *DockerEnvironment) createContainerConfig(ctx context.Context) *container.Config {
	w := wool.Get(ctx).In("Docker.createContainerConfig")
	config := &container.Config{
		Image:        docker.image.FullName(),
		Env:          resources.EnvironmentVariableAsStrings(docker.envs),
		Tty:          true,
		WorkingDir:   docker.workingDir,
		ExposedPorts: docker.exposedPortSet(),
	}

	if len(docker.cmd) > 0 {
		w.Debug("overriding command", wool.Field("cmd", docker.cmd))
		config.Cmd = docker.cmd
	} else if docker.pause {
		config.Cmd = []string{"sleep", "infinity"}
		config.Entrypoint = []string{}
	}

	return config
}

func (docker *DockerEnvironment) createHostConfig(_ context.Context) *container.HostConfig {
	mounts := append([]mount.Mount{
		{
			Type:   mount.TypeBind,
			Source: "/var/run/docker.sock",
			Target: "/var/run/docker.sock",
		},
	}, docker.mounts...)

	hostConfig := &container.HostConfig{
		Mounts:       mounts,
		AutoRemove:   false,
		PortBindings: docker.portBindings(),
	}

	if runtime.GOOS == "linux" {
		hostConfig.ExtraHosts = []string{"host.docker.internal:172.17.0.1"}
	}

	return hostConfig
}

func (docker *DockerEnvironment) startContainer(ctx context.Context, containerID string) error {
	w := wool.Get(ctx).In("Docker.startContainer")

	if err := docker.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return w.Wrapf(err, "cannot start container")
	}

	return docker.waitForContainerToRun(ctx, containerID)
}

func (docker *DockerEnvironment) waitForContainerToRun(ctx context.Context, containerID string) error {
	w := wool.Get(ctx).In("Docker.waitForContainerToRun")

	deadline := time.Now().Add(30 * time.Second)
	for {
		inspect, err := docker.client.ContainerInspect(ctx, containerID)
		if err != nil {
			return w.Wrapf(err, "cannot inspect container")
		}

		if inspect.State.Running {
			return nil
		}

		if time.Now().After(deadline) {
			return w.NewError("container did not start in time")
		}

		// If status exited: error
		if inspect.State.Status == "exited" {
			return w.NewError("container exited with status %s", inspect.State.Status)
		}

		w.Debug("container not running yet", wool.Field("status", inspect.State.Status))
		time.Sleep(time.Second)
	}
}

func (docker *DockerEnvironment) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	w := wool.Get(ctx).In("WithEnvironmentVariables")
	w.Debug("adding", wool.Field("envs", envs))
	docker.envs = append(docker.envs, envs...)
}

func (docker *DockerEnvironment) ContainerID() (string, error) {
	if docker.instance == nil {
		return "", fmt.Errorf("no running container")
	}
	return docker.instance.ID, nil
}

func (docker *DockerEnvironment) WithMount(sourceDir string, targetDir string) error {
	// Check if the source directory exists and is absolute path
	if !filepath.IsAbs(sourceDir) {
		return fmt.Errorf("source directory must be an absolute path")
	}

	docker.mounts = append(docker.mounts, mount.Mount{
		Type:   mount.TypeBind,
		Source: sourceDir,
		Target: targetDir,
	})
	return nil
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

func (docker *DockerEnvironment) IsContainerPresent(ctx context.Context) (bool, error) {
	w := wool.Get(ctx).In("Docker.IsContainerPresent")
	// List all containers
	containers, err := docker.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return false, err
	}
	w.Debug("checking if container is running", wool.Field("name", docker.name))

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

func (docker *DockerEnvironment) GetLogs(ctx context.Context) error {
	w := wool.Get(ctx).In("Docker.GetLogs")
	w.Debug("getting logs")
	if !docker.running {
		return nil
	}
	options := container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: false}
	logReader, err := docker.client.ContainerLogs(ctx, docker.instance.ID, options)
	if err != nil {
		return w.Wrapf(err, "cannot get container logs")
	}
	docker.reader = logReader
	Forward(ctx, logReader, docker.out)
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

func (docker *DockerEnvironment) WithBinary(bin string) error {
	proc, err := docker.NewProcess("which", bin)
	if err != nil {
		return err
	}
	err = proc.Run(context.Background())
	return err
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

	envs []*resources.EnvironmentVariable

	cmd []string

	// optional override
	dir    string
	ID     string
	waitOn string
}

func (proc *DockerProc) WithEnvironmentVariablesAppend(ctx context.Context, added *resources.EnvironmentVariable, sep string) {
	for _, env := range proc.envs {
		if env.Key == env.Key {
			env.Value = fmt.Sprintf("%v%s%v", env.Value, sep, added.Value)
			return
		}
	}
	proc.envs = append(proc.envs, added)
}

func (proc *DockerProc) IsRunning(ctx context.Context) (bool, error) {
	pid, err := proc.FindPid(ctx)
	if err != nil {
		return false, err
	}
	return pid > 0, nil
}

func (proc *DockerProc) WaitOn(bin string) {
	proc.waitOn = bin
}

func (proc *DockerProc) WithDir(dir string) {
	proc.dir = dir
}

func (proc *DockerProc) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	w := wool.Get(ctx).In("WithEnvironmentVariables")
	w.Debug("adding", wool.Field("envs", envs))
	proc.envs = append(proc.envs, envs...)
}

func (docker *DockerEnvironment) NewProcess(bin string, args ...string) (Proc, error) {
	cmd := append([]string{bin}, args...)
	return &DockerProc{env: docker, cmd: cmd, output: docker.out}, nil
}
func (proc *DockerProc) Run(ctx context.Context) error {
	w := wool.Get(ctx).In("DockerProc.Run")
	w.Debug("running process", wool.Field("cmd", proc.cmd))

	if err := proc.start(ctx); err != nil {
		return w.Wrapf(err, "cannot start process")
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Get process status
			inspect, err := proc.env.client.ContainerExecInspect(ctx, proc.ID)
			if err != nil {
				return w.Wrapf(err, "cannot inspect process")
			}

			// Get PS output for debugging
			psOutput, err := proc.getPSOutput(ctx)
			if err != nil {
				w.Debug("error getting ps output", wool.ErrField(err))
			} else {
				w.Debug("current processes in container",
					wool.Field("ps_output", psOutput),
					wool.Field("looking_for_cmd", proc.cmd))
			}

			if !inspect.Running {
				w.Debug("process inspection shows not running",
					wool.Field("exit_code", inspect.ExitCode),
					wool.Field("pid", inspect.Pid))

				if inspect.ExitCode == 0 {
					return nil
				}
				return fmt.Errorf("process exited with code %d", inspect.ExitCode)
			}

			active, err := proc.isProcessActive(ctx)
			if err != nil {
				return w.Wrapf(err, "error checking process status")
			}

			pid, _ := proc.FindPid(ctx)
			w.Debug("process status check",
				wool.Field("active", active),
				wool.Field("pid", pid),
				wool.Field("exec_running", inspect.Running))

			if !active {
				w.Debug("process has exited")
				return nil
			}
		}
	}
}

// Add new helper method to get PS output
func (proc *DockerProc) getPSOutput(ctx context.Context) (string, error) {
	w := wool.Get(ctx).In("DockerProc.getPSOutput")

	execConfig := container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"ps", "aux"},
	}

	execIDResp, err := proc.env.client.ContainerExecCreate(ctx, proc.env.instance.ID, execConfig)
	if err != nil {
		return "", w.Wrapf(err, "cannot create exec to list processes")
	}

	execAttachResp, err := proc.env.client.ContainerExecAttach(ctx, execIDResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", w.Wrapf(err, "cannot attach to exec")
	}
	defer execAttachResp.Close()

	var outBuf, errBuf strings.Builder
	if _, err := stdcopy.StdCopy(&outBuf, &errBuf, execAttachResp.Reader); err != nil {
		return "", w.Wrapf(err, "cannot copy output from exec")
	}

	return outBuf.String(), nil
}

func (proc *DockerProc) FindPid(ctx context.Context) (int, error) {
	w := wool.Get(ctx).In("DockerProc.FindPid")
	// Construct the command to execute 'ps' inside the container
	psCmd := []string{"/bin/ps"}
	execConfig := container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          psCmd,
	}

	// Create an exec instance inside the container to run the command
	execIDResp, err := proc.env.client.ContainerExecCreate(ctx, proc.env.instance.ID, execConfig)
	if err != nil {
		return 0, w.Wrapf(err, "cannot create exec to list processes")
	}

	// Attach to the exec instance to capture the output
	execAttachResp, err := proc.env.client.ContainerExecAttach(ctx, execIDResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return 0, w.Wrapf(err, "cannot attach to exec")
	}
	defer execAttachResp.Close()

	// Capture and process the command output
	var outBuf, errBuf strings.Builder
	if _, err := stdcopy.StdCopy(&outBuf, &errBuf, execAttachResp.Reader); err != nil {
		return 0, w.Wrapf(err, "cannot copy output from exec")
	}
	// Parse the output from 'ps' to find the process by command
	lines := strings.Split(outBuf.String(), "\n")
	if len(lines) == 1 {
		return 0, nil
	}
	// Split the header into fields
	fields := strings.Fields(lines[0])

	// Find the index of the CMD field
	pidIndex := -1
	cmdIndex := -1
	for i, field := range fields {
		if field == "CMD" || field == "COMMAND" {
			cmdIndex = i
		}
		if field == "PID" {
			pidIndex = i
		}
	}
	if pidIndex < 0 {
		return 0, fmt.Errorf("cannot find PID field in ps output")
	}
	if cmdIndex < 0 {
		return 0, fmt.Errorf("cannot find CMD field in ps output")
	}

	for _, line := range lines[1:] {
		fs := strings.Fields(line)
		if len(fs) < max(pidIndex, cmdIndex) {
			continue // Ensure there are enough fs for PID and CMD
		}
		cmd := fs[cmdIndex:]

		if proc.Match(cmd) {
			pid, err := strconv.Atoi(fs[pidIndex])
			if err != nil {
				return 0, w.Wrapf(err, "cannot parse PID")
			}
			return pid, nil

		}
	}
	return -1, nil
}

// isProcessActive checks if a process with the given PID is still running in the container.
func (proc *DockerProc) isProcessActive(ctx context.Context) (bool, error) {
	pid, err := proc.FindPid(ctx)
	if err != nil {
		return false, err
	}
	return pid > 0, nil

}

func (proc *DockerProc) Start(ctx context.Context) error {
	w := wool.Get(ctx).In("DockerProc.Start")
	w.Debug("starting process", wool.Field("cmd", proc.cmd))
	return proc.start(ctx)
}

func (proc *DockerProc) start(ctx context.Context) error {
	w := wool.Get(ctx).In("DockerProc.start")
	w.Debug("running process", wool.Field("cmd", proc.cmd), wool.Field("envs", proc.env.envs))

	// Ensure the container is running
	err := proc.env.GetContainer(ctx)
	if err != nil {
		return err
	}

	// Filter out PATH!
	envs := make([]*resources.EnvironmentVariable, 0)
	for _, env := range proc.envs {
		if env.Key == "PATH" {
			continue
		}
		envs = append(envs, env)
	}
	// Create an exec configuration
	execConfig := container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Env:          resources.EnvironmentVariableAsStrings(envs),
		Cmd:          proc.cmd,
	}
	if proc.dir != "" {
		execConfig.WorkingDir = proc.dir
	}
	w.Debug("creating exec", wool.Field("cmd", proc.cmd))
	// Create an exec instance
	execIDResp, err := proc.env.client.ContainerExecCreate(ctx, proc.env.instance.ID, execConfig)
	if err != nil {
		return err
	}
	// Start the exec instance
	execStartCheck := container.ExecStartOptions{
		Detach: false,
		Tty:    false,
	}
	execResp, err := proc.env.client.ContainerExecAttach(ctx, execIDResp.ID, execStartCheck)
	if err != nil {
		return err
	}
	proc.ID = execIDResp.ID

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
	w.Debug("stopping process", wool.Field("cmd", proc.cmd))

	pid, err := proc.FindPid(ctx)
	if err != nil {
		return err
	}
	if pid < 0 {
		return nil
	}
	w.Debug("killing process", wool.Field("pid", pid), wool.Field("cmd", proc.cmd))
	err = proc.stop(ctx, pid, false)

	// Start a go-routing to kill it forcefully after some time
	go func() {
		time.Sleep(3 * time.Second)
		pid, err = proc.FindPid(ctx)
		if err != nil {
			w.Warn("can't get PID")
		}
		if pid < 0 {
			return
		}
		_ = proc.stop(ctx, pid, true)
	}()
	return err
}

func (proc *DockerProc) stop(ctx context.Context, pid int, force bool) error {
	w := wool.Get(ctx).In("DockerProc.Stop")
	w.Debug("stopping process", wool.Field("pid", pid))

	killCmd := []string{"/bin/kill", fmt.Sprintf("%d", pid)}
	if force {
		killCmd = append(killCmd, "-9")
	}
	execConfig := container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          killCmd,
	}
	execIDResp, err := proc.env.client.ContainerExecCreate(context.Background(), proc.env.instance.ID, execConfig)
	if err != nil {
		return w.Wrapf(err, "cannot create exec to kill")
	}

	execStartCheck := container.ExecStartOptions{Detach: false, Tty: false}
	execResp, err := proc.env.client.ContainerExecAttach(context.Background(), execIDResp.ID, execStartCheck)
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

func (proc *DockerProc) Match(cmd []string) bool {
	if proc.waitOn != "" {
		return cmd[0] == proc.waitOn
	}
	return strings.Contains(proc.cmd[0], cmd[0])
}

func (docker *DockerEnvironment) GetImageIfNotPresent(ctx context.Context, imag *resources.DockerImage) error {
	return GetImageIfNotPresent(ctx, docker.client, imag, docker.out)
}

func GetImageIfNotPresent(ctx context.Context, c *client.Client, imag *resources.DockerImage, out io.Writer) error {
	w := wool.Get(ctx).In("Docker.GetImageIfNotPresent")
	if exists, err := ImageExists(ctx, c, imag); err != nil {
		return w.Wrapf(err, "cannot check if image exists")
	} else if exists {
		w.Trace("found Docker image locally")
		return nil
	}
	_, _ = w.Forward([]byte(fmt.Sprintf("pulling Docker image %s. Will show progress every 5 seconds.", imag.FullName())))
	progress, err := c.ImagePull(ctx, imag.FullName(), image.PullOptions{})
	if err != nil {
		return w.Wrapf(err, "cannot pull image: %s", imag.FullName())
	}

	PrintDownloadPercentage(progress, out)

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

func Forward(ctx context.Context, reader io.Reader, writer io.Writer) {
	// Create a new scanner for the buffer
	scanner := bufio.NewScanner(reader)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:

				// Scan the buffer line by line
				for scanner.Scan() {
					// Get the current line and trim the newline character
					line := strings.TrimSuffix(scanner.Text(), "\n")

					// Write the trimmed line to the output
					_, err := writer.Write([]byte(line))
					if err != nil {
						_, _ = writer.Write([]byte("Error while writing container logs"))
					}
				}

				// Check if the scanner encountered any errors
				if err := scanner.Err(); err != nil {
					_, _ = writer.Write([]byte(fmt.Sprintf("Error while scanning container logs: %s", err)))
				}
			}
		}
	}()
}

func (docker *DockerEnvironment) WithOutput(w io.Writer) {
	docker.out = w
}

func (docker *DockerEnvironment) WithCommand(cmd ...string) {
	docker.cmd = cmd
}

func (docker *DockerEnvironment) WithWorkDir(dir string) {
	docker.workingDir = dir
}

// ContainerDeleted checks if the container with ID is gone
func (docker *DockerEnvironment) ContainerDeleted() (bool, error) {
	containers, err := docker.client.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		return false, err
	}
	for _, c := range containers {
		if c.ID == docker.instance.ID {
			return false, nil
		}
	}
	return true, nil
}

func (docker *DockerEnvironment) ImageExists(ctx context.Context, imag *resources.DockerImage) (bool, error) {
	return ImageExists(ctx, docker.client, imag)

}

func ImageExists(ctx context.Context, c *client.Client, imag *resources.DockerImage) (bool, error) {
	w := wool.Get(ctx).In("Docker.ImageExists")
	images, err := c.ImageList(ctx, image.ListOptions{})
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
