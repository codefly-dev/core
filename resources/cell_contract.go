package resources

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CellContractSchema is the cell-contract version this consumer understands.
const CellContractSchema = "obin-infra/cell-contract/v1"

// CellContract mirrors infra-base's `obinctl cell-contract <coordinate>` output.
// It carries the facts an Environment needs to target a cell — cluster context,
// image registry, managed-Postgres FQDN and its egress CIDRs, the secret store,
// the app host suffix, the fleet workloads path — so an operator never hand-types
// them into workspace.codefly.yaml. Hand-typing the egress CIDR wrong silently
// drops all DB traffic at runtime; consuming the contract removes that class of
// bug. Emitted by infra-base, consumed here — nothing owned by both. (infra-base #329.)
type CellContract struct {
	Schema          string                          `json:"schema"`
	Cell            string                          `json:"cell"`
	Coordinate      string                          `json:"coordinate"`
	Cloud           string                          `json:"cloud"`
	Location        string                          `json:"location"`
	Cluster         CellContractCluster             `json:"cluster"`
	Registry        *CellContractRegistry           `json:"registry,omitempty"`
	NamespacePrefix string                          `json:"namespace_prefix"`
	SecretStore     EnvironmentSecretStoreReference `json:"secret_store"`
	DNS             CellContractDNS                 `json:"dns"`
	Gitops          CellContractGitops              `json:"gitops"`
	Postgres        *CellContractPostgres           `json:"postgres,omitempty"`
	ObjectStorage   *CellContractObjectStorage      `json:"object_storage,omitempty"`
}

type CellContractCluster struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	PrivateAPI bool   `json:"private_api"`
	AccessMode string `json:"access_mode"`
}

type CellContractRegistry struct {
	URL  string `json:"url"`
	Auth string `json:"auth"`
}

type CellContractDNS struct {
	RegistrarZone string `json:"registrar_zone"`
	AppHostSuffix string `json:"app_host_suffix"`
}

type CellContractGitops struct {
	Repo                string `json:"repo"`
	WorkloadsPathPrefix string `json:"workloads_path_prefix"`
}

type CellContractPostgres struct {
	Kind        string   `json:"kind"`
	FQDN        string   `json:"fqdn"`
	EgressCIDRs []string `json:"egress_cidrs"`
	Databases   []string `json:"databases"`
}

type CellContractObjectStorage struct {
	Kind    string `json:"kind"`
	Backend string `json:"backend"`
}

// ParseCellContract decodes and validates a cell-contract/v1 document (the JSON
// emitted by `obinctl cell-contract`).
func ParseCellContract(data []byte) (*CellContract, error) {
	var c CellContract
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("decoding cell contract: %w", err)
	}
	if c.Schema != CellContractSchema {
		return nil, fmt.Errorf("unsupported cell-contract schema %q (want %q)", c.Schema, CellContractSchema)
	}
	if c.Cluster.Context == "" {
		return nil, fmt.Errorf("cell contract for %q carries no cluster context", c.Cell)
	}
	return &c, nil
}

// ToEnvironment maps a cell contract into the deploy-target fields of an
// Environment. envName is the environment identity sent to service agents (e.g.
// "azure"); namespace is the k8s namespace the app deploys into — the contract
// carries the cell's naming prefix, but the app chooses its own namespace. Fields
// the contract does not carry are left zero so existing workspace defaults apply.
func (c *CellContract) ToEnvironment(envName, namespace string) *Environment {
	env := &Environment{
		Name:           envName,
		Namespace:      namespace,
		Cluster:        &EnvironmentCluster{Kind: c.Cluster.Kind, Context: c.Cluster.Context},
		ServiceSecrets: &EnvironmentServiceSecrets{SecretStore: c.SecretStore},
		Gitops: &EnvironmentGitops{
			RepoURL: c.Gitops.Repo,
			Branch:  "main",
			Path:    strings.TrimRight(c.Gitops.WorkloadsPathPrefix, "/") + "/" + namespace,
		},
	}
	if c.Registry != nil {
		env.Registry = &EnvironmentRegistry{URL: c.Registry.URL, Auth: c.Registry.Auth}
	}
	if c.Postgres != nil {
		env.ManagedServices = map[string]EnvironmentManagedService{
			"store": {
				Kind:         c.Postgres.Kind,
				ExternalName: c.Postgres.FQDN,
				// The fact hand-typing gets wrong silently — sourced from the cell,
				// not transcribed.
				EgressCIDRs: c.Postgres.EgressCIDRs,
				SecretReferences: []EnvironmentManagedSecretReference{{
					Name:        "secret-store",
					RemoteKey:   namespace + "/store",
					SecretStore: c.SecretStore,
				}},
			},
		}
	}
	return env
}
