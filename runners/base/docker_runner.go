package base

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
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

	// mu guards envs / portMappings / mounts which may be mutated via
	// WithEnvironmentVariables / WithPortMapping / WithMount while another
	// goroutine is reading them during container creation. The leak review
	// flagged the append-without-lock pattern.
	mu           sync.Mutex
	mounts       []mount.Mount
	envs         []*resources.EnvironmentVariable
	portMappings []*DockerPortMapping

	instance *DockerContainerInstance
	out      io.Writer
	reader   io.ReadCloser

	running bool
}

var _ RunnerEnvironment = &DockerEnvironment{}

// NewDockerEnvironment creates a new docker environment.
//
// If GetImageIfNotPresent fails, the docker client is closed before
// returning the error so the caller doesn't have to know about the
// half-constructed env (and isn't expected to call Shutdown on a
// nil return). Same defensive close in NewDockerHeadlessEnvironment.
func NewDockerEnvironment(ctx context.Context, image *resources.DockerImage, dir, name string) (*DockerEnvironment, error) {
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
		_ = cli.Close()
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
		client: cli,
		out:    w,
		image:  image,
		name:   ContainerName(name),
	}
	// Pull the image if needed
	if err = env.GetImageIfNotPresent(ctx, image); err != nil {
		_ = cli.Close()
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
		// Clean up the created-but-unstartable container — otherwise it
		// accumulates under the workspace name and blocks the next run
		// from creating its own container with the same name. Use a fresh
		// bounded ctx in case the caller's is already cancelled.
		rmCtx, rmCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer rmCancel()
		if rmErr := docker.client.ContainerRemove(rmCtx, resp.ID, container.RemoveOptions{Force: true}); rmErr != nil {
			w.Warn("cannot remove container after failed start",
				wool.Field("id", resp.ID), wool.ErrField(rmErr))
		}
		docker.instance = nil
		return w.Wrapf(err, "cannot start container")
	}

	docker.running = true
	return nil
}

func (docker *DockerEnvironment) createContainerConfig(ctx context.Context) *container.Config {
	w := wool.Get(ctx).In("Docker.createContainerConfig")
	docker.mu.Lock()
	envCopy := append([]*resources.EnvironmentVariable(nil), docker.envs...)
	docker.mu.Unlock()
	config := &container.Config{
		Image:        docker.image.FullName(),
		Env:          resources.EnvironmentVariableAsStrings(envCopy),
		Tty:          true,
		WorkingDir:   docker.workingDir,
		ExposedPorts: docker.exposedPortSet(),
		// Ryuk-style session labels. Every codefly-created container is
		// tagged with the CLI's PID; on the next `codefly run` startup, a
		// sweep removes containers whose owning CLI has died. Mirrors the
		// pgid file mechanism for native processes. Cheaper than running
		// a ryuk sidecar and matches codefly's "startup cleanup, not
		// runtime reaper" existing posture.
		Labels: map[string]string{
			LabelCodeflyOwner:   "true",
			LabelCodeflySession: strconv.Itoa(os.Getpid()),
			LabelCodeflyName:    docker.name,
		},
	}

	if len(docker.cmd) > 0 {
		w.Debug("overriding command", wool.Field("cmd", docker.cmd))
		config.Cmd = docker.cmd
	} else if docker.pause {
		config.Cmd = []string{"sleep", "infinity"}
	}

	return config
}

// Container labels used by the startup sweep to identify codefly-owned
// containers and their spawning CLI. These are set on every container
// created via DockerEnvironment and consumed by ReapStaleContainers.
const (
	LabelCodeflyOwner   = "codefly.owner"    // always "true"
	LabelCodeflySession = "codefly.session"  // PID of the spawning CLI
	LabelCodeflyName    = "codefly.name"     // container's logical name
)

func (docker *DockerEnvironment) createHostConfig(_ context.Context) *container.HostConfig {
	docker.mu.Lock()
	userMounts := append([]mount.Mount(nil), docker.mounts...)
	docker.mu.Unlock()
	mounts := append([]mount.Mount{
		{
			Type:   mount.TypeBind,
			Source: "/var/run/docker.sock",
			Target: "/var/run/docker.sock",
		},
	}, userMounts...)

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
	w.Trace("adding", wool.Field("envs", envs))
	docker.mu.Lock()
	defer docker.mu.Unlock()
	docker.envs = append(docker.envs, envs...)
}

func (docker *DockerEnvironment) ContainerID() (string, error) {
	if docker.instance == nil {
		return "", fmt.Errorf("no running container")
	}
	return docker.instance.ID, nil
}

func (docker *DockerEnvironment) WithMount(sourceDir string, targetDir string) {
	docker.mu.Lock()
	defer docker.mu.Unlock()
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
	docker.mu.Lock()
	mappings := append([]*DockerPortMapping(nil), docker.portMappings...)
	docker.mu.Unlock()
	set := nat.PortSet{}
	for _, portMapping := range mappings {
		containerPort := nat.Port(fmt.Sprintf("%d/tcp", portMapping.Container))
		set[containerPort] = struct{}{}
	}
	return set
}

func (docker *DockerEnvironment) portBindings() nat.PortMap {
	docker.mu.Lock()
	mappings := append([]*DockerPortMapping(nil), docker.portMappings...)
	docker.mu.Unlock()
	portMap := nat.PortMap{}
	for _, portMapping := range mappings {
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
	// Use a fresh bounded context so shutdown still runs when the caller
	// is already cancelled, but can't hang forever if the docker daemon
	// is unresponsive.
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := docker.client.ContainerStop(stopCtx, docker.instance.ID, container.StopOptions{Timeout: shared.Pointer(3)})
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
	defer docker.client.Close()
	// Close the follow-mode log reader held by GetLogs. Without this, the
	// Forward goroutine streaming container logs keeps a file descriptor
	// open against the docker daemon for every agent that started logging.
	if docker.reader != nil {
		_ = docker.reader.Close()
		docker.reader = nil
	}
	exists, err := docker.IsContainerPresent(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot check if container is running")
	}
	if exists {
		// Try graceful Stop first. If it fails (docker daemon unreachable,
		// container already gone, etc.), log and continue — Remove with
		// Force=true below will finish the job, but we prefer the in-
		// container SIGTERM grace to happen first so apps get a chance
		// to flush state / close db connections before we force-kill.
		if err := docker.Stop(ctx); err != nil {
			w.Warn("stop failed; falling back to force remove", wool.ErrField(err))
		}
		if err := docker.remove(); err != nil {
			return w.Wrapf(err, "cannot remove container")
		}
	}
	return nil
}

func (docker *DockerEnvironment) remove() error {
	if docker.instance == nil || docker.instance.ID == "" {
		return nil
	}
	rmCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := docker.client.ContainerRemove(rmCtx, docker.instance.ID, container.RemoveOptions{Force: true})
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

	// Pipe support for interactive/bidirectional communication.
	// Set before Start() via StdinPipe()/StdoutPipe().
	stdinReader  *io.PipeReader  // internal: feeds into docker exec stdin
	stdinWriter  *io.PipeWriter  // returned to caller
	stdoutReader *io.PipeReader  // returned to caller
	stdoutWriter *io.PipeWriter  // internal: receives docker exec stdout
}

func (proc *DockerProc) WithEnvironmentVariablesAppend(ctx context.Context, added *resources.EnvironmentVariable, sep string) {
	for _, env := range proc.envs {
		if env.Key == added.Key {
			env.Value = fmt.Sprintf("%v%s%v", env.Value, sep, added.Value)
			return
		}
	}
	proc.envs = append(proc.envs, added)
}

func (proc *DockerProc) IsRunning(ctx context.Context) (bool, error) {
	// FindPid scans /proc inside the container. There's a brief window
	// after Start where the new process hasn't populated /proc yet —
	// a blocking-fast assertion (`require.True(IsRunning)` immediately
	// after Start) flakes on CI's slower docker-in-docker scheduling.
	// Give the process a short grace period to appear before declaring
	// it dead. ctx still short-circuits the wait if the caller cancels.
	const startupGrace = 500 * time.Millisecond
	const pollInterval = 50 * time.Millisecond
	deadline := time.Now().Add(startupGrace)
	for {
		pid, err := proc.FindPid(ctx)
		if err != nil {
			return false, err
		}
		if pid > 0 {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// Wait blocks until the container process exits or ctx is cancelled.
// Polls IsRunning since Docker doesn't expose a clean blocking-wait here.
func (proc *DockerProc) Wait(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			running, err := proc.IsRunning(ctx)
			if err != nil {
				return err
			}
			if !running {
				return nil
			}
		}
	}
}

func (proc *DockerProc) WaitOn(bin string) {
	proc.waitOn = bin
}

func (proc *DockerProc) WithDir(dir string) {
	proc.dir = dir
}

func (proc *DockerProc) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	w := wool.Get(ctx).In("WithEnvironmentVariables")
	w.Trace("adding", wool.Field("envs", envs))
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

	// Previously this polled ContainerExecInspect AND a full /proc scan
	// (isProcessActive) every second — the /proc scan is a docker exec sh
	// invocation per tick and is redundant: ContainerExecInspect already
	// returns Running/ExitCode authoritatively. testcontainers-go uses the
	// same inspect-only pattern; see their docker.go:567-606.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			inspect, err := proc.env.client.ContainerExecInspect(ctx, proc.ID)
			if err != nil {
				return w.Wrapf(err, "cannot inspect process")
			}
			if inspect.Running {
				continue
			}
			if inspect.ExitCode == 0 {
				return nil
			}
			return fmt.Errorf("process exited with code %d", inspect.ExitCode)
		}
	}
}

// FindPid locates the PID of the matching child process inside the
// container by scanning /proc. /proc is guaranteed on every Linux
// container; `ps` is not (debian-slim images ship without procps, Alpine
// busybox has a different flag set). Output format is one line per PID:
//
//	<pid>\t<cmdline-space-separated>
//
// where cmdline is the full argv reconstructed from /proc/<pid>/cmdline
// (NUL-separated in the kernel — we convert NUL→space for splitting).
func (proc *DockerProc) FindPid(ctx context.Context) (int, error) {
	w := wool.Get(ctx).In("DockerProc.FindPid")

	// Shell-based /proc scanner. Needs /bin/sh which every Linux container
	// has; no ps, no procps dependency.
	script := `for d in /proc/[0-9]*; do
  pid=${d##*/}
  if [ -r "$d/cmdline" ]; then
    cmd=$(tr '\0' ' ' < "$d/cmdline")
    if [ -z "$cmd" ]; then
      [ -r "$d/comm" ] && cmd=$(cat "$d/comm")
    fi
    printf '%s\t%s\n' "$pid" "$cmd"
  fi
done`
	execConfig := container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"/bin/sh", "-c", script},
	}

	execIDResp, err := proc.env.client.ContainerExecCreate(ctx, proc.env.instance.ID, execConfig)
	if err != nil {
		return 0, w.Wrapf(err, "cannot create exec to list processes")
	}

	execAttachResp, err := proc.env.client.ContainerExecAttach(ctx, execIDResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return 0, w.Wrapf(err, "cannot attach to exec")
	}
	defer execAttachResp.Close()

	var outBuf, errBuf strings.Builder
	if _, err := stdcopy.StdCopy(&outBuf, &errBuf, execAttachResp.Reader); err != nil {
		return 0, w.Wrapf(err, "cannot copy output from exec")
	}
	if strings.TrimSpace(outBuf.String()) == "" {
		// Shell exec produced nothing. Could be: empty /proc (rare), or /bin/sh
		// missing (distroless images). Surface the stderr so the caller has a
		// chance at diagnosis rather than silently returning "not running".
		if errStr := strings.TrimSpace(errBuf.String()); errStr != "" {
			return 0, fmt.Errorf("proc scan produced no output: %s", errStr)
		}
		return -1, nil
	}

	for _, line := range strings.Split(outBuf.String(), "\n") {
		if line == "" {
			continue
		}
		// Split into pid + cmdline. Tab separator so cmdline can contain spaces.
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		pidStr := line[:tab]
		cmdline := strings.TrimSpace(line[tab+1:])
		if cmdline == "" {
			continue
		}

		cmd := strings.Fields(cmdline)
		if !proc.Match(cmd) {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			return 0, w.Wrapf(err, "cannot parse PID %q", pidStr)
		}
		return pid, nil
	}
	return -1, nil
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
		AttachStdin:  proc.stdinReader != nil,
		Env:          resources.EnvironmentVariableAsStrings(envs),
		Cmd:          proc.cmd,
	}
	if proc.dir != "" {
		execConfig.WorkingDir = proc.dir
	}
	w.Trace("creating exec", wool.Field("cmd", proc.cmd))
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

	// Stdin goroutine: copy from the caller's pipe into the exec connection.
	if proc.stdinReader != nil {
		go func() {
			_, _ = io.Copy(execResp.Conn, proc.stdinReader)
			// When the caller closes StdinPipe, signal EOF to the process
			// by closing the write direction of the connection.
			if cw, ok := execResp.Conn.(interface{ CloseWrite() error }); ok {
				_ = cw.CloseWrite()
			}
		}()
	}

	// Stdout/stderr goroutine: demultiplex the Docker stream.
	// stdcopy.StdCopy writes the raw container bytes — no line-scanning,
	// no TrimSpace, no 64KiB Scanner cap. This is the idiomatic pattern
	// from testcontainers-go (docker.go:887) and Docker's own CLI.
	go func() {
		defer execResp.Close()

		var stdoutDest, stderrDest io.Writer
		if proc.stdoutWriter != nil {
			stdoutDest = proc.stdoutWriter
			stderrDest = io.Discard
			if proc.output != nil {
				stderrDest = proc.output
			}
			defer proc.stdoutWriter.Close()
		} else if proc.output != nil {
			stdoutDest = proc.output
			stderrDest = proc.output
		} else {
			stdoutDest = io.Discard
			stderrDest = io.Discard
		}
		if _, err := stdcopy.StdCopy(stdoutDest, stderrDest, execResp.Reader); err != nil {
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

	// Force-kill after a delay if the process is still running. Previously
	// this goroutine reused outer-scope `pid, err` via closure (using `=`,
	// not `:=`), which raced if Stop was called twice. Local shadowing
	// isolates the deferred force-kill state.
	go func() {
		time.Sleep(3 * time.Second)
		forcePID, findErr := proc.FindPid(ctx)
		if forcePID < 0 {
			return // process already exited, nothing to do
		}
		if findErr != nil {
			w.Warn("could not get PID for force-kill (process may already have exited)", wool.ErrField(findErr))
			return
		}
		_ = proc.stop(ctx, forcePID, true)
	}()
	return err
}

func (proc *DockerProc) stop(ctx context.Context, pid int, force bool) error {
	w := wool.Get(ctx).In("DockerProc.Stop")
	w.Debug("stopping process", wool.Field("pid", pid))

	// Use /bin/sh to send the signal rather than invoking /bin/kill
	// directly: many slim/distroless images (e.g. the astral-sh/uv image)
	// strip util-linux, so /bin/kill isn't present. sh's `kill` builtin
	// is always available as long as we have a shell.
	var killCmd []string
	if force {
		killCmd = []string{"/bin/sh", "-c", fmt.Sprintf("kill -9 %d", pid)}
	} else {
		killCmd = []string{"/bin/sh", "-c", fmt.Sprintf("kill %d", pid)}
	}
	execConfig := container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          killCmd,
	}
	// Bounded context: a kill should never take more than 5 seconds. Using
	// context.Background() lets a stalled docker daemon hang the caller
	// indefinitely, which is what we saw in the leak review.
	killCtx, killCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer killCancel()

	execIDResp, err := proc.env.client.ContainerExecCreate(killCtx, proc.env.instance.ID, execConfig)
	if err != nil {
		return w.Wrapf(err, "cannot create exec to kill")
	}

	execStartCheck := container.ExecStartOptions{Detach: false, Tty: false}
	execResp, err := proc.env.client.ContainerExecAttach(killCtx, execIDResp.ID, execStartCheck)
	if err != nil {
		return w.Wrapf(err, "cannot kill process")
	}
	// Close the hijacked connection — forgetting this leaked one FD per
	// process stop (the audit caught this as a HIGH-severity issue).
	defer execResp.Close()

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


func (proc *DockerProc) WithOutput(output io.Writer) {
	proc.output = output
}

func (proc *DockerProc) StdinPipe() (io.WriteCloser, error) {
	if proc.stdinWriter != nil {
		return nil, fmt.Errorf("StdinPipe already called")
	}
	proc.stdinReader, proc.stdinWriter = io.Pipe()
	return proc.stdinWriter, nil
}

func (proc *DockerProc) StdoutPipe() (io.ReadCloser, error) {
	if proc.stdoutReader != nil {
		return nil, fmt.Errorf("StdoutPipe already called")
	}
	proc.stdoutReader, proc.stdoutWriter = io.Pipe()
	return proc.stdoutReader, nil
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

	// PrintDownloadPercentage owns the reader's lifecycle now: it drains
	// the pull-progress stream via a bufio.Scanner loop that runs until
	// EOF, and it defer-Closes on return. The previous io.Copy-to-discard
	// here was both redundant (scanner already drained) and racy after
	// the close, surfacing as "read on closed response body" once the
	// FD-leak fix landed.
	PrintDownloadPercentage(progress, out)
	_, _ = w.Forward([]byte("Docker image pulled."))
	w.Debug("done pulling")
	return nil
}

func (docker *DockerEnvironment) PortMappings() []*DockerPortMapping {
	return docker.portMappings
}

func (docker *DockerEnvironment) WithPort(ctx context.Context, port uint16) {
	docker.WithPortMapping(ctx, port, port)
}

func (docker *DockerEnvironment) WithPortMapping(ctx context.Context, local uint16, container uint16) {
	w := wool.Get(ctx).In("WithPort")
	w.Debug("setting port", wool.Field("local", local), wool.Field("container", container))
	docker.mu.Lock()
	defer docker.mu.Unlock()
	docker.portMappings = append(docker.portMappings, &DockerPortMapping{
		Host:      local,
		Container: container,
	})
}

func Forward(ctx context.Context, reader io.Reader, writer io.Writer) {
	scanner := bufio.NewScanner(reader)
	go func() {
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}
			// scanner.Text() already strips the line delimiter — re-add it so
			// downstream readers (TUI, log files) see proper line breaks
			// rather than every container line concatenated end-to-end.
			_, _ = writer.Write(append(scanner.Bytes(), '\n'))
		}

		// "use of closed network connection" / io.EOF / context-cancelled
		// are the normal shutdown signals when the container exits or the
		// caller cancels — surfacing them as errors just adds noise to the
		// CLI output. Anything else is worth reporting.
		err := scanner.Err()
		if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) ||
			strings.Contains(err.Error(), "use of closed network connection") {
			return
		}
		_, _ = writer.Write([]byte(fmt.Sprintf("Error while scanning container logs: %s\n", err)))
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
