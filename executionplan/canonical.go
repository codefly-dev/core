package executionplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Canonical returns a deep copy of the plan with every unordered set sorted.
// The sequences a plan orders semantically — schema steps and selection paths —
// keep their order. The receiver is not modified.
func (plan *Plan) Canonical() *Plan {
	out := *plan
	out.Nodes = canonicalNodes(plan.Nodes)
	out.Edges = canonicalEdges(plan.Edges)
	out.Configurations = canonicalConfigurations(plan.Configurations)
	out.SchemaSteps = canonicalSchemaSteps(plan.SchemaSteps)
	if plan.Invocation != nil {
		invocation := *plan.Invocation
		out.Invocation = &invocation
	}
	return &out
}

func canonicalNodes(nodes []Node) []Node {
	if len(nodes) == 0 {
		return nil
	}
	out := slices.Clone(nodes)
	for i := range out {
		if out[i].Backend != nil {
			backend := *out[i].Backend
			out[i].Backend = &backend
		}
		out[i].Artifacts = canonicalArtifacts(out[i].Artifacts)
		out[i].Endpoints = canonicalEndpoints(out[i].Endpoints)
		out[i].Selection = canonicalSelection(out[i].Selection)
	}
	slices.SortFunc(out, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func canonicalArtifacts(artifacts []Artifact) []Artifact {
	if len(artifacts) == 0 {
		return nil
	}
	out := slices.Clone(artifacts)
	for i := range out {
		out[i].Selection = canonicalSelection(out[i].Selection)
	}
	// Every distinguishing field participates: slices.SortFunc is not stable, so
	// elements that tie on a partial key would keep their input order and the
	// "canonical" form would depend on the order the producer emitted them.
	slices.SortFunc(out, func(a, b Artifact) int {
		return compareAll(
			strings.Compare(a.Reference, b.Reference),
			strings.Compare(a.Version, b.Version),
			strings.Compare(a.Digest, b.Digest),
			strings.Compare(string(a.Verification), string(b.Verification)),
			compareSelection(a.Selection, b.Selection),
		)
	})
	return out
}

func canonicalEndpoints(endpoints []EndpointRequirement) []EndpointRequirement {
	if len(endpoints) == 0 {
		return nil
	}
	out := slices.Clone(endpoints)
	for i := range out {
		out[i].RequiredBy = sortedStrings(out[i].RequiredBy)
	}
	slices.SortFunc(out, func(a, b EndpointRequirement) int {
		return compareAll(
			strings.Compare(a.Name, b.Name),
			strings.Compare(a.API, b.API),
			strings.Compare(a.Visibility, b.Visibility),
			slices.Compare(a.RequiredBy, b.RequiredBy),
		)
	})
	return out
}

func canonicalEdges(edges []Edge) []Edge {
	if len(edges) == 0 {
		return nil
	}
	out := slices.Clone(edges)
	for i := range out {
		out[i].Endpoints = sortedStrings(out[i].Endpoints)
		out[i].Selection = canonicalSelection(out[i].Selection)
	}
	slices.SortFunc(out, func(a, b Edge) int {
		return compareAll(
			strings.Compare(string(a.Kind), string(b.Kind)),
			strings.Compare(a.From, b.From),
			strings.Compare(a.To, b.To),
		)
	})
	return out
}

func canonicalConfigurations(origins []ConfigurationOrigin) []ConfigurationOrigin {
	if len(origins) == 0 {
		return nil
	}
	out := slices.Clone(origins)
	for i := range out {
		out[i].Selection = canonicalSelection(out[i].Selection)
	}
	slices.SortFunc(out, func(a, b ConfigurationOrigin) int {
		return compareAll(
			strings.Compare(a.Consumer, b.Consumer),
			strings.Compare(a.Key, b.Key),
			strings.Compare(string(a.Origin), string(b.Origin)),
		)
	})
	return out
}

func canonicalSchemaSteps(steps []SchemaStep) []SchemaStep {
	if len(steps) == 0 {
		return nil
	}
	out := slices.Clone(steps)
	for i := range out {
		if out[i].Backend != nil {
			backend := *out[i].Backend
			out[i].Backend = &backend
		}
		out[i].After = sortedStrings(out[i].After)
		out[i].Before = sortedStrings(out[i].Before)
		out[i].Selection = canonicalSelection(out[i].Selection)
	}
	return out
}

func canonicalSelection(selection Selection) Selection {
	selection.Via = slices.Clone(selection.Via)
	selection.Over = sortedStrings(selection.Over)
	return selection
}

func sortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := slices.Clone(values)
	slices.Sort(out)
	return out
}

func compareSelection(a Selection, b Selection) int {
	return compareAll(
		strings.Compare(string(a.Reason), string(b.Reason)),
		slices.Compare(a.Via, b.Via),
		slices.Compare(a.Over, b.Over),
		strings.Compare(a.Detail, b.Detail),
	)
}

func compareAll(comparisons ...int) int {
	for _, comparison := range comparisons {
		if comparison != 0 {
			return comparison
		}
	}
	return 0
}

// MarshalCanonical validates the plan and returns its canonical JSON encoding,
// invocation metadata included. This is the serialized plan artifact.
func (plan *Plan) MarshalCanonical() ([]byte, error) {
	canonical := plan.Canonical()
	if err := canonical.Validate(); err != nil {
		return nil, err
	}
	return marshalCompact(canonical)
}

// SemanticBytes returns the canonical encoding of everything that changes what
// the invocation does. Invocation metadata is dropped, so the same declarations
// produce identical bytes across runs and across checkout locations.
func (plan *Plan) SemanticBytes() ([]byte, error) {
	canonical := plan.Canonical()
	if err := canonical.Validate(); err != nil {
		return nil, err
	}
	canonical.Invocation = nil
	return marshalCompact(canonical)
}

// SemanticFingerprint is the lowercase sha256 hex of the semantic bytes under a
// versioned hash format.
//
// It identifies the PLAN, not the build. Two plans share a fingerprint when they
// resolved to the same selection, backends, pinned artifact identities, wiring
// and policy — which is not the same as "the same bytes will run". A plan
// carries no service spec, no test formula and, for an artifact resolved from a
// local checkout, no content digest, so editing a dependency's source or spec
// leaves the fingerprint unchanged.
//
// A consumer deciding whether to reuse warm state must therefore consult
// UncoveredContent first: fingerprint equality implies input equality only when
// it is empty.
func (plan *Plan) SemanticFingerprint() (string, error) {
	semantic, err := plan.SemanticBytes()
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	digest.Write([]byte(SemanticHashFormatV1))
	digest.Write([]byte{0})
	digest.Write(semantic)
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// UncoveredContent returns the sorted ids of selected nodes whose inputs the
// semantic fingerprint does not pin — a node with no artifact, or one whose
// artifacts carry no content digest. For those nodes two different working
// trees produce the same fingerprint, so reusing warm state across a
// fingerprint match can reattach to a stale build.
//
// An empty result means every selected node is content-addressed and
// fingerprint equality does imply input equality.
func (plan *Plan) UncoveredContent() []string {
	var uncovered []string
	for _, node := range plan.Nodes {
		if len(node.Artifacts) == 0 {
			uncovered = append(uncovered, node.ID)
			continue
		}
		for _, artifact := range node.Artifacts {
			if artifact.Digest == "" {
				uncovered = append(uncovered, node.ID)
				break
			}
		}
	}
	slices.Sort(uncovered)
	return uncovered
}

func marshalCompact(plan *Plan) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(plan); err != nil {
		return nil, fmt.Errorf("%w: encode plan: %v", ErrInvalid, err)
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// Unmarshal decodes a serialized plan and validates it. A plan carrying an
// unknown schema is refused rather than best-effort parsed.
func Unmarshal(data []byte) (*Plan, error) {
	// The schema is read first and on its own: a later schema carries fields
	// this version does not know, so a strict decode would report a newer plan
	// as a malformed one and hide the only fact the operator can act on.
	var envelope struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("%w: decode plan: %v", ErrInvalid, err)
	}
	if envelope.Schema != SchemaV1 {
		return nil, fmt.Errorf("%w: schema %q is not %q", ErrInvalid, envelope.Schema, SchemaV1)
	}
	var plan Plan
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return nil, fmt.Errorf("%w: decode plan: %v", ErrInvalid, err)
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	return &plan, nil
}
