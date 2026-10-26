package companions_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/codefly-dev/core/companions"
	"github.com/stretchr/testify/require"
)

// repositoryRoot is the checkout the specs' paths are relative to.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filename))
}

func buildSpecs(t *testing.T) []companions.BuildSpec {
	t.Helper()
	specs, err := companions.BuildSpecs()
	require.NoError(t, err)
	require.NotEmpty(t, specs)
	return specs
}

func dockerfile(t *testing.T, spec companions.BuildSpec) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repositoryRoot(t), filepath.FromSlash(spec.Dockerfile)))
	require.NoError(t, err)
	return string(content)
}

// The specs are what the builder is handed; a path that does not resolve in
// this checkout is a build that fails on the builder's machine, not here.
func TestBuildSpecsResolveAgainstTheCheckout(t *testing.T) {
	root := repositoryRoot(t)
	for _, spec := range buildSpecs(t) {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(spec.Dockerfile)))
		require.NoErrorf(t, err, "%s dockerfile", spec.Name)
		require.Truef(t, info.Mode().IsRegular(), "%s dockerfile is not a file", spec.Name)

		info, err = os.Stat(filepath.Join(root, filepath.FromSlash(spec.Context)))
		require.NoErrorf(t, err, "%s context", spec.Name)
		require.Truef(t, info.IsDir(), "%s context is not a directory", spec.Name)

		require.NotEmptyf(t, spec.Version, "%s has no version", spec.Name)
		require.NotEmptyf(t, spec.Platforms, "%s declares no platform", spec.Name)
	}
}

// Every companion directory that carries a Dockerfile must have a spec, or the
// builder silently stops publishing an image this repository still ships.
func TestEveryCompanionDockerfileHasABuildSpec(t *testing.T) {
	root := repositoryRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "companions"))
	require.NoError(t, err)

	specified := map[string]struct{}{}
	for _, spec := range buildSpecs(t) {
		specified[spec.Name] = struct{}{}
	}
	onDisk := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "companions", entry.Name(), "Dockerfile")); err != nil {
			continue
		}
		require.Containsf(t, specified, entry.Name(), "companions/%s has a Dockerfile but no build spec", entry.Name())
		onDisk++
	}
	specs := buildSpecs(t)
	require.Len(t, specified, len(specs), "two build specs share a name")
	require.Equal(t, onDisk, len(specs), "a build spec names a companion directory that has no Dockerfile")
}

// The build inputs name an image and a version; where it is published is the
// builder's decision, so no registry may leak into what this repository hands over.
func TestBuildSpecsNameNoRegistry(t *testing.T) {
	for _, spec := range buildSpecs(t) {
		fields := append([]string{spec.Name, spec.Version, spec.Dockerfile, spec.Context, spec.Base, spec.CLIBinary}, spec.Platforms...)
		for _, field := range fields {
			require.NotContainsf(t, field, "ghcr.io", "%s build spec names a registry: %q", spec.Name, field)
			require.NotContainsf(t, field, "codeflydev/", "%s build spec names a registry: %q", spec.Name, field)
		}
	}
}

// The builder walks the specs in order, so a base has to be built — and
// therefore pushed and resolvable — before anything that builds on it.
func TestBuildSpecsOrderEveryBaseBeforeItsDependents(t *testing.T) {
	position := map[string]int{}
	for index, spec := range buildSpecs(t) {
		if spec.Base != "" {
			base, ok := position[spec.Base]
			require.Truef(t, ok, "%s builds on %s, which is not built before it", spec.Name, spec.Base)
			require.Less(t, base, index)
		}
		position[spec.Name] = index
	}
}

// The base image is an argument so the builder can point it at whichever
// registry it publishes to. Its default is a pin that has to track the base
// companion's own manifest, or a dependent builds on a tag that never exists.
func TestDependentDockerfilesDefaultToTheBaseCompanionVersion(t *testing.T) {
	specs := buildSpecs(t)
	version := map[string]string{}
	for _, spec := range specs {
		version[spec.Name] = spec.Version
	}

	dependents := 0
	for _, spec := range specs {
		if spec.Base == "" {
			require.NotContainsf(t, dockerfile(t, spec), companions.BaseImageArg,
				"%s declares %s but no base companion", spec.Name, companions.BaseImageArg)
			continue
		}
		dependents++
		content := dockerfile(t, spec)
		require.Containsf(t, content, "ARG "+companions.BaseImageArg+"=",
			"%s must resolve its base image through the %s build argument", spec.Name, companions.BaseImageArg)
		require.Containsf(t, content, "${"+companions.BaseImageArg+"}",
			"%s declares %s without using it", spec.Name, companions.BaseImageArg)
		require.Containsf(t, content, "/"+spec.Base+":"+version[spec.Base],
			"%s pins a base other than %s:%s", spec.Name, spec.Base, version[spec.Base])
	}
	require.NotZero(t, dependents)
}

// The builder stages the cross-compiled CLI before the build; a path that
// drifts from the Dockerfile's COPY produces an image with no codefly binary.
func TestCompanionDockerfilesCopyTheDeclaredCLIBinary(t *testing.T) {
	staged := 0
	for _, spec := range buildSpecs(t) {
		content := dockerfile(t, spec)
		if spec.CLIBinary == "" {
			require.NotContainsf(t, content, "bin/linux/", "%s copies a CLI binary it does not declare", spec.Name)
			continue
		}
		staged++
		require.Containsf(t, content, "COPY "+spec.CLIBinary, "%s does not copy its declared CLI binary", spec.Name)
	}
	require.NotZero(t, staged)
}

// ${TARGETARCH} is expanded per target platform, so a binary staged without it
// is the build host's architecture baked into every platform's image.
func TestStagedCLIBinariesAreArchitectureAware(t *testing.T) {
	for _, spec := range buildSpecs(t) {
		if spec.CLIBinary == "" {
			continue
		}
		require.Containsf(t, spec.CLIBinary, "${TARGETARCH}",
			"%s stages an architecture-blind CLI binary but targets %v", spec.Name, spec.Platforms)
	}
}

// The publisher builds the codefly base first and treats every other
// companion as a leaf whose failure the run can survive, so it is correct only
// while every base is codefly. A companion introducing a second base tier has
// to teach the publisher about it, and this fails first rather than publishing
// a dependent against a missing base.
func TestEveryDependentBuildsOnTheCodeflyBase(t *testing.T) {
	for _, spec := range buildSpecs(t) {
		if spec.Base == "" {
			continue
		}
		require.Equalf(t, "codefly", spec.Base,
			"%s builds on %s, and the builder only orders the codefly base", spec.Name, spec.Base)
	}
}

// Where a companion is published is the builder's decision. The one reference
// core still carries is the base-image argument's default, which the publisher
// overrides with the digest it just pushed; any other registry literal is a
// build that silently ignores the builder's registry.
func TestRegistryLiteralsAreConfinedToTheBaseImageDefault(t *testing.T) {
	for _, spec := range buildSpecs(t) {
		for number, line := range strings.Split(dockerfile(t, spec), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if !strings.Contains(trimmed, "ghcr.io") && !strings.Contains(trimmed, "codeflydev/") {
				continue
			}
			require.Truef(t, strings.HasPrefix(trimmed, "ARG "+companions.BaseImageArg+"="),
				"%s:%d names a registry outside the %s default: %s",
				spec.Dockerfile, number+1, companions.BaseImageArg, trimmed)
		}
	}
}

// This is a constraint of today's publisher, not a property of companions.
// `codefly companion publish` derives the Dockerfile path from the companion
// name, builds every image from the core directory, and applies one --platform
// value to the whole run, so a spec asking for a narrower context or a
// different platform set is a spec the publisher silently ignores — the image
// is built some other way than the specs describe, and nothing reports it.
//
// The fix when a companion genuinely needs its own context is to teach the
// publisher to read these specs, not to loosen this test.
func TestBuildSpecsMatchWhatTheCurrentPublisherApplies(t *testing.T) {
	specs := buildSpecs(t)
	for _, spec := range specs {
		require.Equalf(t, ".", spec.Context,
			"%s declares the context %q, but the publisher builds every companion from the repository root", spec.Name, spec.Context)
		require.Equalf(t, specs[0].Platforms, spec.Platforms,
			"%s declares the platforms %v, but the publisher applies %v to the whole run", spec.Name, spec.Platforms, specs[0].Platforms)
	}
}

// A COPY spread over continued lines is one instruction: its later sources
// are inputs too, and a comment inside it is not part of it.
func TestInstructionsJoinContinuedLines(t *testing.T) {
	dockerfile := "FROM alpine\n# a comment\nCOPY a \\\n  # inside\n  b /dst/\nRUN true\n"
	instructions := companions.Instructions(dockerfile)
	require.Len(t, instructions, 3)
	require.Equal(t, 3, instructions[1].Line)
	sources, kind := companions.CopySources(instructions[1].Text)
	require.Equal(t, companions.FromContext, kind)
	require.Equal(t, []string{"a", "b"}, sources)
}

func TestCopySourcesReadsEveryContextCopyShape(t *testing.T) {
	for line, expected := range map[string]struct {
		sources []string
		kind    companions.CopyKind
	}{
		"COPY facades/go /src":                          {[]string{"facades/go"}, companions.FromContext},
		"COPY a b /dst/":                                {[]string{"a", "b"}, companions.FromContext},
		"COPY --chown=1000:1000 facades/ts /opt":        {[]string{"facades/ts"}, companions.FromContext},
		"COPY --link --chown=root:root bin/x /bin/x":    {[]string{"bin/x"}, companions.FromContext},
		"  COPY bin/linux/${TARGETARCH}/codefly /bin/c": {[]string{"bin/linux/${TARGETARCH}/codefly"}, companions.FromContext},
		"COPY --from=builder /go/bin/buf /usr/local/":   {nil, companions.FromStage},
		"RUN apk add --no-cache bash":                   {nil, companions.NotCopy},
		"":                                              {nil, companions.NotCopy},
		"copy facades/py /opt":                          {[]string{"facades/py"}, companions.FromContext},
		"ADD entrypoint.sh /usr/local/bin/":             {[]string{"entrypoint.sh"}, companions.FromContext},
		"add --chown=1 a https://example.com/x.tgz /d/": {[]string{"a"}, companions.FromContext},
		"ADD git@github.com:o/r.git /src":               {nil, companions.FromContext},
		"COPY <<EOF /etc/conf":                          {nil, companions.FromContext},
		`COPY ["a", "b"]`:                               {nil, companions.Unparsed},
		"COPY onlyone":                                  {nil, companions.Unparsed},
	} {
		sources, kind := companions.CopySources(line)
		require.Equalf(t, expected.kind, kind, "kind for %q", line)
		require.Equalf(t, expected.sources, sources, "sources for %q", line)
	}
}

// A COPY source is resolved against the declared context, so one written
// relative to the companion directory instead reaches the builder as a path
// that does not exist.
func TestDockerfileCopySourcesResolveInTheDeclaredContext(t *testing.T) {
	root := repositoryRoot(t)
	for _, spec := range buildSpecs(t) {
		context := filepath.Join(root, filepath.FromSlash(spec.Context))
		for _, instruction := range companions.Instructions(dockerfile(t, spec)) {
			sources, kind := companions.CopySources(instruction.Text)
			require.NotEqualf(t, companions.Unparsed, kind,
				"%s:%d is a COPY this test cannot read, so nothing checks its sources: %s",
				spec.Dockerfile, instruction.Line, strings.TrimSpace(instruction.Text))
			if kind != companions.FromContext {
				continue
			}
			for _, source := range sources {
				// The builder cross-compiles the CLI into the context
				// immediately before the build, so it is the one source
				// absent from a clean checkout. Only the exact declared path
				// is exempt — a typo anywhere else under bin/ still fails.
				if spec.CLIBinary != "" && source == spec.CLIBinary {
					continue
				}
				_, err := os.Stat(filepath.Join(context, filepath.FromSlash(source)))
				require.NoErrorf(t, err, "%s:%d copies %s, which does not exist in the %s context",
					spec.Dockerfile, instruction.Line, source, spec.Context)
			}
		}
	}
}

// dockerIgnorePatterns returns the repository-root .dockerignore entries,
// comments and blanks removed.
func dockerIgnorePatterns(t *testing.T) []string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repositoryRoot(t), ".dockerignore"))
	require.NoError(t, err)

	var patterns []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	require.NotEmpty(t, patterns)
	return patterns
}

// Every companion builds from the repository root, so .dockerignore decides
// what a Dockerfile can still read. An entry that covers a path a build needs
// turns into a missing COPY source on the builder, not here.
func TestDockerIgnoreExcludesNothingTheBuildsNeed(t *testing.T) {
	var required []string
	for _, spec := range buildSpecs(t) {
		required = append(required, spec.Dockerfile)
		if spec.CLIBinary != "" {
			required = append(required, spec.CLIBinary)
		}
		for _, instruction := range companions.Instructions(dockerfile(t, spec)) {
			if sources, kind := companions.CopySources(instruction.Text); kind == companions.FromContext {
				required = append(required, sources...)
			}
		}
	}
	require.NotEmpty(t, required)

	for _, pattern := range dockerIgnorePatterns(t) {
		// The comparison below is a plain path-prefix test, which is only a
		// faithful reading of .dockerignore while the patterns are literal
		// paths. A glob or a negation needs a real matcher, so fail rather
		// than quietly report that nothing is excluded.
		require.NotContainsf(t, pattern, "*",
			"%q is a glob; this test compares literal path prefixes and would not see what it excludes", pattern)
		require.NotContainsf(t, pattern, "!",
			"%q is a negation; this test compares literal path prefixes and would not see what it re-includes", pattern)

		for _, path := range required {
			require.Falsef(t, path == pattern || strings.HasPrefix(path, pattern+"/"),
				".dockerignore excludes %q, which the companion builds copy as %q", pattern, path)
		}
	}
}
