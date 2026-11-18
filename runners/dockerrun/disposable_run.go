package dockerrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/runstate"
	"github.com/codefly-dev/core/wool"
)

// runtimeCacheDir is where core keeps the data directories it mounts into
// containers (WithPersistentCacheMount), one directory per container name.
const runtimeCacheDir = "runtime-cache"

// DisposableRunRemoval is what RemoveDisposableRun took back.
type DisposableRunRemoval struct {
	// Containers are the names of the containers removed.
	Containers []string
	// DataDirectories are the host directories removed: what core mounted
	// into those containers to keep their data in.
	DataDirectories []string
}

// RemoveDisposableRun removes what a disposable run left on the container
// runtime, once it has ended: every container created in its scope, running or
// stopped, the anonymous volumes those containers created, and the data
// directories core mounted into them (WithPersistentCacheMount).
//
// It is the container runtime's half of the rule runstate keeps for native
// state, on the same fact and no other: run.Disposable, the statement of the
// invocation that generated the run's naming scope (runstate.For). Such a scope
// is never run again, so nothing can come back for a container named under it.
// For any other run this removes nothing: a stop keeps a developer's containers
// for the next run to reuse, and their data directories are that developer's
// databases.
//
// scope is the run's own (home, workspace and the run's naming scope), which is
// what every container an agent of the run created is labelled with, so no
// agent states anything more and a container an agent built on a Core older
// than the label is simply not found. A container bound to a session ledger is
// left to that ledger, whose record decides what becomes of it, and one
// labelled by another host is not this run's. A named volume is not removed:
// it was made apart from the container and may be someone else's.
//
// The caller is the party that ended the run, after its agents have exited: a
// stopped run stops its agents and never destroys them, so no agent removes
// its own container. What cannot be removed is reported and the rest still
// goes.
func RemoveDisposableRun(ctx context.Context, scope ContainerRecoveryScope, run runstate.Run) (DisposableRunRemoval, error) {
	var removal DisposableRunRemoval
	if !run.Disposable || run.NamingScope == "" {
		return removal, nil
	}
	if scope.id == "" {
		return removal, errors.New("container recovery scope is unresolved")
	}
	w := wool.Get(ctx).In("dockerrun.RemoveDisposableRun")
	cli, _, err := newDockerClient()
	if err != nil {
		// No Docker here: the run created no container.
		w.Trace("docker client unavailable — no container of the disposable run to remove", wool.ErrField(err))
		return removal, ctx.Err()
	}
	defer func() { _ = cli.Close() }()
	if err = pingDockerClient(ctx, cli); err != nil {
		w.Trace("docker engine unavailable — no container of the disposable run to remove", wool.ErrField(err))
		return removal, ctx.Err()
	}
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	containers, err := cli.ContainerList(listCtx, client.ContainerListOptions{All: true,
		Filters: client.Filters{}.Add("label", LabelCodeflyOwner+"="+labelTrue).Add("label", LabelCodeflyRecoveryScope+"="+scope.id)})
	cancel()
	if err != nil {
		return removal, fmt.Errorf("list the disposable run's containers: %w", err)
	}
	var failures []error
	for _, c := range containers.Items {
		if !containerOfDisposableRun(c, scope) {
			continue
		}
		name := strings.TrimPrefix(firstContainerName(c), "/")
		removeCtx, removeCancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := cli.ContainerRemove(removeCtx, c.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
		removeCancel()
		if err != nil && !errdefs.IsNotFound(err) {
			failures = append(failures, fmt.Errorf("remove container %s of the disposable run: %w", name, err))
			continue
		}
		removal.Containers = append(removal.Containers, name)
		// Only once the container is gone: nothing writes there any more.
		for _, dir := range containerDataDirectories(c) {
			if slices.Contains(removal.DataDirectories, dir) {
				continue
			}
			if err := os.RemoveAll(dir); err != nil {
				failures = append(failures, fmt.Errorf("remove data directory %s of container %s: %w", dir, name, err))
				continue
			}
			removal.DataDirectories = append(removal.DataDirectories, dir)
		}
	}
	return removal, errors.Join(append(failures, ctx.Err())...)
}

// containerOfDisposableRun reports whether c is a container the run of scope
// created: Codefly's, labelled with exactly that scope, not another host's,
// and not a session ledger's.
func containerOfDisposableRun(c container.Summary, scope ContainerRecoveryScope) bool {
	if scope.id == "" || c.Labels[LabelCodeflyOwner] != labelTrue || c.Labels[LabelCodeflyRecoveryScope] != scope.id {
		return false
	}
	if namespace := c.Labels[LabelCodeflyRecoveryNamespace]; namespace != scope.namespace && namespace != "" {
		return false
	}
	return c.Labels[LabelCodeflyInvocation] == ""
}

// containerDataDirectories are the directories core keeps c's data in: the
// one named after the container under this home's runtime cache, when Docker
// itself says c mounts something from it. The mount is the proof: a directory
// is never removed on a name alone, nor under another home.
func containerDataDirectories(c container.Summary) []string {
	name := c.Labels[LabelCodeflyName]
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return nil
	}
	dir := filepath.Join(resources.CodeflyHomeDir(), runtimeCacheDir, name)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil
	}
	for _, point := range c.Mounts {
		if point.Type != mount.TypeBind {
			continue
		}
		source := point.Source
		if evaluated, err := filepath.EvalSymlinks(source); err == nil {
			source = evaluated
		}
		if rel, err := filepath.Rel(resolved, source); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return []string{dir}
		}
	}
	return nil
}

func firstContainerName(c container.Summary) string {
	if len(c.Names) > 0 {
		return c.Names[0]
	}
	return c.ID
}
