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

func (holder *EnvironmentVariableManager) GetProjectProvider(ctx context.Context, key string) (string, error) {
	w := wool.Get(ctx).In("EnvironmentVariableManager.GetServiceProvider")
	providerInfo := &basev0.ProviderInformation{Origin: ProjectProviderOrigin}
	key = ProviderInformationEnvKey(providerInfo, key)
	w.Debug("looking", wool.Field("key", key))
	for _, env := range holder.envs {
		if value, ok := strings.CutPrefix(env, key); ok {
			return value, nil
		}
	}
	return "", fmt.Errorf("cannot find project provider env variable: %s", key)
}

func (holder *EnvironmentVariableManager) GetServiceProvider(ctx context.Context, unique string, key string) (string, error) {
	w := wool.Get(ctx).In("EnvironmentVariableManager.GetServiceProvider")
	providerInfo := &basev0.ProviderInformation{Origin: unique}
	key = ProviderInformationEnvKey(providerInfo, key)
	w.Debug("looking", wool.Field("key", key))
	for _, env := range holder.envs {
		if value, ok := strings.CutPrefix(env, key); ok {
			return value[1:], nil
		}
	}
	return "", fmt.Errorf("cannot find service provider env variable: %s", key)
}

func (holder *EnvironmentVariableManager) GetEndpoint(ctx context.Context, unique string) (*EndpointInstance, error) {
	w := wool.Get(ctx).In("EnvironmentVariableManager.GetEndpoint")
	endpoint, err := ParseEndpoint(unique)
	if err != nil {
		return nil, w.Wrapf(err, "cannot parse endpoint")
	}
	key := EndpointEnvironmentVariableKey(endpoint)
	w.Debug("looking", wool.Field("key", key))
	for _, env := range holder.envs {
		if value, ok := strings.CutPrefix(env, key); ok {
			addresses := strings.Split(value[1:], ",")
			return &EndpointInstance{Addresses: addresses}, nil
		}
	}
	return nil, fmt.Errorf("cannot find endpoint env variable: %s", key)
}
