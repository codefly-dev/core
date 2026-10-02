// Package solutionhost defines the static, deploy-time half of the solution
// lifecycle: the documents that say what is present on a host and what
// authority a principal holds there, the signature envelope both travel in,
// and the rules that bind the two together to one approved build.
//
// A solution or a module is **present** on a host because delivery declared
// it, and holds **authority** because a reviewed, signed document granted it
// to one exact build — never because its process announced itself, and never
// for a build other than the one approved. Two document types carry that:
//
//   - SolutionHostBinding is the presence document. It declares one deployment
//     instance of one solution or one module on one host, at a generation the
//     host reconciles towards, naming the workloads it runs, the exact build
//     each must be running, and the workload identity each must present.
//   - AuthorityDocument is the authority document. It declares which bindings
//     a principal holds, within a ceiling the caller supplies, effective for
//     one approved build and from one presence generation onwards.
//
// Neither half activates alone: see Activate, which requires a matched
// (authority, presence, build) tuple.
//
// Nothing here renders, reconciles, signs, mints, or observes. This is the
// documents, their parsers, the checks a renderer and a host both run before
// applying a generation, and the verification half of the signature envelope.
// Signing authority belongs to the reviewed delivery pipeline: a signer in this
// package would put one in every binary that imports core.
//
// The name is qualified because "binding" is already taken in this repository:
// RunnableBinding and PreparedBinding bind a runnable invocation, and
// resources.InterfaceBinding binds an interface. A SolutionHostBinding binds a
// solution or a module to a host; an AuthorityBinding is one unit of authority
// a principal holds.
//
// # One schema, and why there is no v1 to fall back to
//
// Each document declares exactly one schema this package reads. The presence
// schema moved from v1 to v2 to carry the kind, the three digests, the
// workloads, the ownership domain and the envelope revision, and v1 is not
// accepted: a document that omits the build a workload must be running, or the
// identity it must present, is a document a host cannot verify anything
// against. Reading it as a weaker v1 would make the weaker shape permanently
// available to anything that can write a delivery document, which is the
// opposite of what these fields are for. A v1 document therefore fails with
// ErrSchema — a loud version skew, in the one place that can fix it.
//
// # Three digests, and why they are three types
//
// A generation pins three different things, and all three are SHA-256 strings:
//
//   - Release.Digest (ReleaseDigest) pins the immutable release. A host holds
//     it against the authority document's approved release.
//   - Artifact.Digest (RenderedDigest) pins the rendered bytes of one unit in
//     the delivery repository. Nothing compares it at runtime; it records what
//     delivery wrote.
//   - Workload.Image.Digest (ImageDigest) pins an OCI image manifest. A host
//     holds it against the resolved image of a running container.
//
// They are distinct named types so that a mix-up is a compile error rather
// than a silent one: every one of them matches the same digest pattern, so
// swapping two type-checks, validates, and surfaces much later as a refusal
// with no readable cause. No rule in this package compares one digest to
// another; the relationship between a workload and its artifact is declared by
// NAME and validated as a name reference. Validate additionally refuses a
// document in which two digests of different kinds are the same string, which
// cannot happen by chance and is the mix-up made loud.
package solutionhost

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

const (
	// FileName is the conventional name of a rendered presence document.
	FileName = "solution-host-binding.codefly.yaml"

	// SchemaPresenceV2 is the only presence schema this package accepts. It
	// requires the kind, the ownership domain, the envelope revision, the
	// release digest, and — for a generation that renders anything the host
	// runs — the workloads, their image digests and their workload identities.
	//
	// The schema string is also what binds a signature to a document TYPE:
	// it is part of the canonical encoding that is signed, so a presence
	// document cannot be presented as an authority document under a signature
	// that verifies. See VerifyPresence.
	SchemaPresenceV2 = "codefly/solution-host-binding/v2"
)

// Kind is what a presence document declares the presence of. A module and a
// solution are delivered the same way and reconciled the same way; they differ
// in what may install them, so a host must never have to infer it from the
// shape of the document.
type Kind string

const (
	// KindSolution is a composed solution instance.
	KindSolution Kind = "solution"
	// KindModule is one module instance.
	KindModule Kind = "module"
)

var kinds = []Kind{KindModule, KindSolution}

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

// ReleaseDigest pins an immutable release. It is never compared against a
// RenderedDigest or an ImageDigest; see the package comment.
type ReleaseDigest string

// RenderedDigest pins the rendered bytes of one artifact in the delivery
// repository. It is never compared against an image digest: rendered bytes and
// an OCI image manifest are different objects that hash the same way.
type RenderedDigest string

// ImageDigest pins an OCI image manifest — the approved build. It is what a
// host holds against the resolved image of a running container, and what an
// authority document declares itself effective for.
type ImageDigest string

var (
	// ErrSchema means a document does not declare a schema this package reads.
	// A consumer branches on it to say "upgrade Core" rather than "invalid
	// document": an unknown schema is a version skew, not a malformed record.
	// A v1 presence document reaches a consumer as this error.
	ErrSchema = errors.New("solution host document schema is not supported")

	// ErrInvalid means a document violates its schema's own rules.
	ErrInvalid = errors.New("solution host document is invalid")

	// ErrMixedRelease means one generation names artifacts rendered from more
	// than one release. A partial rollout must never combine them, so this is a
	// property of a single document and is caught before any host state is read.
	ErrMixedRelease = errors.New("solution host binding mixes artifacts from two releases")

	// ErrDigestConfusion means two digests of different kinds are the same
	// string in one document. Rendered bytes, an OCI image manifest and a
	// release do not hash alike by accident, so this is the silent mix-up the
	// three digest types exist to catch, caught once more in the data.
	ErrDigestConfusion = errors.New("solution host binding uses one digest for two different objects")
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
// render one identity string — publisher "a" with name "b/c", and publisher
// "a/b" with name "c", both give "a/b/c@1.0.0". Every artifact references its
// release by that string, and a consumer keying on it would conflate the two.
var segmentPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// SolutionHostBinding is one deployment instance of one solution or one module
// on one host: what delivery declares should be running, at a generation the
// host reconciles towards. It is desired state and nothing else — it carries no
// observation, no health, and no credential.
type SolutionHostBinding struct {
	// Schema is the document's version. Always present, always checked first,
	// and part of the signed canonical encoding so a signature is bound to the
	// document type as well as its contents.
	Schema string `yaml:"schema" json:"schema"`

	// Kind is whether this declares a solution instance or a module instance.
	// Required: a host must not infer it, and the two differ in what may
	// install them.
	Kind Kind `yaml:"kind" json:"kind"`

	// Binding is the stable ID of one deployment instance. A second instance of
	// the same solution gets its own binding ID and inherits nothing from this
	// one — no route, no generation history, no installation.
	Binding string `yaml:"binding" json:"binding"`

	// Generation is strictly monotonic per binding ID. It starts at 1. A host
	// rejects an older generation rather than merging it; see Host.Admit.
	Generation uint64 `yaml:"generation" json:"generation"`

	// OwnershipDomain is the slice of the host's binding space this document
	// speaks for. A delivery may add, change and REMOVE within its own domain,
	// and may say nothing at all about any other. It is what lets a
	// module-scoped delivery express removal without "remove everything else"
	// being expressible at all.
	//
	// The string alone is not the enforcement. Host.Admit refuses a delivered
	// set that straddles two domains, and refuses a document that reaches a
	// binding an earlier generation applied under a different domain; and the
	// domain is inside the signed canonical encoding, so a carrier that relays
	// the document cannot widen it. Required.
	OwnershipDomain string `yaml:"ownership_domain" json:"ownership_domain"`

	// EnvelopeRevision is the revision of the ceiling this document was
	// validated against. Required and at least 1: a document that names no
	// revision cannot be told apart from one built against a narrower envelope,
	// and zero would be exactly that document with a number in the field.
	EnvelopeRevision uint64 `yaml:"envelope_revision" json:"envelope_revision"`

	// Host is the coordinate and component instance this binding targets. A
	// host verifies the target before applying, so a document delivered to the
	// wrong place is refused rather than reconciled.
	Host HostTarget `yaml:"host" json:"host"`

	// Release is the release this generation deploys.
	Release Release `yaml:"release" json:"release"`

	// Routes are the aliases this binding claims. They must be unique within a
	// host, which is a property of the set rather than of this document; see
	// Host.Admit. A tombstone claims none.
	Routes []Route `yaml:"routes,omitempty" json:"routes,omitempty"`

	// Artifacts are the rendered artifacts of this generation, each pinned by
	// its rendered digest. At least one is required unless this generation is a
	// tombstone.
	Artifacts []Artifact `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`

	// Workloads are the things the host runs from this generation, and what it
	// must find true of each before it issues anything.
	//
	// A present generation declares workloads if and only if it renders a
	// backend artifact: a generation that renders something to run and declares
	// no workload would leave the host with no build and no identity to check,
	// and a workload declared by a generation that renders nothing to run it
	// points at nothing. A tombstone declares none.
	Workloads []Workload `yaml:"workloads,omitempty" json:"workloads,omitempty"`

	// Modules are the effective module pins this render resolved.
	Modules []ModulePin `yaml:"modules,omitempty" json:"modules,omitempty"`

	// Endpoints are the endpoints the deployed instance exposes. They are
	// declarations, not addresses: the host resolves where each one lives.
	Endpoints []Endpoint `yaml:"endpoints,omitempty" json:"endpoints,omitempty"`

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

// Release identifies the release a generation deploys.
type Release struct {
	Publisher string `yaml:"publisher" json:"publisher"`
	Name      string `yaml:"name" json:"name"`
	Version   string `yaml:"version" json:"version"`

	// Digest is the immutable release digest, required. It is what a host holds
	// against the release an authority document approved, so a generation that
	// omits it is a generation no authority can be matched to.
	Digest ReleaseDigest `yaml:"digest" json:"digest"`
}

// Identity is the release's stable name, independent of its digest. Artifacts
// reference it, which is what makes a mixed-release generation detectable.
// Publisher and Name are single-segment (see segmentPattern), so the joined
// string maps back to exactly one of them.
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

	// Digest pins the rendered bytes. Required.
	Digest RenderedDigest `yaml:"digest" json:"digest"`
}

// Workload is one thing the host runs from this generation: which rendered
// artifact produces it, which single container authenticates, the exact build
// that container must be running, and the identity it must present.
//
// The image digest and the expected identity are per workload and never on
// Artifact: one rendered artifact is a kustomize output that can declare
// several containers, so an image digest on the artifact would be ambiguous
// exactly when it matters.
type Workload struct {
	// Name is the workload's name — the Deployment or StatefulSet the host runs.
	Name string `yaml:"name" json:"name"`

	// Artifact is the Artifact.Name that renders this workload. It is a NAME
	// reference, validated against the declared artifacts, and it must name a
	// backend artifact: that is the verified relationship between the rendered
	// bytes and the image below, and it is deliberately not a digest compared
	// against another digest.
	Artifact string `yaml:"artifact" json:"artifact"`

	// Container is the one application container that authenticates. One, not
	// a list: a host issuing a credential has to know which container's
	// identity it is answering for, and "whichever one presented it" is how a
	// sidecar ends up holding a workload's authority.
	Container string `yaml:"container" json:"container"`

	// Image is what Container must be running.
	Image Image `yaml:"image" json:"image"`

	// Identity is the workload identity the host must expect this container to
	// present. It names an identity; it never carries the credential proving it.
	Identity WorkloadIdentity `yaml:"identity" json:"identity"`

	// NonAuthenticating are the init and sidecar containers that must never be
	// accepted as the authenticating container. Named explicitly, and empty is
	// a declaration rather than an absence: a host that cannot tell "there are
	// none" from "nobody said" has to choose between refusing every workload
	// and trusting any container that asks.
	NonAuthenticating []string `yaml:"non_authenticating" json:"non_authenticating"`
}

// Image is the approved build of one container: the repository it comes from
// and the OCI image manifest digest it must resolve to.
type Image struct {
	// Repository is the image name only — no tag and no digest. A tag is
	// mutable and a digest already has its own field, so a repository carrying
	// either would give one container two answers about what it runs.
	Repository string `yaml:"repository" json:"repository"`

	// Digest is the OCI image manifest digest: the approved build.
	Digest ImageDigest `yaml:"digest" json:"digest"`
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

// WorkloadIdentity is the identity the host expects a workload's authenticating
// container to present: the audience a token must be bound to, the subject that
// must present it, and the SPIFFE ID of the X.509-SVID the destination must
// show. All three name an identity. None is a credential, and this document has
// no field that could carry one.
type WorkloadIdentity struct {
	Audience string `yaml:"audience" json:"audience"`
	Subject  string `yaml:"subject" json:"subject"`

	// SPIFFEID is the SPIFFE ID the workload must present, required. A subject
	// string is what a host records; an SVID is what it can verify on the
	// connection, so the document names the thing that is checkable rather than
	// leaving a host to guess which subject corresponds to which SVID.
	SPIFFEID string `yaml:"spiffe_id" json:"spiffe_id"`
}

// Parse decodes and validates one presence document. Decoding is strict: an
// unknown field is an error rather than a silently ignored intention, which is
// what makes the schema version the only way to add one — and what makes a
// document that tries to nominate its own verification key fail here rather
// than be quietly dropped. YAML is a superset of JSON, so a JSON-rendered
// document parses here too.
func Parse(data []byte) (*SolutionHostBinding, error) {
	document, err := decodeStrict[SolutionHostBinding](data, "solution host binding", SchemaPresenceV2)
	if err != nil {
		return nil, err
	}
	if err := document.Validate(); err != nil {
		return nil, err
	}
	return document, nil
}

// decodeStrict decodes exactly one YAML document into T, refusing unknown
// fields and trailing documents. Both document types decode through it so
// neither can grow a laxer reading of the same bytes than the other.
//
// The schema is read and checked FIRST, leniently, before the strict decode.
// Order matters here and is not a detail: a document of an older schema has
// fields this one does not, so a strict decode would refuse it for an unknown
// field and the caller would be told its document is malformed. It is not
// malformed — it is older than the reader, and the only useful answer is
// ErrSchema, which says so and says what this Core reads.
func decodeStrict[T any](data []byte, label, schema string) (*T, error) {
	var header struct {
		Schema string `yaml:"schema"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("decode %s: %w", label, err)
	}
	if header.Schema != schema {
		return nil, fmt.Errorf("%w: %q (this Core reads %q)", ErrSchema, header.Schema, schema)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var value T
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode %s: %w", label, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decode %s: multiple YAML documents are not allowed", label)
		}
		return nil, fmt.Errorf("decode %s: %w", label, err)
	}
	return &value, nil
}

// Marshal renders a validated presence document. A renderer marshals through
// here so an invalid document is never written to a delivery repository.
func Marshal(document *SolutionHostBinding) ([]byte, error) {
	if err := document.Validate(); err != nil {
		return nil, err
	}
	return yaml.Marshal(document)
}

// Validate checks everything one presence document can be checked against on
// its own: the schema version, the kind, every identifier, the required
// digests, the workload-to-artifact relationships, and the single-release
// invariant. Whether a generation may be applied, whether its route aliases
// are free, and whether it reaches outside its ownership domain are properties
// of a host — see Host.Admit.
func (document *SolutionHostBinding) Validate() error {
	if document == nil {
		return fmt.Errorf("%w: document is required", ErrInvalid)
	}
	if document.Schema != SchemaPresenceV2 {
		return fmt.Errorf("%w: %q (this Core reads %q)", ErrSchema, document.Schema, SchemaPresenceV2)
	}
	if !slices.Contains(kinds, document.Kind) {
		return fmt.Errorf("%w: kind %q is not one of %v", ErrInvalid, document.Kind, kinds)
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
	if !namePattern.MatchString(document.OwnershipDomain) {
		return fmt.Errorf("%w: ownership domain %q is invalid", ErrInvalid, document.OwnershipDomain)
	}
	if document.EnvelopeRevision == 0 {
		return fmt.Errorf("%w: envelope revision starts at 1; 0 names no ceiling", ErrInvalid)
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
	if document.Removed {
		// A tombstone declares absence. Anything it still listed would be a
		// half-removal: a route the host cannot tell whether to withdraw, or a
		// build it cannot tell whether to keep admitting.
		//
		// Checked before the collections rather than after, so the refusal
		// names the half-removal. Validating the collections first would
		// refuse a tombstone that still carries a workload for the workload's
		// dangling artifact reference, which is true and is not the point.
		for _, declared := range []struct {
			label string
			count int
		}{
			{"routes", len(document.Routes)}, {"artifacts", len(document.Artifacts)},
			{"workloads", len(document.Workloads)},
			{"modules", len(document.Modules)}, {"endpoints", len(document.Endpoints)},
		} {
			if declared.count != 0 {
				return fmt.Errorf("%w: a removed generation declares no %s", ErrInvalid, declared.label)
			}
		}
		return nil
	}
	if err := document.validateArtifacts(); err != nil {
		return err
	}
	if err := document.validateRoutes(); err != nil {
		return err
	}
	if err := document.validateWorkloads(); err != nil {
		return err
	}
	if err := document.validateModules(); err != nil {
		return err
	}
	if err := document.validateEndpoints(); err != nil {
		return err
	}
	if err := document.validateDigestKinds(); err != nil {
		return err
	}
	if len(document.Artifacts) == 0 {
		return fmt.Errorf("%w: a present generation declares at least one rendered artifact", ErrInvalid)
	}
	// Workloads if and only if there is something to run. One direction stops a
	// generation that renders a backend from leaving the host with no build and
	// no identity to hold a container to; the other stops a workload being
	// declared for bytes this generation never rendered.
	runnable := slices.ContainsFunc(document.Artifacts, func(artifact Artifact) bool {
		return artifact.Surface == SurfaceBackend
	})
	switch {
	case runnable && len(document.Workloads) == 0:
		return fmt.Errorf("%w: this generation renders a %s artifact, so it declares the workloads that run it, their approved build and their identity",
			ErrInvalid, SurfaceBackend)
	case !runnable && len(document.Workloads) != 0:
		return fmt.Errorf("%w: this generation renders no %s artifact, so it runs no workload", ErrInvalid, SurfaceBackend)
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
	// Required: the release digest is what an authority document's approved
	// release is held against, so a generation without one can be matched to no
	// authority at all.
	if !digestPattern.MatchString(string(release.Digest)) {
		return fmt.Errorf("%w: release %q requires a SHA-256 release digest, got %q", ErrInvalid, release.Identity(), release.Digest)
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
		// Renders are already digest-pinned, so an artifact without one is a
		// declaration that cannot be checked against bytes.
		if !digestPattern.MatchString(string(artifact.Digest)) {
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

// validateWorkloads checks each workload and its relationship to the artifacts
// this generation declares. The relationship is by NAME: the artifact that
// renders a workload is named, and the name is resolved here. Nothing compares
// the rendered digest against the image digest, because those two strings
// describe different objects.
func (document *SolutionHostBinding) validateWorkloads() error {
	rendered := make(map[string]Surface, len(document.Artifacts))
	for _, artifact := range document.Artifacts {
		rendered[artifact.Name] = artifact.Surface
	}
	seen := make(map[string]struct{}, len(document.Workloads))
	for _, workload := range document.Workloads {
		if !namePattern.MatchString(workload.Name) {
			return fmt.Errorf("%w: workload name %q is invalid", ErrInvalid, workload.Name)
		}
		if _, exists := seen[workload.Name]; exists {
			return fmt.Errorf("%w: duplicate workload %q", ErrInvalid, workload.Name)
		}
		seen[workload.Name] = struct{}{}
		surface, declared := rendered[workload.Artifact]
		if !declared {
			return fmt.Errorf("%w: workload %q is rendered by artifact %q, which this generation does not declare", ErrInvalid, workload.Name, workload.Artifact)
		}
		if surface != SurfaceBackend {
			return fmt.Errorf("%w: workload %q names %s artifact %q; a workload the host runs is rendered by a %s artifact",
				ErrInvalid, workload.Name, surface, workload.Artifact, SurfaceBackend)
		}
		if !namePattern.MatchString(workload.Container) {
			return fmt.Errorf("%w: workload %q authenticating container %q is invalid", ErrInvalid, workload.Name, workload.Container)
		}
		containers := make(map[string]struct{}, len(workload.NonAuthenticating))
		for _, container := range workload.NonAuthenticating {
			if !namePattern.MatchString(container) {
				return fmt.Errorf("%w: workload %q non-authenticating container %q is invalid", ErrInvalid, workload.Name, container)
			}
			if _, exists := containers[container]; exists {
				return fmt.Errorf("%w: workload %q lists non-authenticating container %q twice", ErrInvalid, workload.Name, container)
			}
			containers[container] = struct{}{}
		}
		// The one container that authenticates cannot also be one that must
		// never be accepted. A document asserting both says nothing a host can
		// act on, and the safe reading is not obvious enough to pick one.
		if _, excluded := containers[workload.Container]; excluded {
			return fmt.Errorf("%w: workload %q names %q as both its authenticating container and one that must never authenticate",
				ErrInvalid, workload.Name, workload.Container)
		}
		if err := workload.Image.validate(workload.Name); err != nil {
			return err
		}
		if err := workload.Identity.validate(workload.Name); err != nil {
			return err
		}
	}
	return nil
}

func (image Image) validate(workload string) error {
	if !namePattern.MatchString(image.Repository) {
		return fmt.Errorf("%w: workload %q image repository %q is invalid", ErrInvalid, workload, image.Repository)
	}
	// A repository carrying its own tag or digest would give one container two
	// answers about what it runs, and the mutable one would usually win.
	if strings.ContainsAny(image.Repository, ":@") {
		return fmt.Errorf("%w: workload %q image repository %q carries a tag or digest; the approved build is the digest field", ErrInvalid, workload, image.Repository)
	}
	if !digestPattern.MatchString(string(image.Digest)) {
		return fmt.Errorf("%w: workload %q requires a SHA-256 OCI image manifest digest, got %q", ErrInvalid, workload, image.Digest)
	}
	return nil
}

func (identity WorkloadIdentity) validate(workload string) error {
	for _, part := range []struct{ label, value string }{{"audience", identity.Audience}, {"subject", identity.Subject}} {
		// A single-line, whitespace-free, printable token. That is the shape of
		// a name; it is not the shape of a PEM block, a wrapped token, or a
		// pasted credential, so the check also holds the "names identities,
		// never credentials" invariant against the obvious accident.
		if part.value == "" || strings.ContainsFunc(part.value, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
			return fmt.Errorf("%w: workload %q %s must be a single-line identity, got %q", ErrInvalid, workload, part.label, part.value)
		}
	}
	if err := validateSPIFFEID(identity.SPIFFEID); err != nil {
		return fmt.Errorf("%w: workload %q %v", ErrInvalid, workload, err)
	}
	return nil
}

// validateSPIFFEID checks a SPIFFE ID as a SPIFFE ID rather than as a generic
// name. A subject that happens to parse as a URL is not an SVID, and a host
// that accepted one would verify a connection against something no workload
// can present.
func validateSPIFFEID(value string) error {
	if value == "" {
		return errors.New("requires a SPIFFE ID for the SVID it must present")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("SPIFFE ID %q does not parse: %v", value, err)
	}
	switch {
	case parsed.Scheme != "spiffe":
		return fmt.Errorf("SPIFFE ID %q has scheme %q, not \"spiffe\"", value, parsed.Scheme)
	case parsed.User != nil:
		return fmt.Errorf("SPIFFE ID %q carries user info", value)
	case parsed.RawQuery != "" || parsed.ForceQuery:
		return fmt.Errorf("SPIFFE ID %q carries a query", value)
	case parsed.Fragment != "":
		return fmt.Errorf("SPIFFE ID %q carries a fragment", value)
	case parsed.Port() != "":
		return fmt.Errorf("SPIFFE ID %q carries a port", value)
	}
	domain := parsed.Hostname()
	if domain == "" {
		return fmt.Errorf("SPIFFE ID %q names no trust domain", value)
	}
	if domain != strings.ToLower(domain) {
		return fmt.Errorf("SPIFFE ID %q trust domain is not lowercase", value)
	}
	// A trust domain alone identifies a trust domain, not a workload within it.
	// Accepting one would let every workload in a domain present the same ID.
	path := strings.TrimPrefix(parsed.Path, "/")
	if path == "" {
		return fmt.Errorf("SPIFFE ID %q names a trust domain and no workload path", value)
	}
	for _, segment := range strings.Split(path, "/") {
		// An empty or relative segment means two different strings normalize to
		// one ID, so a comparison against a presented SVID would depend on who
		// normalized it.
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("SPIFFE ID %q path is not normalized", value)
		}
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

// validateDigestKinds refuses a document that uses one digest string for two
// objects of different kinds. The three digest types already make the mix-up a
// compile error for code built against this package; this catches it in the
// data, which is where a renderer assembling YAML by hand would introduce it.
//
// Two digests of different kinds being equal is not a coincidence that happens:
// it would need a SHA-256 collision between rendered bytes, an OCI manifest and
// a release. So equality is evidence of a confusion, and the loud refusal is
// strictly better than the mint failure it would otherwise become.
func (document *SolutionHostBinding) validateDigestKinds() error {
	kindOf := make(map[string]string)
	note := func(kind, value, where string) error {
		if value == "" {
			return nil
		}
		if previous, exists := kindOf[value]; exists && previous != kind {
			return fmt.Errorf("%w: %s and %s are the same digest %s", ErrDigestConfusion, previous, where, value)
		}
		kindOf[value] = kind
		return nil
	}
	if err := note("release", string(document.Release.Digest), "the release digest"); err != nil {
		return err
	}
	for _, artifact := range document.Artifacts {
		if err := note("rendered", string(artifact.Digest), fmt.Sprintf("the rendered digest of %s artifact %q", artifact.Surface, artifact.Name)); err != nil {
			return err
		}
	}
	for _, workload := range document.Workloads {
		if err := note("image", string(workload.Image.Digest), fmt.Sprintf("the image digest of workload %q", workload.Name)); err != nil {
			return err
		}
	}
	return nil
}

// CanonicalBytes returns a deterministic JSON encoding of a validated document:
// object keys in name order at every depth, collections in a defined order, and
// every number kept as its exact literal. It is both the input to Digest and
// the payload a signature covers — see VerifyPresence.
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
	normalized.Workloads = make([]Workload, 0, len(document.Workloads))
	for _, workload := range document.Workloads {
		// The exclusion list is a set, so its delivered order must not reach the
		// digest: a renderer emitting it from a Go map would otherwise produce a
		// different digest per process and the host would read a rewritten
		// generation.
		workload.NonAuthenticating = slices.Sorted(slices.Values(workload.NonAuthenticating))
		normalized.Workloads = append(normalized.Workloads, workload)
	}
	sort.Slice(normalized.Workloads, func(i, j int) bool { return normalized.Workloads[i].Name < normalized.Workloads[j].Name })
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
	return canonicalJSON(normalized)
}

// canonicalJSON encodes a value with object keys in name order at every depth
// and every number kept as its exact literal.
func canonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
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
	return digestOf(canonical), nil
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
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

// Builds returns the approved image digests this generation declares, sorted
// and deduplicated. It is what an authority document's approved build is
// matched against; see Activate.
func (document *SolutionHostBinding) Builds() []ImageDigest {
	if document == nil {
		return nil
	}
	builds := make([]ImageDigest, 0, len(document.Workloads))
	for _, workload := range document.Workloads {
		if !slices.Contains(builds, workload.Image.Digest) {
			builds = append(builds, workload.Image.Digest)
		}
	}
	slices.Sort(builds)
	return builds
}
