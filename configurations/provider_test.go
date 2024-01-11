package configurations_test

import (
	"testing"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/stretchr/testify/assert"
)

func TestProviderEnv(t *testing.T) {
	tcs := []struct {
		name  string
		info  *basev0.ProviderInformation
		key   string
		value string
		env   string
	}{
		{"project_origin", &basev0.ProviderInformation{Name: "auth", Origin: configurations.ProjectProviderOrigin}, "AUTH0_DOMAIN", "codefly-dev.us.auth0.com", "CODEFLY_PROVIDER___AUTH____AUTH0_DOMAIN=codefly-dev.us.auth0.com"},
		{"service", &basev0.ProviderInformation{Name: "connection", Origin: "app/svc"}, "uri", "postgres://username:password@localhost:5432/database_name?sslmode=disable", "CODEFLY_PROVIDER__APP__SVC___CONNECTION____URI=postgres://username:password@localhost:5432/database_name?sslmode=disable"},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			env := configurations.ProviderInformationEnv(tc.info, tc.key, tc.value)
			assert.Equal(t, tc.env, env)
		})
	}
}
