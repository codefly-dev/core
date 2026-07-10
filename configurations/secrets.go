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
//
// References are safe to commit; the resolved value never touches disk. Only
// known schemes are treated as references — a plaintext value that happens to
// contain "://" (a postgres:// URL, say) is passed through untouched. The
// resolver seam (SecretResolver) is designed so more backends can be added.
const OnePasswordScheme = "op"

// ProviderOnePassword is the `secrets.kind` that selects the 1Password backend.
const ProviderOnePassword = "1password"

var secretReferenceSchemes = map[string]bool{
	OnePasswordScheme: true,
}

// SecretReference is a parsed secret URI, e.g. op://vault/item/field.
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
// via its `secrets` block. No providers means plaintext-only (no resolvers):
// secret values are used verbatim from the files.
func ResolversFromEnvironment(env *resources.Environment) ([]SecretResolver, error) {
	if env == nil {
		return nil, nil
	}
	var resolvers []SecretResolver
	for _, provider := range env.Secrets {
		switch provider.Kind {
		case ProviderOnePassword:
			resolvers = append(resolvers, NewOnePasswordResolver(provider.Account))
		default:
			return nil, fmt.Errorf("unknown secret provider %q (supported: %s)",
				provider.Kind, ProviderOnePassword)
		}
	}
	return resolvers, nil
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

// runCommand runs a resolver's CLI and returns its stdout with a single
// trailing newline stripped — the one the CLI appends. Leading and internal
// whitespace is preserved so a multi-line secret (a PEM key, say) survives
// intact. Only stderr is surfaced on failure so a resolved secret value never
// reaches an error or a log.
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
	out := strings.TrimSuffix(stdout.String(), "\n")
	out = strings.TrimSuffix(out, "\r")
	return out, nil
}

// secretResolution resolves references across a single Load pass, caching by
// URI so an item referenced twice is fetched once.
type secretResolution struct {
	resolvers map[string]SecretResolver
	cache     map[string]string
	warned    map[string]bool
}

func newSecretResolution(resolvers []SecretResolver) *secretResolution {
	m := make(map[string]SecretResolver)
	for _, r := range resolvers {
		if _, ok := m[r.Scheme()]; !ok {
			m[r.Scheme()] = r
		}
	}
	return &secretResolution{resolvers: m, cache: make(map[string]string), warned: make(map[string]bool)}
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
		dec := json.NewDecoder(bytes.NewReader(data.Content))
		dec.UseNumber()
		if err := dec.Decode(&node); err != nil {
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
	case map[any]any:
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
	if len(e.resolvers) == 0 || env == nil || env.Local() || e.warned[origin] {
		return
	}
	e.warned[origin] = true
	w := wool.Get(ctx).In("configurations.secrets")
	w.Warn("plaintext secret used with a configured secret backend — dev-only, prefer an op://… reference",
		wool.Field("origin", origin), wool.Field("environment", env.Name))
}
