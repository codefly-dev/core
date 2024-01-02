package golang

import "github.com/codefly-dev/core/runners"

func DownloadModules() *runners.Command {
	return runners.NewCommand("go", "mod", "download")
}

func Run(envs []string) *runners.Command {
	return runners.NewCommand("/build/app").WithEnvs(envs)

}

func Build() *runners.Command {
	return runners.NewCommand("go", "build", "-gcflags", "all=-N -l", "-o", "/build/app", "main.go")
}
