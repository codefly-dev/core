package resources_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func documentConfiguration(kind, content string, secret bool) *basev0.Configuration {
	return &basev0.Configuration{Origin: "app/api", Infos: []*basev0.ConfigurationInformation{{
		Name: "settings", Data: &basev0.ConfigurationData{Kind: kind, Content: []byte(content), Secret: secret},
	}}}
}

func TestEnvironmentManagerPreservesStructuredConfiguration(t *testing.T) {
	ctx := context.Background()
	manager := resources.NewEnvironmentVariableManager()
	manager.SetEnvironment(&basev0.Environment{Name: "staging"})
	public := `{"nested":{"list":[null,true,9007199254740993,"00123"]}}`
	secret := `{"credentials":{"token":"private-sentinel"}}`
	require.NoError(t, manager.AddConfigurations(ctx, documentConfiguration("json", public, false), documentConfiguration("json", secret, true)))
	values, err := manager.Configurations()
	require.NoError(t, err)
	require.NotContains(t, strings.Join(resources.EnvironmentVariableAsStrings(values), "\n"), "private-sentinel")
	key := resources.ConfigurationDocumentKey("app/api", "settings", "staging", false)
	encoded, err := resources.FindValueInEnvironmentVariables(ctx, key, resources.EnvironmentVariableAsStrings(values))
	require.NoError(t, err)
	decoded, err := resources.DecodeConfigurationDocument(encoded, "app/api", "settings", "staging", false)
	require.NoError(t, err)
	require.Equal(t, public, string(decoded))
	secrets, err := manager.Secrets()
	require.NoError(t, err)
	require.Len(t, secrets, 1)
	decoded, err = resources.DecodeConfigurationDocument(secrets[0].ValueAsString(), "app/api", "settings", "staging", true)
	require.NoError(t, err)
	require.Equal(t, secret, string(decoded))
	all, err := manager.All()
	require.NoError(t, err)
	require.Len(t, all, len(values)+len(secrets))
}

func TestConfigurationDocumentRejectsInvalidInputWithoutDisclosingContents(t *testing.T) {
	for _, test := range []struct{ name, kind, content, environment string }{
		{"malformed json", "json", `{"private-sentinel":`, "staging"},
		{"malformed yaml", "yaml", "private-sentinel: [", "staging"},
		{"multiple yaml documents", "yaml", "private-sentinel: one\n---\nsecond: two", "staging"},
		{"unsupported", "xml", "private-sentinel", "staging"},
		{"oversized", "json", `"` + strings.Repeat("private-sentinel", resources.MaxConfigurationDocumentBytes) + `"`, "staging"},
		{"missing environment", "json", `"private-sentinel"`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			values, err := resources.ConfigurationAsEnvironmentVariables(documentConfiguration(test.kind, test.content, true), test.environment, true)
			require.Error(t, err)
			require.Nil(t, values)
			require.NotContains(t, err.Error(), "private-sentinel")
		})
	}
}

func TestConfigurationDocumentJSONTypesAndYAML(t *testing.T) {
	for _, content := range []string{`null`, `true`, `3.25`, `9007199254740993`, `"text"`, `[1,false,null]`, `{"nested":{"value":1}}`} {
		variables, err := resources.ConfigurationAsEnvironmentVariables(documentConfiguration("json", content, false), "staging", false)
		require.NoError(t, err)
		require.Len(t, variables, 1)
		decoded, err := resources.DecodeConfigurationDocument(variables[0].ValueAsString(), "app/api", "settings", "staging", false)
		require.NoError(t, err)
		require.Equal(t, content, string(decoded))
	}
	variables, err := resources.ConfigurationAsEnvironmentVariables(documentConfiguration("yaml", "nested:\n  count: 9007199254740993\n  list: [true, null]\n", false), "staging", false)
	require.NoError(t, err)
	decoded, err := resources.DecodeConfigurationDocument(variables[0].ValueAsString(), "app/api", "settings", "staging", false)
	require.NoError(t, err)
	require.Equal(t, `{"nested":{"count":9007199254740993,"list":[true,null]}}`, string(decoded))
}

func TestConfigurationDocumentRejectsScopeAndVersionConfusion(t *testing.T) {
	variables, err := resources.ConfigurationAsEnvironmentVariables(documentConfiguration("json", `{"token":"private-sentinel"}`, true), "staging", true)
	require.NoError(t, err)
	encoded := variables[0].ValueAsString()
	for _, scope := range []struct {
		origin, name, environment string
		secret                    bool
	}{
		{"other/api", "settings", "staging", true},
		{"app/api", "other", "staging", true},
		{"app/api", "settings", "production", true},
		{"app/api", "settings", "staging", false},
	} {
		_, err := resources.DecodeConfigurationDocument(encoded, scope.origin, scope.name, scope.environment, scope.secret)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private-sentinel")
	}
	for _, invalid := range []string{
		strings.Replace(encoded, resources.ConfigurationDocumentSchema, "codefly/configuration-document/v2", 1),
		strings.TrimSuffix(encoded, "}") + `,"unexpected":true}`,
		encoded + "{}",
	} {
		_, err := resources.DecodeConfigurationDocument(invalid, "app/api", "settings", "staging", true)
		require.Error(t, err)
	}
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(encoded), &object))
	delete(object, "content")
	invalid, err := json.Marshal(object)
	require.NoError(t, err)
	_, err = resources.DecodeConfigurationDocument(string(invalid), "app/api", "settings", "staging", true)
	require.Error(t, err)
	require.NotEqual(t, resources.ConfigurationDocumentKey("app/a-b", "settings", "staging", false), resources.ConfigurationDocumentKey("app/a_b", "settings", "staging", false))
}

func TestRawConfigurationRefusesStructuredData(t *testing.T) {
	manager := resources.NewEnvironmentVariableManager()
	document := documentConfiguration("json", `{"value":true}`, false)
	require.Error(t, manager.AddRawConfigurations(context.Background(), document))
	values, err := manager.All()
	require.NoError(t, err)
	require.Empty(t, values)
	values, err = resources.ConfigurationAsRawEnvironmentVariables(document)
	require.Error(t, err)
	require.Nil(t, values)

	flat := &basev0.Configuration{Infos: []*basev0.ConfigurationInformation{{
		ConfigurationValues: []*basev0.ConfigurationValue{{Key: "EXACT_key", Value: "value"}},
	}}}
	values, err = resources.ConfigurationAsRawEnvironmentVariables(flat)
	require.NoError(t, err)
	require.Equal(t, []string{"EXACT_key=value"}, resources.EnvironmentVariableAsStrings(values))
	require.NoError(t, manager.AddRawConfigurations(context.Background(), flat))
	flat.Infos = append(flat.Infos, document.Infos...)
	values, err = manager.All()
	require.Error(t, err, "documents introduced after admission must not disappear")
	require.Nil(t, values)
}
