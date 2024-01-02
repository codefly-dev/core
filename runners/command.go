package runners

import "github.com/codefly-dev/core/wool"

type Command struct {
	args  []string
	envs  []string
	level wool.Loglevel
}

func NewCommand(args ...string) *Command {
	return &Command{args: args}
}

func (c *Command) AsSlice() []string {
	return c.args
}

func (c *Command) Envs() []string {
	return c.envs
}

func (c *Command) LogLevel() wool.Loglevel {
	return c.level
}

func (c *Command) WithEnvs(envs []string) *Command {
	c.envs = envs
	return c
}

func (c *Command) Level(loglevel wool.Loglevel) *Command {
	c.level = loglevel
	return c
}
