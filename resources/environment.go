package resources

import (
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

/*
An environment is where your modules are deployed.

It exists at the  level.
*/

type EnvironmentExistsError struct {
	name string
}

func (err *EnvironmentExistsError) Error() string {
	return fmt.Sprintf("environment %s already exists", err.name)
}

// EnvironmentCluster declares which Kubernetes cluster an environment
// targets. Lets `codefly deploy --env <name>` route kubectl to the
// right kubeconfig instead of string-matching env names in CLI source.
//
//	Kind: cluster category — "k3d", "kind", "minikube", "eks", "gke", "aks",
//	       or "external". Drives behavior decisions (image-import for k3d
//	       is a no-op on EKS, ECR auth only matters on EKS, etc.).
//	Kubeconfig: path to the kubeconfig file. Tilde expansion is supported.
//	            If empty, defaults to $KUBECONFIG or ~/.kube/config.
//	Context: optional kubectl context within the kubeconfig.
type EnvironmentCluster struct {
	Kind       string `yaml:"kind,omitempty"`
	Kubeconfig string `yaml:"kubeconfig,omitempty"`
	Context    string `yaml:"context,omitempty"`
}

// EnvironmentRegistry declares the container image registry an environment
// pushes to. Was previously a CLI `--org` flag with a hardcoded ECR URL.
//
//	URL: registry base — "localhost:5001", "ghcr.io/myorg",
//	     "621829027644.dkr.ecr.us-east-1.amazonaws.com/myrepo".
//	Auth: how to authenticate before push — "" (anonymous / docker-creds),
//	      "ecr" (run `aws ecr get-login-password`), "gcr" / "gar" (gcloud
//	      access token), "ghcr" (GITHUB_TOKEN env). The CLI handles auth
//	      side-effects based on this value.
type EnvironmentRegistry struct {
	URL  string `yaml:"url,omitempty"`
	Auth string `yaml:"auth,omitempty"`
}

// Environment is a configuration for an environment
type Environment struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	NamingScope string `yaml:"naming-scope,omitempty"`

	// Deploy-target overrides (CLI-side; not serialized to proto).
	// Empty values fall back to legacy defaults (local k3d, ~/.kube/config,
	// the default namespace, the --org flag's hardcoded registry) so
	// existing workspace YAMLs keep working unchanged.
	Cluster   *EnvironmentCluster  `yaml:"cluster,omitempty"`
	Registry  *EnvironmentRegistry `yaml:"registry,omitempty"`
	Namespace string               `yaml:"namespace,omitempty"`
}

func (env *Environment) Proto() (*basev0.Environment, error) {
	proto := &basev0.Environment{
		Name:        env.Name,
		Description: env.Description,
		NamingScope: env.NamingScope,
	}
	err := Validate(proto)
	if err != nil {
		return nil, err
	}
	return proto, nil
}

func (env *Environment) Local() bool {
	return strings.HasPrefix(env.Name, "local")
}

func EnvironmentFromProto(env *basev0.Environment) *Environment {
	return &Environment{
		Name:        env.Name,
		Description: env.Description,
		NamingScope: env.NamingScope,
	}
}

// An EnvironmentReference at the  level
type EnvironmentReference struct {
	Name string `yaml:"name"`
}

func (ref *EnvironmentReference) String() string {
	return ref.Name
}

// LocalEnvironment is a local environment that is always available
func LocalEnvironment() *Environment {
	return &Environment{
		Name: "local",
		Cluster: &EnvironmentCluster{
			Kind: "k3d",
		},
	}
}

// IsK3d reports whether the environment targets a k3d cluster. Used to
// decide whether to import freshly-built images into the cluster
// (k3d-only — EKS/GKE pull from a registry instead).
func (env *Environment) IsK3d() bool {
	if env.Cluster != nil && env.Cluster.Kind != "" {
		return env.Cluster.Kind == "k3d"
	}
	// Legacy fallback: any env not explicitly cluster-typed is treated
	// as local-k3d. Preserves the old "default to k3d image import"
	// behavior in cli/pkg/deployments/manager.go.
	return env.Local()
}
