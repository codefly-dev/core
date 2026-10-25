package companions

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// BaseImageArg is the Docker build argument a companion Dockerfile declares
// for the codefly companion image it builds on. A spec names the companion it
// depends on; the builder resolves that name and version to a reference and
// passes it under this argument.
const BaseImageArg = "CODEFLY_BASE_IMAGE"

// BuildSpec is everything needed to build one companion image, and
// deliberately nothing about where that image is published. This repository
// owns the Dockerfile, the build context and the version; the builder — the
// codefly CLI — owns registry resolution, tagging and push. It is the same
// split agents already have: an agent repository checks in a Dockerfile and
// declares a version, and the CLI is what turns that into a published image.
type BuildSpec struct {
	// Name is the companion's directory under companions/, and the image name
	// the builder qualifies with its registry.
	Name string
	// Version is the image tag, read from the companion's info.codefly.yaml.
	Version string
	// Dockerfile is the repository-root-relative POSIX path to the Dockerfile.
	Dockerfile string
	// Context is the repository-root-relative POSIX path to the build context
	// the Dockerfile is evaluated against. Every companion is built from the
	// repository root, so one builder invocation serves the whole set.
	Context string
	// Platforms are buildx "os/arch" targets. More than one yields a
	// multi-arch manifest list.
	Platforms []string
	// Base names the companion image this Dockerfile builds on, empty when it
	// builds only on an upstream image. The builder resolves the named
	// companion to a reference and passes it as BaseImageArg.
	Base string
	// CLIBinary is the context-relative path the Dockerfile expects a
	// cross-compiled linux codefly binary at, empty when the image bakes none.
	// A path carrying ${TARGETARCH} is expanded by BuildKit per target
	// platform and so needs one binary staged per platform in Platforms.
	CLIBinary string
}

// specs declares the companion images in build order: a spec's Base always
// precedes it, because the builder cannot resolve a base reference for an
// image that has not been built yet.
var specs = []BuildSpec{
	{
		Name:       "codefly",
		Dockerfile: "companions/codefly/Dockerfile",
		Context:    ".",
		Platforms:  []string{"linux/amd64", "linux/arm64"},
		CLIBinary:  "bin/linux/${TARGETARCH}/codefly",
	},
	{
		Name:       "execution",
		Dockerfile: "companions/execution/Dockerfile",
		Context:    ".",
		Platforms:  []string{"linux/amd64", "linux/arm64"},
		CLIBinary:  "bin/linux/${TARGETARCH}/codefly",
	},
	{
		Name:       "go",
		Dockerfile: "companions/go/Dockerfile",
		Context:    ".",
		Platforms:  []string{"linux/amd64", "linux/arm64"},
		Base:       "codefly",
	},
	{
		Name:       "node",
		Dockerfile: "companions/node/Dockerfile",
		Context:    ".",
		Platforms:  []string{"linux/amd64", "linux/arm64"},
		Base:       "codefly",
	},
	{
		Name:       "proto",
		Dockerfile: "companions/proto/Dockerfile",
		Context:    ".",
		Platforms:  []string{"linux/amd64", "linux/arm64"},
		Base:       "codefly",
	},
	{
		Name:       "python",
		Dockerfile: "companions/python/Dockerfile",
		Context:    ".",
		Platforms:  []string{"linux/amd64", "linux/arm64"},
		Base:       "codefly",
	},
}

// BuildSpecs returns the build spec for every companion image, each version
// resolved from its info.codefly.yaml, in build order.
func BuildSpecs() ([]BuildSpec, error) {
	out := make([]BuildSpec, 0, len(specs))
	for _, spec := range specs {
		version, err := manifestVersion(spec.Name)
		if err != nil {
			return nil, fmt.Errorf("cannot derive <%s> companion version: %w", spec.Name, err)
		}
		spec.Version = version
		spec.Platforms = slices.Clone(spec.Platforms)
		out = append(out, spec)
	}
	return out, nil
}

// CopyKind classifies a Dockerfile line for CopySources.
type CopyKind int

const (
	// NotCopy is any line that is not a COPY instruction.
	NotCopy CopyKind = iota
	// FromStage is COPY --from=<stage|image>, which reads from another image
	// rather than from the build context.
	FromStage
	// FromContext is a COPY that reads paths out of the build context.
	FromContext
	// Unparsed is a COPY whose shape this parser does not understand — the
	// JSON form, or one with no source and destination. It is reported rather
	// than skipped: a COPY nobody reads is an image input nothing checks.
	Unparsed
)

// CopySources returns the build-context sources of a COPY instruction.
//
// Leading flags are stripped rather than abandoning the line. Skipping any
// flagged COPY would leave `COPY --chown=…` and `COPY --link` — both ordinary
// context reads — unread, so their sources would escape every check.
func CopySources(line string) ([]string, CopyKind) {
	fields := strings.Fields(line)
	if len(fields) == 0 || fields[0] != "COPY" {
		return nil, NotCopy
	}
	operands := fields[1:]
	for len(operands) > 0 && strings.HasPrefix(operands[0], "--") {
		if strings.HasPrefix(operands[0], "--from=") {
			return nil, FromStage
		}
		operands = operands[1:]
	}
	if len(operands) < 2 || strings.HasPrefix(operands[0], "[") {
		return nil, Unparsed
	}
	return operands[:len(operands)-1], FromContext
}

// Inputs returns the repository-root-relative POSIX paths, read from the
// checkout at root, that this companion's image is built from in this
// repository: its Dockerfile and every build-context COPY source, a file or a
// directory. The staged CLI binary is not one: it is built from codefly-dev/cli
// at publish time. A change under any input changes the image, so it needs a
// new Version: publishing skips a tag the registry already has.
func (s BuildSpec) Inputs(root string) ([]string, error) {
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(s.Dockerfile))) //nolint:gosec // the spec's own Dockerfile under the checkout root
	if err != nil {
		return nil, fmt.Errorf("cannot read the <%s> Dockerfile: %w", s.Name, err)
	}
	inputs := []string{s.Dockerfile}
	for number, line := range strings.Split(string(content), "\n") {
		sources, kind := CopySources(line)
		switch kind {
		case Unparsed:
			return nil, fmt.Errorf("%s:%d is a COPY whose sources cannot be read: %s", s.Dockerfile, number+1, strings.TrimSpace(line))
		case FromContext:
			for _, source := range sources {
				if s.CLIBinary != "" && source == s.CLIBinary {
					continue
				}
				inputs = append(inputs, path.Clean(path.Join(s.Context, source)))
			}
		}
	}
	return inputs, nil
}
