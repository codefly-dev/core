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

// EnvironmentSecretProvider configures one secret backend for an environment.
// Values in *.secret.ref.* manifests are references (op://…) resolved at Load
// time through the backend's CLI; nothing secret is written to disk. It is a
// list so more backends can be added later.
//
//	Kind:    "1password".
//	Account: 1Password account shorthand passed as `op --account`.
type EnvironmentSecretProvider struct {
	Kind    string `yaml:"kind"`
	Account string `yaml:"account,omitempty"`
}

// EnvironmentGitops identifies the reviewed repository snapshot reconciled for
// one environment.
type EnvironmentGitops struct {
	RepoURL      string `yaml:"repo-url"`
	FetchRepoURL string `yaml:"fetch-repo-url,omitempty"`
	Path         string `yaml:"path"`
	Branch       string `yaml:"branch"`
	Revision     string `yaml:"revision"`
	Checkout     string `yaml:"checkout,omitempty"`
	Inventory    string `yaml:"inventory"`
}

// EnvironmentIngressRoute binds one public service endpoint to exact hosts.
type EnvironmentIngressRoute struct {
	Name     string   `yaml:"name"`
	Service  string   `yaml:"service"`
	Endpoint string   `yaml:"endpoint"`
	Hosts    []string `yaml:"hosts"`
}

// EnvironmentSecretStoreReference selects the External Secrets store that
// resolves a managed-service handoff.
type EnvironmentSecretStoreReference struct {
	Name string `yaml:"name"`
	Kind string `yaml:"kind"`
}

// EnvironmentManagedSecretReference maps a remote managed secret into a
// namespaced Kubernetes Secret.
type EnvironmentManagedSecretReference struct {
	Name        string                          `yaml:"name"`
	RemoteKey   string                          `yaml:"remote-key"`
	SecretStore EnvironmentSecretStoreReference `yaml:"secret-store"`
}

// EnvironmentManagedService describes an environment-owned replacement for a
// service that is otherwise part of the module graph.
type EnvironmentManagedService struct {
	Kind             string                              `yaml:"kind"`
	ExternalName     string                              `yaml:"external-name"`
	EgressCIDRs      []string                            `yaml:"egress-cidrs,omitempty"`
	SecretReferences []EnvironmentManagedSecretReference `yaml:"secret-references,omitempty"`
}

// Environment is a configuration for an environment
type Environment struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	NamingScope string `yaml:"naming-scope,omitempty"`
	Fixture     string `yaml:"fixture,omitempty"`

	// ConfigurationProfile selects the checked-in configuration directory
	// independently from the environment identity sent to service agents.
	// This is useful for a production execution profile running against local
	// backing services: agents still receive environment.name=production while
	// Codefly deliberately loads configurations/local. It is explicit and
	// opt-in; the default remains the environment's own name.
	ConfigurationProfile string `yaml:"configuration-profile,omitempty"`

	// Deploy-target overrides (CLI-side; not serialized to proto).
	// Empty values fall back to legacy defaults (local k3d, ~/.kube/config,
	// the default namespace, the --org flag's hardcoded registry) so
	// existing workspace YAMLs keep working unchanged.
	Cluster   *EnvironmentCluster  `yaml:"cluster,omitempty"`
	Registry  *EnvironmentRegistry `yaml:"registry,omitempty"`
	Namespace string               `yaml:"namespace,omitempty"`
	Gitops    *EnvironmentGitops   `yaml:"gitops,omitempty"`

	Ingress         []EnvironmentIngressRoute            `yaml:"ingress,omitempty"`
	ManagedServices map[string]EnvironmentManagedService `yaml:"managed-services,omitempty"`

	// Secrets lists the secret backends for this environment. Reference-only
	// manifests fail when their backend is absent. Legacy plaintext *.secret.*
	// files remain local-only. CLI-side; not serialized to proto.
	Secrets []*EnvironmentSecretProvider `yaml:"secrets,omitempty"`
}

func (env *Environment) Proto() (*basev0.Environment, error) {
	if env.ConfigurationProfile != "" {
		if err := validateResourcePathComponent("configuration profile", env.ConfigurationProfile); err != nil {
			return nil, err
		}
	}
	proto := &basev0.Environment{
		Name:        env.Name,
		Description: env.Description,
		NamingScope: env.NamingScope,
		Fixture:     env.Fixture,
	}
	err := Validate(proto)
	if err != nil {
		return nil, err
	}
	return proto, nil
}

func (env *Environment) ConfigurationProfileName() (string, error) {
	name := strings.TrimSpace(env.ConfigurationProfile)
	if name == "" {
		name = env.Name
	}
	if err := validateResourcePathComponent("configuration profile", name); err != nil {
		return "", err
	}
	return name, nil
}

func (env *Environment) Local() bool {
	return strings.HasPrefix(env.Name, "local")
}

func EnvironmentFromProto(env *basev0.Environment) *Environment {
	return &Environment{
		Name:        env.Name,
		Description: env.Description,
		NamingScope: env.NamingScope,
		Fixture:     env.Fixture,
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
