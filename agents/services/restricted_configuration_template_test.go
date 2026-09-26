package services

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

func templatedDeploymentRequest() *builderv0.DeploymentRequest {
	return &builderv0.DeploymentRequest{
		Configuration: &basev0.Configuration{
			Origin: "mod/store",
			Infos: []*basev0.ConfigurationInformation{{
				Name: "postgres",
				ConfigurationValues: []*basev0.ConfigurationValue{
					{Key: "CONNECTION", Secret: true, Template: &basev0.ConfigurationValueTemplate{
						Segments: []*basev0.ConfigurationValueTemplateSegment{
							{Content: &basev0.ConfigurationValueTemplateSegment_Literal{Literal: "postgresql://reader:"}},
							{Content: &basev0.ConfigurationValueTemplateSegment_Reference{
								Reference: &basev0.ConfigurationValueReference{
									Configuration: "postgres",
									Key:           "POSTGRES_PASSWORD",
									Escape:        basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_URL_USERINFO,
								},
							}},
							{Content: &basev0.ConfigurationValueTemplateSegment_Literal{Literal: "@store:5432/app"}},
						},
					}},
				},
			}},
		},
	}
}

const templatedCarrier = "CODEFLY__SERVICE_SECRET_CONFIGURATION__MOD__STORE__POSTGRES__CONNECTION"

// A templated value carries no value, so the secret-value check admits it — and
// nothing in a restricted render assembles it. Without a secret reference for
// its carrier the value reaches the workload as nothing at all: the service
// boots, serves, and the credential is silently absent. The gate must say so.
func TestRestrictedDeploymentRequestRequiresAReferenceForATemplatedValue(t *testing.T) {
	err := validateRestrictedDeploymentRequest(templatedDeploymentRequest(), nil)
	if err == nil {
		t.Fatal("a templated value with no secret reference must be refused")
	}
	if !strings.Contains(err.Error(), templatedCarrier) {
		t.Errorf("the refusal must name the carrier whose reference is missing, got %v", err)
	}
	if !strings.Contains(err.Error(), "CONNECTION") {
		t.Errorf("the refusal must name the configuration value, got %v", err)
	}
}

// With the reference declared, the assembly is delivered by the environment's
// secret store and the request is admitted.
func TestRestrictedDeploymentRequestAdmitsATemplatedValueDeliveredByReference(t *testing.T) {
	references := map[string]*builderv0.KubernetesSecretKeyReference{
		templatedCarrier: {Name: "example-service-secrets", Key: "connection"},
	}
	if err := validateRestrictedDeploymentRequest(templatedDeploymentRequest(), references); err != nil {
		t.Fatalf("a templated value with its reference declared must be admitted: %v", err)
	}
}

// A malformed template must be refused here rather than reach a renderer.
func TestRestrictedDeploymentRequestRefusesAMalformedTemplate(t *testing.T) {
	req := templatedDeploymentRequest()
	req.Configuration.Infos[0].ConfigurationValues[0].Secret = false
	references := map[string]*builderv0.KubernetesSecretKeyReference{
		"CODEFLY__SERVICE_CONFIGURATION__MOD__STORE__POSTGRES__CONNECTION": {Name: "s", Key: "k"},
	}
	if err := validateRestrictedDeploymentRequest(req, references); err == nil {
		t.Fatal("a template on a non-secret value must be refused")
	}
}

// The gate looks the reference up by the carrier name the emitter would use, so
// the two must agree: a drift here would refuse a request that is correct, or
// admit one whose credential never arrives.
func TestRestrictedCarrierNameMatchesTheEmitter(t *testing.T) {
	req := templatedDeploymentRequest()
	conf := req.GetConfiguration()
	value := conf.GetInfos()[0].GetConfigurationValues()[0]
	if got := resources.ConfigurationValueEnvironmentKey(conf, conf.GetInfos()[0].GetName(), value); got != templatedCarrier {
		t.Errorf("carrier = %q, want %q", got, templatedCarrier)
	}
}

// The whole path, not just the gate: a restricted render used to fail outright
// on a templated value, because the secret emitter appended one carrier per
// secret value whatever its value was — so manager.Secrets() came back with a
// single empty entry and the render refused itself with "restricted rendering
// cannot receive secret values", a message about a secret it never received.
// With the reference declared, the render now succeeds and carries the
// secretKeyRef instead of the assembly.
func TestDeployKustomizeRendersATemplatedValueByReference(t *testing.T) {
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	manager := resources.NewEnvironmentVariableManager()
	identity := &resources.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service", Version: "1.2.3"}
	base := &Base{
		Wool:                 wool.Get(ctx),
		Identity:             identity,
		Information:          &Information{Service: resources.ToServiceWithCase(identity)},
		EnvironmentVariables: manager,
		loaded:               true,
	}
	base.SetDockerImage(&resources.DockerImage{
		Name:   "example/service",
		Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	builder := &BuilderWrapper{Base: base}
	base.Builder = builder
	destination := t.TempDir()

	conf := templatedDeploymentRequest().GetConfiguration()
	response, err := builder.DeployKustomize(ctx, &builderv0.DeploymentRequest{
		Environment: &basev0.Environment{Name: "test"},
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace:   "codefly",
				Destination: destination,
				Profile:     builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
				SecretReferences: map[string]*builderv0.KubernetesSecretKeyReference{
					templatedCarrier: {Name: "service-secrets", Key: "connection"},
				},
			},
		}},
		Configuration: conf,
	}, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Inputs:               DeploymentInputs{OwnConfiguration: true},
		Parameters:           struct{ Name string }{Name: "gitops"},
	})
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_SUCCESS, response.GetState().GetState())
	require.True(t, response.GetDeployment().GetKubernetes().GetValidation().GetRestricted())

	deploymentManifest, err := os.ReadFile(filepath.Join(destination, "base", "deployment.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(deploymentManifest), "key: connection",
		"the assembly must be delivered by the declared secret reference")
	// Neither the assembly nor the empty string the value literally holds may
	// reach the render.
	_, err = os.Stat(filepath.Join(destination, "base", "secret.yaml"))
	require.True(t, os.IsNotExist(err), "a restricted render must carry no Secret manifest")
	configMapManifest, err := os.ReadFile(filepath.Join(destination, "base", "config-map.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(configMapManifest), "CONNECTION",
		"a templated secret must not land in the ConfigMap")
}
