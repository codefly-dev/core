package resources

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"gopkg.in/yaml.v3"
)

const ConfigurationDocumentSchema = "codefly/configuration-document/v1"

// MaxConfigurationDocumentBytes bounds each carrier below the supported process
// environment's per-variable limit, including its metadata and escaping.
const MaxConfigurationDocumentBytes = 64 * 1024

type configurationDocument struct {
	Schema      string          `json:"schema"`
	Origin      string          `json:"origin"`
	Name        string          `json:"name"`
	Environment string          `json:"environment"`
	Secret      bool            `json:"secret"`
	Content     json.RawMessage `json:"content"`
}

// ConfigurationDocumentPrefix and SecretConfigurationDocumentPrefix start the
// carriers of structured configuration documents.
const (
	ConfigurationDocumentPrefix = "CODEFLY__CONFIGURATION_DOCUMENT_V1__"
	// #nosec G101 -- a carrier name prefix, not a credential.
	SecretConfigurationDocumentPrefix = "CODEFLY__SECRET_CONFIGURATION_DOCUMENT_V1__"
)

// ConfigurationDocumentKey keeps document identities separate from flat values
// without folding case, punctuation, or nested object keys together.
func ConfigurationDocumentKey(origin, name, environment string, secret bool) string {
	identity, _ := json.Marshal([]string{origin, name, environment})
	digest := sha256.Sum256(identity)
	prefix := ConfigurationDocumentPrefix
	if secret {
		prefix = SecretConfigurationDocumentPrefix
	}
	return prefix + hex.EncodeToString(digest[:])
}

func configurationDocumentVariable(origin, name, environment string, data *basev0.ConfigurationData) (*EnvironmentVariable, error) {
	if origin == "" || name == "" || environment == "" {
		return nil, fmt.Errorf("configuration document requires origin, name and environment")
	}
	if len(data.Content) > MaxConfigurationDocumentBytes {
		return nil, fmt.Errorf("configuration document exceeds %d bytes", MaxConfigurationDocumentBytes)
	}
	content := data.Content
	switch data.Kind {
	case "json":
		if !json.Valid(content) {
			return nil, fmt.Errorf("configuration document is not valid JSON")
		}
	case "yaml", "yml":
		var value any
		decoder := yaml.NewDecoder(bytes.NewReader(content))
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("configuration document is not valid YAML")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("configuration document must contain one YAML document")
		}
		var err error
		content, err = json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("configuration document must contain JSON-compatible YAML")
		}
	default:
		return nil, fmt.Errorf("configuration document format is unsupported; expected JSON or YAML")
	}
	document := configurationDocument{
		Schema: ConfigurationDocumentSchema, Origin: origin, Name: name,
		Environment: environment, Secret: data.Secret, Content: content,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("cannot encode configuration document")
	}
	if len(encoded) > MaxConfigurationDocumentBytes {
		return nil, fmt.Errorf("configuration document carrier exceeds %d bytes", MaxConfigurationDocumentBytes)
	}
	return Env(ConfigurationDocumentKey(origin, name, environment, data.Secret), string(encoded)), nil
}

// DecodeConfigurationDocument validates both the wire version and the complete
// requested scope. Errors never include document bytes, including secret data.
func DecodeConfigurationDocument(encoded, origin, name, environment string, secret bool) (json.RawMessage, error) {
	if len(encoded) > MaxConfigurationDocumentBytes {
		return nil, fmt.Errorf("configuration document carrier exceeds %d bytes", MaxConfigurationDocumentBytes)
	}
	var document configurationDocument
	decoder := json.NewDecoder(bytes.NewBufferString(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("invalid configuration document carrier")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("invalid configuration document carrier")
	}
	if document.Schema != ConfigurationDocumentSchema {
		return nil, fmt.Errorf("unsupported configuration document schema")
	}
	if origin == "" || name == "" || environment == "" || document.Origin != origin ||
		document.Name != name || document.Environment != environment || document.Secret != secret {
		return nil, fmt.Errorf("configuration document scope does not match the requested configuration")
	}
	if !json.Valid(document.Content) {
		return nil, fmt.Errorf("configuration document has no valid JSON content")
	}
	return document.Content, nil
}
