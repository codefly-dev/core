package resources

import (
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// A ConfigurationValueTemplate lets a producer hand out a secret it never has to
// store assembled: a connection string, say, that embeds a password. The
// producer declares the assembly (literals around references to its own secret
// configuration values); whoever delivers the value evaluates it where the
// primitives are, so a secret store holds only the primitives and nobody types
// the assembled form.
//
// EvaluateConfigurationValueTemplate is the reference semantics. A renderer that
// translates a template into another engine (an External Secrets template, for
// one) must produce exactly these bytes for every input.

// ConfigurationValueLookup resolves one of the producer's own secret
// configuration values by configuration group and key.
type ConfigurationValueLookup func(configuration, key string) (string, bool)

// ValidateConfigurationValueTemplate reports whether a template is one a
// renderer can deliver: at least one segment, every segment either a literal or
// a complete reference, and every escape one this package knows.
func ValidateConfigurationValueTemplate(template *basev0.ConfigurationValueTemplate) error {
	if template == nil {
		return fmt.Errorf("configuration value template is nil")
	}
	if len(template.GetSegments()) == 0 {
		return fmt.Errorf("configuration value template has no segments")
	}
	for index, segment := range template.GetSegments() {
		switch content := segment.GetContent().(type) {
		case *basev0.ConfigurationValueTemplateSegment_Literal:
		case *basev0.ConfigurationValueTemplateSegment_Reference:
			reference := content.Reference
			if reference == nil || strings.TrimSpace(reference.GetConfiguration()) == "" || strings.TrimSpace(reference.GetKey()) == "" {
				return fmt.Errorf("configuration value template segment %d references no configuration key", index)
			}
			if _, known := basev0.ConfigurationValueEscape_name[int32(reference.GetEscape())]; !known {
				return fmt.Errorf("configuration value template segment %d has unknown escape %d", index, reference.GetEscape())
			}
		default:
			return fmt.Errorf("configuration value template segment %d is neither a literal nor a reference", index)
		}
	}
	return nil
}

// ValidateTemplatedConfigurationValue reports whether a value carrying a
// template is well formed: a template belongs only on a secret value that
// carries no value of its own, since a value and its assembly could disagree.
func ValidateTemplatedConfigurationValue(value *basev0.ConfigurationValue) error {
	if value.GetTemplate() == nil {
		return nil
	}
	if !value.GetSecret() {
		return fmt.Errorf("configuration value %q carries a template but is not secret", value.GetKey())
	}
	if value.GetValue() != "" {
		return fmt.Errorf("configuration value %q carries both a value and a template", value.GetKey())
	}
	if err := ValidateConfigurationValueTemplate(value.GetTemplate()); err != nil {
		return fmt.Errorf("configuration value %q: %w", value.GetKey(), err)
	}
	return nil
}

// EvaluateConfigurationValueTemplate assembles a template's value from the
// producer's secret configuration values. A missing reference is an error,
// never an empty string: a connection string with an empty password still
// parses, and fails only when it is dialed.
func EvaluateConfigurationValueTemplate(template *basev0.ConfigurationValueTemplate, lookup ConfigurationValueLookup) (string, error) {
	if err := ValidateConfigurationValueTemplate(template); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, segment := range template.GetSegments() {
		if reference := segment.GetReference(); reference != nil {
			value, found := lookup(reference.GetConfiguration(), reference.GetKey())
			if !found {
				return "", fmt.Errorf("configuration value template references %s/%s, which is not set", reference.GetConfiguration(), reference.GetKey())
			}
			out.WriteString(EscapeConfigurationValue(reference.GetEscape(), value))
			continue
		}
		out.WriteString(segment.GetLiteral())
	}
	return out.String(), nil
}

// EscapeConfigurationValue encodes a referenced value for insertion.
//
// URL_USERINFO percent-encodes every byte but the RFC 3986 unreserved set, with
// upper-case hex. It is deliberately the strictest encoding a URL component
// accepts, and equal to Go's url.QueryEscape with "+" (its encoding of a space)
// written as "%20" — which is what a text/template engine with the standard
// urlquery function and a replace can reproduce exactly.
func EscapeConfigurationValue(escape basev0.ConfigurationValueEscape, value string) string {
	if escape != basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_URL_USERINFO {
		return value
	}
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			out.WriteByte(c)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[c>>4])
		out.WriteByte(hex[c&0x0f])
	}
	return out.String()
}
