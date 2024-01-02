package runners

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/agents/network"
	"github.com/codefly-dev/core/wool"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

type DockerRunner struct {
	client     *client.Client
	Containers []*ContainerInstance
}

type ContainerInstance struct {
	ID    string
	Name  string
	Image string
	Host  string
	Port  int
}

// NewDockerRunner creates a new docker runner
func NewDockerRunner(ctx context.Context) (*DockerRunner, error) {
	w := wool.Get(ctx).In("NewDockerRunner")
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, w.Wrapf(err, "cannot create docker client")
	}
	return &DockerRunner{
		client: cli,
	}, nil
}

type VolumeMount struct {
	Source string
	Target string
}

type Option struct {
	Cmd     []string
	Volumes []VolumeMount
	Envs    []string
}

type DockerOptionOld func(option *Option)

func WithCmd(cmd ...string) DockerOptionOld {
	return func(opt *Option) {
		opt.Cmd = append(opt.Cmd, cmd...)
	}
}

func WithVolume(source, target string) DockerOptionOld {
	return func(opt *Option) {
		opt.Volumes = append(opt.Volumes, VolumeMount{Source: source, Target: target})
	}
}

func WithEnvironmentVariable(key, value string) DockerOptionOld {
	return func(opt *Option) {
		opt.Envs = append(opt.Envs, fmt.Sprintf("%s=%s", key, value))
	}
}

type CreateDockerInput struct {
	DockerImage
	ApplicationEndpointInstance *network.ApplicationEndpointInstance
}

func (r *DockerRunner) CreateContainer(ctx context.Context, input CreateDockerInput, opts ...DockerOptionOld) error {
	w := wool.Get(ctx).In("Runner.CreateContainer")
	options := &Option{}
	for _, opt := range opts {
		opt(options)
	}
	name := input.ApplicationEndpointInstance.Name()
	w.Debug("PortBinding") //, input.ApplicationEndpointInstance.ApplicationEndpoint.PortBinding)

	good, err := r.ContainerReady(ctx, name)
	if err != nil {
		return w.Wrapf(err, "cannot check if container is ready")
	}
	if good {
		return nil
	}

	portMapping := nat.PortMap{
		nat.Port(input.ApplicationEndpointInstance.ApplicationEndpoint.PortBinding): []nat.PortBinding{
			{
				HostPort: input.ApplicationEndpointInstance.StringPort(),
			},
		},
	}
	w.Debug("port mapping") //: %v", portMapping)

	cfg := &container.Config{
		Image: input.Name,
		ExposedPorts: nat.PortSet{
			nat.Port(input.ApplicationEndpointInstance.ApplicationEndpoint.PortBinding): struct{}{},
		},
	}
	// err = r.EnsureImage(input.Name)

	var mounts []mount.Mount
	for _, volume := range options.Volumes {
		// Mounting volumes (in this case, for nginx context)
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: volume.Source, // replace with the actual path to your nginx.conf on your host
			Target: volume.Target,
		})
	}

	if options.Cmd != nil {
		cfg.Cmd = options.Cmd
	}
	cfg.Env = options.Envs

	resp, err := r.client.ContainerCreate(ctx,
		cfg,
		&container.HostConfig{
			AutoRemove:   true,
			PortBindings: portMapping,
			Mounts:       mounts,
		}, nil, nil, name)
	if err != nil {
		return w.Wrapf(err, "cannot create container")
	}
	if err != nil {
		return w.Wrapf(err, "cannot create container")
	}
	instance := ContainerInstance{
		Name: name,
		ID:   resp.ID,
	}
	r.Containers = append(r.Containers, &instance)
	return nil
}

func (r *DockerRunner) cleanContainers(ctx context.Context, name string) error {
	w := wool.Get(ctx).In("Runner.cleanContainers")
	containers, err := r.client.ContainerList(ctx, types.ContainerListOptions{All: true})
	if err != nil {
		return w.Wrapf(err, "cannot list containers")
	}
	var id string

	match := fmt.Sprintf("/%s", name)
	for i := range containers {
		c := containers[i]
		for _, cn := range c.Names {
			if cn == match { // Docker prefixes names with a "/"
				id = c.ID
				goto found
			}
		}
	}
found:
	if id == "" {
		return nil
	}
	inspectedContainer, err := r.client.ContainerInspect(ctx, id)
	if err != nil {
		return w.Wrapf(err, "cannot inspect container")
	}

	if inspectedContainer.State.Status == "exited" || inspectedContainer.State.Status == "dead" {
		// Restart the container if it's stopped TODO: port strategy
		//if err := r.client.ContainerRestart(r.ctx, id, nil); err != nil {
		//	return w.Wrap(err, "cannot restart container")
		//}
		//w.Info("container restarted")
		if err := r.client.ContainerRemove(context.Background(), id, types.ContainerRemoveOptions{}); err != nil {
			return w.Wrapf(err, "cannot remove container")
		}
	} else {
		w.Info("container is running")
		// Stop the container
		if err := r.client.ContainerStop(context.Background(), id, container.StopOptions{}); err != nil {
			return w.Wrapf(err, "cannot stop container")
		}

		// Remove the container
		if err := r.client.ContainerRemove(context.Background(), id, types.ContainerRemoveOptions{}); err != nil {
			return w.Wrapf(err, "cannot remove stopped container")
		}
	}
	//// Stop the container
	//if err := r.client.ContainerStop(context.Background(), id, container.StopOptions{}); err != nil {
	//	w.Warn("Error stopping container %s: %v", name, err)
	//	return w.Wrap(err, "cannot stop container")
	//}

	//// Remove the container
	//if err := r.client.ContainerRemove(context.Background(), id, types.ContainerRemoveOptions{}); err != nil {
	//	return w.Wrap(err, "cannot remove stopped container")
	//}
	//status, errs := r.client.ContainerWait(context.Background(), id, container.WaitConditionRemoved)
	//select {
	//case err := <-errs:
	//	if err != nil {
	//		w.Debugf("cannot wait for container to be removed: %v", err)
	//		return nil
	//	}
	//case s := <-status:
	//	if s.StatusCode == 0 {
	//		w.Debugf("container %s removed successfully", name)
	//		return nil
	//	}
	//}
	//return w.Error("not sure why I am here")
	return nil
}

func (r *DockerRunner) StartContainer(ctx context.Context, c *ContainerInstance) error {
	if err := r.client.ContainerStart(ctx, c.ID, types.ContainerStartOptions{}); err != nil {
		return err
	}
	return nil
}

func (r *DockerRunner) Start(ctx context.Context) error {
	w := wool.Get(ctx).In("Runner.Start")
	for _, c := range r.Containers {
		err := r.StartContainer(ctx, c)
		if err != nil {
			return w.Wrapf(err, "cannot start container")
		}
		// After the container starts, get its logs
		out, err := r.client.ContainerLogs(ctx, c.ID,
			types.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: false, Details: false})
		if err != nil {
			return w.Wrapf(err, "cannot get container logs")
		}

		go func(name string) {
			ForwardLogs(out, w)
		}(c.Name)
	}
	return nil
}

func (r *DockerRunner) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("Runner.Stop")
	for _, c := range r.Containers {
		err := r.cleanContainers(ctx, c.Name)
		if err != nil {
			w.Error(err.Error())
		}
	}
	return nil
}

func (r *DockerRunner) EnsureImage(ctx context.Context, imageName string) error {
	w := wool.Get(ctx).In("Runner.EnsureImage")
	// Check if image is available locally
	images, err := r.client.ImageList(ctx, types.ImageListOptions{})
	if err != nil {
		return w.Wrapf(err, "cannot list images")
	}

	imageExists := false
	for i := range images {
		image := images[i]
		for _, tag := range image.RepoTags {
			if tag == imageName {
				imageExists = true
				break
			}
		}
	}

	// If image is not available locally, pull it
	if !imageExists {
		fmt.Println("Name not found locally. Pulling...")
		_, err := r.client.ImagePull(ctx, imageName, types.ImagePullOptions{})
		if err != nil {
			panic(err)
		}
		fmt.Println("Name pulled successfully.")
	}
	return nil
}

func (r *Runner) IP(*network.ApplicationEndpointInstance) (string, error) {
	// Get IP Address
	return "172.17.0.2", nil
	//w.Debugf("Instance container %v", instance)
	//// Get container JSON object
	//container, err := r.client.ContainerInspect(context.Background(), instance.Name())
	//if err != nil {
	//	return "", w.Wrap(err, "cannot inspect container")
	//}
	//ipAddress := container.NetworkSettings.IPAddress
	//// If using the default bridge network, you might want to get the IP from the Networks map
	//if ipAddress != "" {
	//	return ipAddress, nil
	//}
	//for _, network := range container.NetworkSettings.Networks {
	//	return network.IPAddress, nil
	//}
	//return "", w.Error("cannot get ip address")
}

func (r *DockerRunner) ContainerReady(ctx context.Context, name string) (bool, error) {
	w := wool.Get(ctx).In("Runner.ContainerReady")
	containers, err := r.client.ContainerList(ctx, types.ContainerListOptions{All: true})
	if err != nil {
		return false, w.Wrapf(err, "cannot list containers")
	}
	var id string

	match := fmt.Sprintf("/%s", name)
	for i := range containers {
		c := containers[i]
		for _, cn := range c.Names {
			if cn == match { // Docker prefixes names with a "/"
				id = c.ID
				goto found
			}
		}
	}
found:
	if id == "" {
		return false, nil
	}
	inspectedContainer, err := r.client.ContainerInspect(ctx, id)
	if err != nil {
		return false, w.Wrapf(err, "cannot inspect container")
	}

	if inspectedContainer.State.Status == "running" {
		return true, nil
	}
	return false, nil
}
