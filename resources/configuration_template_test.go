package resources

import (
	"net/url"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

func literal(text string) *basev0.ConfigurationValueTemplateSegment {
	return &basev0.ConfigurationValueTemplateSegment{Content: &basev0.ConfigurationValueTemplateSegment_Literal{Literal: text}}
}

func reference(configuration, key string, escape basev0.ConfigurationValueEscape) *basev0.ConfigurationValueTemplateSegment {
	return &basev0.ConfigurationValueTemplateSegment{Content: &basev0.ConfigurationValueTemplateSegment_Reference{
		Reference: &basev0.ConfigurationValueReference{Configuration: configuration, Key: key, Escape: escape},
	}}
}

// adversarialSecrets holds values a generated secret never contains but an
// operator-supplied one might: every URL delimiter, a space, a plus, a percent,
// a quote, template delimiters and non-ASCII.
var adversarialSecrets = []string{
	"0123456789abcdef",
	"p@ss:w/rd?#[]",
	"with space+plus",
	"100%literal",
	`quote"'back\slash`,
	"{{ .inject }}",
	"$&,;=!*()",
	"unicodé-ß-密码",
	"~-._",
}

func TestEscapeURLUserinfoIsQueryEscapeWithEncodedSpaces(t *testing.T) {
	for _, secret := range adversarialSecrets {
		want := strings.ReplaceAll(url.QueryEscape(secret), "+", "%20")
		got := EscapeConfigurationValue(basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_URL_USERINFO, secret)
		if got != want {
			t.Errorf("escape(%q) = %q, want %q", secret, got, want)
		}
	}
}

func TestEvaluatedConnectionStringRoundTripsThePassword(t *testing.T) {
	template := &basev0.ConfigurationValueTemplate{Segments: []*basev0.ConfigurationValueTemplateSegment{
		literal("postgresql://reader:"),
		reference("postgres", "POSTGRES_READ_ONLY_PASSWORD", basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_URL_USERINFO),
		literal("@store.example.svc.cluster.local:5432/app?sslmode=disable"),
	}}
	for _, secret := range adversarialSecrets {
		assembled, err := EvaluateConfigurationValueTemplate(template, func(configuration, key string) (string, bool) {
			return secret, configuration == "postgres" && key == "POSTGRES_READ_ONLY_PASSWORD"
		})
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(assembled)
		if err != nil {
			t.Fatalf("parse assembled connection for %q: %v", secret, err)
		}
		password, _ := parsed.User.Password()
		if password != secret || parsed.User.Username() != "reader" || parsed.Host != "store.example.svc.cluster.local:5432" ||
			parsed.Path != "/app" || parsed.Query().Get("sslmode") != "disable" {
			t.Errorf("secret %q did not round-trip: %q", secret, assembled)
		}
	}
}

func TestEvaluateRefusesAnUnsetReference(t *testing.T) {
	template := &basev0.ConfigurationValueTemplate{Segments: []*basev0.ConfigurationValueTemplateSegment{
		reference("postgres", "POSTGRES_PASSWORD", basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_NONE),
	}}
	if _, err := EvaluateConfigurationValueTemplate(template, func(string, string) (string, bool) { return "", false }); err == nil {
		t.Fatal("an unset reference must fail, not assemble an empty value")
	}
}

func TestValidateTemplatedConfigurationValue(t *testing.T) {
	template := &basev0.ConfigurationValueTemplate{Segments: []*basev0.ConfigurationValueTemplateSegment{literal("x")}}
	cases := []struct {
		name  string
		value *basev0.ConfigurationValue
		ok    bool
	}{
		{"no template", &basev0.ConfigurationValue{Key: "k", Value: "v"}, true},
		{"secret without value", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: template}, true},
		{"not secret", &basev0.ConfigurationValue{Key: "k", Template: template}, false},
		{"value and template", &basev0.ConfigurationValue{Key: "k", Value: "v", Secret: true, Template: template}, false},
		{"empty template", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: &basev0.ConfigurationValueTemplate{}}, false},
		{"empty segment", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: &basev0.ConfigurationValueTemplate{
			Segments: []*basev0.ConfigurationValueTemplateSegment{{}},
		}}, false},
		{"incomplete reference", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: &basev0.ConfigurationValueTemplate{
			Segments: []*basev0.ConfigurationValueTemplateSegment{reference("postgres", "", 0)},
		}}, false},
		{"unknown escape", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: &basev0.ConfigurationValueTemplate{
			Segments: []*basev0.ConfigurationValueTemplateSegment{reference("postgres", "KEY", 99)},
		}}, false},
	}
	for _, c := range cases {
		err := ValidateTemplatedConfigurationValue(c.value)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}
