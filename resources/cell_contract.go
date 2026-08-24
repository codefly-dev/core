package resources

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CellContractSchema is the cell-descriptor version this consumer understands.
const CellContractSchema = "codefly/cell/v1"

// CellContract is the cell descriptor codefly accepts from any platform. It
// carries the facts an Environment needs to target a cell — cluster context,
// image registries, managed databases and their egress CIDRs, the secret stores,
// the app host suffix, the fleet workloads path — so an operator never hand-types
// them into workspace.codefly.yaml. Hand-typing an egress CIDR wrong silently
// drops all DB traffic at runtime; consuming the descriptor removes that class of
// bug. codefly owns this contract; any platform conforms to it — infra-base's
// `obinctl cell-contract <coordinate>` is producer #1, but a customer's
// provisioner or a hand-written BYOC descriptor could be others.
//
// Resource collections are lists so a cell that gains a second registry or
// database never forces a v2, and every kind is an open string (kind: aks,
// auth: acr) so a gke/ecr/cloud-sql producer adds a value, not a codefly change —
// codefly core carries no Azure or infra-base knowledge. The namespace is not a
// published cell fact: codefly puts each module in its own namespace itself and
// passes it into ToEnvironment. (infra-base #329.)
type CellContract struct {
	Schema       string                            `json:"schema"`
	Cell         string                            `json:"cell"`
	Coordinate   string                            `json:"coordinate"`
	Cluster      CellContractCluster               `json:"cluster"`
	DNS          CellContractDNS                   `json:"dns"`
	Registries   []CellContractRegistry            `json:"registries"`
	Databases    []CellContractDatabase            `json:"databases"`
	SecretStores []EnvironmentSecretStoreReference `json:"secret_stores"`
	Gitops       *CellContractGitops               `json:"gitops,omitempty"`
	ObjectStores []CellContractObjectStore         `json:"object_stores,omitempty"`
}

type CellContractCluster struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	PrivateAPI bool   `json:"private_api"`
	AccessMode string `json:"access_mode"`
}

// CellContractRegistry is one image registry the cell pushes to. Kind is an open
// string codefly switches on to authenticate (e.g. "acr" -> az acr login), never
// an enum — a gke/ecr producer adds a value, not a code change.
type CellContractRegistry struct {
	Kind   string `json:"kind"`
	URL    string `json:"url"`
	Public bool   `json:"public"`
}

type CellContractDNS struct {
	RegistrarZone string `json:"registrar_zone"`
	AppHostSuffix string `json:"app_host_suffix"`
}

type CellContractGitops struct {
	Repo                string `json:"repo"`
	WorkloadsPathPrefix string `json:"workloads_path_prefix"`
}

// CellContractDatabase is one managed database instance the cell offers.
// EgressCIDRs is the load-bearing fact — a wrong value silently drops all DB
// traffic — so it is sourced here, not transcribed. Kind is an open string (e.g.
// "azure-postgres-flexible"); DatabaseNames lists the connectable databases
// inside the instance.
type CellContractDatabase struct {
	Engine        string   `json:"engine"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	FQDN          string   `json:"fqdn"`
	EgressCIDRs   []string `json:"egress_cidrs"`
	DatabaseNames []string `json:"database_names"`
	PasswordAuth  bool     `json:"password_auth"`
}

// CellContractObjectStore is one managed object-storage endpoint. Kind is an open
// string (e.g. "azure-blob"). Optional: a producer that resolves the endpoint
// post-apply omits it.
type CellContractObjectStore struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// ParseCellContract decodes and validates a codefly/cell/v1 descriptor (e.g. the
// JSON emitted by `obinctl cell-contract`, which conforms to this schema).
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

// ToEnvironment maps a cell descriptor into the deploy-target fields of an
// Environment. envName is the environment identity sent to service agents (e.g.
// "azure"); namespace is the k8s namespace the app deploys into — codefly puts
// each module in its own namespace, so the descriptor never carries it. Resource
// collections are single-instance on today's cells, so index [0] is taken; fields
// the descriptor does not carry are left zero so existing workspace defaults apply.
func (c *CellContract) ToEnvironment(envName, namespace string) *Environment {
	env := &Environment{
		Name:      envName,
		Namespace: namespace,
		Cluster:   &EnvironmentCluster{Kind: c.Cluster.Kind, Context: c.Cluster.Context},
	}
	if len(c.SecretStores) > 0 {
		env.ServiceSecrets = &EnvironmentServiceSecrets{SecretStore: c.SecretStores[0]}
	}
	if c.Gitops != nil {
		env.Gitops = &EnvironmentGitops{
			RepoURL: c.Gitops.Repo,
			Branch:  "main",
			Path:    strings.TrimRight(c.Gitops.WorkloadsPathPrefix, "/") + "/" + namespace,
		}
	}
	if len(c.Registries) > 0 {
		// Kind is codefly's auth selector (acr -> az acr login); the URL is opaque.
		env.Registry = &EnvironmentRegistry{URL: c.Registries[0].URL, Auth: c.Registries[0].Kind}
	}
	if len(c.Databases) > 0 {
		db := c.Databases[0]
		ref := EnvironmentManagedSecretReference{
			Name:      "secret-store",
			RemoteKey: namespace + "/store",
		}
		if len(c.SecretStores) > 0 {
			ref.SecretStore = c.SecretStores[0]
		}
		env.ManagedServices = map[string]EnvironmentManagedService{
			"store": {
				Kind:         db.Kind,
				ExternalName: db.FQDN,
				// The fact hand-typing gets wrong silently — sourced from the cell,
				// not transcribed.
				EgressCIDRs:      db.EgressCIDRs,
				SecretReferences: []EnvironmentManagedSecretReference{ref},
			},
		}
	}
	return env
}
