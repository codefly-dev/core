package services

import (
	"errors"
	"fmt"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ErrOutputProfileNotSelected refuses a deployment request that names no
// Kubernetes output profile: the zero value does not authorize rendering.
var ErrOutputProfileNotSelected = errors.New("no Kubernetes output profile is selected")

// ErrOutputProfileUnknown refuses a profile number this contract does not
// define: one the schema deleted, or one it never named. A request decoded
// from an older schema can carry the first, since deleting an enum value
// deletes its name and not its number — the number still decodes, and only
// this judgement refuses it.
var ErrOutputProfileUnknown = errors.New("the Kubernetes output profile is not one this contract defines")

// OutputProfile is a Kubernetes output profile this contract defines, judged
// once by ParseOutputProfile: never the zero value, never a number the schema
// deleted, never one it never named. It is the only form a rendering
// entrypoint acts on — what it renders inline, what it refuses, what it
// reports — so a request whose number survived decoding is refused before
// anything is written, rather than rendered under whatever a comparison on
// the raw number happened to answer. A deleted profile compared raw is simply
// "not restricted", and a render that emits inline Secret data on that answer
// is the opposite of what the profile meant.
type OutputProfile struct {
	profile builderv0.KubernetesOutputProfile
}

// ParseOutputProfile judges a decoded profile. It accepts exactly the profiles
// this contract renders and refuses everything else by number: the zero value
// with ErrOutputProfileNotSelected, a number the schema reserves (a deleted
// value) or never defined with ErrOutputProfileUnknown, naming what the
// contract does define.
func ParseOutputProfile(profile builderv0.KubernetesOutputProfile) (OutputProfile, error) {
	switch profile {
	case builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1,
		builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1:
		return OutputProfile{profile: profile}, nil
	case builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_UNSPECIFIED:
		return OutputProfile{}, fmt.Errorf("%w: select one of %s", ErrOutputProfileNotSelected, definedOutputProfiles())
	}
	if reservedOutputProfileNumber(profile) {
		return OutputProfile{}, fmt.Errorf("%w: number %d names a value the schema deleted (its number is reserved, and a request decoded from an older schema still carries it); select one of %s",
			ErrOutputProfileUnknown, int32(profile), definedOutputProfiles())
	}
	return OutputProfile{}, fmt.Errorf("%w: number %d is not a value of KubernetesOutputProfile; select one of %s",
		ErrOutputProfileUnknown, int32(profile), definedOutputProfiles())
}

// Proto is the wire value of the profile, for the response that reports it.
func (p OutputProfile) Proto() builderv0.KubernetesOutputProfile {
	return p.profile
}

// Restricted reports whether the profile selects the secret-free,
// digest-pinned, policy-restricted contract: RESTRICTED_PORTABLE_V1, and only
// it. The zero value is not restricted and not anything else — it never
// reaches a renderer, which parses first.
func (p OutputProfile) Restricted() bool {
	return p.profile == builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1
}

// InlineSecrets reports whether the profile may embed Secret data in the
// rendered tree: EPHEMERAL_LOCAL_APPLY_V1, and only it.
func (p OutputProfile) InlineSecrets() bool {
	return p.profile == builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1
}

func (p OutputProfile) String() string {
	return p.profile.String()
}

// selected reports whether this value came out of ParseOutputProfile: the zero
// value did not, and a renderer handed one refuses it.
func (p OutputProfile) selected() bool {
	return p.profile != builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_UNSPECIFIED
}

// definedOutputProfiles names the profiles this contract renders, for the
// refusals above.
func definedOutputProfiles() string {
	return strings.Join([]string{
		builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1.String(),
		builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1.String(),
	}, ", ")
}

// reservedOutputProfileNumber reports whether the schema reserves this number:
// the record a deletion leaves behind, read from the descriptor rather than
// kept as a second list here.
func reservedOutputProfileNumber(profile builderv0.KubernetesOutputProfile) bool {
	descriptor := builderv0.KubernetesOutputProfile(0).Descriptor()
	return descriptor.ReservedRanges().Has(protoreflect.EnumNumber(profile))
}
