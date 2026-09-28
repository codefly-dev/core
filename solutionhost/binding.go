// Package solutionhost defines SolutionHostBinding: the versioned document that
// declares which solution runs on which host, so a solution's presence is
// delivered rather than announced.
//
// Today a solution becomes present by registering itself — a runtime heartbeats
// and the host learns of it as a side effect of a process being up. This package
// holds the opposite record: delivery declares the binding, the host reconciles
// it, and a runtime only reports health against a binding it never created.
// Nothing here renders, reconciles, signs, or observes; this is the document,
// its parser, and the checks a renderer and a host both run before applying a
// generation.
//
// The name is qualified because "binding" is already taken in this repository:
// RunnableBinding and PreparedBinding bind a runnable invocation, and
// resources.InterfaceBinding binds an interface. A SolutionHostBinding binds a
// solution to a host.
//
// The shape follows the "desired host binding" record proposed in
// obin-ai/handbook#151 §5 and its lifecycle in §6. That proposal is open, not
// settled: what is implemented here is the document and its invariants, and the
// schema is versioned so a later settlement is a version step rather than a
// silent reinterpretation of v1 bytes.
//
// # Digests, and why one is required and one is not
//
// Every rendered artifact digest is required from v1: renders are already
// digest-pinned, so a document that does not carry them is hiding information
// that exists. The release digest is optional in v1 because signed releases do
// not exist yet. Requiring it now would block declared presence on the signing
// work; dropping it would lose the field that connects publication to
// deployment. It becomes required at a later schema version — see SchemaV1.
package solutionhost

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

const (
	// FileName is the conventional name of a rendered binding document.
	FileName = "solution-host-binding.codefly.yaml"

	// SchemaV1 is the only schema this package accepts. v1 requires a rendered
	// digest for every artifact and leaves Release.Digest optional. The schema
	// step that makes the release digest required is a new constant here and a
	// new accepted value, never a stricter reading of these bytes: a v1 document
	// stays a v1 document forever.
	SchemaV1 = "codefly/solution-host-binding/v1"
)

// Surface is the kind of artifact a render produced.
type Surface string

const (
	// SurfaceFrontend is a browser-served artifact.
	SurfaceFrontend Surface = "frontend"
	// SurfaceBackend is a workload artifact the host runs.
	SurfaceBackend Surface = "backend"
	// SurfaceClient is a client-surface artifact a registered client loads.
	SurfaceClient Surface = "client"
)

var surfaces = []Surface{SurfaceFrontend, SurfaceBackend, SurfaceClient}

var (
	// ErrSchema means the document does not declare a schema this package reads.
	// A consumer branches on it to say "upgrade Core" rather than "invalid
	// document": an unknown schema is a version skew, not a malformed record.
	ErrSchema = errors.New("solution host binding schema is not supported")

	// ErrInvalid means the document violates the schema's own rules.
	ErrInvalid = errors.New("solution host binding is invalid")

	// ErrMixedRelease means one generation names artifacts rendered from more
	// than one release. A partial rollout must never combine them, so this is a
	// property of a single document and is caught before any host state is read.
	ErrMixedRelease = errors.New("solution host binding mixes artifacts from two releases")
)

// binding IDs identify one deployment instance and come from delivery, which
// may mint a ULID, a UUID or a readable slug. The pattern admits all three and
// excludes whitespace, separators the route vocabulary uses, and anything that
// would need escaping in a path or a label.
var bindingPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,126}[A-Za-z0-9]$`)

// route aliases and coordinates are lowercase dotted or slashed names, so that
// a host's reserved namespace ("codefly", "codefly/admin") is a prefix of the
// aliases it reserves.
var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)

// release publishers and names are single-segment. Identity() joins them with
// "/" and "@", so a "/" inside either part would let two different releases
// render one identity string — publisher "obin" with name "crm/web", and
// publisher "obin/crm" with name "web", both give "obin/crm/web@1.0.0". Every
// artifact references its release by that string, and a consumer keying on it
// would conflate the two.
var segmentPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// SolutionHostBinding is one deployment instance of one solution on one host:
// what delivery declares should be running, at a generation the host reconciles
// towards. It is desired state and nothing else — it carries no observation, no
// health, and no credential.
type SolutionHostBinding struct {
	// Schema is the document's version. Always present, always checked first.
	Schema string `yaml:"schema" json:"schema"`

	// Binding is the stable ID of one deployment instance. A second instance of
	// the same solution gets its own binding ID and inherits nothing from this
	// one — no route, no generation history, no installation.
	Binding string `yaml:"binding" json:"binding"`

	// Generation is strictly monotonic per binding ID. It starts at 1. A host
	// rejects an older generation rather than merging it; see Host.Admit.
	Generation uint64 `yaml:"generation" json:"generation"`

	// Host is the coordinate and component instance this binding targets. A
	// host verifies the target before applying, so a document delivered to the
	// wrong place is refused rather than reconciled.
	Host HostTarget `yaml:"host" json:"host"`

	// Release is the solution release this generation deploys.
	Release Release `yaml:"release" json:"release"`

	// Routes are the aliases this binding claims. They must be unique within a
	// host, which is a property of the set rather than of this document; see
	// Host.Admit. A tombstone claims none.
	Routes []Route `yaml:"routes,omitempty" json:"routes,omitempty"`

	// Artifacts are the rendered artifacts of this generation, each pinned by
	// digest. At least one is required unless this generation is a tombstone.
	Artifacts []Artifact `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`

	// Modules are the effective module pins this render resolved.
	Modules []ModulePin `yaml:"modules,omitempty" json:"modules,omitempty"`

	// Endpoints are the endpoints the deployed instance exposes. They are
	// declarations, not addresses: the host resolves where each one lives.
	Endpoints []Endpoint `yaml:"endpoints,omitempty" json:"endpoints,omitempty"`

	// Workload is the identity the host must expect the workload to present. It
	// names an identity; it never carries the credential that proves it.
	Workload WorkloadIdentity `yaml:"workload" json:"workload"`

	// Removed marks this generation a tombstone: the binding is declared absent.
	// Removal is a generation, never the disappearance of a document, so that a
	// lost or unreadable mount can never be read as "remove everything".
	Removed bool `yaml:"removed,omitempty" json:"removed,omitempty"`
}

// HostTarget is where a binding runs: the coordinate that names the host and
// the component instance within it.
type HostTarget struct {
	Coordinate string `yaml:"coordinate" json:"coordinate"`
	Component  string `yaml:"component" json:"component"`
}

// Release identifies the solution release a generation deploys.
type Release struct {
	Publisher string `yaml:"publisher" json:"publisher"`
	Name      string `yaml:"name" json:"name"`
	Version   string `yaml:"version" json:"version"`

	// Digest is the immutable release digest. It is OPTIONAL in v1 and required
	// at a later schema version: signed releases do not exist yet, and declared
	// presence must not wait on them. When present it must be a SHA-256 digest.
	Digest string `yaml:"digest,omitempty" json:"digest,omitempty"`
}

// Identity is the release's stable name, independent of its digest. Artifacts
// reference it, which is what makes a mixed-release generation detectable
// before any release is signed. Publisher and Name are single-segment (see
// segmentPattern), so the joined string maps back to exactly one of them.
func (release Release) Identity() string {
	return release.Publisher + "/" + release.Name + "@" + release.Version
}

// Route is one alias this binding claims on its host, and the surface it fronts.
type Route struct {
	Alias   string  `yaml:"alias" json:"alias"`
	Surface Surface `yaml:"surface" json:"surface"`
}

// Artifact is one rendered artifact of this generation.
type Artifact struct {
	Surface Surface `yaml:"surface" json:"surface"`
	Name    string  `yaml:"name" json:"name"`

	// Release is the release identity this artifact was rendered from. It must
	// equal the document's own Release.Identity(): one generation is one
	// release, so a half-finished rollout cannot be declared.
	Release string `yaml:"release" json:"release"`

	// Digest pins the rendered bytes. Required from v1.
	Digest string `yaml:"digest" json:"digest"`
}

// ModulePin is one effective module pin this render resolved.
type ModulePin struct {
	Module  string `yaml:"module" json:"module"`
	Package string `yaml:"package" json:"package"`
	Version string `yaml:"version" json:"version"`
}

// Endpoint is one endpoint the deployed instance exposes. It is named, not
// addressed: a declared address would be a resolution result frozen into a
// delivery document, true on one cluster for as long as nothing moved.
type Endpoint struct {
	Name       string `yaml:"name" json:"name"`
	Service    string `yaml:"service,omitempty" json:"service,omitempty"`
	Module     string `yaml:"module,omitempty" json:"module,omitempty"`
	API        string `yaml:"api" json:"api"`
	Visibility string `yaml:"visibility,omitempty" json:"visibility,omitempty"`
}

// WorkloadIdentity is the service-account identity the host expects this
// binding's workload to present: the audience a token must be bound to and the
// subject that must present it. Both name an identity. Neither is a credential,
// and this document has no field that could carry one.
type WorkloadIdentity struct {
	Audience string `yaml:"audience" json:"audience"`
	Subject  string `yaml:"subject" json:"subject"`
}

// Parse decodes and validates one binding document. Decoding is strict: an
// unknown field is an error rather than a silently ignored intention, which is
// what makes the schema version the only way to add one. YAML is a superset of
// JSON, so a JSON-rendered document parses here too.
func Parse(data []byte) (*SolutionHostBinding, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var document SolutionHostBinding
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode solution host binding: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decode solution host binding: multiple YAML documents are not allowed")
		}
		return nil, fmt.Errorf("decode solution host binding: %w", err)
	}
	if err := document.Validate(); err != nil {
		return nil, err
	}
	return &document, nil
}

// Marshal renders a validated document. A renderer marshals through here so an
// invalid document is never written to a delivery repository.
func Marshal(document *SolutionHostBinding) ([]byte, error) {
	if err := document.Validate(); err != nil {
		return nil, err
	}
	return yaml.Marshal(document)
}

// Validate checks everything one document can be checked against on its own:
// the schema version, every identifier, the required digests, and the
// single-release invariant. Whether a generation may be applied, and whether
// its route aliases are free, are properties of a host — see Host.Admit.
func (document *SolutionHostBinding) Validate() error {
	if document == nil {
		return fmt.Errorf("%w: document is required", ErrInvalid)
	}
	if document.Schema != SchemaV1 {
		return fmt.Errorf("%w: %q (this Core reads %q)", ErrSchema, document.Schema, SchemaV1)
	}
	if !bindingPattern.MatchString(document.Binding) {
		return fmt.Errorf("%w: binding ID %q is invalid", ErrInvalid, document.Binding)
	}
	if document.Binding == reservedOwner {
		// composition.ValidateCollisions exempts the owner "base" from reserved
		// namespaces, and Host.Admit passes binding IDs as claim owners. A
		// binding called "base" would inherit that exemption.
		return fmt.Errorf("%w: binding ID %q is reserved", ErrInvalid, reservedOwner)
	}
	if document.Generation == 0 {
		return fmt.Errorf("%w: generation starts at 1", ErrInvalid)
	}
	if !namePattern.MatchString(document.Host.Coordinate) {
		return fmt.Errorf("%w: host coordinate %q is invalid", ErrInvalid, document.Host.Coordinate)
	}
	if !namePattern.MatchString(document.Host.Component) {
		return fmt.Errorf("%w: host component %q is invalid", ErrInvalid, document.Host.Component)
	}
	if err := document.Release.validate(); err != nil {
		return err
	}
	if err := document.validateArtifacts(); err != nil {
		return err
	}
	if err := document.validateRoutes(); err != nil {
		return err
	}
	if err := document.validateModules(); err != nil {
		return err
	}
	if err := document.validateEndpoints(); err != nil {
		return err
	}
	if err := document.Workload.validate(); err != nil {
		return err
	}
	if document.Removed {
		// A tombstone declares absence. Anything it still listed would be a
		// half-removal: a route the host cannot tell whether to withdraw.
		for _, declared := range []struct {
			label string
			count int
		}{
			{"routes", len(document.Routes)}, {"artifacts", len(document.Artifacts)},
			{"modules", len(document.Modules)}, {"endpoints", len(document.Endpoints)},
		} {
			if declared.count != 0 {
				return fmt.Errorf("%w: a removed generation declares no %s", ErrInvalid, declared.label)
			}
		}
	} else if len(document.Artifacts) == 0 {
		return fmt.Errorf("%w: a present generation declares at least one rendered artifact", ErrInvalid)
	}
	return nil
}

func (release Release) validate() error {
	if !segmentPattern.MatchString(release.Publisher) {
		return fmt.Errorf("%w: release publisher %q is invalid: it is one segment, never a path", ErrInvalid, release.Publisher)
	}
	if !segmentPattern.MatchString(release.Name) {
		return fmt.Errorf("%w: release name %q is invalid: it is one segment, never a path", ErrInvalid, release.Name)
	}
	if _, err := semver.StrictNewVersion(release.Version); err != nil {
		return fmt.Errorf("%w: release version %q is not semantic: %v", ErrInvalid, release.Version, err)
	}
	// Optional in v1, and checked when present: an unreadable digest is worse
	// than an absent one, because it looks like a pin and pins nothing.
	if release.Digest != "" && !digestPattern.MatchString(release.Digest) {
		return fmt.Errorf("%w: release digest %q is not a SHA-256 digest", ErrInvalid, release.Digest)
	}
	return nil
}

func (document *SolutionHostBinding) validateArtifacts() error {
	identity := document.Release.Identity()
	seen := make(map[string]struct{}, len(document.Artifacts))
	for _, artifact := range document.Artifacts {
		if !slices.Contains(surfaces, artifact.Surface) {
			return fmt.Errorf("%w: artifact surface %q is not one of %v", ErrInvalid, artifact.Surface, surfaces)
		}
		if !namePattern.MatchString(artifact.Name) {
			return fmt.Errorf("%w: artifact name %q is invalid", ErrInvalid, artifact.Name)
		}
		// Required from v1. Renders are already digest-pinned, so an artifact
		// without one is a declaration that cannot be checked against bytes.
		if !digestPattern.MatchString(artifact.Digest) {
			return fmt.Errorf("%w: %s artifact %q requires a SHA-256 rendered digest, got %q", ErrInvalid, artifact.Surface, artifact.Name, artifact.Digest)
		}
		if artifact.Release != identity {
			return fmt.Errorf("%w: %s artifact %q was rendered from %q, not %q", ErrMixedRelease, artifact.Surface, artifact.Name, artifact.Release, identity)
		}
		key := string(artifact.Surface) + "\x00" + artifact.Name
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate %s artifact %q", ErrInvalid, artifact.Surface, artifact.Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (document *SolutionHostBinding) validateRoutes() error {
	declared := make(map[Surface]struct{}, len(document.Artifacts))
	for _, artifact := range document.Artifacts {
		declared[artifact.Surface] = struct{}{}
	}
	seen := make(map[string]struct{}, len(document.Routes))
	for _, route := range document.Routes {
		if !namePattern.MatchString(route.Alias) {
			return fmt.Errorf("%w: route alias %q is invalid", ErrInvalid, route.Alias)
		}
		if !slices.Contains(surfaces, route.Surface) {
			return fmt.Errorf("%w: route %q surface %q is not one of %v", ErrInvalid, route.Alias, route.Surface, surfaces)
		}
		// A route to a surface this generation did not render points at nothing.
		if _, exists := declared[route.Surface]; !exists {
			return fmt.Errorf("%w: route %q fronts %s, which this generation renders no artifact for", ErrInvalid, route.Alias, route.Surface)
		}
		if _, exists := seen[route.Alias]; exists {
			return fmt.Errorf("%w: duplicate route alias %q", ErrInvalid, route.Alias)
		}
		seen[route.Alias] = struct{}{}
	}
	return nil
}

func (document *SolutionHostBinding) validateModules() error {
	seen := make(map[string]struct{}, len(document.Modules))
	for _, pin := range document.Modules {
		if !namePattern.MatchString(pin.Module) {
			return fmt.Errorf("%w: module %q is invalid", ErrInvalid, pin.Module)
		}
		if !namePattern.MatchString(pin.Package) {
			return fmt.Errorf("%w: module %q package %q is invalid", ErrInvalid, pin.Module, pin.Package)
		}
		// An effective pin is one resolved version, never a constraint: a
		// constraint here would let two hosts reading the same generation run
		// different code.
		if _, err := semver.StrictNewVersion(pin.Version); err != nil {
			return fmt.Errorf("%w: module %q version %q is not an exact semantic version: %v", ErrInvalid, pin.Module, pin.Version, err)
		}
		if _, exists := seen[pin.Module]; exists {
			return fmt.Errorf("%w: duplicate module pin %q", ErrInvalid, pin.Module)
		}
		seen[pin.Module] = struct{}{}
	}
	return nil
}

func (document *SolutionHostBinding) validateEndpoints() error {
	seen := make(map[string]struct{}, len(document.Endpoints))
	for _, endpoint := range document.Endpoints {
		if !namePattern.MatchString(endpoint.Name) {
			return fmt.Errorf("%w: endpoint name %q is invalid", ErrInvalid, endpoint.Name)
		}
		for _, part := range []struct{ label, value string }{{"module", endpoint.Module}, {"service", endpoint.Service}} {
			if part.value != "" && !namePattern.MatchString(part.value) {
				return fmt.Errorf("%w: endpoint %q %s %q is invalid", ErrInvalid, endpoint.Name, part.label, part.value)
			}
		}
		if !namePattern.MatchString(endpoint.API) {
			return fmt.Errorf("%w: endpoint %q api %q is invalid", ErrInvalid, endpoint.Name, endpoint.API)
		}
		if endpoint.Visibility != "" && !namePattern.MatchString(endpoint.Visibility) {
			return fmt.Errorf("%w: endpoint %q visibility %q is invalid", ErrInvalid, endpoint.Name, endpoint.Visibility)
		}
		key := endpoint.Module + "\x00" + endpoint.Service + "\x00" + endpoint.Name
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate endpoint %q", ErrInvalid, endpoint.Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (identity WorkloadIdentity) validate() error {
	for _, part := range []struct{ label, value string }{{"audience", identity.Audience}, {"subject", identity.Subject}} {
		// A single-line, whitespace-free, printable token. That is the shape of
		// a name; it is not the shape of a PEM block, a wrapped token, or a
		// pasted credential, so the check also holds the "names identities,
		// never credentials" invariant against the obvious accident.
		if part.value == "" || strings.ContainsFunc(part.value, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
			return fmt.Errorf("%w: workload %s must be a single-line identity, got %q", ErrInvalid, part.label, part.value)
		}
	}
	return nil
}

// CanonicalBytes returns a deterministic JSON encoding of a validated document:
// object keys in name order at every depth, collections in a defined order, and
// every number kept as its exact literal.
//
// Sorting by key name is what keeps the encoding independent of this struct's
// Go declaration order. Without it the digest below is a function of how the
// fields happen to be written, so moving two of them for readability changes
// every digest ever computed — and a host comparing its stored digest against a
// freshly computed one would read its own Core upgrade as a rewritten
// generation and stop reconciling. The digest must move only when the document
// does.
func (document *SolutionHostBinding) CanonicalBytes() ([]byte, error) {
	if err := document.Validate(); err != nil {
		return nil, err
	}
	normalized := *document
	normalized.Routes = slices.Clone(document.Routes)
	sort.Slice(normalized.Routes, func(i, j int) bool { return normalized.Routes[i].Alias < normalized.Routes[j].Alias })
	normalized.Artifacts = slices.Clone(document.Artifacts)
	sort.Slice(normalized.Artifacts, func(i, j int) bool {
		if normalized.Artifacts[i].Surface != normalized.Artifacts[j].Surface {
			return normalized.Artifacts[i].Surface < normalized.Artifacts[j].Surface
		}
		return normalized.Artifacts[i].Name < normalized.Artifacts[j].Name
	})
	normalized.Modules = slices.Clone(document.Modules)
	sort.Slice(normalized.Modules, func(i, j int) bool { return normalized.Modules[i].Module < normalized.Modules[j].Module })
	normalized.Endpoints = slices.Clone(document.Endpoints)
	sort.Slice(normalized.Endpoints, func(i, j int) bool {
		left, right := normalized.Endpoints[i], normalized.Endpoints[j]
		if left.Module != right.Module {
			return left.Module < right.Module
		}
		if left.Service != right.Service {
			return left.Service < right.Service
		}
		return left.Name < right.Name
	})
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	// Round-trip through a generic value: encoding/json emits map keys in name
	// order, so re-encoding drops the struct's declaration order. UseNumber
	// keeps each number as the literal it was written as, rather than a float64
	// that would round a large generation into a neighbour's.
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return nil, err
	}
	return json.Marshal(generic)
}

// Digest is the SHA-256 of the canonical encoding. A host records it alongside
// the applied generation; see Applied. Two digests are comparable only when
// both were produced by the same canonical encoding — a Core release that
// changes the encoding must treat stored digests as stale rather than as
// evidence a generation was rewritten. The encoding is pinned by test against
// the shipped fixtures, so changing it cannot happen by accident.
func (document *SolutionHostBinding) Digest() (string, error) {
	canonical, err := document.CanonicalBytes()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Aliases returns the route aliases this generation claims, sorted. A tombstone
// claims none.
func (document *SolutionHostBinding) Aliases() []string {
	if document == nil {
		return nil
	}
	aliases := make([]string, 0, len(document.Routes))
	for _, route := range document.Routes {
		aliases = append(aliases, route.Alias)
	}
	sort.Strings(aliases)
	return aliases
}
