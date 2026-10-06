package runnable

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"google.golang.org/protobuf/proto"
)

// ErrNotATool distinguishes a valid operation that its owner did not expose.
var ErrNotATool = errors.New("operation is not exposed as a tool")

// ValidateToolExposure checks an explicit exposure without inferring effects
// from scopes, names or transports. Nil is valid for a non-tool operation.
func ValidateToolExposure(tool *runnablev0.ToolExposure) error {
	if tool == nil {
		return nil
	}
	name := tool.GetName()
	if len(name) == 0 || len(name) > 64 {
		return fmt.Errorf("%w: tool name must contain 1..64 ASCII characters", ErrInvalid)
	}
	for i, c := range []byte(name) {
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (i == 0 || !(c >= '0' && c <= '9' || c == '_' || c == '-')) {
			return fmt.Errorf("%w: invalid tool name", ErrInvalid)
		}
	}
	description := tool.GetDescription()
	if len(description) == 0 || len(description) > 4096 || !utf8.ValidString(description) || strings.TrimSpace(description) != description || strings.ContainsRune(description, '\x00') {
		return fmt.Errorf("%w: tool description must be nonempty bounded UTF-8 text", ErrInvalid)
	}
	switch tool.GetEffect() {
	case runnablev0.ToolExposure_EFFECT_READ_ONLY, runnablev0.ToolExposure_EFFECT_MUTATION:
	default:
		return fmt.Errorf("%w: tool effect must explicitly declare read-only or mutation", ErrInvalid)
	}
	return nil
}

// ToolFromPrepared verifies the entire prepared binding before projecting its
// exposure. The returned copy carries no route, credential or scope. Consumers
// obtain schemas from that same binding's Contract and freeze its identity at
// admission; they must still check installation and current caller authority.
// This projection does not authenticate the source of a delivered binding.
func ToolFromPrepared(binding *runnablev0.PreparedBinding) (*runnablev0.ToolExposure, error) {
	if err := VerifyPrepared(binding); err != nil {
		return nil, err
	}
	if binding.GetPolicy().GetTool() == nil {
		return nil, ErrNotATool
	}
	return proto.CloneOf(binding.GetPolicy().GetTool()), nil
}
