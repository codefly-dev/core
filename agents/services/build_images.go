package services

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/distribution/reference"
	"google.golang.org/protobuf/proto"
)

// BuildImageBindingsContract requires the agent to declare its loaded service's
// image inputs and render every selected binding into its verified recipe.
// Merely compiling the transport does not implement this operation contract.
const BuildImageBindingsContract = "base-image-bindings/v1"

var imageRoleName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidateBuildImageInput rejects ambiguous or mutable image bindings. The
// variant is an agent-owned compatibility declaration, not a host-side roster.
func ValidateBuildImageInput(input *builderv0.BuildImageInput) error {
	if input == nil || !imageRoleName.MatchString(input.GetRole()) {
		return fmt.Errorf("image input requires a lowercase role name")
	}
	if input.Variant == "" || strings.TrimSpace(input.Variant) != input.Variant {
		return fmt.Errorf("image role %q requires an explicit recipe variant", input.Role)
	}
	if input.Reference == "scratch" {
		return nil
	}
	named, err := reference.ParseNormalizedNamed(input.Reference)
	if err != nil {
		return fmt.Errorf("image role %q: invalid reference: %w", input.Role, err)
	}
	digested, ok := named.(reference.Digested)
	if !ok || digested.Digest().Algorithm().String() != "sha256" || digested.Digest().Validate() != nil {
		return fmt.Errorf("image role %q requires a sha256-pinned reference", input.Role)
	}
	if !strings.HasPrefix(input.Reference, reference.Domain(named)+"/") {
		return fmt.Errorf("image role %q requires a fully qualified registry reference", input.Role)
	}
	return nil
}

// ResolveBuildImages validates bindings against the agent's declared recipe
// inputs. Missing bindings retain defaults; unknown roles, duplicate roles and
// unsupported variants refuse. The result owns its messages and preserves the
// agent's declaration order. Consumers render only from this result.
func ResolveBuildImages(defaults, bindings []*builderv0.BuildImageInput) ([]*builderv0.BuildImageInput, error) {
	result := make([]*builderv0.BuildImageInput, 0, len(defaults))
	indices := make(map[string]int, len(defaults))
	for _, input := range defaults {
		if err := ValidateBuildImageInput(input); err != nil {
			return nil, err
		}
		if _, exists := indices[input.Role]; exists {
			return nil, fmt.Errorf("duplicate declared image role %q", input.Role)
		}
		indices[input.Role] = len(result)
		result = append(result, proto.CloneOf(input))
	}
	seen := make(map[string]bool, len(bindings))
	for _, input := range bindings {
		if err := ValidateBuildImageInput(input); err != nil {
			return nil, err
		}
		i, exists := indices[input.Role]
		if !exists {
			return nil, fmt.Errorf("unsupported image role %q", input.Role)
		}
		if seen[input.Role] {
			return nil, fmt.Errorf("duplicate image binding %q", input.Role)
		}
		seen[input.Role] = true
		if input.Variant != result[i].Variant {
			return nil, fmt.Errorf("image role %q: unsupported recipe variant %q; this service requires %q", input.Role, input.Variant, result[i].Variant)
		}
		// scratch is a built-in empty filesystem, not a registry image. Replacing
		// it changes the recipe contract even if a caller copies the variant name.
		if (input.Reference == "scratch") != (result[i].Reference == "scratch") {
			return nil, fmt.Errorf("image role %q cannot substitute scratch and a registry image", input.Role)
		}
		result[i] = proto.CloneOf(input)
	}
	return result, nil
}

// RequireBuildImageBindings holds an opt-in request to the running executor's
// advertisement. A known agent name, library version or generated getter is
// never evidence that an executor implements the operation.
func RequireBuildImageBindings(capabilities *builderv0.BuildCapabilitiesResponse) error {
	if !slices.Contains(capabilities.GetExecutionContracts(), BuildImageBindingsContract) {
		return fmt.Errorf("builder must advertise %s to select environment image bindings", BuildImageBindingsContract)
	}
	if len(capabilities.GetImageInputs()) == 0 {
		return fmt.Errorf("builder advertises %s but declares no image inputs", BuildImageBindingsContract)
	}
	_, err := ResolveBuildImages(capabilities.ImageInputs, nil)
	return err
}
