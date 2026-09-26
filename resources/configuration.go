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
// A codefly carrier name embeds structural identity — the module and service
// names of a service configuration, the module/service/endpoint/API of an
// endpoint — and those are names, not configuration keys: a service called
// auth-gateway does not make its endpoint address a credential, and running the
// key classifier over the whole name says it does. So:
//
//   - a secret configuration carrier is always sensitive;
//   - an endpoint, self-endpoint or REST-route carrier is an address, and only
//     an operator-chosen prefix in front of it (WithPublicEnvironmentVariablePrefix)
//     is classified;
//   - a service configuration carrier is classified by its configuration name
//     and key, without the module and service segments;
//   - every other name, including a workspace configuration carrier (which has
//     no identity segments), is classified whole by IsSensitiveKey.
func IsSensitiveEnvironmentVariable(name string) bool {
	canonical := strings.ToUpper(name)
	for _, prefix := range secretCarrierPrefixes {
		if strings.HasPrefix(canonical, prefix) {
			return true
		}
	}
	for _, prefix := range addressCarrierPrefixes {
		if at := strings.Index(canonical, prefix); at >= 0 {
			return IsSensitiveKey(canonical[:at])
		}
	}
	if rest, ok := strings.CutPrefix(canonical, ServiceConfigurationPrefix+"__"); ok {
		// rest is MODULE__SERVICE__NAME__KEY (UniqueToKey, then NameToKey).
		if segments := strings.SplitN(rest, "__", 3); len(segments) == 3 {
			return IsSensitiveKey(segments[2])
		}
	}
	return IsSensitiveKey(canonical)
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
