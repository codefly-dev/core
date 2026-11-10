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
	// Environment defaults precede process overrides, just as in os/exec.
	// Count and materialize only winners, not superseded duplicate entries.
	// An environment that would exceed the limit delivers its largest values
	// by file rather than refusing a process nothing is wrong with.
	all := resources.EffectiveEnvironmentVariables(envs...)
	file := hasFile(all)
	carriers := &processCarriers{}
	// The directory is created before the choice is made, so the paths the
	// choice counts are the ones the process will read; it is removed again
	// when nothing is delivered by file.
	if file || resources.EnvironmentBytes(all) > resources.MaxCarriedEnvironmentBytes {
		dir, err := os.MkdirTemp("", "codefly-carriers-")
		if err != nil {
			return nil, err
		}
		all = resources.SpillToFileCarriers(all, 0, resources.PlannedPathLen(dir))
		if !hasFile(all) {
			_ = os.Remove(dir)
		} else {
			carriers.dir = dir
			if all, err = resources.MaterializeFileCarriers(dir, dir, all); err != nil {
				carriers.release()
				return nil, err
			}
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

func hasFile(envs []*resources.EnvironmentVariable) bool {
	for _, env := range envs {
		if env.File {
			return true
		}
	}
	return false
}
