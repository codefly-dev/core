package resources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func loadCoordinateFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "coordinates", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCoordinateContractCarriesExplicitEnvironment(t *testing.T) {
	for _, fixture := range []string{"password-auth.json", "managed-identity.json", "config-injection.json"} {
		t.Run(fixture, func(t *testing.T) {
			contract, err := ParseCoordinateContract(loadCoordinateFixture(t, fixture))
			if err != nil {
				t.Fatal(err)
			}
			env, err := contract.ToEnvironment("staging", "product")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*env, contract.Environment) {
				t.Fatalf("import changed declaration: %+v", env)
			}
			if _, invented := env.ManagedServices["store"]; invented {
				t.Fatal("import manufactured a store binding")
			}
			encoded, err := yaml.Marshal(env)
			if err != nil {
				t.Fatal(err)
			}
			var reloaded Environment
			if err := yaml.Unmarshal(encoded, &reloaded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*env, reloaded) {
				t.Fatalf("workspace serialization lost declarations: %s", encoded)
			}
		})
	}
}

func TestCoordinateContractDoesNotInferAuthentication(t *testing.T) {
	contract, err := ParseCoordinateContract(loadCoordinateFixture(t, "password-auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	env, err := contract.ToEnvironment("staging", "product")
	if err != nil {
		t.Fatal(err)
	}
	accounts := env.ManagedServices["accounts"]
	if len(accounts.SecretReferences) != 1 || accounts.SecretReferences[0].Name != "accounts-credentials" ||
		accounts.SecretReferences[0].RemoteKey != "owners/accounts/staging" {
		t.Fatalf("changed explicit secret references: %+v", accounts)
	}
	if len(env.ManagedServices["events"].SecretReferences) != 0 {
		t.Fatal("invented a secret for a service with no declared secret reference")
	}
	if env.Gitops.Branch != "release" || env.Gitops.Path != "reviewed/product" {
		t.Fatalf("changed explicit delivery target: %+v", env.Gitops)
	}
	if env.ServiceSecrets.Services["api"].RemoteKeys["API_KEY"].Property != "token" {
		t.Fatal("lost application secret configuration")
	}
}

// Config and secret injection needs target, config, secrets and identity only: a
// producer emitting for that flow declares no managed services, registry,
// ingress or delivery target, and the contract must admit it without them.
func TestCoordinateContractCarriesResolvedServiceConfig(t *testing.T) {
	contract, err := ParseCoordinateContract(loadCoordinateFixture(t, "config-injection.json"))
	if err != nil {
		t.Fatal(err)
	}
	env, err := contract.ToEnvironment("staging", "product")
	if err != nil {
		t.Fatal(err)
	}
	if len(env.ManagedServices) != 0 || env.Registry != nil || env.Gitops != nil {
		t.Fatalf("import invented infrastructure inventory: %+v", env)
	}
	api := env.ServiceConfig.Services["api"].Values
	if api["DATABASE_HOST"] != "product-staging.database.example" || api["DATABASE_PORT"] != "6432" {
		t.Fatalf("api values = %+v", api)
	}
	if got := env.ServiceConfig.Services["worker"].Values["DATABASE_HOST"]; got != "product-staging.database.example" {
		t.Fatalf("worker DATABASE_HOST = %q", got)
	}
	if got := env.ServiceSecrets.Services["api"].RemoteKeys["DATABASE_PASSWORD"].Property; got != "password" {
		t.Fatalf("secret half of the same service = %q", got)
	}
	api["DATABASE_HOST"] = "other.example"
	second, err := contract.ToEnvironment("staging", "product")
	if err != nil {
		t.Fatal(err)
	}
	if second.ServiceConfig.Services["api"].Values["DATABASE_HOST"] != "product-staging.database.example" {
		t.Fatal("mutating an imported value changed the source declaration")
	}
}

func TestCoordinateContractRejectsUnresolvedServiceConfig(t *testing.T) {
	cases := []struct {
		name        string
		field       string
		replacement string
		want        string
	}{
		{"empty value", `"6432"`, `""`, `value "DATABASE_PORT" is empty`},
		{"unknown field", `"values"`, `"value"`, "not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := strings.Replace(string(loadCoordinateFixture(t, "config-injection.json")), tc.field, tc.replacement, 1)
			if _, err := ParseCoordinateContract([]byte(data)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	for _, tc := range []struct {
		data string
		want string
	}{
		{`{"schema":"codefly/coordinate/v1","environment":{"name":"x","service-config":{}}}`, "declares no services"},
		{`{"schema":"codefly/coordinate/v1","environment":{"name":"x","service-config":{"services":{"api":{}}}}}`, `service "api" declares no values`},
	} {
		if _, err := ParseCoordinateContract([]byte(tc.data)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("want %q, got %v", tc.want, err)
		}
	}
}

// A key declared on both sides renders two entries of the same name into one
// container, where one silently overwrites the other. The contract refuses it
// rather than resolving it; the same key under a different service does not
// collide, and config-injection.json already carries that case.
func TestCoordinateContractRefusesKeyDeclaredAsBothValueAndSecret(t *testing.T) {
	data := strings.Replace(string(loadCoordinateFixture(t, "config-injection.json")), `"DATABASE_PASSWORD"`, `"DATABASE_PORT"`, 1)
	_, err := ParseCoordinateContract([]byte(data))
	if err == nil || !strings.Contains(err.Error(), `"DATABASE_PORT"`) || !strings.Contains(err.Error(), `service "api"`) {
		t.Fatalf("want a collision refusal naming the service and key, got %v", err)
	}
}

func TestCoordinateContractSupportsLocalConfiguration(t *testing.T) {
	contract, err := ParseCoordinateContract([]byte(`{"schema":"codefly/coordinate/v1","environment":{"name":"local","configuration-profile":"development","secrets":[{"kind":"provider","account":"team"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	env, err := contract.ToEnvironment("local", "")
	if err != nil {
		t.Fatal(err)
	}
	if env.Cluster != nil || env.Registry != nil || env.ConfigurationProfile != "development" || env.Secrets[0].Account != "team" {
		t.Fatalf("local configuration was reinterpreted: %+v", env)
	}
}

func TestCoordinateContractRejectsInvalidDeclarations(t *testing.T) {
	cases := []struct {
		name        string
		field       string
		replacement string
		want        string
	}{
		{"unsupported version", `"codefly/coordinate/v1"`, `"codefly/coordinate/v2"`, "unsupported coordinate-contract schema"},
		{"capability", `"managed-service-identity"`, `"unknown-capability"`, "unsupported capability"},
		{"principal", `"principal": "accounts-client"`, `"principal": " "`, "principal"},
		{"endpoint", `"external-name": "accounts.example"`, `"external-name": ""`, "endpoint"},
		{"port zero", `"port": 8443`, `"port": 0`, "port"},
		{"port high", `"port": 8443`, `"port": 65536`, "port"},
		{"port negative", `"port": 8443`, `"port": -1`, "port"},
		{"CIDR", `"192.0.2.0/24"`, `"invalid"`, "CIDR"},
		{"namespace", `"namespace": "product"`, `"namespace": "../product"`, "namespace"},
		{"service key", `"accounts": {`, `"../accounts": {`, "managed service"},
		{"duplicate field", `"port": 8443`, `"port": 8443, "port": 9443`, "already defined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := strings.Replace(string(loadCoordinateFixture(t, "managed-identity.json")), tc.field, tc.replacement, 1)
			if _, err := ParseCoordinateContract([]byte(data)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	for _, data := range []string{
		`{"schema":"codefly/coordinate/v1","environment":{"name":"x","cluster":{}}}`,
		`{"schema":"codefly/coordinate/v1","environment":{"name":"x","registry":{}}}`,
		`{"schema":"codefly/coordinate/v1","environment":{"name":"x","gitops":{"repo-url":"url","path":"path"}}}`,
		`{"schema":"codefly/coordinate/v1","environment":{"name":"x","service-secrets":{"secret-store":{}}}}`,
		`{"schema":"codefly/coordinate/v1","environment":{}}`,
		`null`, `{} {}`,
	} {
		if _, err := ParseCoordinateContract([]byte(data)); err == nil {
			t.Fatalf("accepted incomplete configuration: %s", data)
		}
	}
}

func TestCoordinateContractRejectsProducerInventory(t *testing.T) {
	if _, err := ParseCoordinateContract(loadCoordinateFixture(t, "no-auth-declaration.json")); err == nil {
		t.Fatal("accepted legacy inventory instead of requiring producer migration")
	}
	for _, field := range []string{`"transport":{"mode":"proxy"}`, `"audit_sinks":[]`, `"password_auth":false`, `"databases":[]`, `"port_typo":1`} {
		data := strings.Replace(string(loadCoordinateFixture(t, "managed-identity.json")), `"port": 8443`, `"port": 8443,`+field, 1)
		if _, err := ParseCoordinateContract([]byte(data)); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected unknown-field rejection for %s, got %v", field, err)
		}
	}
}

func TestCoordinateContractValidatesExplicitSecretReferences(t *testing.T) {
	for _, field := range []string{"name", "remote-key", "secret-store"} {
		var raw map[string]any
		if err := json.Unmarshal(loadCoordinateFixture(t, "password-auth.json"), &raw); err != nil {
			t.Fatal(err)
		}
		env := raw["environment"].(map[string]any)
		service := env["managed-services"].(map[string]any)["accounts"].(map[string]any)
		ref := service["secret-references"].([]any)[0].(map[string]any)
		delete(ref, field)
		data, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseCoordinateContract(data); err == nil {
			t.Fatalf("accepted reference missing %s", field)
		}
	}
	data := strings.Replace(string(loadCoordinateFixture(t, "password-auth.json")), `"property": "token"`, `"property": "token", "proprety": "other"`, 1)
	if _, err := ParseCoordinateContract([]byte(data)); err == nil {
		t.Fatal("accepted unknown field inside custom secret reference decoder")
	}
}

func TestCoordinateEnvironmentOwnsItsConfiguration(t *testing.T) {
	for _, fixture := range []string{"password-auth.json", "managed-identity.json"} {
		contract, err := ParseCoordinateContract(loadCoordinateFixture(t, fixture))
		if err != nil {
			t.Fatal(err)
		}
		before, err := yaml.Marshal(contract.Environment)
		if err != nil {
			t.Fatal(err)
		}
		first, err := contract.ToEnvironment("staging", "product")
		if err != nil {
			t.Fatal(err)
		}
		service := first.ManagedServices["accounts"]
		service.EgressCIDRs[0] = "0.0.0.0/0"
		if service.Identity != nil {
			service.Identity.Annotations["identity.example/principal"] = "other"
			service.Identity.Labels["identity.example/enabled"] = "false"
		}
		if len(service.SecretReferences) > 0 {
			service.SecretReferences[0].RemoteKey = "other"
			first.ServiceSecrets.Services["api"].RemoteKeys["API_KEY"] = EnvironmentSecretRemoteRef{Key: "other"}
		}
		second, err := contract.ToEnvironment("staging", "product")
		if err != nil {
			t.Fatal(err)
		}
		after, err := yaml.Marshal(second)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatal("mutating an imported environment changed the source or another import")
		}
	}
}

func TestCoordinateContractRefusesRetargetingAndDirectInvalidValues(t *testing.T) {
	contract, err := ParseCoordinateContract(loadCoordinateFixture(t, "managed-identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range [][2]string{{"other", "product"}, {"staging", "other"}, {"staging", "../product"}} {
		if _, err := contract.ToEnvironment(target[0], target[1]); err == nil {
			t.Fatalf("accepted retargeting to %v", target)
		}
	}
	service := contract.Environment.ManagedServices["accounts"]
	service.Port = 0
	contract.Environment.ManagedServices["accounts"] = service
	if _, err := contract.ToEnvironment("staging", "product"); err == nil {
		t.Fatal("directly constructed invalid value bypassed admission")
	}
}
