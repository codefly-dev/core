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

func loadCellFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "cells", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCellContractCarriesExplicitEnvironment(t *testing.T) {
	for _, fixture := range []string{"password-auth.json", "managed-identity.json"} {
		t.Run(fixture, func(t *testing.T) {
			contract, err := ParseCellContract(loadCellFixture(t, fixture))
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

func TestCellContractDoesNotInferAuthentication(t *testing.T) {
	contract, err := ParseCellContract(loadCellFixture(t, "password-auth.json"))
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

func TestCellContractSupportsLocalConfiguration(t *testing.T) {
	contract, err := ParseCellContract([]byte(`{"schema":"codefly/cell/v2","environment":{"name":"local","configuration-profile":"development","secrets":[{"kind":"provider","account":"team"}]}}`))
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

func TestCellContractRejectsInvalidDeclarations(t *testing.T) {
	cases := []struct {
		name        string
		field       string
		replacement string
		want        string
	}{
		{"old schema", `"codefly/cell/v2"`, `"codefly/cell/v1"`, "unsupported cell-contract schema"},
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
			data := strings.Replace(string(loadCellFixture(t, "managed-identity.json")), tc.field, tc.replacement, 1)
			if _, err := ParseCellContract([]byte(data)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	for _, data := range []string{
		`{"schema":"codefly/cell/v2","environment":{"name":"x","cluster":{}}}`,
		`{"schema":"codefly/cell/v2","environment":{"name":"x","registry":{}}}`,
		`{"schema":"codefly/cell/v2","environment":{"name":"x","gitops":{"repo-url":"url","path":"path"}}}`,
		`{"schema":"codefly/cell/v2","environment":{"name":"x","service-secrets":{"secret-store":{}}}}`,
		`{"schema":"codefly/cell/v2","environment":{}}`,
		`null`, `{} {}`,
	} {
		if _, err := ParseCellContract([]byte(data)); err == nil {
			t.Fatalf("accepted incomplete configuration: %s", data)
		}
	}
}

func TestCellContractRejectsProducerInventory(t *testing.T) {
	if _, err := ParseCellContract(loadCellFixture(t, "no-auth-declaration.json")); err == nil {
		t.Fatal("accepted legacy inventory instead of requiring producer migration")
	}
	for _, field := range []string{`"transport":{"mode":"proxy"}`, `"audit_sinks":[]`, `"password_auth":false`, `"databases":[]`, `"port_typo":1`} {
		data := strings.Replace(string(loadCellFixture(t, "managed-identity.json")), `"port": 8443`, `"port": 8443,`+field, 1)
		if _, err := ParseCellContract([]byte(data)); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected unknown-field rejection for %s, got %v", field, err)
		}
	}
}

func TestCellContractValidatesExplicitSecretReferences(t *testing.T) {
	for _, field := range []string{"name", "remote-key", "secret-store"} {
		var raw map[string]any
		if err := json.Unmarshal(loadCellFixture(t, "password-auth.json"), &raw); err != nil {
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
		if _, err := ParseCellContract(data); err == nil {
			t.Fatalf("accepted reference missing %s", field)
		}
	}
	data := strings.Replace(string(loadCellFixture(t, "password-auth.json")), `"property": "token"`, `"property": "token", "proprety": "other"`, 1)
	if _, err := ParseCellContract([]byte(data)); err == nil {
		t.Fatal("accepted unknown field inside custom secret reference decoder")
	}
}

func TestCellEnvironmentOwnsItsConfiguration(t *testing.T) {
	for _, fixture := range []string{"password-auth.json", "managed-identity.json"} {
		contract, err := ParseCellContract(loadCellFixture(t, fixture))
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

func TestCellContractRefusesRetargetingAndDirectInvalidValues(t *testing.T) {
	contract, err := ParseCellContract(loadCellFixture(t, "managed-identity.json"))
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
