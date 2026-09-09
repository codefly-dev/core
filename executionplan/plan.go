package executionplan

import "time"

const (
	// SchemaV1 is the only plan schema this package accepts.
	SchemaV1 = "codefly.execution-plan/v1"
	// SemanticHashFormatV1 is mixed into the semantic digest so that changing how
	// the digest is computed changes every fingerprint instead of colliding with
	// the previous format.
	SemanticHashFormatV1 = "codefly.execution-plan.semantic-hash/v1"
)

// Phase is the operation the plan was resolved for. The same closure can resolve
// differently per phase, so it is part of semantic identity.
type Phase string

const (
	PhaseBuild  Phase = "build"
	PhaseRun    Phase = "run"
	PhaseTest   Phase = "test"
	PhaseDeploy Phase = "deploy"
)

// NodeKind distinguishes the resources a plan can select.
type NodeKind string

const (
	NodeService NodeKind = "service"
)

// Resolution says whether a selected node resolved to something executable.
type Resolution string

const (
	Resolved   Resolution = "resolved"
	Unresolved Resolution = "unresolved"
)

// EdgeKind shares the vocabulary of the typed dependency kinds so a typed
// declaration maps through without a schema change. KindDeclared is an edge from
// a declaration that carries no kind.
type EdgeKind string

const (
	KindDeclared   EdgeKind = "declared"
	KindBuild      EdgeKind = "build"
	KindRuntime    EdgeKind = "runtime"
	KindCompletion EdgeKind = "completion"
	KindSchema     EdgeKind = "schema"
	KindExternal   EdgeKind = "external"
)

// Verification is the provenance the artifact's bytes will carry. Unverified is
// a deliberate opt-out (an overlay `git` directive), not missing information.
type Verification string

const (
	// VerificationLocal means the bytes come from a local checkout, so no
	// artifact verification applies to them.
	VerificationLocal Verification = "local"
	// VerificationVerified means the bytes are pulled as a signed,
	// digest-checked artifact.
	VerificationVerified Verification = "verified"
	// VerificationUnverified means artifact verification was explicitly waived.
	VerificationUnverified Verification = "unverified"
)

// OriginKind is where a configuration value will come from at runtime.
type OriginKind string

const (
	OriginServiceEndpoint        OriginKind = "service-endpoint"
	OriginWorkspaceConfiguration OriginKind = "workspace-configuration"
	OriginServiceConfiguration   OriginKind = "service-configuration"
)

// SelectionReason explains one choice: why a node or edge is in the closure, or
// which resolution rule produced an artifact.
type SelectionReason string

const (
	ReasonRequestedTarget      SelectionReason = "requested-target"
	ReasonDeclaredDependency   SelectionReason = "declared-dependency"
	ReasonTransitiveDependency SelectionReason = "transitive-dependency"
	ReasonSchemaPrerequisite   SelectionReason = "schema-prerequisite"
	ReasonLocalOverlay         SelectionReason = "local-overlay"
	ReasonCommittedPin         SelectionReason = "committed-pin"
	ReasonWorkspaceLayout      SelectionReason = "workspace-layout"
	ReasonInvocationOverride   SelectionReason = "invocation-override"
)

// Lifecycle is what happens to the resources a plan selects when the invocation
// ends.
type Lifecycle string

const (
	LifecycleStop        Lifecycle = "stop"
	LifecycleKeepRunning Lifecycle = "keep-running"
	LifecycleReset       Lifecycle = "reset"
)

// Plan is one resolved, immutable execution plan.
type Plan struct {
	Schema         string                `json:"schema"`
	Requested      Target                `json:"requested"`
	Nodes          []Node                `json:"nodes"`
	Edges          []Edge                `json:"edges,omitempty"`
	Configurations []ConfigurationOrigin `json:"configurations,omitempty"`
	SchemaSteps    []SchemaStep          `json:"schemaSteps,omitempty"`
	StatePolicy    StatePolicy           `json:"statePolicy"`
	// Invocation is the only non-semantic part of a plan: it identifies this
	// run, not what the run does, and is excluded from SemanticFingerprint so
	// two invocations of the same plan compare equal.
	Invocation *Invocation `json:"invocation,omitempty"`
}

// Target is what the caller asked for.
type Target struct {
	Workspace   string `json:"workspace"`
	Service     string `json:"service"`
	Phase       Phase  `json:"phase"`
	Environment string `json:"environment,omitempty"`
}

// Node is one selected member of the closure.
type Node struct {
	ID         string     `json:"id"`
	Kind       NodeKind   `json:"kind"`
	Module     string     `json:"module"`
	Name       string     `json:"name"`
	Resolution Resolution `json:"resolution"`
	// Unresolved carries the actionable reason an unresolved node could not be
	// resolved. A node reached by an edge but never resolved fails validation
	// rather than being dropped from the closure.
	Unresolved string                `json:"unresolved,omitempty"`
	Backend    *Backend              `json:"backend,omitempty"`
	Artifacts  []Artifact            `json:"artifacts,omitempty"`
	Endpoints  []EndpointRequirement `json:"endpoints,omitempty"`
	Selection  Selection             `json:"selection"`
}

// Backend is the exact agent chosen to operate a node.
type Backend struct {
	Kind      string `json:"kind"`
	Publisher string `json:"publisher"`
	Name      string `json:"name"`
	Version   string `json:"version"`
}

// Artifact is an immutable input pinned for this plan. Reference is a workspace
// or registry identity, never a local path: a plan resolved from two checkouts
// of the same workspace must fingerprint identically.
type Artifact struct {
	Reference    string       `json:"reference"`
	Version      string       `json:"version,omitempty"`
	Digest       string       `json:"digest,omitempty"`
	Verification Verification `json:"verification"`
	Selection    Selection    `json:"selection"`
}

// EndpointRequirement is an endpoint a node must expose for this closure.
type EndpointRequirement struct {
	Name       string   `json:"name"`
	API        string   `json:"api,omitempty"`
	Visibility string   `json:"visibility,omitempty"`
	RequiredBy []string `json:"requiredBy,omitempty"`
}

// Edge is a resolved dependency: From must be satisfied before To.
type Edge struct {
	From string   `json:"from"`
	To   string   `json:"to"`
	Kind EdgeKind `json:"kind"`
	// Endpoints are the endpoint names the consumer named. Empty means it
	// consumes every endpoint the producer exposes; the producer's node states
	// which ones that resolved to.
	Endpoints []string  `json:"endpoints,omitempty"`
	Selection Selection `json:"selection"`
}

// ConfigurationOrigin says where one configuration key a consumer receives comes
// from. It deliberately has no value field, so no code path can serialize a
// configuration value, credential or control nonce into a plan.
type ConfigurationOrigin struct {
	Consumer      string     `json:"consumer"`
	Key           string     `json:"key"`
	Origin        OriginKind `json:"origin"`
	Producer      string     `json:"producer,omitempty"`
	Secret        bool       `json:"secret,omitempty"`
	SecretRef     string     `json:"secretRef,omitempty"`
	SecretVersion string     `json:"secretVersion,omitempty"`
	Selection     Selection  `json:"selection"`
}

// SchemaStep is one schema prerequisite. Steps are semantically ordered and are
// never sorted by canonicalization.
type SchemaStep struct {
	ID        string    `json:"id"`
	Module    string    `json:"module"`
	Name      string    `json:"name"`
	Targets   []string  `json:"targets,omitempty"`
	Backend   *Backend  `json:"backend,omitempty"`
	Selection Selection `json:"selection"`
}

// StatePolicy is how the invocation treats state it finds and leaves behind.
type StatePolicy struct {
	// Reuse allows reattaching to a warm session whose plan has an equal
	// semantic fingerprint.
	Reuse     bool      `json:"reuse"`
	Lifecycle Lifecycle `json:"lifecycle"`
}

// Selection is the explanation for one choice.
type Selection struct {
	Reason SelectionReason `json:"reason"`
	// Via is the dependency path that pulled this in, nearest requirer last. It
	// is a path, so canonicalization preserves its order.
	Via []string `json:"via,omitempty"`
	// Over names the candidates this choice beat when precedence decided it.
	Over   []string `json:"over,omitempty"`
	Detail string   `json:"detail,omitempty"`
}

// Invocation identifies one run. Nothing here contributes to semantic identity.
type Invocation struct {
	ID           string    `json:"id"`
	StartedAt    time.Time `json:"startedAt"`
	WorkspaceDir string    `json:"workspaceDir,omitempty"`
	Actor        string    `json:"actor,omitempty"`
}
