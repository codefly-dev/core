package resources

import (
	"fmt"
	"net/url"
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
// one) must produce exactly these bytes for every input — which takes both
// halves: the escape, and the resolution of a reference to a value.
// ProducerConfigurationValueLookup is that resolution, and it is the only one
// core ships, so two renderers cannot disagree about which value a reference
// names.

// ConfigurationValueLookup resolves one of the producer's own configuration
// values by configuration group and key. It reports found only for a value a
// template may insert: one that exists and carries a value of its own.
// Returning true with an empty string is a contract violation — the distinction
// between "absent" and "empty" is the whole point of the bool, and
// EvaluateConfigurationValueTemplate refuses both rather than assemble a
// connection string with an empty password.
type ConfigurationValueLookup func(configuration, key string) (string, bool)

// ProducerConfigurationValueLookup resolves a template's references against the
// configuration of the producer that declared it, and nothing else. Scoping the
// lookup to one Configuration is what makes "never another service's" true
// rather than merely documented: a Configuration carries exactly one origin, so
// there is no name a reference could spell that would reach another producer's
// secrets.
//
// Matching is Match, as everywhere else configuration is addressed by name:
// case insensitive, with "-" and "_" equivalent. A value that carries a
// template of its own is not a primitive and so does not resolve — a template
// assembles primitives, never another assembly.
func ProducerConfigurationValueLookup(conf *basev0.Configuration) ConfigurationValueLookup {
	return func(configuration, key string) (string, bool) {
		for _, info := range conf.GetInfos() {
			if !Match(info.GetName(), configuration) {
				continue
			}
			for _, value := range info.GetConfigurationValues() {
				if value == nil || !Match(value.GetKey(), key) {
					continue
				}
				if value.GetTemplate() != nil || value.GetValue() == "" {
					return "", false
				}
				return value.GetValue(), true
			}
		}
		return "", false
	}
}

// templateEngineDelimiters are the delimiters a text/template renderer reads as
// the start and end of an action. A literal carrying one cannot survive a
// translation into such an engine, where the literals become template source.
var templateEngineDelimiters = []string{"{{", "}}"}

// ValidateConfigurationValueTemplate reports whether a template is one a
// renderer can deliver: at least one segment, every segment either a literal a
// target engine can carry or a complete reference, and every reference stating
// an escape this package knows.
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
			for _, delimiter := range templateEngineDelimiters {
				if strings.Contains(content.Literal, delimiter) {
					return fmt.Errorf("configuration value template segment %d is a literal containing %q, which a template engine would read as its own syntax", index, delimiter)
				}
			}
		case *basev0.ConfigurationValueTemplateSegment_Reference:
			reference := content.Reference
			if reference == nil {
				return fmt.Errorf("configuration value template segment %d references no configuration key", index)
			}
			if err := validateConfigurationValueReferenceName("configuration", reference.GetConfiguration()); err != nil {
				return fmt.Errorf("configuration value template segment %d: %w", index, err)
			}
			if err := validateConfigurationValueReferenceName("key", reference.GetKey()); err != nil {
				return fmt.Errorf("configuration value template segment %d: %w", index, err)
			}
			if err := validateConfigurationValueEscape(reference.GetEscape()); err != nil {
				return fmt.Errorf("configuration value template segment %d: %w", index, err)
			}
		default:
			return fmt.Errorf("configuration value template segment %d is neither a literal nor a reference", index)
		}
	}
	return nil
}

// validateConfigurationValueReferenceName rejects a name a lookup would not
// resolve to what it reads like. Surrounding whitespace is the case that
// matters: it is invisible in a declaration and Match does not fold it, so
// " postgres " would validate and then resolve to nothing. Rejecting it here
// keeps validation and resolution describing the same name.
func validateConfigurationValueReferenceName(field, name string) error {
	if name == "" {
		return fmt.Errorf("reference states no %s", field)
	}
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("reference %s %q is surrounded by whitespace, which no lookup folds", field, name)
	}
	return nil
}

// validateConfigurationValueEscape requires the producer to have stated an
// encoding. UNSPECIFIED is not verbatim insertion: a value carrying "@" or "/"
// inserted verbatim into a URL moves the host the assembled value points at,
// and parsers disagree about which "@" delimits the userinfo — so a forgotten
// field must fail, not encode.
func validateConfigurationValueEscape(escape basev0.ConfigurationValueEscape) error {
	if escape == basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_UNSPECIFIED {
		return fmt.Errorf("reference states no escape; state %s for verbatim insertion",
			basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_NONE)
	}
	if _, known := basev0.ConfigurationValueEscape_name[int32(escape)]; !known {
		return fmt.Errorf("reference has unknown escape %d", escape)
	}
	return nil
}

// ValidateTemplatedConfigurationValue reports whether a value carrying a
// template is well formed: a template belongs only on a secret value that
// carries no value of its own, since a value and its assembly could disagree.
func ValidateTemplatedConfigurationValue(value *basev0.ConfigurationValue) error {
	if value == nil {
		return fmt.Errorf("configuration value is nil")
	}
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
// producer's configuration values. A reference that does not resolve — absent,
// empty, or itself an assembly — is an error, never an empty string: a
// connection string with an empty password still parses, and fails only when it
// is dialed.
func EvaluateConfigurationValueTemplate(template *basev0.ConfigurationValueTemplate, lookup ConfigurationValueLookup) (string, error) {
	if err := ValidateConfigurationValueTemplate(template); err != nil {
		return "", err
	}
	if lookup == nil {
		return "", fmt.Errorf("configuration value template needs a lookup to resolve its references")
	}
	var out strings.Builder
	for _, segment := range template.GetSegments() {
		if reference := segment.GetReference(); reference != nil {
			value, found := lookup(reference.GetConfiguration(), reference.GetKey())
			if !found {
				return "", fmt.Errorf("configuration value template references %s/%s, which is not set to a value it can insert", reference.GetConfiguration(), reference.GetKey())
			}
			if value == "" {
				return "", fmt.Errorf("configuration value template references %s/%s, which resolved to an empty value", reference.GetConfiguration(), reference.GetKey())
			}
			encoded, err := EscapeConfigurationValue(reference.GetEscape(), value)
			if err != nil {
				return "", fmt.Errorf("configuration value template references %s/%s: %w", reference.GetConfiguration(), reference.GetKey(), err)
			}
			out.WriteString(encoded)
			continue
		}
		out.WriteString(segment.GetLiteral())
	}
	return out.String(), nil
}

// EscapeConfigurationValue encodes a referenced value for insertion. An escape
// it does not implement is an error, never the value unchanged: returning the
// raw value for an escape this build does not know would hand a URL the one
// encoding the producer ruled out.
//
// URL_USERINFO percent-encodes every byte but the RFC 3986 unreserved set, with
// upper-case hex. It is deliberately the strictest encoding a URL component
// accepts, and it is written here as the equivalence it is documented by —
// url.QueryEscape with "+" (its encoding of a space) written as "%20" — so no
// hand-rolled table can drift from the statement a renderer reproduces with the
// standard urlquery function and a replace.
func EscapeConfigurationValue(escape basev0.ConfigurationValueEscape, value string) (string, error) {
	switch escape {
	case basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_NONE:
		return value, nil
	case basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_URL_USERINFO:
		return strings.ReplaceAll(url.QueryEscape(value), "+", "%20"), nil
	default:
		return "", validateConfigurationValueEscape(escape)
	}
}

// ConfigurationValueAsString returns the string a configuration value carries:
// its value, or, when its producer declared an assembly instead, the assembly
// evaluated against that producer's own primitives. Every consumer that wants
// the value a service will actually see goes through this — reading .Value
// directly yields the empty string a templated value literally holds, which is a
// credential that is silently absent rather than a failure anyone notices.
func ConfigurationValueAsString(conf *basev0.Configuration, value *basev0.ConfigurationValue) (string, error) {
	if err := ValidateTemplatedConfigurationValue(value); err != nil {
		return "", err
	}
	if value.GetTemplate() == nil {
		return value.GetValue(), nil
	}
	return EvaluateConfigurationValueTemplate(value.GetTemplate(), ProducerConfigurationValueLookup(conf))
}

// ConfigurationValueEndpointReferences returns every ${endpoint:…} reference a
// configuration value carries, in its value and in the literals of the template
// it declared. A templated value holds its text in those literals and nowhere
// else, so a caller reading only .Value sees no reference — and a caller that
// orders a consumer after the producers its configuration names would then not
// order it at all, starting the service before the address in its connection
// string exists.
func ConfigurationValueEndpointReferences(value *basev0.ConfigurationValue) []string {
	references := EndpointReferences(value.GetValue())
	for _, segment := range value.GetTemplate().GetSegments() {
		references = append(references, EndpointReferences(segment.GetLiteral())...)
	}
	return references
}
