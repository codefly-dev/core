package base

import (
	"os"
	"sync"

	"github.com/codefly-dev/core/resources"
)

// processCarriers is the environment one host process starts with, and the
// private directory its file-delivered values were written to.
type processCarriers struct {
	environ []string
	dir     string
	once    sync.Once
}

// prepareProcessCarriers writes the file-delivered values of envs into a fresh
// 0700 directory (resources.MaterializeFileCarriers), and refuses an
// environment the platform would refuse at exec
// (resources.CheckProcessEnvironment), so a process is never started with a
// value it cannot receive. The directory is created only when a value needs
// it; release removes it once the process has exited.
func prepareProcessCarriers(envs ...[]*resources.EnvironmentVariable) (*processCarriers, error) {
	var all []*resources.EnvironmentVariable
	file := false
	for _, set := range envs {
		for _, env := range set {
			if env == nil {
				continue
			}
			file = file || env.File
			all = append(all, env)
		}
	}
	carriers := &processCarriers{}
	if file {
		dir, err := os.MkdirTemp("", "codefly-carriers-")
		if err != nil {
			return nil, err
		}
		carriers.dir = dir
		if all, err = resources.MaterializeFileCarriers(dir, dir, all); err != nil {
			carriers.release()
			return nil, err
		}
	}
	if err := resources.CheckProcessEnvironment(all); err != nil {
		carriers.release()
		return nil, err
	}
	carriers.environ = resources.EnvironmentVariableAsStrings(all)
	return carriers, nil
}

// release removes the directory the process read its file-delivered values
// from. It is called once the process has exited: the SDK reads a file carrier
// once and keeps it, so nothing reads it afterwards.
func (c *processCarriers) release() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		if c.dir != "" {
			_ = os.RemoveAll(c.dir)
		}
	})
}
