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

// The builder publishes the codefly base and then everything that builds on
// it, so it is correct only while every base is codefly. A companion
// introducing a second base tier has to teach the builder about it, and this
// fails first rather than publishing a dependent against a missing base.
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

// The builder stages one context and passes one platform list to every
// companion, so a spec that wants a narrower context or a different platform
// set is a spec the builder silently ignores.
func TestBuildSpecsShareOneContextAndPlatformSet(t *testing.T) {
	specs := buildSpecs(t)
	for _, spec := range specs {
		require.Equalf(t, ".", spec.Context,
			"%s builds from %q, but every companion is built from the repository root", spec.Name, spec.Context)
		require.Equalf(t, specs[0].Platforms, spec.Platforms,
			"%s targets %v; the whole set is built for %v", spec.Name, spec.Platforms, specs[0].Platforms)
	}
}

// A COPY source is resolved against the declared context, so one written
// relative to the companion directory instead reaches the builder as a path
// that does not exist. Sources under bin/ are the CLI binaries the builder
// stages before the build and are absent from a clean checkout.
func TestDockerfileCopySourcesResolveInTheDeclaredContext(t *testing.T) {
	root := repositoryRoot(t)
	for _, spec := range buildSpecs(t) {
		context := filepath.Join(root, filepath.FromSlash(spec.Context))
		for number, line := range strings.Split(dockerfile(t, spec), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 || fields[0] != "COPY" || strings.HasPrefix(fields[1], "--") {
				continue
			}
			for _, source := range fields[1 : len(fields)-1] {
				if strings.HasPrefix(source, "bin/") {
					continue
				}
				_, err := os.Stat(filepath.Join(context, filepath.FromSlash(source)))
				require.NoErrorf(t, err, "%s:%d copies %s, which does not exist in the %s context",
					spec.Dockerfile, number+1, source, spec.Context)
			}
		}
	}
}
