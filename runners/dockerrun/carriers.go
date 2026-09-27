package dockerrun

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types/container"
)

// A container's file-delivered configuration values are copied into the
// container, owned by the user the container runs as, rather than
// bind-mounted from the host. A bind mount keeps the host's ownership: a
// 0600 file the runner writes is unreadable to a container that runs as any
// other non-root user, and making it readable on the host would expose it to
// every other host user. Copied in, a value is readable by the container's
// user and nobody else in the container, and nothing of it stays on the host.

const (
	// carrierDirMode lets the container's user list and traverse its
	// carrier directory; nobody else.
	carrierDirMode = 0o500
	// carrierFileMode lets the container's user read a carried value, secret
	// or not; nobody else.
	carrierFileMode = 0o400
)

// deliverCarriers plans the container's file-delivered values
// (resources.PlanFileCarriers) and sets the container environment to their
// paths as the container sees them, under resources.ContainerFileCarrierMount.
// The files are copied in once the container exists (copyCarriers). An
// environment the platform would refuse is refused here, before the container
// is created.
func (docker *DockerEnvironment) deliverCarriers(config *container.Config, _ *container.HostConfig) error {
	docker.mu.Lock()
	envs := append([]*resources.EnvironmentVariable(nil), docker.envs...)
	docker.mu.Unlock()
	planned, files, err := resources.PlanFileCarriers(resources.ContainerFileCarrierMount, envs)
	if err != nil {
		return err
	}
	if err := resources.CheckProcessEnvironment(planned); err != nil {
		return err
	}
	config.Env = resources.EnvironmentVariableAsStrings(planned)
	docker.mu.Lock()
	docker.carriers = files
	docker.mu.Unlock()
	return nil
}

// copyCarriers copies the planned file-delivered values into container id,
// owned by the user it runs as. It is called between create and start, so a
// process never starts before its values are in place.
func (docker *DockerEnvironment) copyCarriers(ctx context.Context, id string) error {
	docker.mu.Lock()
	files := append([]resources.FileCarrier(nil), docker.carriers...)
	docker.mu.Unlock()
	return docker.copyFiles(ctx, id, files)
}

// execCarriers delivers an exec's file-delivered values by copying them into
// the running container, and returns the exec's environment.
func (docker *DockerEnvironment) execCarriers(ctx context.Context, id string, envs []*resources.EnvironmentVariable) ([]*resources.EnvironmentVariable, error) {
	planned, files, err := resources.PlanFileCarriers(resources.ContainerFileCarrierMount, envs)
	if err != nil {
		return nil, err
	}
	if len(files) > 0 {
		if id == "" {
			return nil, fmt.Errorf("%w: no container to deliver a value by file into", resources.ErrFileCarrier)
		}
		if err := docker.copyFiles(ctx, id, files); err != nil {
			return nil, err
		}
	}
	if err := resources.CheckProcessEnvironment(planned); err != nil {
		return nil, err
	}
	return planned, nil
}

func (docker *DockerEnvironment) copyFiles(ctx context.Context, id string, files []resources.FileCarrier) error {
	if len(files) == 0 {
		return nil
	}
	inspect, err := docker.client.ContainerInspect(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: inspect container: %v", resources.ErrFileCarrier, err)
	}
	user := ""
	if inspect.Config != nil {
		user = inspect.Config.User
	}
	uid, gid, err := resolveCarrierOwner(user, func(file string) ([]byte, error) {
		return docker.readContainerFile(ctx, id, file)
	})
	if err != nil {
		return err
	}
	archive, err := carrierArchive(files, uid, gid)
	if err != nil {
		return err
	}
	if err := docker.client.CopyToContainer(ctx, id, "/", archive, container.CopyToContainerOptions{CopyUIDGID: true}); err != nil {
		return fmt.Errorf("%w: copy into container: %v", resources.ErrFileCarrier, err)
	}
	return nil
}

// readContainerFile reads one small file of the container's filesystem.
func (docker *DockerEnvironment) readContainerFile(ctx context.Context, id, file string) ([]byte, error) {
	reader, _, err := docker.client.CopyFromContainer(ctx, id, file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	archive := tar.NewReader(reader)
	if _, err := archive.Next(); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(archive, 1<<20))
}

// carrierArchive is the tar stream CopyToContainer extracts at "/": the
// carrier directory and every file, owned by uid:gid, readable by that user
// only.
func carrierArchive(files []resources.FileCarrier, uid, gid int) (io.Reader, error) {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	dir := strings.TrimPrefix(resources.ContainerFileCarrierMount, "/")
	if err := writer.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: dir + "/", Mode: carrierDirMode, Uid: uid, Gid: gid}); err != nil {
		return nil, err
	}
	for _, file := range files {
		header := &tar.Header{Typeflag: tar.TypeReg, Name: path.Join(dir, file.Name), Mode: carrierFileMode, Uid: uid, Gid: gid, Size: int64(len(file.Content))}
		if err := writer.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := writer.Write(file.Content); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return &buffer, nil
}

// resolveCarrierOwner resolves a container's user, as Docker spells it
// ("", "name", "uid", "name:group", "uid:gid"), to the numeric owner its
// carrier files are given. A name is looked up in the container's own
// /etc/passwd and /etc/group, read through read. A user that cannot be
// resolved is an error: a value no one can say who may read is not delivered.
func resolveCarrierOwner(user string, read func(file string) ([]byte, error)) (int, int, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		return 0, 0, nil
	}
	name, group, hasGroup := strings.Cut(user, ":")
	uid, gid, err := lookupID(name, "/etc/passwd", read, true)
	if err != nil {
		return 0, 0, err
	}
	if hasGroup {
		if gid, _, err = lookupID(group, "/etc/group", read, false); err != nil {
			return 0, 0, err
		}
	}
	return uid, gid, nil
}

// lookupID resolves a numeric id as itself, or a name through the database
// file ("name:x:id:..." lines). For a user it also returns its primary group
// (the passwd entry's fourth field, or the id itself when numeric and absent
// from the database).
func lookupID(value, database string, read func(string) ([]byte, error), user bool) (int, int, error) {
	if id, err := strconv.Atoi(value); err == nil && id >= 0 {
		primary := id
		if user {
			if content, readErr := read(database); readErr == nil {
				if entry := findEntry(content, func(fields []string) bool { return len(fields) > 3 && fields[2] == value }); entry != nil {
					if group, err := strconv.Atoi(entry[3]); err == nil {
						primary = group
					}
				}
			}
		}
		return id, primary, nil
	}
	content, err := read(database)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: cannot resolve container user %q: %s: %v", resources.ErrFileCarrier, value, database, err)
	}
	entry := findEntry(content, func(fields []string) bool { return fields[0] == value })
	if entry == nil || len(entry) < 3 {
		return 0, 0, fmt.Errorf("%w: container user %q is not in %s", resources.ErrFileCarrier, value, database)
	}
	id, err := strconv.Atoi(entry[2])
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %s entry for %q has no numeric id", resources.ErrFileCarrier, database, value)
	}
	primary := id
	if user && len(entry) > 3 {
		if group, err := strconv.Atoi(entry[3]); err == nil {
			primary = group
		}
	}
	return id, primary, nil
}

func findEntry(content []byte, match func([]string) bool) []string {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if fields := strings.Split(line, ":"); len(fields) > 2 && match(fields) {
			return fields
		}
	}
	return nil
}
