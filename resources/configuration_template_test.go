package resources

import (
	"net/url"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

func templateLiteral(text string) *basev0.ConfigurationValueTemplateSegment {
	return &basev0.ConfigurationValueTemplateSegment{Content: &basev0.ConfigurationValueTemplateSegment_Literal{Literal: text}}
}

func templateReference(configuration, key string, escape basev0.ConfigurationValueEscape) *basev0.ConfigurationValueTemplateSegment {
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
	// Every byte, not only the adversarial strings: the documented equivalence
	// is a claim about the whole input space, and a renderer reproducing it with
	// urlquery and a replace will be handed bytes no fixture lists.
	for b := 0; b < 256; b++ {
		secret := string([]byte{byte(b)})
		want := strings.ReplaceAll(url.QueryEscape(secret), "+", "%20")
		got, err := EscapeConfigurationValue(basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_URL_USERINFO, secret)
		if err != nil {
			t.Fatalf("escape(byte %d): %v", b, err)
		}
		if got != want {
			t.Errorf("escape(byte %d) = %q, want %q", b, got, want)
		}
	}
	for _, secret := range adversarialSecrets {
		want := strings.ReplaceAll(url.QueryEscape(secret), "+", "%20")
		got, err := EscapeConfigurationValue(basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_URL_USERINFO, secret)
		if err != nil {
			t.Fatalf("escape(%q): %v", secret, err)
		}
		if got != want {
			t.Errorf("escape(%q) = %q, want %q", secret, got, want)
		}
	}
}

// An escape this build does not implement must fail, not hand back the value
// unchanged: the one encoding the producer ruled out is the raw value.
func TestEscapeRefusesAnEscapeItDoesNotImplement(t *testing.T) {
	for _, escape := range []basev0.ConfigurationValueEscape{
		basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_UNSPECIFIED,
		basev0.ConfigurationValueEscape(99),
	} {
		got, err := EscapeConfigurationValue(escape, "p@ss/word")
		if err == nil {
			t.Errorf("escape %d returned %q, want an error", escape, got)
		}
		if got != "" {
			t.Errorf("escape %d returned %q, want no value alongside the error", escape, got)
		}
	}
}

func connectionTemplate(escape basev0.ConfigurationValueEscape) *basev0.ConfigurationValueTemplate {
	return &basev0.ConfigurationValueTemplate{Segments: []*basev0.ConfigurationValueTemplateSegment{
		// The literals are already interpolated here: a producer writes
		// ${endpoint:…} in them and InterpolateConfigurationEndpoints resolves it
		// before a template is ever evaluated.
		templateLiteral("postgresql://reader:"),
		templateReference("postgres", "POSTGRES_READ_ONLY_PASSWORD", escape),
		templateLiteral("@store.example.svc.cluster.local:5432/app?sslmode=disable"),
	}}
}

func TestEvaluatedConnectionStringRoundTripsThePassword(t *testing.T) {
	template := connectionTemplate(basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_URL_USERINFO)
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

// A producer that forgets the escape must not get verbatim insertion. Verbatim,
// this secret moves the host: parsers that split userinfo on the first "@" dial
// evil.example.com with the credential meant for store.example.
func TestEvaluateRefusesAReferenceThatStatesNoEscape(t *testing.T) {
	template := connectionTemplate(basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_UNSPECIFIED)
	assembled, err := EvaluateConfigurationValueTemplate(template, func(string, string) (string, bool) {
		return "pw@evil.example.com:5432/app?x=", true
	})
	if err == nil {
		t.Fatalf("an unstated escape must fail, assembled %q", assembled)
	}
	if !strings.Contains(err.Error(), "no escape") {
		t.Errorf("error should name the missing escape, got %v", err)
	}
	// Stating NONE explicitly is a producer's choice and still works.
	verbatim, err := EvaluateConfigurationValueTemplate(
		connectionTemplate(basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_NONE),
		func(string, string) (string, bool) { return "plain", true })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(verbatim, "reader:plain@") {
		t.Errorf("explicit NONE should insert verbatim, got %q", verbatim)
	}
}

func TestEvaluateRefusesAnUnsetReference(t *testing.T) {
	template := &basev0.ConfigurationValueTemplate{Segments: []*basev0.ConfigurationValueTemplateSegment{
		templateReference("postgres", "POSTGRES_PASSWORD", basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_NONE),
	}}
	if _, err := EvaluateConfigurationValueTemplate(template, func(string, string) (string, bool) { return "", false }); err == nil {
		t.Fatal("an unset reference must fail, not assemble an empty value")
	}
	// found-but-empty is the case a lookup built on GetConfigurationValue
	// reports, since that returns ("", nil) for a key it never found. An empty
	// password still parses as a connection string and fails only when dialed,
	// so it must fail here.
	if assembled, err := EvaluateConfigurationValueTemplate(template, func(string, string) (string, bool) { return "", true }); err == nil {
		t.Fatalf("a reference resolving to an empty value must fail, assembled %q", assembled)
	}
	if _, err := EvaluateConfigurationValueTemplate(template, nil); err == nil {
		t.Fatal("no lookup must fail rather than assemble literals only")
	}
}

// ProducerConfigurationValueLookup is the resolution a renderer must reproduce:
// scoped to one producer, Match-folded, and reporting not-found for anything a
// template cannot insert.
func TestProducerConfigurationValueLookup(t *testing.T) {
	conf := &basev0.Configuration{
		Origin: "mod/store",
		Infos: []*basev0.ConfigurationInformation{{
			Name: "postgres-main",
			ConfigurationValues: []*basev0.ConfigurationValue{
				{Key: "POSTGRES_PASSWORD", Value: "hunter2", Secret: true},
				{Key: "EMPTY", Value: "", Secret: true},
				{Key: "ASSEMBLED", Secret: true, Template: connectionTemplate(
					basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_NONE)},
			},
		}},
	}
	lookup := ProducerConfigurationValueLookup(conf)
	for _, c := range []struct {
		name          string
		configuration string
		key           string
		want          string
		found         bool
	}{
		{"exact", "postgres-main", "POSTGRES_PASSWORD", "hunter2", true},
		{"match folds case and separator", "POSTGRES_MAIN", "postgres-password", "hunter2", true},
		{"empty is not resolvable", "postgres-main", "EMPTY", "", false},
		{"an assembly is not a primitive", "postgres-main", "ASSEMBLED", "", false},
		{"absent key", "postgres-main", "NOPE", "", false},
		{"another producer's group is unreachable", "vault", "POSTGRES_PASSWORD", "", false},
	} {
		got, found := lookup(c.configuration, c.key)
		if got != c.want || found != c.found {
			t.Errorf("%s: lookup(%q, %q) = (%q, %v), want (%q, %v)", c.name, c.configuration, c.key, got, found, c.want, c.found)
		}
	}
}

func TestValidateTemplatedConfigurationValue(t *testing.T) {
	template := &basev0.ConfigurationValueTemplate{Segments: []*basev0.ConfigurationValueTemplateSegment{templateLiteral("x")}}
	cases := []struct {
		name  string
		value *basev0.ConfigurationValue
		ok    bool
	}{
		{"nil value", nil, false},
		{"no template", &basev0.ConfigurationValue{Key: "k", Value: "v"}, true},
		{"secret without value", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: template}, true},
		{"not secret", &basev0.ConfigurationValue{Key: "k", Template: template}, false},
		{"value and template", &basev0.ConfigurationValue{Key: "k", Value: "v", Secret: true, Template: template}, false},
		{"empty template", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: &basev0.ConfigurationValueTemplate{}}, false},
		{"empty segment", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: &basev0.ConfigurationValueTemplate{
			Segments: []*basev0.ConfigurationValueTemplateSegment{{}},
		}}, false},
		{"incomplete reference", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: &basev0.ConfigurationValueTemplate{
			Segments: []*basev0.ConfigurationValueTemplateSegment{templateReference("postgres", "", 1)},
		}}, false},
		{"unknown escape", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: &basev0.ConfigurationValueTemplate{
			Segments: []*basev0.ConfigurationValueTemplateSegment{templateReference("postgres", "KEY", 99)},
		}}, false},
		{"unstated escape", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: &basev0.ConfigurationValueTemplate{
			Segments: []*basev0.ConfigurationValueTemplateSegment{templateReference("postgres", "KEY", 0)},
		}}, false},
		// A name a lookup would not resolve to what it reads like: Match folds
		// case and "-"/"_", never surrounding whitespace, so this validated and
		// then resolved to nothing.
		{"padded configuration", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: &basev0.ConfigurationValueTemplate{
			Segments: []*basev0.ConfigurationValueTemplateSegment{templateReference(" postgres ", "KEY", 1)},
		}}, false},
		{"padded key", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: &basev0.ConfigurationValueTemplate{
			Segments: []*basev0.ConfigurationValueTemplateSegment{templateReference("postgres", "KEY\t", 1)},
		}}, false},
		// A literal is the template source in any engine a renderer translates
		// into, so one carrying that engine's delimiters cannot be reproduced.
		{"literal opens an action", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: &basev0.ConfigurationValueTemplate{
			Segments: []*basev0.ConfigurationValueTemplateSegment{templateLiteral("prefix{{ .password }}")},
		}}, false},
		{"literal closes an action", &basev0.ConfigurationValue{Key: "k", Secret: true, Template: &basev0.ConfigurationValueTemplate{
			Segments: []*basev0.ConfigurationValueTemplateSegment{templateLiteral("trailing }} brace")},
		}}, false},
	}
	for _, c := range cases {
		err := ValidateTemplatedConfigurationValue(c.value)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}
