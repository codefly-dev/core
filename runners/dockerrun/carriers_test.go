package dockerrun

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

// A container's large value is planned into its environment as a path under
// the carrier directory, with no host mount: the file is copied into the
// container, never left on the host.
func TestAContainerReceivesALargeValueAsAPathWithNoHostMount(t *testing.T) {
	large := resources.Env("CODEFLY__WORKSPACE_SECRET_CONFIGURATION__VAULT__BUNDLE", strings.Repeat("d", resources.FileCarrierThreshold+1))
	large.File, large.Secret = true, true
	docker := &DockerEnvironment{name: "carrier-test", envs: []*resources.EnvironmentVariable{resources.Env("CODEFLY__SMALL", "v"), large}}
	config, host := &container.Config{}, &container.HostConfig{}
	require.NoError(t, docker.deliverCarriers(config, host))

	require.Contains(t, config.Env, "CODEFLY__SMALL=v")
	require.Empty(t, host.Mounts, "nothing of the value is bind-mounted from the host")
	var path string
	for _, entry := range config.Env {
		require.Less(t, len(entry)+1, resources.MaxEnvironmentStringBytes)
		require.NotContains(t, entry, large.ValueAsString())
		if key, value, _ := strings.Cut(entry, "="); key == resources.FileCarrierKey(large.Key) {
			path = value
		}
	}
	require.True(t, strings.HasPrefix(path, resources.ContainerFileCarrierMount+"/"), path)
	require.Len(t, docker.carriers, 1)
	require.Equal(t, path, resources.ContainerFileCarrierMount+"/"+docker.carriers[0].Name)
	require.True(t, docker.carriers[0].Secret)
}

// The copied tree is owned by the container's user and readable by it alone:
// a non-root container reads its values, and no other user in it does.
func TestTheCarrierArchiveIsOwnedByTheContainerUserAlone(t *testing.T) {
	files := []resources.FileCarrier{{Name: "KEY.0123", Content: []byte("value"), Secret: true}}
	archive, err := carrierArchive(files, 10001, 20002)
	require.NoError(t, err)
	reader := tar.NewReader(archive)
	var names []string
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		names = append(names, header.Name)
		require.Equal(t, 10001, header.Uid, header.Name)
		require.Equal(t, 20002, header.Gid, header.Name)
		require.Zero(t, header.Mode&0o077, "%s is accessible by group or others", header.Name)
		if header.Typeflag == tar.TypeReg {
			require.Equal(t, int64(0o400), header.Mode)
			content, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Equal(t, "value", string(content))
		} else {
			require.Equal(t, int64(0o500), header.Mode)
		}
	}
	require.Equal(t, []string{"codefly/configuration/", "codefly/configuration/KEY.0123"}, names)
}

// The owner is the user the container runs as, however Docker spells it, and
// a user that cannot be resolved refuses the delivery instead of guessing.
func TestCarrierOwnerResolvesTheContainerUser(t *testing.T) {
	databases := map[string]string{
		"/etc/passwd": "root:x:0:0:root:/root:/bin/sh\n# comment\nnonroot:x:65532:65533::/home/nonroot:/sbin/nologin\n",
		"/etc/group":  "root:x:0:\nworkers:x:4000:\n",
	}
	read := func(file string) ([]byte, error) {
		if content, ok := databases[file]; ok {
			return []byte(content), nil
		}
		return nil, errors.New("absent")
	}
	for _, c := range []struct {
		user     string
		uid, gid int
	}{
		{"", 0, 0},
		{"nonroot", 65532, 65533},
		{"65532", 65532, 65533},
		{"10001", 10001, 10001},
		{"10001:4000", 10001, 4000},
		{"nonroot:workers", 65532, 4000},
	} {
		uid, gid, err := resolveCarrierOwner(c.user, read)
		require.NoError(t, err, c.user)
		require.Equal(t, [2]int{c.uid, c.gid}, [2]int{uid, gid}, c.user)
	}
	_, _, err := resolveCarrierOwner("ghost", read)
	require.ErrorIs(t, err, resources.ErrFileCarrier)
	_, _, err = resolveCarrierOwner("nonroot:ghosts", read)
	require.ErrorIs(t, err, resources.ErrFileCarrier)
	_, _, err = resolveCarrierOwner("app", func(string) ([]byte, error) { return nil, errors.New("distroless: no passwd") })
	require.ErrorIs(t, err, resources.ErrFileCarrier)
}

// A container with nothing large is declared exactly as before.
func TestAContainerWithoutLargeValuesPlansNoCarrier(t *testing.T) {
	docker := &DockerEnvironment{name: "carrier-none", envs: []*resources.EnvironmentVariable{resources.Env("CODEFLY__SMALL", "v")}}
	config, host := &container.Config{}, &container.HostConfig{}
	require.NoError(t, docker.deliverCarriers(config, host))
	require.Empty(t, host.Mounts)
	require.Empty(t, docker.carriers)
	require.Equal(t, []string{"CODEFLY__SMALL=v"}, config.Env)
}

// A value to be delivered by file at exec with no container to copy it into
// is refused, never silently inlined.
func TestAnExecCannotDeliverAFileWithoutAContainer(t *testing.T) {
	docker := &DockerEnvironment{name: "carrier-exec"}
	large := resources.Env("CODEFLY__LARGE", strings.Repeat("d", resources.FileCarrierThreshold+1))
	large.File = true
	_, err := docker.execCarriers(t.Context(), "", []*resources.EnvironmentVariable{large})
	require.ErrorIs(t, err, resources.ErrFileCarrier)
}

func execValues(count, size int) []*resources.EnvironmentVariable {
	var envs []*resources.EnvironmentVariable
	for i := 0; i < count; i++ {
		envs = append(envs, resources.Env(fmt.Sprintf("CODEFLY__WORKSPACE_CONFIGURATION__G__K%02d", i), strings.Repeat("v", size)))
	}
	return envs
}

// An exec runs on top of the container's environment: what the container
// carries counts against the limit, so the exec spills more of its own.
func TestAnExecCountsTheEnvironmentTheContainerCarries(t *testing.T) {
	envs := execValues(10, 20<<10)
	alone, _, err := planExecCarriers(nil, envs)
	require.NoError(t, err)
	require.Len(t, alone, 10, "alone it fits and nothing moves")

	var inherited []string
	var held []*resources.EnvironmentVariable
	for i := 0; i < 4; i++ {
		held = append(held, resources.Env(fmt.Sprintf("CODEFLY__INHERITED_%d", i), strings.Repeat("i", 100<<10)))
		inherited = append(inherited, held[i].String())
	}
	planned, files, err := planExecCarriers(inherited, envs)
	require.NoError(t, err)
	require.NotEmpty(t, files, "the container's 400 KiB leaves the exec too little room inline")
	all := append(held, planned...)
	require.NoError(t, resources.CheckProcessEnvironment(all))
}

// A key the container carries inline and the exec delivers by file reaches the
// process by file alone: the inline copy is set empty, so ResolveFileCarriers
// does not see the pair.
func TestAnExecNeverDeliversAKeyBothInlineAndByFile(t *testing.T) {
	envs := execValues(30, 20<<10)
	inherited := []string{"PATH=/bin"}
	for _, env := range envs {
		inherited = append(inherited, env.Key+"=old")
	}
	planned, files, err := planExecCarriers(inherited, envs)
	require.NoError(t, err)
	require.NotEmpty(t, files)

	dir := t.TempDir()
	environ := []string{}
	for _, entry := range inherited {
		environ = append(environ, entry)
	}
	for _, env := range planned {
		value := env.ValueAsString()
		if resources.IsFileCarrierKey(env.Key) {
			for _, file := range files {
				if value == resources.ContainerFileCarrierMount+"/"+file.Name {
					require.NoError(t, os.WriteFile(filepath.Join(dir, file.Name), file.Content, 0o600))
					value = filepath.Join(dir, file.Name)
				}
			}
		}
		// The exec's own entries override the container's, as docker exec does.
		for i, entry := range environ {
			if strings.HasPrefix(entry, env.Key+"=") {
				environ = append(environ[:i], environ[i+1:]...)
				break
			}
		}
		environ = append(environ, env.Key+"="+value)
	}
	read, err := resources.ResolveFileCarriers(environ)
	require.NoError(t, err, "no key arrives both inline and by file")
	require.Len(t, read, len(files))
}
