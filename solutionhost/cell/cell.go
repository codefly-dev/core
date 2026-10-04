// Package cell is the ONE implementation of the cell file — codefly/cell/v1,
// the inventory of one environment's cell that a composition's publish writes
// to the delivery repository at deployments/cells/<environment>/cell.yaml and
// the platform derives its mesh policy, admission set and RBAC from, rather
// than from a hand-written set. Its Go model, its strict decoder and every
// refusal rule live here, and the fixtures it ships (Fixtures) are what the
// CLI's renderer and publisher and the platform's loader are driven through,
// so a reader that started accepting a workload it cannot police fails here
// and everywhere at once. Nothing outside this package may parse or validate
// a cell file.
//
// It lives under solutionhost because the cell is the other half of what a
// publish delivers: the presence and authority documents declare the
// identities a host activates, and the cell declares the workloads, images,
// identities, endpoints and reach the platform admits around them.
//
// The model carries what the platform compares — never what it could derive:
// a selector is carried explicitly because a bootstrap Job carries no "app"
// label and a policy assuming one selects its pods with nothing; the
// authenticating container is named because inferring it from several is the
// sidecar attack; the trust domain is carried at the top as well as inside
// every SPIFFE ID so a reader re-derives an identity and refuses a mismatch
// rather than parse the domain out of the string it is checking.
package cell

const (
	// SchemaV1 is the cell file's schema, the only one this package reads.
	SchemaV1 = "codefly/cell/v1"
	// FileName is the cell file's name within its environment directory.
	FileName = "cell.yaml"
)

// Workload kinds a cell carries: every pod-producing object of a rendered
// unit.
const (
	KindDeployment  = "Deployment"
	KindStatefulSet = "StatefulSet"
	KindDaemonSet   = "DaemonSet"
	KindJob         = "Job"
	KindCronJob     = "CronJob"
)

// File is the inventory of one environment's cell.
type File struct {
	Schema     string `yaml:"schema"`
	Coordinate string `yaml:"coordinate,omitempty"`
	Component  string `yaml:"component,omitempty"`
	// Domain is the ownership domain this composition delivers under — the one
	// every document it renders asserts. A host accepts a domain only from a
	// signer it lets speak for it, and that policy is the platform's, keyed
	// by the composition's release-workflow identity; carrying the domain
	// here lets the platform hold its policy against what the composition
	// declares at build time, rather than have the host refuse the first
	// delivery.
	Domain string `yaml:"domain,omitempty"`
	// TrustDomain is the SPIFFE trust domain every workload's identity is
	// issued under, carried at the top level as well as inside each spiffe_id
	// so the platform can re-derive an identity and refuse a mismatch rather
	// than parse the domain back out of the string it is checking.
	TrustDomain string `yaml:"trust_domain,omitempty"`
	Environment string `yaml:"environment"`
	// Namespaces are the namespaces the composition's modules render into, one
	// per module, in name order.
	Namespaces []Namespace `yaml:"namespaces"`
}

// Namespace is one module's namespace and what runs in it.
type Namespace struct {
	Name      string     `yaml:"name"`
	Module    string     `yaml:"module"`
	Workloads []Workload `yaml:"workloads"`
	// Egress is the external reach the environment grants workloads of this
	// namespace: the hosts its services are declared to dial and the CIDRs
	// of the managed services that replace its services. It is the only
	// egress the render holds.
	Egress []Egress `yaml:"egress,omitempty"`
	// Delivery is the Job that POSTs this namespace's presence documents to
	// the host, when the module delivers presence: a pod the closed admission
	// set would refuse unless declared, and the one pod of the namespace that
	// runs as the delivery account. Its name carries the settled set's digest
	// and is decided at publish, so it is declared by its labels.
	Delivery *Delivery `yaml:"delivery,omitempty"`
}

// Delivery is the presence delivery Job as admission must know it: the labels
// its pods carry, the account they run as, and the one container and image
// that deliver.
type Delivery struct {
	Kind           string            `yaml:"kind"`
	Selector       map[string]string `yaml:"selector"`
	ServiceAccount string            `yaml:"service_account"`
	SPIFFEID       string            `yaml:"spiffe_id,omitempty"`
	Container      string            `yaml:"container"`
	Image          Image             `yaml:"image"`
}

// Workload is one thing the host runs — every pod-producing object of a
// rendered unit: a Deployment, StatefulSet or DaemonSet, and the Jobs and
// CronJobs that bootstrap it, which run their own images and must be declared
// or the platform's closed approved set refuses them — with the identity it
// runs as and the endpoints it serves.
type Workload struct {
	// Name is the workload's object name.
	Name string `yaml:"name"`
	// Kind is the workload's Kubernetes kind.
	Kind string `yaml:"kind"`
	// Selector is the exact label set that selects the workload's pods: the
	// selector of a Deployment, StatefulSet or DaemonSet, the pod template's
	// labels of a Job or CronJob. Carried explicitly, because a bootstrap Job
	// carries no "app" label and a policy assuming one selects its pods with
	// nothing. Match labels only: an expression selector is refused at
	// render, since the cell cannot carry one.
	Selector map[string]string `yaml:"selector"`
	// Service is the module-qualified service the workload runs: <module>/<service>.
	Service string `yaml:"service"`
	// ServiceAccount is the account the pod runs as.
	ServiceAccount string `yaml:"service_account"`
	// SPIFFEID is the identity the host's issuer gives that account, when the
	// environment declares a host trust domain:
	// spiffe://<trust_domain>/ns/<namespace>/sa/<service_account>.
	SPIFFEID string `yaml:"spiffe_id,omitempty"`
	// Authenticating names the one container that authenticates as the
	// workload — the same designation the presence document carries — so an
	// admission policy compares that container's image to the approved build
	// and treats every other container, init containers included, as a closed
	// set that never does. Inferring it from a single container is safe;
	// inferring it from several is the sidecar attack, so it is named.
	Authenticating string `yaml:"authenticating"`
	// Containers are the workload's containers and the exact image each runs.
	Containers []Container `yaml:"containers"`
	// InitContainers are the workload's init containers, which never
	// authenticate as the workload.
	InitContainers []Container `yaml:"init_containers,omitempty"`
	// Artifact is the rendered unit the workload comes from, pinned by the
	// SHA-256 of its rendered bytes (sha256:<64 hex>).
	Artifact Artifact `yaml:"artifact"`
	// Release is the module package the unit was rendered from, when it has one.
	Release *Release `yaml:"release,omitempty"`
	// Endpoints are the endpoints the service declares, with the port each is
	// served on when the service declares one.
	Endpoints []Endpoint `yaml:"endpoints,omitempty"`
	// Ingress are the environment's ingress routes to this workload's endpoints.
	Ingress []Ingress `yaml:"ingress,omitempty"`
	// Verifier marks the serving workloads of the service the environment's
	// host block names as the delivery API: the one that verifies delivered
	// documents, from which the platform derives the narrow RBAC that needs
	// (token reviews, pod reads in delivered namespaces) and the carrier's
	// allow into it. Derived from host.delivery, never guessed.
	Verifier bool `yaml:"verifier,omitempty"`
	// Bindings are the cell-provided resources the service is declared to
	// bind, from the environment's cell declaration; the cell provisions each
	// and derives the grant.
	Bindings []string `yaml:"bindings,omitempty"`
	// CloudIdentity is whether the workload mints a cloud credential from the
	// node's metadata server, declared by the environment: a network path no
	// egress waypoint carries, which the platform allows per workload.
	CloudIdentity bool `yaml:"cloud_identity,omitempty"`
}

// Container is one container and its pinned image.
type Container struct {
	Name  string `yaml:"name"`
	Image Image  `yaml:"image"`
}

// Image is an image reference split into the two things the platform
// compares: the repository, and the OCI manifest digest.
type Image struct {
	Repository string `yaml:"repository"`
	Digest     string `yaml:"digest"`
}

// Artifact is one rendered unit and the digest of its rendered bytes.
type Artifact struct {
	Name   string `yaml:"name"`
	Digest string `yaml:"digest"`
}

// Release identifies the module package a unit was rendered from.
type Release struct {
	Publisher string `yaml:"publisher"`
	Name      string `yaml:"name"`
	Version   string `yaml:"version"`
}

// Endpoint is one declared endpoint of a workload's service.
type Endpoint struct {
	Name string `yaml:"name"`
	API  string `yaml:"api,omitempty"`
	// Port is the CONTAINER port the endpoint is served on — the port a
	// connection lands on after Service resolution, which is what a mesh
	// authorizes — as the service declares it and the render verifies against
	// the rendered Service's target. Zero when the service declares none.
	Port         uint32   `yaml:"port,omitempty"`
	Visibility   string   `yaml:"visibility,omitempty"`
	AllowModules []string `yaml:"allow_modules,omitempty"`
	// Consumers are the module-qualified services that declare a dependency
	// on this endpoint, from every composed service's service-dependencies.
	// Empty means no declared edge reaches it, which is visible here rather
	// than found by an audit.
	Consumers []string `yaml:"consumers,omitempty"`
}

// Ingress is one ingress route into an endpoint.
type Ingress struct {
	Endpoint string   `yaml:"endpoint"`
	Hosts    []string `yaml:"hosts"`
}

// Egress is the external reach one service is declared to need: the hosts
// the environment declares it dials (a declaration, never derived), each on
// the port it is reached on, and the CIDRs of a managed service that replaces
// it.
type Egress struct {
	Service string       `yaml:"service"`
	Hosts   []EgressHost `yaml:"hosts,omitempty"`
	CIDRs   []string     `yaml:"cidrs,omitempty"`
}

// EgressHost is one host and the port it is reached on. The port is always
// explicit here, because a mesh allows a (host, port) and a reader defaulting
// it would be a second place the default lives.
type EgressHost struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}
