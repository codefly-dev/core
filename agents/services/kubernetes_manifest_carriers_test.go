package services_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/agents/services"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// validateRenderedConfigMap runs the public restricted validator over a real
// kustomize overlay carrying one ConfigMap with the given data key, as an
// agent-produced tree reaches it.
func validateRenderedConfigMap(t *testing.T, key string) *builderv0.KubernetesManifestValidation {
	t.Helper()
	root := t.TempDir()
	overlay := filepath.Join(root, "overlays", "test")
	require.NoError(t, os.MkdirAll(overlay, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(overlay, "kustomization.yaml"), []byte("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: [config-map.yaml]\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(overlay, "config-map.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: example\n  namespace: example\ndata:\n  "+key+": synthetic-fixture\n"), 0o600))
	return services.ValidateKubernetesManifestTree(context.Background(), root, "test", "example",
		builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1, false, "", "")
}

// The restricted validator holds a rendered tree to the same carrier boundary
// as the resources classifier: an explicit secret carrier is refused whatever
// its key says, a configuration carrier is read by its complete key, and a
// public carrier is accepted whatever its group or service is called. The four
// refusals are the independent carrier review's counterexamples, which the
// last-segment parser this replaced accepted as restricted.
func TestRestrictedValidatorHoldsTheCarrierBoundary(t *testing.T) {
	for _, key := range []string{
		"CODEFLY__WORKSPACE_SECRET_CONFIGURATION__DATASOURCE_OAUTH__CLIENT_ID",
		"CODEFLY__SERVICE_SECRET_CONFIGURATION__EXAMPLE__SERVICE__POLICY__CLIENT_ID",
		"CODEFLY__SECRET_CONFIGURATION_DOCUMENT_V1__0123456789abcdef",
		"CODEFLY__WORKSPACE_CONFIGURATION__POLICY__ACCESS_TOKEN__FORMAT",
		"CODEFLY__WORKSPACE_CONFIGURATION__DATASOURCE_OAUTH__CLIENT_SECRET",
		"DATABASE_PASSWORD",
		// A token behind an address spelling inside a public configuration
		// carrier: the outermost carrier is read first, and the key whole.
		"CODEFLY__WORKSPACE_CONFIGURATION__SETTINGS__CODEFLY__ENDPOINT__APP__SVC__REST__ACCESS_TOKEN",
	} {
		t.Run("refused/"+key, func(t *testing.T) {
			require.True(t, resources.IsSensitiveEnvironmentVariable(key), "the classifier calls it sensitive")
			got := validateRenderedConfigMap(t, key)
			require.False(t, got.GetRestricted())
			require.Equal(t, builderv0.KubernetesManifestValidation_STATUS_FAILED, got.GetStaticValidation())
			require.Len(t, got.GetViolations(), 1)
			require.Contains(t, got.GetViolations()[0], "contains credential-like key")
		})
	}
	for _, key := range []string{
		"CODEFLY__WORKSPACE_CONFIGURATION__DATASOURCE_OAUTH__PROVIDERS",
		"CODEFLY__WORKSPACE_CONFIGURATION__DATASOURCE_OAUTH__CLIENT_ID",
		"CODEFLY__WORKSPACE_CONFIGURATION__CREDENTIAL_POLICY__CLIENT_ID",
		"CODEFLY__SERVICE_CONFIGURATION__SAAS__AUTH_GATEWAY__PASSWORD_POLICY__MIN_LENGTH",
		"CODEFLY__ENDPOINT__SAAS__AUTH_GATEWAY__REST__REST",
		"NEXT_PUBLIC_CODEFLY__ENDPOINT__SAAS__AUTH_GATEWAY__REST__REST",
	} {
		t.Run("accepted/"+key, func(t *testing.T) {
			require.False(t, resources.IsSensitiveEnvironmentVariable(key), "the classifier calls it public")
			got := validateRenderedConfigMap(t, key)
			require.Empty(t, got.GetViolations())
			require.Equal(t, builderv0.KubernetesManifestValidation_STATUS_PASSED, got.GetStaticValidation())
			require.True(t, got.GetRestricted())
		})
	}
}
