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

// Instruction is one logical Dockerfile instruction: its physical lines
// joined where a line ends in the escape character, comment lines inside it
// dropped, and Line the physical line it starts on.
type Instruction struct {
	Line int
	Text string
}

// Instructions splits a Dockerfile into its logical instructions. A COPY
// written over several lines is one instruction, so its sources on the later
// lines are read with it rather than missed.
func Instructions(dockerfile string) []Instruction {
	var out []Instruction
	var current []string
	start := 0
	for number, raw := range strings.Split(dockerfile, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if len(current) == 0 && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
			continue
		}
		if len(current) > 0 && strings.HasPrefix(trimmed, "#") {
			continue // a comment inside a continued instruction is not part of it
		}
		if len(current) == 0 {
			start = number + 1
		}
		if strings.HasSuffix(line, "\\") {
			current = append(current, strings.TrimSuffix(line, "\\"))
			continue
		}
		current = append(current, line)
		out = append(out, Instruction{Line: start, Text: strings.Join(current, " ")})
		current = nil
	}
	if len(current) > 0 {
		out = append(out, Instruction{Line: start, Text: strings.Join(current, " ")})
	}
	return out
}

// CopyKind classifies an instruction for CopySources.
type CopyKind int

const (
	// NotCopy is any instruction that is neither COPY nor ADD.
	NotCopy CopyKind = iota
	// FromStage is COPY --from=<stage|image>, which reads from another image
	// rather than from the build context.
	FromStage
	// FromContext is a COPY or ADD that reads paths out of the build context.
	// An ADD source that is a URL or a git remote is not one, and is left out.
	FromContext
	// Unparsed is a COPY or ADD whose shape this parser does not understand —
	// the JSON form, or one with no source and destination. It is reported
	// rather than skipped: a copy nobody reads is an image input nothing
	// checks.
	Unparsed
)

// CopySources returns the build-context sources of a COPY or ADD instruction,
// in any case (Dockerfile instructions are case-insensitive). Pass it a
// logical instruction (Instructions), not a physical line.
//
// Leading flags are stripped rather than abandoning the line. Skipping any
// flagged COPY would leave `COPY --chown=…` and `COPY --link` — both ordinary
// context reads — unread, so their sources would escape every check.
func CopySources(instruction string) ([]string, CopyKind) {
	fields := strings.Fields(instruction)
	if len(fields) == 0 {
		return nil, NotCopy
	}
	command := strings.ToUpper(fields[0])
	if command != "COPY" && command != "ADD" {
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
	var sources []string
	for _, source := range operands[:len(operands)-1] {
		if strings.HasPrefix(source, "<<") {
			continue // a heredoc: its content is in the Dockerfile itself
		}
		if command == "ADD" && remoteSource(source) {
			continue
		}
		sources = append(sources, source)
	}
	return sources, FromContext
}

// remoteSource reports whether an ADD source is fetched rather than read from
// the build context.
func remoteSource(source string) bool {
	for _, prefix := range []string{"http://", "https://", "git@", "git://", "ssh://"} {
		if strings.HasPrefix(source, prefix) {
			return true
		}
	}
	return false
}

// Inputs returns the repository-root-relative POSIX paths, read from the
// checkout at root, that this companion's image is built from in this
// repository: its Dockerfile and every build-context COPY or ADD source, a file or a
// directory. The staged CLI binary is not one: it is built from codefly-dev/cli
// at publish time. A change under any input changes the image, so it needs a
// new Version: publishing skips a tag the registry already has.
func (s BuildSpec) Inputs(root string) ([]string, error) {
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(s.Dockerfile))) //nolint:gosec // the spec's own Dockerfile under the checkout root
	if err != nil {
		return nil, fmt.Errorf("cannot read the <%s> Dockerfile: %w", s.Name, err)
	}
	inputs := []string{s.Dockerfile}
	for _, instruction := range Instructions(string(content)) {
		sources, kind := CopySources(instruction.Text)
		switch kind {
		case Unparsed:
			return nil, fmt.Errorf("%s:%d is a COPY or ADD whose sources cannot be read: %s", s.Dockerfile, instruction.Line, strings.TrimSpace(instruction.Text))
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
