package configurations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// Secret values live in *.secret.* files as either a literal plaintext value
// (the local/dev-only escape hatch — unencrypted on disk) or a reference the
// configured backend resolves at Load() time. A reference is a URI whose
// scheme names a secret provider:
//
//	client_secret: op://dev-vault/auth0/client_secret
//	client_secret: aws-sm://codefly/dev/auth0#client_secret
//
// References are safe to commit; the resolved value never touches disk. Only
// these known schemes are treated as references — a plaintext value that
// happens to contain "://" (a postgres:// URL, say) is passed through
// untouched.
const (
	OnePasswordScheme       = "op"
	AWSSecretsManagerScheme = "aws-sm"
)

// Provider names selected via an environment's `secrets.provider`.
const (
	ProviderOnePassword       = "1password"
	ProviderAWSSecretsManager = "aws-secrets-manager"
)

var secretReferenceSchemes = map[string]bool{
	OnePasswordScheme:       true,
	AWSSecretsManagerScheme: true,
}

// SecretReference is a parsed secret URI: op://vault/item/field or
// aws-sm://secret-id#json-key.
type SecretReference struct {
	Scheme string
	Path   string
	Raw    string
}

// ParseSecretReference reports whether value is a known secret reference and,
// if so, splits it into scheme and path. Unknown schemes return false so the
// value is treated as plaintext.
func ParseSecretReference(value string) (*SecretReference, bool) {
	scheme, path, found := strings.Cut(value, "://")
	if !found || !secretReferenceSchemes[scheme] {
		return nil, false
	}
	return &SecretReference{Scheme: scheme, Path: path, Raw: value}, true
}

// SecretResolver resolves references for a single backend.
type SecretResolver interface {
	Scheme() string
	Resolve(ctx context.Context, ref *SecretReference) (string, error)
}

// ResolversFromEnvironment builds the secret resolvers an environment selects
// via its `secrets` block. An empty provider means plaintext-only (no
// resolvers): secret values are used verbatim from the files.
func ResolversFromEnvironment(env *resources.Environment) ([]SecretResolver, error) {
	if env == nil || env.Secrets == nil || env.Secrets.Provider == "" {
		return nil, nil
	}
	switch env.Secrets.Provider {
	case ProviderOnePassword:
		return []SecretResolver{NewOnePasswordResolver(env.Secrets.Account)}, nil
	case ProviderAWSSecretsManager:
		return []SecretResolver{NewAWSSecretsManagerResolver(env.Secrets.Region)}, nil
	default:
		return nil, fmt.Errorf("unknown secret provider %q (supported: %s, %s)",
			env.Secrets.Provider, ProviderOnePassword, ProviderAWSSecretsManager)
	}
}

// OnePasswordResolver resolves op://vault/item/field references through the
// 1Password `op` CLI. `op read` accepts the full URI and prints the field
// value to stdout — biometric/session unlock is handled by the CLI itself.
type OnePasswordResolver struct {
	account string
	bin     string
}

func NewOnePasswordResolver(account string) *OnePasswordResolver {
	return &OnePasswordResolver{account: account, bin: "op"}
}

func (r *OnePasswordResolver) Scheme() string { return OnePasswordScheme }

func (r *OnePasswordResolver) Resolve(ctx context.Context, ref *SecretReference) (string, error) {
	args := []string{"read", "--no-newline"}
	if r.account != "" {
		args = append(args, "--account", r.account)
	}
	args = append(args, ref.Raw)
	return runCommand(ctx, r.bin, args...)
}

// AWSSecretsManagerResolver resolves aws-sm://secret-id[#json-key] references
// through the `aws` CLI, which authenticates via the ambient IAM credentials.
// Without a #json-key the whole SecretString is returned; with one the
// SecretString is parsed as JSON and the named key extracted.
type AWSSecretsManagerResolver struct {
	region string
	bin    string
}

func NewAWSSecretsManagerResolver(region string) *AWSSecretsManagerResolver {
	return &AWSSecretsManagerResolver{region: region, bin: "aws"}
}

func (r *AWSSecretsManagerResolver) Scheme() string { return AWSSecretsManagerScheme }

func (r *AWSSecretsManagerResolver) Resolve(ctx context.Context, ref *SecretReference) (string, error) {
	id, key, hasKey := strings.Cut(ref.Path, "#")
	args := []string{"secretsmanager", "get-secret-value", "--secret-id", id, "--query", "SecretString", "--output", "text"}
	if r.region != "" {
		args = append(args, "--region", r.region)
	}
	out, err := runCommand(ctx, r.bin, args...)
	if err != nil {
		return "", err
	}
	if !hasKey {
		return out, nil
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(out), &fields); err != nil {
		return "", fmt.Errorf("cannot parse secret %q as JSON to extract key %q: %w", id, key, err)
	}
	val, ok := fields[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret %q", key, id)
	}
	return fmt.Sprintf("%v", val), nil
}

// runCommand runs a resolver's CLI and returns its trimmed stdout. Only stderr
// is surfaced on failure so a resolved secret value never reaches an error or a log.
func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// secretResolution resolves references across a single Load pass, caching by
// URI so an item referenced twice is fetched once.
type secretResolution struct {
	resolvers map[string]SecretResolver
	cache     map[string]string
}

func newSecretResolution(resolvers []SecretResolver) *secretResolution {
	m := make(map[string]SecretResolver)
	for _, r := range resolvers {
		if _, ok := m[r.Scheme()]; !ok {
			m[r.Scheme()] = r
		}
	}
	return &secretResolution{resolvers: m, cache: make(map[string]string)}
}

func (e *secretResolution) resolve(ctx context.Context, ref *SecretReference) (string, error) {
	if v, ok := e.cache[ref.Raw]; ok {
		return v, nil
	}
	resolver, ok := e.resolvers[ref.Scheme]
	if !ok {
		return "", fmt.Errorf("secret reference %q requires the %q backend, which is not configured for this environment", ref.Raw, ref.Scheme)
	}
	v, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	e.cache[ref.Raw] = v
	return v, nil
}

// resolveString resolves value if it is a reference, otherwise returns it
// unchanged. changed reports whether a resolution happened.
func (e *secretResolution) resolveString(ctx context.Context, value string) (resolved string, changed bool, err error) {
	ref, ok := ParseSecretReference(value)
	if !ok {
		return value, false, nil
	}
	v, err := e.resolve(ctx, ref)
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (e *secretResolution) resolveConfiguration(ctx context.Context, conf *basev0.Configuration, env *resources.Environment) error {
	for _, info := range conf.Infos {
		for _, value := range info.ConfigurationValues {
			if !value.Secret {
				continue
			}
			resolved, changed, err := e.resolveString(ctx, value.Value)
			if err != nil {
				return err
			}
			if changed {
				value.Value = resolved
			} else {
				e.warnPlaintext(ctx, env, conf.Origin)
			}
		}
		if info.Data != nil && info.Data.Secret {
			if err := e.resolveData(ctx, info.Data, env, conf.Origin); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveData resolves references embedded in a structured secret blob
// (.secret.yaml / .secret.json). It parses the content, resolves any string
// references in place, and re-marshals only if something changed.
func (e *secretResolution) resolveData(ctx context.Context, data *basev0.ConfigurationData, env *resources.Environment, origin string) error {
	var node any
	switch data.Kind {
	case "yaml", "yml":
		if err := yaml.Unmarshal(data.Content, &node); err != nil {
			return err
		}
	case "json":
		if err := json.Unmarshal(data.Content, &node); err != nil {
			return err
		}
	default:
		return nil
	}
	resolved, changed, err := e.resolveNode(ctx, node)
	if err != nil {
		return err
	}
	if !changed {
		e.warnPlaintext(ctx, env, origin)
		return nil
	}
	var out []byte
	switch data.Kind {
	case "yaml", "yml":
		out, err = yaml.Marshal(resolved)
	case "json":
		out, err = json.Marshal(resolved)
	}
	if err != nil {
		return err
	}
	data.Content = out
	return nil
}

func (e *secretResolution) resolveNode(ctx context.Context, node any) (any, bool, error) {
	switch v := node.(type) {
	case string:
		return e.resolveString(ctx, v)
	case map[string]any:
		changed := false
		for key, val := range v {
			nv, c, err := e.resolveNode(ctx, val)
			if err != nil {
				return nil, false, err
			}
			if c {
				v[key] = nv
				changed = true
			}
		}
		return v, changed, nil
	case []any:
		changed := false
		for i, val := range v {
			nv, c, err := e.resolveNode(ctx, val)
			if err != nil {
				return nil, false, err
			}
			if c {
				v[i] = nv
				changed = true
			}
		}
		return v, changed, nil
	default:
		return node, false, nil
	}
}

// warnPlaintext flags a plaintext secret used where a backend is configured.
// Local envs stay silent — plaintext is the sanctioned local escape hatch.
func (e *secretResolution) warnPlaintext(ctx context.Context, env *resources.Environment, origin string) {
	if len(e.resolvers) == 0 || env == nil || env.Local() {
		return
	}
	w := wool.Get(ctx).In("configurations.secrets")
	w.Warn("plaintext secret used with a configured secret backend — dev-only, prefer a op://… or aws-sm://… reference",
		wool.Field("origin", origin), wool.Field("environment", env.Name))
}
