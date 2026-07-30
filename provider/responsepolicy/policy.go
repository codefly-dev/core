// Package responsepolicy filters untrusted vendor responses into safe,
// schema-declared bytes. It owns Accept-Encoding decoding, bounds every
// dimension of the response, rejects ambiguous JSON, and applies manifest
// selectors so that FORWARD_SAFE fields are the only values that leave the
// host, secret-bearing fields are captured to a pre-authorized sink or reported
// only by presence, and everything undeclared is dropped. Any drift — a missing
// required field, a value of the wrong shape, or a sink failure — fails closed
// and forwards nothing.
package responsepolicy

import (
	"context"
	"fmt"
	"strings"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/manifest"
)

// Sink durably stores a captured secret and returns only an opaque reference.
// The production backend is out of scope; the host injects a concrete Sink.
type Sink interface {
	Put(ctx context.Context, target SinkTarget, secret string) (*providerv0.OpaqueReference, error)
}

// SinkTarget is the exact pre-authorized capture destination. It is derived by
// the host from the plan, never from provider input.
type SinkTarget struct {
	Purpose providerv0.CredentialPurpose
	Key     string
}

// Field is one host-derived response disposition. It mirrors the manifest
// response field but adds host-decided required-ness and the pre-authorized
// sink key for captures.
type Field struct {
	Selector    manifest.Selector
	Disposition manifest.ResponseDisposition
	Purpose     providerv0.CredentialPurpose
	Required    bool
	SinkKey     string
}

// Policy is the host-derived response policy for one admitted method/path/
// status/content type.
type Policy struct {
	Fields []Field
	Limits Limits
}

// CaptureOutcome is the explicit result vocabulary for a capture selector.
type CaptureOutcome string

const (
	OutcomeCaptured   CaptureOutcome = "CAPTURED"
	OutcomeAbsent     CaptureOutcome = "ABSENT"
	OutcomeSinkFailed CaptureOutcome = "SINK_FAILED"
)

// Forwarded is one safe field cleared for return to the provider.
type Forwarded struct {
	Selector string
	Value    *providerv0.PublicValue
}

// Capture is the durable result of one capture selector.
type Capture struct {
	Selector  string
	Reference *providerv0.OpaqueReference
	Outcome   CaptureOutcome
}

// Result is the fully filtered response. SafeJSON is a canonical projection
// containing only forwarded values and presence markers — never raw bytes.
type Result struct {
	Forwarded  []Forwarded
	Suppressed []string
	Captures   []Capture
	SafeJSON   []byte
}

// RequiresBody reports whether any field must be present. A successful response
// with no body but a policy that requires a field is drift and must fail closed
// rather than silently report success — a vendor cannot skip a required capture
// by returning an empty body.
func (p Policy) RequiresBody() bool {
	for _, field := range p.Fields {
		if field.Required {
			return true
		}
	}
	return false
}

// Filter decodes, bounds, and filters a response body against the policy. It
// returns an error — and no partial result — whenever any declared behavior
// cannot be honored, so the caller can fail closed without ever forwarding
// original bytes.
func (p Policy) Filter(ctx context.Context, raw []byte, contentEncoding, contentType string, sink Sink) (*Result, error) {
	if err := validateJSONContentType(contentType); err != nil {
		return nil, err
	}
	root, err := decodeBody(raw, contentEncoding, p.Limits)
	if err != nil {
		return nil, err
	}
	result := &Result{}
	safe := map[string]any{}
	for _, field := range p.Fields {
		matches, err := evaluate(field.Selector, root)
		if err != nil {
			return nil, fmt.Errorf("selector %s: %w", field.Selector.Path, err)
		}
		switch field.Disposition {
		case manifest.ResponseForwardSafe:
			if len(matches) == 0 && field.Required {
				return nil, fmt.Errorf("required forward field %s is absent", field.Selector.Path)
			}
			for _, m := range matches {
				value, err := toPublicValue(m.value)
				if err != nil {
					return nil, fmt.Errorf("forward field %s: %w", m.path, err)
				}
				result.Forwarded = append(result.Forwarded, Forwarded{Selector: m.path, Value: value})
				safe[m.path] = m.value
			}
		case manifest.ResponseSuppressPresence:
			if len(matches) == 0 && field.Required {
				return nil, fmt.Errorf("required suppressed field %s is absent", field.Selector.Path)
			}
			if len(matches) > 0 {
				result.Suppressed = append(result.Suppressed, field.Selector.Path)
				safe[field.Selector.Path] = map[string]any{"present": true}
			}
		case manifest.ResponseCaptureToSink:
			captures, err := p.capture(ctx, field, matches, sink)
			if err != nil {
				return nil, err
			}
			result.Captures = append(result.Captures, captures...)
			for _, capture := range captures {
				safe[capture.Selector] = map[string]any{"captured": capture.Outcome == OutcomeCaptured}
			}
		default:
			return nil, fmt.Errorf("field %s has an unknown disposition", field.Selector.Path)
		}
	}
	encoded, err := canonicalJSON(safe)
	if err != nil {
		return nil, err
	}
	result.SafeJSON = encoded
	return result, nil
}

// capture resolves and durably stores every matched secret for one capture
// field. It fails closed on a wrong-typed value, a missing required capture, or
// a sink failure so an unstored secret is never silently dropped.
func (p Policy) capture(ctx context.Context, field Field, matches []match, sink Sink) ([]Capture, error) {
	if sink == nil {
		return nil, fmt.Errorf("capture field %s requires a sink", field.Selector.Path)
	}
	if len(matches) == 0 {
		if field.Required {
			return nil, fmt.Errorf("required capture field %s is absent", field.Selector.Path)
		}
		return []Capture{{Selector: field.Selector.Path, Outcome: OutcomeAbsent}}, nil
	}
	captures := make([]Capture, 0, len(matches))
	for _, m := range matches {
		secret, ok := m.value.(string)
		if !ok {
			return nil, fmt.Errorf("capture field %s resolved a non-string value", m.path)
		}
		reference, err := sink.Put(ctx, SinkTarget{Purpose: field.Purpose, Key: field.SinkKey}, secret)
		if err != nil {
			return nil, fmt.Errorf("capture field %s sink failed: %w", m.path, err)
		}
		if reference == nil {
			return nil, fmt.Errorf("capture field %s sink returned no reference", m.path)
		}
		captures = append(captures, Capture{Selector: m.path, Reference: reference, Outcome: OutcomeCaptured})
	}
	return captures, nil
}

func validateJSONContentType(contentType string) error {
	media, _, _ := strings.Cut(contentType, ";")
	media = strings.ToLower(strings.TrimSpace(media))
	if media == "application/json" || strings.HasSuffix(media, "+json") {
		return nil
	}
	return fmt.Errorf("response content type %q is not admitted", contentType)
}
