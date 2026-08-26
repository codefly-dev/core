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

// EnvironmentServiceSecrets declares the External Secrets store that resolves a
// regular (app) service's secret-service-configurations for an environment. It is
// the app-service counterpart to EnvironmentManagedService.SecretReferences: the
// promotion bundle projects each consuming service's secret-<service> from this
// store, so the Secret its promotable manifests reference via non-optional
// secretKeyRefs materializes in-cluster without any secret value entering git,
// state, or manifests.
type EnvironmentServiceSecrets struct {
	SecretStore EnvironmentSecretStoreReference            `yaml:"secret-store"`
	Services    map[string]EnvironmentServiceSecretMapping `yaml:"services,omitempty"`
}

// EnvironmentServiceSecretMapping overrides how one service resolves its
// secret-service-configurations. A secret key absent from RemoteKeys defaults to
// the "<service>/<key>" path in the store. SecretStore, when set, overrides the
// environment-wide EnvironmentServiceSecrets.SecretStore for this service so that
// services in a single environment can resolve from different External Secrets
// stores — mirroring EnvironmentManagedSecretReference, which also carries a
// per-reference store.
type EnvironmentServiceSecretMapping struct {
	SecretStore *EnvironmentSecretStoreReference `yaml:"secret-store,omitempty"`
	RemoteKeys  map[string]string                `yaml:"remote-keys,omitempty"`
}

// EnvironmentResourceQuota sizes the ResourceQuota rendered into an
// environment's namespace. Requests and Limits map to the ResourceQuota's hard
// requests.cpu/requests.memory and limits.cpu/limits.memory; Pods caps the pod
// count. DefaultContainer, when set, also renders a LimitRange giving every
// container in the namespace default requests/limits — a ResourceQuota that
// caps a compute resource otherwise rejects any pod that leaves that resource
// unset.
type EnvironmentResourceQuota struct {
	Requests         *EnvironmentResourceList       `yaml:"requests,omitempty"`
	Limits           *EnvironmentResourceList       `yaml:"limits,omitempty"`
	Pods             string                         `yaml:"pods,omitempty"`
	DefaultContainer *EnvironmentContainerResources `yaml:"default-container,omitempty"`
}

// EnvironmentResourceList is a cpu/memory pair expressed as Kubernetes quantity
// strings ("500m", "512Mi"). Kubernetes validates the quantity syntax at
// admission; only presence is checked here.
type EnvironmentResourceList struct {
	CPU    string `yaml:"cpu,omitempty"`
	Memory string `yaml:"memory,omitempty"`
}

// EnvironmentContainerResources is the per-container default requests/limits a
// LimitRange applies to pods that omit their own.
type EnvironmentContainerResources struct {
	Requests *EnvironmentResourceList `yaml:"requests,omitempty"`
	Limits   *EnvironmentResourceList `yaml:"limits,omitempty"`
}

func (l *EnvironmentResourceList) empty() bool {
	return l == nil || (strings.TrimSpace(l.CPU) == "" && strings.TrimSpace(l.Memory) == "")
}

// Validate reports whether a declared resource-quota carries at least one cap.
// A non-nil block that sets nothing would render an empty ResourceMap that
// caps nothing yet still claims namespace ownership, so it fails at load. A nil
// receiver is a valid "not declared" state.
func (q *EnvironmentResourceQuota) Validate() error {
	if q == nil {
		return nil
	}
	if q.Requests.empty() && q.Limits.empty() && strings.TrimSpace(q.Pods) == "" {
		if q.DefaultContainer == nil ||
			(q.DefaultContainer.Requests.empty() && q.DefaultContainer.Limits.empty()) {
			return fmt.Errorf("resource-quota must set at least one of requests, limits, pods, or default-container")
		}
	}
	return nil
}

// validate reports whether the store reference resolves to a usable name/kind.
// label names the offending block in the error so a misdeclared store fails
// loudly at load instead of projecting an ExternalSecret against store "".
func (ref EnvironmentSecretStoreReference) validate(label string) error {
	if strings.TrimSpace(ref.Name) == "" {
		return fmt.Errorf("%s: name cannot be empty", label)
	}
	if strings.TrimSpace(ref.Kind) == "" {
		return fmt.Errorf("%s: kind cannot be empty", label)
	}
	return nil
}

// Validate checks the structural invariants of a declared service-secrets block.
// A non-nil ServiceSecrets that carries an empty store name/kind or a remote-key
// path that resolves to "" would otherwise load without error and reach the
// cluster as a broken (or store-less) ExternalSecret; this makes that fail at
// workspace load instead. A nil receiver is a valid "not declared" state.
func (s *EnvironmentServiceSecrets) Validate() error {
	if s == nil {
		return nil
	}
	if err := s.SecretStore.validate("service-secrets secret-store"); err != nil {
		return err
	}
	for name, mapping := range s.Services {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("service-secrets: service name cannot be empty")
		}
		if mapping.SecretStore != nil {
			if err := mapping.SecretStore.validate(fmt.Sprintf("service-secrets service %q secret-store", name)); err != nil {
				return err
			}
		}
		for key, remote := range mapping.RemoteKeys {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("service-secrets service %q: remote-key name cannot be empty", name)
			}
			if strings.TrimSpace(remote) == "" {
				return fmt.Errorf("service-secrets service %q: remote-key %q resolves to an empty path", name, key)
			}
		}
	}
	return nil
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

	// Dns carries the environment's DNS contract, sourced from the cell
	// descriptor (CellContract.DNS). Its AppHostSuffix lets the network layer
	// derive an external endpoint's public host from declared config instead of
	// a local dns.codefly.yaml, keeping a promotable render value-free. CLI-side;
	// not serialized to proto.
	Dns *EnvironmentDNS `yaml:"dns,omitempty"`

	// ServiceSecrets declares where this environment's regular services resolve
	// their secret-service-configurations. Absent, no service secret projection is
	// rendered and secret-<service> stays an operator precondition. CLI-side; not
	// serialized to proto.
	ServiceSecrets *EnvironmentServiceSecrets `yaml:"service-secrets,omitempty"`

	// ResourceQuota, when set, renders a ResourceQuota (and an optional
	// LimitRange of container defaults) into this environment's namespace so one
	// workspace sharing a cell cannot starve another. Absent, no quota is
	// rendered and the namespace stays uncapped. CLI-side; not serialized to
	// proto.
	ResourceQuota *EnvironmentResourceQuota `yaml:"resource-quota,omitempty"`

	// Secrets lists the secret backends for this environment. Reference-only
	// manifests fail when their backend is absent. Legacy plaintext *.secret.*
	// files remain local-only. CLI-side; not serialized to proto.
	Secrets []*EnvironmentSecretProvider `yaml:"secrets,omitempty"`
}

// EnvironmentDNS is the environment's DNS contract, sourced from a cell
// descriptor (CellContractDNS).
type EnvironmentDNS struct {
	// AppHostSuffix is the public host suffix an app's external endpoints hang
	// off of in this cell (e.g. "staging.eastus2.azure.example.com"). Empty means
	// no declared suffix, so external hosts fall back to a local dns.codefly.yaml.
	AppHostSuffix string `yaml:"app-host-suffix,omitempty"`
}

// AppHost returns the public hostname a service's external endpoints are
// reachable at in this environment, or "" when no app host suffix is declared.
// The label is "<service>-<module>" (the service-module subdomain convention,
// see shared.ToDNSCase) so services sharing a name across modules do not collide
// under one cell suffix; the suffix already scopes the cell/environment.
func (env *Environment) AppHost(service *ServiceIdentity) string {
	if env == nil || env.Dns == nil || env.Dns.AppHostSuffix == "" || service == nil {
		return ""
	}
	label := strings.ToLower(fmt.Sprintf("%s-%s", service.Name, service.Module))
	return fmt.Sprintf("%s.%s", label, env.Dns.AppHostSuffix)
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
