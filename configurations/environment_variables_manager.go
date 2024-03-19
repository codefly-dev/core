package configurations

import (
	"context"
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/wool"
)

type EnvironmentVariableManager struct {
	envs []string
}

func NewEnvironmentVariableManager() *EnvironmentVariableManager {
	return &EnvironmentVariableManager{}
}

func (holder *EnvironmentVariableManager) Add(envs ...string) {
	holder.envs = append(holder.envs, envs...)
}

func (holder *EnvironmentVariableManager) GetProjectProvider(_ context.Context, name string, key string) (string, error) {
	providerInfo := &basev0.ProviderInformation{Origin: ProjectProviderOrigin, Name: name}
	key = ProviderInformationEnvKey(providerInfo, key)
	for _, env := range holder.envs {
		if value, ok := strings.CutPrefix(env, key); ok {
			return value[1:], nil
		}
	}
	return "", fmt.Errorf("cannot find project provider env variable: %s", key)
}

func (holder *EnvironmentVariableManager) GetServiceProvider(_ context.Context, unique string, name string, key string) (string, error) {
	providerInfo := &basev0.ProviderInformation{Origin: unique, Name: name}
	key = ProviderInformationEnvKey(providerInfo, key)
	for _, env := range holder.envs {
		if value, ok := strings.CutPrefix(env, key); ok {
			return value[1:], nil
		}
	}
	return "", fmt.Errorf("cannot find service provider env variable: %s", key)
}

func (holder *EnvironmentVariableManager) GetEndpoint(ctx context.Context, unique string) (*EndpointInstance, error) {
	w := wool.Get(ctx).In("configurations.GetEndpoint")
	if holder == nil {
		return DefaultEndpointInstance(unique)
	}
	endpoint, err := ParseEndpoint(unique)
	if err != nil {
		return nil, w.Wrapf(err, "cannot parse info")
	}
	key := EndpointEnvironmentVariableKey(endpoint)
	for _, env := range holder.envs {
		if value, ok := strings.CutPrefix(env, key); ok {
			instance := &EndpointInstance{}
			err = instance.WithAddress(value[1:])
			return instance, err
		}
	}
	return nil, fmt.Errorf("cannot find info env variable: %s", key)
}

func (holder *EnvironmentVariableManager) Get() []string {
	return holder.envs
}

func (holder *EnvironmentVariableManager) GetBase() []string {
	var envs []string
	for _, env := range holder.envs {
		tokens := strings.Split(env, "____")
		if len(tokens) == 2 {
			envs = append(envs, tokens[1])
		}
	}
	return envs
}

func (holder *EnvironmentVariableManager) Find(_ context.Context, key string) (string, error) {
	for _, env := range holder.envs {
		if strings.HasPrefix(env, key) {
			return env, nil
		}
	}
	return "", fmt.Errorf("cannot find env variable: %s", key)
}
