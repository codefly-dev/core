package resources

import (
	"context"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// templatedStoreConfiguration is a producer that hands out an assembled
// connection string: the password is the only thing in the secret store.
func templatedStoreConfiguration() *basev0.Configuration {
	return &basev0.Configuration{
		Origin: "mod/store",
		Infos: []*basev0.ConfigurationInformation{{
			Name: "postgres",
			ConfigurationValues: []*basev0.ConfigurationValue{
				{Key: "POSTGRES_PASSWORD", Value: "p@ss/w:rd", Secret: true},
				{Key: "CONNECTION", Secret: true, Template: &basev0.ConfigurationValueTemplate{
					Segments: []*basev0.ConfigurationValueTemplateSegment{
						templateLiteral("postgresql://reader:"),
						templateReference("postgres", "POSTGRES_PASSWORD",
							basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_URL_USERINFO),
						templateLiteral("@store:5432/app"),
					},
				}},
			},
		}},
	}
}

func envByKey(t *testing.T, envs []*EnvironmentVariable) map[string]string {
	t.Helper()
	out := make(map[string]string, len(envs))
	for _, env := range envs {
		out[env.Key] = env.ValueAsString()
	}
	return out
}

// A templated value used to reach the workload as KEY= — no value, no error.
// The service boots, serves, and is simply absent from whatever it should have
// connected to.
func TestConfigurationAsEnvironmentVariablesAssemblesATemplatedSecret(t *testing.T) {
	envs, err := ConfigurationAsEnvironmentVariables(templatedStoreConfiguration(), "production", true)
	if err != nil {
		t.Fatal(err)
	}
	byKey := envByKey(t, envs)
	carrier := "CODEFLY__SERVICE_SECRET_CONFIGURATION__MOD__STORE__POSTGRES__CONNECTION"
	want := "postgresql://reader:p%40ss%2Fw%3Ard@store:5432/app"
	if got := byKey[carrier]; got != want {
		t.Errorf("%s = %q, want %q", carrier, got, want)
	}
	if _, present := byKey[""]; present {
		t.Error("no carrier should be nameless")
	}
}

func TestConfigurationAsRawEnvironmentVariablesAssemblesATemplatedSecret(t *testing.T) {
	envs, err := ConfigurationAsRawEnvironmentVariables(templatedStoreConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	if got := envByKey(t, envs)["CONNECTION"]; got != "postgresql://reader:p%40ss%2Fw%3Ard@store:5432/app" {
		t.Errorf("CONNECTION = %q", got)
	}
}

// An unassemblable template must fail the delivery rather than emit nothing:
// this is the exact state a producer reaches when it renames the primitive it
// references.
func TestConfigurationAsEnvironmentVariablesFailsOnAnUnassemblableTemplate(t *testing.T) {
	conf := templatedStoreConfiguration()
	conf.Infos[0].ConfigurationValues[0].Key = "RENAMED"
	if envs, err := ConfigurationAsEnvironmentVariables(conf, "production", true); err == nil {
		t.Fatalf("expected an error, got %v", EnvironmentVariableAsStrings(envs))
	}
	if _, err := ConfigurationAsRawEnvironmentVariables(conf); err == nil {
		t.Fatal("raw injection must fail too")
	}
}

// A malformed template is a loud error at delivery, not a silently skipped
// value: before this it passed straight through, since nothing called the
// validator at all.
func TestConfigurationAsEnvironmentVariablesFailsOnAMalformedTemplate(t *testing.T) {
	conf := templatedStoreConfiguration()
	conf.Infos[0].ConfigurationValues[1].Secret = false
	if _, err := ConfigurationAsEnvironmentVariables(conf, "production", false); err == nil {
		t.Fatal("a template on a non-secret value must fail")
	}
}

// A render that carries no secret values does not assemble: the assembly is
// delivered by the secret reference declared for the carrier, and emitting the
// empty string the value literally holds would unset the credential.
func TestManagerDeliversTemplatesByReferenceWhenAsked(t *testing.T) {
	build := func() *EnvironmentVariableManager {
		manager := NewEnvironmentVariableManager()
		manager.SetEnvironment(&basev0.Environment{Name: "production"})
		if err := manager.AddConfigurations(context.Background(), templatedStoreConfiguration()); err != nil {
			t.Fatal(err)
		}
		return manager
	}
	carrier := "CODEFLY__SERVICE_SECRET_CONFIGURATION__MOD__STORE__POSTGRES__CONNECTION"

	byValue := build()
	secrets, err := byValue.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	if _, present := envByKey(t, secrets)[carrier]; !present {
		t.Errorf("by value, %s must be emitted assembled", carrier)
	}

	byReference := build()
	byReference.DeliverConfigurationTemplatesByReference()
	secrets, err = byReference.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	byKey := envByKey(t, secrets)
	if value, present := byKey[carrier]; present {
		t.Errorf("by reference, %s must not be emitted, got %q", carrier, value)
	}
	// The primitive is still a secret value of its own and is unaffected.
	if _, present := byKey["CODEFLY__SERVICE_SECRET_CONFIGURATION__MOD__STORE__POSTGRES__POSTGRES_PASSWORD"]; !present {
		t.Error("by reference, a plain secret value must still be emitted")
	}
	// A deployment scope derives from this manager, so it must carry the
	// decision: dropping it there would re-assemble the secret into the render.
	scoped := byReference.DeploymentScope()
	if err := scoped.AddConfigurations(context.Background(), templatedStoreConfiguration()); err != nil {
		t.Fatal(err)
	}
	scopedSecrets, err := scoped.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	if value, present := envByKey(t, scopedSecrets)[carrier]; present {
		t.Errorf("a deployment scope must inherit by-reference delivery, got %q", value)
	}
}

// The carrier name the restricted gate looks a secret reference up by has to be
// the one the emitter would have used.
func TestConfigurationValueEnvironmentKeyMatchesTheEmittedCarrier(t *testing.T) {
	conf := templatedStoreConfiguration()
	envs, err := ConfigurationAsEnvironmentVariables(conf, "production", true)
	if err != nil {
		t.Fatal(err)
	}
	emitted := envByKey(t, envs)
	for _, value := range conf.Infos[0].ConfigurationValues {
		key := ConfigurationValueEnvironmentKey(conf, conf.Infos[0].Name, value)
		if _, present := emitted[key]; !present {
			t.Errorf("%q is not a carrier the emitter produced: %v", key, EnvironmentVariableAsStrings(envs))
		}
	}
	public := &basev0.ConfigurationValue{Key: "HOST", Value: "store"}
	if got := ConfigurationValueEnvironmentKey(conf, "postgres", public); got != "CODEFLY__SERVICE_CONFIGURATION__MOD__STORE__POSTGRES__HOST" {
		t.Errorf("public carrier = %q", got)
	}
}

// Reading .Value by name is the same silent-empty hazard as emitting it: a
// consumer that asks for a templated value must get the assembly, or an error,
// never "" with a nil error.
func TestGetConfigurationValueAssemblesATemplatedValue(t *testing.T) {
	conf := templatedStoreConfiguration()
	got, err := GetConfigurationValue(context.Background(), conf, "postgres", "CONNECTION")
	if err != nil {
		t.Fatal(err)
	}
	if got != "postgresql://reader:p%40ss%2Fw%3Ard@store:5432/app" {
		t.Errorf("CONNECTION = %q", got)
	}
	// An unassemblable template is an error, not an empty string.
	conf.Infos[0].ConfigurationValues[0].Key = "RENAMED"
	if got, err := GetConfigurationValue(context.Background(), conf, "postgres", "CONNECTION"); err == nil {
		t.Errorf("expected an error, got %q", got)
	}
}

// One ConfigurationInformation cannot resolve an assembly — the references are
// scoped to the whole Configuration — so it must say so rather than hand back
// the empty string the value holds.
func TestConfigurationValueRefusesATemplatedValue(t *testing.T) {
	conf := templatedStoreConfiguration()
	got, err := ConfigurationValue(context.Background(), conf.Infos[0], "CONNECTION")
	if err == nil {
		t.Fatalf("expected an error, got %q", got)
	}
	if !strings.Contains(err.Error(), "GetConfigurationValue") {
		t.Errorf("the error should name what to use instead, got %v", err)
	}
	if plain, err := ConfigurationValue(context.Background(), conf.Infos[0], "POSTGRES_PASSWORD"); err != nil || plain != "p@ss/w:rd" {
		t.Errorf("a plain value must still resolve: %q, %v", plain, err)
	}
}

// A ${endpoint:…} a producer wrote in a template literal is the only place a
// templated value's text lives. The dependency-ordering readers collect
// references to order a consumer after its producers; missing these starts the
// service before the address in its connection string exists.
func TestConfigurationValueEndpointReferencesSeesTemplateLiterals(t *testing.T) {
	value := &basev0.ConfigurationValue{Key: "CONNECTION", Secret: true, Template: &basev0.ConfigurationValueTemplate{
		Segments: []*basev0.ConfigurationValueTemplateSegment{
			templateLiteral("postgresql://reader:"),
			templateReference("postgres", "POSTGRES_PASSWORD",
				basev0.ConfigurationValueEscape_CONFIGURATION_VALUE_ESCAPE_URL_USERINFO),
			templateLiteral("@${endpoint:mod/store/postgres}/app"),
		},
	}}
	got := ConfigurationValueEndpointReferences(value)
	if len(got) != 1 || got[0] != "mod/store/postgres" {
		t.Errorf("references = %v, want [mod/store/postgres]", got)
	}
	plain := &basev0.ConfigurationValue{Key: "URL", Value: "${endpoint:mod/other/http}/v1"}
	if got := ConfigurationValueEndpointReferences(plain); len(got) != 1 || got[0] != "mod/other/http" {
		t.Errorf("a plain value must still be read: %v", got)
	}
}
