// Package imagepolicy checks Dockerfile defaults against requirements supplied
// by a deployment target. It does not build images or replace artifact scanning.
package imagepolicy

import (
	_ "crypto/sha256" // register the image digest algorithm for reference.Parse
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/distribution/reference"
	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
)

var (
	// ErrUnpinnedBase identifies a base without a content digest.
	ErrUnpinnedBase = errors.New("base image is not pinned by digest")
	// ErrRegistryNotAllowed identifies a base outside the allowed registry paths.
	ErrRegistryNotAllowed = errors.New("base image registry is not allowed")
	// ErrMissingLabel identifies a required final-stage label without a known value.
	ErrMissingLabel = errors.New("required label is missing")
	// ErrUnresolvedBase identifies a base with an unknown default or invalid reference.
	ErrUnresolvedBase = errors.New("base image reference cannot be resolved")
	// ErrInvalidDockerfile identifies input the Dockerfile parser cannot read.
	ErrInvalidDockerfile = errors.New("invalid Dockerfile")
)

// Policy is what a deployment target requires. A zero Policy requires nothing.
type Policy struct {
	// AllowedRegistries contains registry hosts, optionally narrowed to repository
	// paths (for example registry.example/team). Matching respects path boundaries.
	// Empty permits any registry; familiar references are normalized to docker.io.
	AllowedRegistries []string
	// RequireDigest refuses a base named by tag alone.
	RequireDigest bool
	// RequiredLabels contains keys the final stage must set to known non-empty
	// values, directly or through an earlier stage in this Dockerfile.
	RequiredLabels []string
}

// Refusal is a named rule failing at a source line (zero for file-wide failures).
type Refusal struct {
	Rule   error
	Line   int
	Detail string
}

func (r Refusal) Error() string {
	return fmt.Sprintf("line %d: %s: %s", r.Line, r.Rule, r.Detail)
}
func (r Refusal) Unwrap() error { return r.Rule }

// Check returns all reference refusals, followed by missing final-stage labels.
// It checks every FROM using global ARG defaults, and the last stage's labels.
// Build argument overrides, alternate build targets, external image metadata and
// COPY --from images are outside this source check. Callers must also validate
// effective build inputs and scan the artifact before enforcing deployment policy.
func Check(dockerfile string, policy Policy) []Refusal {
	if !policy.RequireDigest && len(policy.AllowedRegistries) == 0 && len(policy.RequiredLabels) == 0 {
		return nil
	}
	parsed, err := parser.Parse(strings.NewReader(dockerfile))
	if err != nil {
		return []Refusal{{Rule: ErrInvalidDockerfile, Detail: err.Error()}}
	}
	stages, metaArgs, err := instructions.Parse(parsed.AST, nil)
	if err != nil {
		return []Refusal{{Rule: ErrInvalidDockerfile, Detail: err.Error()}}
	}
	if len(stages) == 0 {
		return []Refusal{{Rule: ErrInvalidDockerfile, Detail: "no build stage"}}
	}
	lex := shell.NewLex(parsed.EscapeToken)
	lex.SkipUnsetEnv = true // retain unknown variables rather than certifying an empty substitution
	global := values{}
	for _, command := range metaArgs {
		applyArgs(lex, command, global, nil, nil)
	}
	var refusals []Refusal
	previous := map[string]stageState{}
	var current stageState
	for _, stage := range stages {
		base, resolved := expand(lex, stage.BaseName, global)
		parent, internal := previous[strings.ToLower(base)]
		current = stageState{labels: values{}, args: values{}, env: values{}}
		if resolved && internal {
			current = stageState{maps.Clone(parent.labels), maps.Clone(parent.args), maps.Clone(parent.env)}
		} else if policy.RequireDigest || len(policy.AllowedRegistries) > 0 {
			line := stage.Location[0].Start.Line
			if !resolved {
				refusals = append(refusals, Refusal{ErrUnresolvedBase, line, stage.BaseName})
			} else {
				refusals = append(refusals, checkReference(base, line, policy)...)
			}
		}
		for _, command := range stage.Commands {
			switch command := command.(type) {
			case *instructions.ArgCommand:
				applyArgs(lex, *command, current.args, current.env, global)
			case *instructions.EnvCommand:
				applyPairs(lex, command.Env, current.env, current.variables())
			case *instructions.LabelCommand:
				applyPairs(lex, command.Labels, current.labels, current.variables())
			}
		}
		// Register only after checking the base: FROM alpine AS alpine still
		// names an external image, not itself.
		if stage.Name != "" {
			previous[strings.ToLower(stage.Name)] = current
		}
	}
	for _, key := range policy.RequiredLabels {
		if value, known := current.labels.Get(key); !known || value == "" {
			refusals = append(refusals, Refusal{ErrMissingLabel, 0, key})
		}
	}
	return refusals
}

func checkReference(base string, line int, policy Policy) []Refusal {
	if base == "scratch" {
		return nil
	}
	ref, err := reference.ParseNormalizedNamed(base)
	if err != nil {
		return []Refusal{{ErrUnresolvedBase, line, base}}
	}
	var refusals []Refusal
	if _, pinned := ref.(reference.Digested); policy.RequireDigest && !pinned {
		refusals = append(refusals, Refusal{ErrUnpinnedBase, line, base})
	}
	if len(policy.AllowedRegistries) > 0 && !allowed(ref.Name(), policy.AllowedRegistries) {
		refusals = append(refusals, Refusal{ErrRegistryNotAllowed, line, base})
	}
	return refusals
}

func allowed(name string, registries []string) bool {
	for _, registry := range registries {
		registry = strings.TrimSuffix(registry, "/")
		if registry != "" && (name == registry || strings.HasPrefix(name, registry+"/")) {
			return true
		}
	}
	return false
}

// A nil value shadows an inherited value whose replacement is unknown.
type values map[string]*string

func (v values) Get(key string) (string, bool) {
	if value := v[key]; value != nil {
		return *value, true
	}
	return "", false
}
func (v values) Keys() []string {
	keys := make([]string, 0, len(v))
	for key := range v {
		keys = append(keys, key)
	}
	return keys
}

type stageState struct{ labels, args, env values }

func (s stageState) variables() values {
	merged := maps.Clone(s.args)
	maps.Copy(merged, s.env) // ENV takes precedence over ARG
	return merged
}

func expand(lex *shell.Lex, word string, env values) (string, bool) {
	value, unmatched, err := lex.ProcessWord(word, env)
	return value, err == nil && len(unmatched) == 0
}

func applyArgs(lex *shell.Lex, command instructions.ArgCommand, args, env, global values) {
	for _, arg := range command.Args {
		if arg.Value == nil {
			if value, ok := global[arg.Key]; ok {
				args[arg.Key] = value
			}
			continue
		}
		variables := maps.Clone(args)
		maps.Copy(variables, env)
		if value, ok := expand(lex, *arg.Value, variables); ok {
			args[arg.Key] = &value
		} else {
			args[arg.Key] = nil
		}
	}
}

func applyPairs(lex *shell.Lex, pairs instructions.KeyValuePairs, dst, env values) {
	for _, pair := range pairs {
		key, keyOK := expand(lex, pair.Key, env)
		value, valueOK := expand(lex, pair.Value, env)
		if keyOK && valueOK {
			dst[key] = &value
		} else {
			dst[key] = nil
		}
	}
}
