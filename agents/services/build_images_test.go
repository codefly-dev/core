package services

import (
	builderv1 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v1"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func testImage(role, ref, variant string) *builderv1.BuildImageInput {
	return &builderv1.BuildImageInput{Role: role, Reference: ref, Variant: variant}
}

// The recipe owns compatibility, even when a caller selects another registry
// or a different digest. A selection must never silently become the default.
func TestBuildImageBindingsRefuseIncompatibleOrAmbiguousSelections(t *testing.T) {
	ref := "docker.io/library/alpine@sha256:" + strings.Repeat("a", 64)
	defaults := []*builderv1.BuildImageInput{testImage("runtime", ref, "alpine3.23-musl"), testImage("empty", "scratch", "scratch")}
	for _, tc := range []struct {
		name     string
		bindings []*builderv1.BuildImageInput
		refusal  string
	}{
		{"unknown", []*builderv1.BuildImageInput{testImage("typo", ref, "alpine3.23-musl")}, "unsupported image role"},
		{"variant", []*builderv1.BuildImageInput{testImage("runtime", ref, "debian-glibc")}, "unsupported recipe variant"},
		{"mutable", []*builderv1.BuildImageInput{testImage("runtime", "docker.io/library/alpine:3.23", "alpine3.23-musl")}, "sha256-pinned"},
		{"implicit-registry", []*builderv1.BuildImageInput{testImage("runtime", "alpine@sha256:"+strings.Repeat("a", 64), "alpine3.23-musl")}, "fully qualified"},
		{"scratch", []*builderv1.BuildImageInput{testImage("runtime", "scratch", "alpine3.23-musl")}, "cannot substitute scratch"},
		{"duplicate", []*builderv1.BuildImageInput{defaults[0], defaults[0]}, "duplicate image binding"},
		{"nil", []*builderv1.BuildImageInput{nil}, "role name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveBuildImages(defaults, tc.bindings)
			require.ErrorContains(t, err, tc.refusal)
		})
	}
	selected := testImage("runtime", "registry.example/approved/alpine@sha256:"+strings.Repeat("b", 64), "alpine3.23-musl")
	actual, err := ResolveBuildImages(defaults, []*builderv1.BuildImageInput{selected})
	require.NoError(t, err)
	require.Equal(t, selected.Reference, actual[0].Reference)
	require.Equal(t, "scratch", actual[1].Reference)
	actual[0].Reference = "mutated"
	actual[1].Reference = "mutated"
	require.Contains(t, selected.Reference, "registry.example")
	require.Equal(t, "scratch", defaults[1].Reference)
	_, err = ResolveBuildImages(append(defaults, defaults[0]), nil)
	require.ErrorContains(t, err, "duplicate declared")
}

// Capability is an explicit executor promise, never inferred from a known
// agent name, an image declaration alone or a new library version.
func TestBuildImageBindingsRequireExplicitExecutorContract(t *testing.T) {
	input := testImage("runtime", "scratch", "scratch")
	require.Error(t, RequireBuildImageBindings(nil))
	require.Error(t, RequireBuildImageBindings(&builderv1.BuildCapabilitiesResponse{ImageInputs: []*builderv1.BuildImageInput{input}}))
	require.ErrorContains(t, RequireBuildImageBindings(&builderv1.BuildCapabilitiesResponse{ExecutionContracts: []string{BuildImageBindingsContract}}), "no image inputs")
	require.NoError(t, RequireBuildImageBindings(&builderv1.BuildCapabilitiesResponse{ExecutionContracts: []string{"unknown-contract/v99", BuildImageBindingsContract}, ImageInputs: []*builderv1.BuildImageInput{input}}))
}
