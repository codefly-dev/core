package sdk

import (
	"context"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/sdk/dependencies"
)

// These aliases preserve the original API and share exactly one implementation
// and ownership registry with sdk/dependencies. New CLI-only consumers should
// import that package so deprecated Env does not pull agent engines into them.
type Dependencies = dependencies.Dependencies
type Option = dependencies.Option
type OptionFunc = dependencies.OptionFunc
type ReadinessTimeout = dependencies.ReadinessTimeout
type ReadinessFailure = dependencies.ReadinessFailure

func WithDependencies(ctx context.Context, opts ...OptionFunc) (*Dependencies, error) {
	return dependencies.WithDependencies(ctx, opts...)
}
func WithDebug() OptionFunc                        { return dependencies.WithDebug() }
func WithTimeout(timeout time.Duration) OptionFunc { return dependencies.WithTimeout(timeout) }
func WithCodeflyBinary(path string) OptionFunc     { return dependencies.WithCodeflyBinary(path) }
func WithNamingScope(scope string) OptionFunc      { return dependencies.WithNamingScope(scope) }
func WithFixture(fixture string) OptionFunc        { return dependencies.WithFixture(fixture) }
func WithRunProfile(profile string) OptionFunc     { return dependencies.WithRunProfile(profile) }
func WithSilence(uniques ...string) OptionFunc     { return dependencies.WithSilence(uniques...) }
func WithExcludedDependencies(uniques ...string) OptionFunc {
	return dependencies.WithExcludedDependencies(uniques...)
}
func WithDependencyHome(home string) OptionFunc { return dependencies.WithDependencyHome(home) }
func WithWorkspaceConfiguration(name, key, value string) OptionFunc {
	return dependencies.WithWorkspaceConfiguration(name, key, value)
}
func WithWorkspaceSecret(name, key, value string) OptionFunc {
	return dependencies.WithWorkspaceSecret(name, key, value)
}
func WithServiceConfiguration(service, name, key, value string) OptionFunc {
	return dependencies.WithServiceConfiguration(service, name, key, value)
}
func WithServiceSecret(service, name, key, value string) OptionFunc {
	return dependencies.WithServiceSecret(service, name, key, value)
}
func WithKeepRunning() OptionFunc               { return dependencies.WithKeepRunning() }
func WithSharedControlChannel() OptionFunc      { return dependencies.WithSharedControlChannel() }
func WithDirectory(dir string) OptionFunc       { return dependencies.WithDirectory(dir) }
func WithService(unique string) OptionFunc      { return dependencies.WithService(unique) }
func WithCommandScopedEnvironment() OptionFunc  { return dependencies.WithCommandScopedEnvironment() }
func Connection(service, name string) string    { return dependencies.Connection(service, name) }
func Service() (*resources.Service, error)      { return dependencies.Service() }
func Module() (*resources.Module, error)        { return dependencies.Module() }
func Inject(env *resources.EnvironmentVariable) { dependencies.Inject(env) }
