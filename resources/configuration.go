package resources

import (
	"context"
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/internal/sensitive"
	"github.com/codefly-dev/core/wool"
)

const ConfigurationWorkspace = "_workspace_origin"

func HasConfigurationInformation(_ context.Context, conf *basev0.Configuration, name string) bool {
	for _, info := range conf.Infos {
		if info.Name == name {
			return true
		}
	}
	return false
}

func FindServiceConfiguration(_ context.Context, confs []*basev0.Configuration, runtimeContext *basev0.RuntimeContext, unique string) (*basev0.Configuration, error) {
	for _, conf := range confs {
		if conf.GetRuntimeContext().GetKind() == runtimeContext.GetKind() && conf.Origin == unique {
			return conf, nil
		}
	}
	return nil, fmt.Errorf("couldn't find service configuration: %s", unique)
}

func Match(s, other string) bool {
	return strings.ReplaceAll(strings.ToLower(s), "-", "_") == strings.ReplaceAll(strings.ToLower(other), "-", "_")
}

// GetConfigurationValue returns the value of the configuration key.
// If the configuration or the key is not found, it returns an empty string.
func GetConfigurationValue(ctx context.Context, conf *basev0.Configuration, name string, key string) (string, error) {
	w := wool.Get(ctx).In("GetConfigurationValue")
	if conf == nil {
		return "", w.NewError("configuration is nil")
	}
	for _, info := range conf.Infos {
		if Match(info.Name, name) {
			for _, value := range info.ConfigurationValues {
				if Match(value.Key, key) {
					// Not value.Value: a value whose producer declared an
					// assembly holds the empty string, and handing that back
					// with a nil error is a credential the caller believes it
					// read.
					return ConfigurationValueAsString(conf, value)
				}
			}
		}
	}
	return "", nil
}

// GetConfigurationInformation returns the configuration information that matches the name.
// If no configuration information is found, it returns nil.
func GetConfigurationInformation(ctx context.Context, conf *basev0.Configuration, name string) (*basev0.ConfigurationInformation, error) {
	w := wool.Get(ctx).In("GetConfigurationValue")
	if conf == nil {
		return nil, w.NewError("configuration is nil")
	}
	return FilterConfigurationInformation(ctx, name, conf.Infos...)
}

// FilterConfigurationInformation returns the first configuration information that matches the name.
// If no configuration information is found, it returns nil.
func FilterConfigurationInformation(_ context.Context, name string, infos ...*basev0.ConfigurationInformation) (*basev0.ConfigurationInformation, error) {
	for _, info := range infos {
		if Match(info.Name, name) {
			return info, nil
		}
	}
	return nil, nil
}

// ConfigurationValue returns the value of the configuration key.
// If the configuration or the key is not found, it returns an error.
func ConfigurationValue(_ context.Context, confInfo *basev0.ConfigurationInformation, key string) (string, error) {
	for _, value := range confInfo.ConfigurationValues {
		if Match(value.Key, key) {
			// An assembly resolves against the whole Configuration its producer
			// published, which one information does not carry. Returning
			// value.Value here would hand back an empty credential, so say what
			// the caller has to use instead.
			if value.GetTemplate() != nil {
				return "", fmt.Errorf("configuration value %s is declared as a template; resolve it with GetConfigurationValue over the whole configuration", key)
			}
			return value.Value, nil
		}
	}
	return "", fmt.Errorf("couldn't find configuration value: %s", key)
}

func FindWorkspaceConfiguration(_ context.Context, confs []*basev0.Configuration, name string) (*basev0.Configuration, error) {
	for _, conf := range confs {
		if !Match(conf.Origin, ConfigurationWorkspace) {
			continue
		}
		if len(conf.Infos) > 0 && Match(conf.Infos[0].Name, name) {
			return conf, nil
		}
	}
	return nil, fmt.Errorf("couldn't find workspace configuration: %s", name)
}

func FindConfigurations(configurations []*basev0.Configuration, runtime *basev0.RuntimeContext) []*basev0.Configuration {
	var found []*basev0.Configuration
	for _, conf := range configurations {
		if Match(conf.GetRuntimeContext().GetKind(), runtime.GetKind()) {
			found = append(found, conf)
		}
	}
	return found
}

func ConfigurationsHash(confs ...*basev0.Configuration) string {
	hasher := NewHasher()
	for _, conf := range confs {
		hasher.Add(ConfigurationHash(conf))
	}
	return hasher.Hash()
}

func ConfigurationHash(conf *basev0.Configuration) string {
	hasher := NewHasher()
	for _, info := range conf.Infos {
		hasher.Add(ConfigurationInformationHash(info))
	}
	return hasher.Hash()
}

func ConfigurationInformationHash(info *basev0.ConfigurationInformation) string {
	return HashString(info.String())
}

func ConfigurationInformationsHash(infos ...*basev0.ConfigurationInformation) (string, error) {
	hasher := NewHasher()
	for _, info := range infos {
		hasher.Add(ConfigurationInformationHash(info))
	}
	return hasher.Hash(), nil
}

func MakeManyConfigurationSummary(confs []*basev0.Configuration) string {
	var summary []string
	for _, conf := range confs {
		summary = append(summary, MakeConfigurationSummary(conf))
	}
	return strings.Join(summary, ", ")
}

func MakeConfigurationSummary(conf *basev0.Configuration) string {
	if conf == nil {
		return ""
	}
	var summary []string
	for _, info := range conf.Infos {
		summary = append(summary, MakeConfigurationInformationSummary(info))
	}
	return fmt.Sprintf("%s: %s", conf.Origin, strings.Join(summary, ", "))

}

func MakeConfigurationInformationSummary(info *basev0.ConfigurationInformation) string {
	var summary []string
	for _, value := range info.ConfigurationValues {
		summary = append(summary, MakeConfigurationValueSummary(value))
	}
	return fmt.Sprintf("%s->%s", info.Name, strings.Join(summary, ", "))
}

func MakeConfigurationValueSummary(value *basev0.ConfigurationValue) string {
	if value == nil {
		return ""
	}
	if value.Secret || IsSensitiveKey(value.Key) {
		return fmt.Sprintf("%s=****", value.Key)
	}
	return fmt.Sprintf("%s=%s", value.Key, value.Value)
}

// IsSensitiveKey recognizes conventional credential-bearing names even when a
// caller forgot to set ConfigurationValue.Secret. Secret metadata remains the
// primary signal; this is defense in depth for logs and diagnostics.
func IsSensitiveKey(key string) bool {
	return sensitive.Key(key)
}

// secretCarrierPrefixes name the environment carriers codefly only ever uses
// for secret values, whatever the configuration key inside them is called.
var secretCarrierPrefixes = []string{
	WorkspaceSecretConfigurationPrefix + "__",
	ServiceSecretConfigurationPrefix + "__",
	SecretConfigurationDocumentPrefix,
}

// addressCarrierPrefixes name the environment carriers whose name is pure
// structure — module, service, endpoint and API names, plus a route — and
// whose value is an address or a visibility, never a credential.
var addressCarrierPrefixes = []string{
	EndpointPrefix + "__",
	SelfEndpointPrefix + "__",
	RestRoutePrefix + "__",
}

// IsSensitiveEnvironmentVariable classifies an environment variable NAME, as
// it reaches a process or a rendered ConfigMap/Secret, by what it carries.
//
// A codefly carrier name embeds structure — the module and service names of a
// service configuration, the configuration (group) name of a service or
// workspace configuration, the module/service/endpoint/API of an endpoint —
// and structure is a name, not a configuration key: a service called
// auth-gateway does not make its endpoint address a credential, a public
// group called datasource-oauth does not make its providers list one, and
// running the key classifier over the whole name says they are. Whether a
// VALUE is a credential is declared where it is authored — a secret value
// rides a secret carrier, whatever its key — and the key classifier is the
// defence in depth over the one thing an author named the value by, its key.
// The OUTERMOST structure is read first, by its own grammar, before anything
// inside it is looked at: a configuration key that happens to spell an
// address carrier is still a key, and cannot select the address rule for the
// name that carries it. So, in this order:
//
//   - a secret configuration carrier is always sensitive;
//   - a service or workspace configuration carrier is classified by its
//     complete KEY alone, without the module, service and configuration-name
//     segments — the same segment the rendered-manifest validator reads;
//   - an endpoint, self-endpoint or REST-route carrier is an address: at the
//     start of the name, or behind an operator-chosen prefix
//     (WithPublicEnvironmentVariablePrefix) that is a plain prefix — one with
//     no double underscore, so it is no carrier of its own — and only that
//     prefix is classified;
//   - every other name is classified whole by IsSensitiveKey.
func IsSensitiveEnvironmentVariable(name string) bool {
	canonical := strings.ToUpper(name)
	for _, prefix := range secretCarrierPrefixes {
		if strings.HasPrefix(canonical, prefix) {
			return true
		}
	}
	if key, ok := ConfigurationCarrierKey(canonical); ok {
		return IsSensitiveKey(key)
	}
	for _, prefix := range addressCarrierPrefixes {
		if at := strings.Index(canonical, prefix); at >= 0 && !strings.Contains(canonical[:at], "__") {
			return IsSensitiveKey(canonical[:at])
		}
	}
	return IsSensitiveKey(canonical)
}

// ConfigurationCarrierKey returns the configuration KEY a configuration
// carrier name ends in, and whether the name is one: a service carrier is
// PREFIX__MODULE__SERVICE__NAME__KEY (UniqueToKey, then NameToKey twice), a
// workspace carrier PREFIX__NAME__KEY, under the public or the secret prefix.
// The structural segments in front of the key never contain a double
// underscore — a module name, a service name and a configuration name are
// each refused where they are loaded if their spelling would carry one
// (validateCarrierName, ConfigurationNameHasAmbiguousSpelling) — so the key
// is everything after the fixed number of them, whatever it contains. It is the one parser of a carrier's key: a reader that
// scans a carrier for credential words scans this segment, never the whole
// name, so the module, service and configuration-name segments cannot trip it.
func ConfigurationCarrierKey(name string) (string, bool) {
	canonical := strings.ToUpper(name)
	for _, carrier := range []struct {
		prefix    string
		structure int
	}{
		{ServiceConfigurationPrefix + "__", 3},
		{ServiceSecretConfigurationPrefix + "__", 3},
		{WorkspaceConfigurationPrefix + "__", 1},
		{WorkspaceSecretConfigurationPrefix + "__", 1},
	} {
		rest, ok := strings.CutPrefix(canonical, carrier.prefix)
		if !ok {
			continue
		}
		segments := strings.SplitN(rest, "__", carrier.structure+1)
		if len(segments) != carrier.structure+1 {
			return "", false
		}
		return segments[carrier.structure], true
	}
	return "", false
}

// ConfigurationNameHasAmbiguousSpelling reports a configuration name whose
// carrier spelling (NameToKey) would carry a double underscore — consecutive
// dashes or underscores in the name — and so could not be told apart from the
// structure/key separator by ConfigurationCarrierKey: "policy--auth" and
// "policy__auth" both spell POLICY__AUTH, and a carrier built from either reads
// as configuration "policy" with key "AUTH__…". The loader refuses such a name
// where the file is read, so no carrier is ever ambiguous.
func ConfigurationNameHasAmbiguousSpelling(name string) bool {
	return strings.Contains(NameToKey(name), "__")
}

// configRuntimeKind folds the nix runtime onto native for configuration
// matching. Both run on the host with native network access, and agents emit
// their connection configuration under the native (and container) contexts, not
// a separate "nix" one — so a nix runtime consumes the native config. Without
// this fold, a caller running with RuntimeContextNix would match no
// configuration and get an empty connection string.
//
// A Kubernetes workload folds onto container for the same reason: producers
// publish their in-cluster addresses under the container context.
func configRuntimeKind(kind string) string {
	if Match(kind, RuntimeContextNix) {
		return RuntimeContextNative
	}
	if Match(kind, RuntimeContextKubernetes) {
		return RuntimeContextContainer
	}
	return kind
}

// These protos cross a process boundary, so a nil config or nil RuntimeContext must
// never panic. The generated `Get*()` accessors are nil-safe (they return the zero
// value through a nil receiver), so reading the kind via `conf.GetRuntimeContext().
// GetKind()` yields "" instead of a deref — and nil configs are skipped outright.
func FilterConfigurations(configurations []*basev0.Configuration, runtimeContext *basev0.RuntimeContext) []*basev0.Configuration {
	var out []*basev0.Configuration
	for _, conf := range configurations {
		if conf == nil {
			continue
		}
		if Match(configRuntimeKind(conf.GetRuntimeContext().GetKind()), configRuntimeKind(runtimeContext.GetKind())) {
			out = append(out, conf)
		}
	}
	return out
}

func ExtractConfiguration(configurations []*basev0.Configuration, runtimeContext *basev0.RuntimeContext) (*basev0.Configuration, error) {
	var out *basev0.Configuration
	for _, conf := range configurations {
		if conf == nil {
			continue
		}
		if Match(configRuntimeKind(conf.GetRuntimeContext().GetKind()), configRuntimeKind(runtimeContext.GetKind())) {
			if out != nil {
				return nil, fmt.Errorf("multiple configurations found for runtime context: %s", runtimeContext.GetKind())
			}
			out = conf
		}
	}
	return out, nil
}
