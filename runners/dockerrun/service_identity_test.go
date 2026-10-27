package dockerrun_test

import (
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/dockerrun"
	"github.com/stretchr/testify/require"
)

func TestContainerServiceIdentityIsExact(t *testing.T) {
	identity, err := dockerrun.ContainerServiceIdentity(nil)
	require.NoError(t, err)
	require.Nil(t, identity, "absence supplies no parent-service proof")
	for _, key := range []string{"", "web", "web/gateway/extra", "web/../gateway", "/gateway", "web/", "web/gateway\n"} {
		_, err := dockerrun.ContainerServiceIdentity(map[string]string{dockerrun.LabelCodeflyService: key})
		require.Error(t, err, key)
	}
	for _, key := range []string{"web/gateway", "web/gateway-admin", "web-gateway/admin"} {
		identity, err := dockerrun.ContainerServiceIdentity(map[string]string{dockerrun.LabelCodeflyService: key})
		require.NoError(t, err)
		require.Equal(t, key, identity.Unique())
	}
	var env dockerrun.DockerEnvironment
	require.Error(t, env.WithServiceIdentity(nil))
	require.Error(t, env.WithServiceIdentity(&resources.ServiceIdentity{Module: "web", Name: "gateway/extra"}))
}
