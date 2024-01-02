package golang

import "github.com/codefly-dev/core/wool"

type Command struct {
	args  []string
	envs  []string
	level wool.Loglevel
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

func DownloadModules() *Command {
	return NewCommand("go", "mod", "download")
}

func Run(envs []string) *Command {
	return NewCommand("/build/app").WithEnvs(envs)

}

func Build() *Command {
	return NewCommand("go", "build", "-gcflags", "all=-N -l", "-o", "/build/app", "main.go")
}

func NewCommand(args ...string) *Command {
	return &Command{args: args}
}
