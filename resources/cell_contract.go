package resources

import (
	"encoding/json"
	"fmt"
	"net"
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
// database never forces a schema v2, and every kind is an open string (kind: aks,
// auth: acr) so a gke/ecr/cloud-sql producer adds a value, not a codefly change —
// codefly core carries no Azure or infra-base knowledge. The namespace is not a
// published cell fact: codefly puts each module in its own namespace itself and
// passes it into ToEnvironment. (infra-base #329.)
type CellContract struct {
	Schema       string                    `json:"schema"`
	Cell         string                    `json:"cell"`
	Coordinate   string                    `json:"coordinate"`
	Cluster      CellContractCluster       `json:"cluster"`
	DNS          CellContractDNS           `json:"dns"`
	Registries   []CellContractRegistry    `json:"registries"`
	Databases    []CellContractDatabase    `json:"databases"`
	SecretStores []CellContractSecretStore `json:"secret_stores"`
	Gitops       *CellContractGitops       `json:"gitops,omitempty"`
	ObjectStores []CellContractObjectStore `json:"object_stores,omitempty"`
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

// CellContractSecretStore selects an External Secrets store the cell exposes.
// It is the wire (JSON) shape; ToEnvironment maps it to the Environment's
// yaml-serialized EnvironmentSecretStoreReference — the two serialization
// contracts are kept as separate types on purpose.
type CellContractSecretStore struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
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
	// A deploy target needs exactly one registry to push images to. Zero would
	// leave env.Registry nil and silently fall back to the legacy hard-coded
	// registry (wrong, and cross-tenant, for a BYOC cell); more than one has no
	// single push target this consumer can pick. Public is informational (is that
	// registry publicly reachable) — not a push/pull selector — so it is not read.
	if len(c.Registries) != 1 {
		return nil, fmt.Errorf("cell contract for %q carries %d registries; this consumer needs exactly one", c.Cell, len(c.Registries))
	}
	// The database and secret store collapse into Environment's single "store"
	// managed service and single ServiceSecrets slot. Reject more than one rather
	// than silently mapping index [0] and dropping the rest — a dropped database's
	// egress CIDRs are exactly the silent DB-traffic outage this contract exists to
	// prevent. The wire stays a list (no schema v2); teaching the consumer to map
	// many is a codefly-internal change when a cell actually has two.
	if len(c.Databases) > 1 {
		return nil, fmt.Errorf("cell contract for %q carries %d databases; this consumer maps one", c.Cell, len(c.Databases))
	}
	if len(c.SecretStores) > 1 {
		return nil, fmt.Errorf("cell contract for %q carries %d secret stores; this consumer maps one", c.Cell, len(c.SecretStores))
	}
	// Egress CIDRs gate all traffic to the managed database. An empty list means no
	// traffic is allowed (the DB is silently unreachable), and a malformed CIDR
	// matches nothing — validate at the seam instead of transcribing the failure
	// downstream.
	for i := range c.Databases {
		db := c.Databases[i]
		if len(db.EgressCIDRs) == 0 {
			return nil, fmt.Errorf("database %q in cell %q carries no egress CIDRs", db.Name, c.Cell)
		}
		for _, cidr := range db.EgressCIDRs {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return nil, fmt.Errorf("database %q in cell %q has invalid egress CIDR %q: %w", db.Name, c.Cell, cidr, err)
			}
		}
	}
	return &c, nil
}

// KnowsDatabase reports whether the cell's managed database exposes a logical
// database of the given name. The connectable database name is app-supplied — the
// cell only publishes what exists — so a caller resolving a service against this
// cell validates the app's requested database here, turning a typo into a
// config-time error instead of a runtime connection failure.
func (c *CellContract) KnowsDatabase(name string) bool {
	for i := range c.Databases {
		for _, n := range c.Databases[i].DatabaseNames {
			if n == name {
				return true
			}
		}
	}
	return false
}

// ToEnvironment maps a cell descriptor into the deploy-target fields of an
// Environment. envName is the environment identity sent to service agents (e.g.
// "azure"); namespace is the k8s namespace the app deploys into — codefly puts
// each module in its own namespace, so the descriptor never carries it. The
// namespace becomes a directory component of the gitops path and a segment of the
// managed secret's remote key, so it is confined to a single path component here,
// the same guard every other resource name in this package passes. Resource
// collections are single-instance (ParseCellContract rejects more), so index [0]
// is taken; fields the descriptor does not carry are left zero so existing
// workspace defaults apply.
func (c *CellContract) ToEnvironment(envName, namespace string) (*Environment, error) {
	if err := validateResourcePathComponent("namespace", namespace); err != nil {
		return nil, err
	}
	env := &Environment{
		Name:      envName,
		Namespace: namespace,
		Cluster:   &EnvironmentCluster{Kind: c.Cluster.Kind, Context: c.Cluster.Context},
	}
	var store EnvironmentSecretStoreReference
	if len(c.SecretStores) > 0 {
		store = EnvironmentSecretStoreReference{Name: c.SecretStores[0].Name, Kind: c.SecretStores[0].Kind}
		env.ServiceSecrets = &EnvironmentServiceSecrets{SecretStore: store}
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
		env.ManagedServices = map[string]EnvironmentManagedService{
			"store": {
				Kind:         db.Kind,
				ExternalName: db.FQDN,
				// The fact hand-typing gets wrong silently — sourced from the cell,
				// not transcribed.
				EgressCIDRs: db.EgressCIDRs,
				SecretReferences: []EnvironmentManagedSecretReference{{
					Name:        "secret-store",
					RemoteKey:   namespace + "/store",
					SecretStore: store,
				}},
			},
		}
	}
	return env, nil
}
