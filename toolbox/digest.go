package toolbox

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
)

// CatalogSnapshot is the immutable first phase of toolbox discovery: identity
// plus lightweight summaries. Full descriptions are deliberately excluded so
// hosts can fetch and approve only the selected tool immediately before use.
type CatalogSnapshot struct {
	Identity  *toolboxv0.IdentityResponse
	Summaries *toolboxv0.ListToolSummariesResponse
	Digest    string
}

// IdempotencyClass is the closed, host-interpreted ToolSpec idempotency set.
// Unknown or free-text values can never authorize an automatic retry.
type IdempotencyClass string

const (
	IdempotencyUnknown       IdempotencyClass = "unknown"
	IdempotencySafe          IdempotencyClass = "idempotent"
	IdempotencySideEffecting IdempotencyClass = "side_effecting"
)

// ApprovedTool is the second discovery phase. Digest binds the toolbox
// identity, complete summary catalog, and this exact full descriptor.
type ApprovedTool struct {
	Summary     *toolboxv0.ToolSummary
	Description *toolboxv0.DescribeToolResponse
	Digest      string
	Idempotency IdempotencyClass
}

// SnapshotServer discovers only identity and summaries. Plugin guards and
// external hosts both call ApproveTool for the selected action, preventing the
// legacy eager ListTools behavior from creeping back into the host contract.
func SnapshotServer(ctx context.Context, server toolboxv0.ToolboxServer) (*CatalogSnapshot, error) {
	if server == nil {
		return nil, fmt.Errorf("toolbox catalog: nil server")
	}
	identity, err := server.Identity(ctx, &toolboxv0.IdentityRequest{})
	if err != nil {
		return nil, fmt.Errorf("toolbox catalog: identity: %w", err)
	}
	summaries, err := server.ListToolSummaries(ctx, &toolboxv0.ListToolSummariesRequest{})
	if err != nil {
		return nil, fmt.Errorf("toolbox catalog: list summaries: %w", err)
	}
	return NewCatalogSnapshot(identity, summaries)
}

// NewCatalogSnapshot validates, canonicalizes, clones, and hashes first-phase
// discovery. Wire ordering does not affect the digest.
func NewCatalogSnapshot(
	identity *toolboxv0.IdentityResponse,
	summaries *toolboxv0.ListToolSummariesResponse,
) (*CatalogSnapshot, error) {
	if identity == nil || identity.GetName() == "" || identity.GetVersion() == "" {
		return nil, fmt.Errorf("toolbox catalog: identity name and version are required")
	}
	if summaries == nil {
		return nil, fmt.Errorf("toolbox catalog: summaries are required")
	}
	summaryByName := make(map[string]*toolboxv0.ToolSummary, len(summaries.GetTools()))
	for i, summary := range summaries.GetTools() {
		if summary == nil || summary.GetName() == "" {
			return nil, fmt.Errorf("toolbox catalog: summary %d has no name", i)
		}
		if _, duplicate := summaryByName[summary.GetName()]; duplicate {
			return nil, fmt.Errorf("toolbox catalog: duplicate summary %q", summary.GetName())
		}
		summaryByName[summary.GetName()] = summary
	}
	names := make([]string, 0, len(summaryByName))
	for name := range summaryByName {
		names = append(names, name)
	}
	sort.Strings(names)
	canonicalSummaries := &toolboxv0.ListToolSummariesResponse{
		Tools: make([]*toolboxv0.ToolSummary, 0, len(names)),
	}
	for _, name := range names {
		canonicalSummaries.Tools = append(canonicalSummaries.Tools,
			proto.Clone(summaryByName[name]).(*toolboxv0.ToolSummary))
	}
	identityClone := proto.Clone(identity).(*toolboxv0.IdentityResponse)
	digest, err := digestMessages(identityClone, canonicalSummaries, nil)
	if err != nil {
		return nil, err
	}
	return &CatalogSnapshot{
		Identity: identityClone, Summaries: canonicalSummaries, Digest: digest,
	}, nil
}

// ApproveTool validates a phase-two descriptor against the phase-one catalog
// and returns the digest the host must bind into scoped authorization.
func (s *CatalogSnapshot) ApproveTool(name string, description *toolboxv0.DescribeToolResponse) (*ApprovedTool, error) {
	if s == nil || s.Identity == nil || s.Summaries == nil {
		return nil, fmt.Errorf("toolbox catalog: incomplete snapshot")
	}
	if name == "" {
		return nil, fmt.Errorf("toolbox catalog: requested tool name is required")
	}
	if description == nil || description.GetTool() == nil {
		return nil, fmt.Errorf("toolbox catalog: description has no tool")
	}
	if description.GetError() != "" {
		return nil, fmt.Errorf("toolbox catalog: describe failed: %s", description.GetError())
	}
	spec := description.GetTool()
	if spec.GetName() != name {
		return nil, fmt.Errorf("toolbox catalog: requested tool %q returned descriptor %q", name, spec.GetName())
	}
	var summary *toolboxv0.ToolSummary
	for _, candidate := range s.Summaries.GetTools() {
		if candidate.GetName() == spec.GetName() {
			summary = candidate
			break
		}
	}
	if summary == nil {
		return nil, fmt.Errorf("toolbox catalog: description %q has no matching summary", spec.GetName())
	}
	if summary.GetDestructive() != spec.GetDestructive() {
		return nil, fmt.Errorf("toolbox catalog: description %q changes destructive classification", spec.GetName())
	}
	if spec.GetInputSchema() == nil {
		return nil, fmt.Errorf("toolbox catalog: description %q has no input schema", spec.GetName())
	}
	if err := ValidateSchema(spec.GetInputSchema()); err != nil {
		return nil, fmt.Errorf("toolbox catalog: description %q: %w", spec.GetName(), err)
	}
	if !sameStrings(summary.GetTags(), spec.GetTags()) {
		return nil, fmt.Errorf("toolbox catalog: description %q tags do not match summary", spec.GetName())
	}
	idempotency, err := ParseIdempotency(spec.GetIdempotency())
	if err != nil {
		return nil, fmt.Errorf("toolbox catalog: description %q: %w", spec.GetName(), err)
	}
	clone := proto.Clone(description).(*toolboxv0.DescribeToolResponse)
	digest, err := digestMessages(s.Identity, s.Summaries, []*toolboxv0.DescribeToolResponse{clone})
	if err != nil {
		return nil, err
	}
	return &ApprovedTool{
		Summary:     proto.Clone(summary).(*toolboxv0.ToolSummary),
		Description: clone,
		Digest:      digest,
		Idempotency: idempotency,
	}, nil
}

// ParseIdempotency converts the protocol field into its closed semantic set.
// Empty remains a supported explicit unknown for existing tool definitions.
func ParseIdempotency(value string) (IdempotencyClass, error) {
	switch IdempotencyClass(value) {
	case "", IdempotencyUnknown:
		return IdempotencyUnknown, nil
	case IdempotencySafe:
		return IdempotencySafe, nil
	case IdempotencySideEffecting:
		return IdempotencySideEffecting, nil
	default:
		return "", fmt.Errorf("unsupported idempotency %q", value)
	}
}

// ToolNames returns the canonical sorted catalog names.
func (s *CatalogSnapshot) ToolNames() []string {
	if s == nil || s.Summaries == nil {
		return nil
	}
	out := make([]string, 0, len(s.Summaries.GetTools()))
	for _, summary := range s.Summaries.GetTools() {
		out = append(out, summary.GetName())
	}
	return out
}

// HasTool reports whether name was present in phase-one discovery.
func (s *CatalogSnapshot) HasTool(name string) bool {
	if s == nil || s.Summaries == nil {
		return false
	}
	for _, summary := range s.Summaries.GetTools() {
		if summary.GetName() == name {
			return true
		}
	}
	return false
}

// Clone returns a deep copy suitable for exposing outside a session.
func (s *CatalogSnapshot) Clone() *CatalogSnapshot {
	if s == nil {
		return nil
	}
	return &CatalogSnapshot{
		Identity:  proto.Clone(s.Identity).(*toolboxv0.IdentityResponse),
		Summaries: proto.Clone(s.Summaries).(*toolboxv0.ListToolSummariesResponse),
		Digest:    s.Digest,
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	a := append([]string(nil), left...)
	b := append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// DigestCallToolRequest returns a deterministic digest of the exact protobuf
// request (tool name, structured arguments, and roots).
func DigestCallToolRequest(request *toolboxv0.CallToolRequest) (string, error) {
	if request == nil || request.GetName() == "" {
		return "", fmt.Errorf("toolbox request digest: request and tool name are required")
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(request)
	if err != nil {
		return "", fmt.Errorf("toolbox request digest: marshal: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func digestMessages(
	identity *toolboxv0.IdentityResponse,
	summaries *toolboxv0.ListToolSummariesResponse,
	descriptions []*toolboxv0.DescribeToolResponse,
) (string, error) {
	hash := sha256.New()
	messages := make([]proto.Message, 0, 2+len(descriptions))
	messages = append(messages, identity, summaries)
	for _, description := range descriptions {
		messages = append(messages, description)
	}
	for _, message := range messages {
		encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
		if err != nil {
			return "", fmt.Errorf("toolbox catalog: deterministic marshal: %w", err)
		}
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(encoded)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(encoded)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
