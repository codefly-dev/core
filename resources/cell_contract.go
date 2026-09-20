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
	AuditSinks   []CellContractAuditSink   `json:"audit_sinks,omitempty"`

	// RequiresCapabilities names the consumer behaviours this descriptor cannot
	// work without. An unknown JSON field decodes to nothing, so a producer that
	// starts publishing a field an older consumer predates would otherwise get a
	// workload deployed with exactly the part that makes it work missing, and no
	// error anywhere. Declaring the capability turns that into a refusal at the
	// seam. Values are open strings; this consumer implements
	// cellContractCapabilities.
	RequiresCapabilities []string `json:"requires_capabilities,omitempty"`
}

// The capabilities this consumer implements, named in a descriptor's
// RequiresCapabilities.
const (
	// CapabilityManagedIdentityTransport is the database transport/identity
	// binding: port, transport mode and the exact runtime principal a workload
	// authenticates as, carried through to the workload renderer.
	CapabilityManagedIdentityTransport = "managed-identity-transport"
	// CapabilityAuditSinks is the audit-sink collection and its delivery facts.
	CapabilityAuditSinks = "audit-sinks"
)

var cellContractCapabilities = map[string]bool{
	CapabilityManagedIdentityTransport: true,
	CapabilityAuditSinks:               true,
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
	Port          int      `json:"port,omitempty"`
	EgressCIDRs   []string `json:"egress_cidrs"`
	DatabaseNames []string `json:"database_names"`
	PasswordAuth  bool     `json:"password_auth"`
	// Transport declares how a workload reaches this instance when dialing the
	// published endpoint directly is not it.
	Transport *CellContractTransport `json:"transport,omitempty"`
	// Identity is the exact runtime principal a workload authenticates as. It is
	// required when PasswordAuth is false: a passwordless instance with no
	// declared identity leaves the workload with no way to authenticate, and a
	// runtime that cannot authenticate can come up and simply never register.
	Identity *CellContractIdentity `json:"identity,omitempty"`
}

// Transport modes this consumer renders.
const (
	// TransportModeDirect dials the database endpoint from the workload.
	TransportModeDirect = "direct"
	// TransportModeProxy runs the declared proxy image beside the workload and
	// dials it on loopback; the proxy holds the authenticated private connection.
	TransportModeProxy = "proxy"
)

// CellContractTransport declares how a workload reaches a managed endpoint.
// Unlike a kind — an open string this consumer only uses to select an auth
// side-effect — a mode decides what the workload renderer must emit, so one this
// consumer does not implement is refused rather than carried through to a
// workload that would come up with no path to the database at all.
type CellContractTransport struct {
	Mode string `json:"mode"`
	// Image and Args are the proxy the pod runs in TransportModeProxy, and
	// LocalPort is the loopback port it listens on.
	Image     string   `json:"image,omitempty"`
	Args      []string `json:"args,omitempty"`
	LocalPort int      `json:"local_port,omitempty"`
}

// CellContractIdentity is the exact runtime principal a workload authenticates
// as. Principal is resolved by the producer — never a template codefly fills in
// from a customer, account or region name. Annotations and Labels are the
// platform's own attachment mechanism: codefly stamps them verbatim onto the
// workload's ServiceAccount and pod template, which is what lets a cell on any
// platform wire its identity webhook without codefly knowing what the keys mean.
type CellContractIdentity struct {
	Kind        string            `json:"kind"`
	Principal   string            `json:"principal"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// CellContractAuditSink is one destination the cell delivers audit records to.
// Kind is an open string naming the sink technology and Target the exact
// destination the producer applied. Residency and Retention carry the producer's
// approved data-handling facts: codefly transports them and infers neither — an
// unapplied retention lock is `locked: false`, not an assumption — and never
// synthesizes a connection string for the sink from its parts.
type CellContractAuditSink struct {
	Name      string                      `json:"name"`
	Kind      string                      `json:"kind"`
	Target    string                      `json:"target"`
	Writer    *CellContractIdentity       `json:"writer,omitempty"`
	Residency string                      `json:"residency,omitempty"`
	Retention *CellContractAuditRetention `json:"retention,omitempty"`
}

// CellContractAuditRetention is the retention the producer applied to a sink.
// Locked reports whether a retention lock is in force, which is an approval fact
// only the producer holds.
type CellContractAuditRetention struct {
	Days   int  `json:"days"`
	Locked bool `json:"locked"`
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
	for _, capability := range c.RequiresCapabilities {
		if !cellContractCapabilities[capability] {
			return nil, fmt.Errorf("cell contract for %q requires capability %q, which this consumer does not implement", c.Cell, capability)
		}
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
		if err := validateDatabaseAccess(db, c.Cell); err != nil {
			return nil, err
		}
	}
	for _, sink := range c.AuditSinks {
		if err := validateAuditSink(sink, c.Cell); err != nil {
			return nil, err
		}
	}
	// A delivery target is declared whole or not at all. Half of one leaves the
	// rendered workloads pointing at an empty repo or the cell's root path, which
	// reconciles somewhere other than where the owner decided they go.
	if c.Gitops != nil {
		if c.Gitops.Repo == "" {
			return nil, fmt.Errorf("cell contract for %q declares a delivery target with no repository", c.Cell)
		}
		if c.Gitops.WorkloadsPathPrefix == "" {
			return nil, fmt.Errorf("cell contract for %q declares a delivery target with no workloads path prefix", c.Cell)
		}
	}
	return &c, nil
}

// validateDatabaseAccess refuses a transport/identity binding this consumer
// cannot honor. Each rejection here is a deployment that would otherwise render
// and start: a workload with no principal to authenticate as, a proxy with no
// image to run, or a connection with no port to dial.
func validateDatabaseAccess(db CellContractDatabase, cell string) error {
	if db.Port < 0 || db.Port > 65535 {
		return fmt.Errorf("database %q in cell %q has out-of-range port %d", db.Name, cell, db.Port)
	}
	if !db.PasswordAuth {
		if db.Identity == nil || db.Identity.Principal == "" {
			return fmt.Errorf("database %q in cell %q is passwordless but declares no runtime identity principal", db.Name, cell)
		}
	}
	// The port is engine-specific and the engine is an open string, so there is
	// nothing to fall back to that would not be a guess baked into codefly.
	if db.Port == 0 && (db.Transport != nil || !db.PasswordAuth) {
		return fmt.Errorf("database %q in cell %q declares a transport binding but no port", db.Name, cell)
	}
	if db.Transport == nil {
		return nil
	}
	switch db.Transport.Mode {
	case TransportModeDirect:
	case TransportModeProxy:
		if db.Transport.Image == "" {
			return fmt.Errorf("database %q in cell %q declares a %s transport with no image", db.Name, cell, TransportModeProxy)
		}
		if db.Transport.LocalPort <= 0 || db.Transport.LocalPort > 65535 {
			return fmt.Errorf("database %q in cell %q declares a %s transport with no usable local port", db.Name, cell, TransportModeProxy)
		}
	default:
		return fmt.Errorf("database %q in cell %q declares transport mode %q, which this consumer does not implement", db.Name, cell, db.Transport.Mode)
	}
	return nil
}

// validateAuditSink refuses a sink codefly could only complete by inventing a
// fact: a destination, or the writer allowed to reach it.
func validateAuditSink(sink CellContractAuditSink, cell string) error {
	if sink.Name == "" {
		return fmt.Errorf("cell %q declares an audit sink with no name", cell)
	}
	if sink.Kind == "" {
		return fmt.Errorf("audit sink %q in cell %q carries no kind", sink.Name, cell)
	}
	if sink.Target == "" {
		return fmt.Errorf("audit sink %q in cell %q carries no target", sink.Name, cell)
	}
	if sink.Writer == nil || sink.Writer.Principal == "" {
		return fmt.Errorf("audit sink %q in cell %q names no writer principal", sink.Name, cell)
	}
	if sink.Retention != nil && sink.Retention.Days <= 0 {
		return fmt.Errorf("audit sink %q in cell %q declares a retention of %d days", sink.Name, cell, sink.Retention.Days)
	}
	return nil
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
	if c.DNS.AppHostSuffix != "" {
		env.Dns = &EnvironmentDNS{AppHostSuffix: c.DNS.AppHostSuffix}
	}
	if len(c.Registries) > 0 {
		// Kind is codefly's auth selector (acr -> az acr login); the URL is opaque.
		env.Registry = &EnvironmentRegistry{URL: c.Registries[0].URL, Auth: c.Registries[0].Kind}
	}
	if len(c.Databases) > 0 {
		db := c.Databases[0]
		managed := EnvironmentManagedService{
			Kind:         db.Kind,
			ExternalName: db.FQDN,
			Port:         db.Port,
			// The fact hand-typing gets wrong silently — sourced from the cell,
			// not transcribed.
			EgressCIDRs: db.EgressCIDRs,
			Identity:    db.Identity.toEnvironment(),
		}
		if db.Transport != nil {
			managed.Transport = &EnvironmentManagedTransport{
				Mode:      db.Transport.Mode,
				Image:     db.Transport.Image,
				Args:      db.Transport.Args,
				LocalPort: db.Transport.LocalPort,
			}
		}
		// An instance that takes no password has no secret to project: the workload
		// authenticates as its own identity instead, so projecting one here would
		// bind the pod to a Secret the cell never writes and block it from starting.
		if db.PasswordAuth {
			managed.SecretReferences = []EnvironmentManagedSecretReference{{
				Name:        "secret-store",
				RemoteKey:   namespace + "/store",
				SecretStore: store,
			}}
		}
		env.ManagedServices = map[string]EnvironmentManagedService{"store": managed}
	}
	for _, sink := range c.AuditSinks {
		env.AuditSinks = append(env.AuditSinks, EnvironmentAuditSink{
			Name:      sink.Name,
			Kind:      sink.Kind,
			Target:    sink.Target,
			Writer:    sink.Writer.toEnvironment(),
			Residency: sink.Residency,
			Retention: sink.Retention.toEnvironment(),
		})
	}
	return env, nil
}

func (i *CellContractIdentity) toEnvironment() *EnvironmentWorkloadIdentity {
	if i == nil {
		return nil
	}
	return &EnvironmentWorkloadIdentity{
		Kind:        i.Kind,
		Principal:   i.Principal,
		Annotations: i.Annotations,
		Labels:      i.Labels,
	}
}

func (r *CellContractAuditRetention) toEnvironment() *EnvironmentAuditRetention {
	if r == nil {
		return nil
	}
	return &EnvironmentAuditRetention{Days: r.Days, Locked: r.Locked}
}
