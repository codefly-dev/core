package services

import (
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func testImage(role, ref, variant string) *builderv0.BuildImageInput {
	return &builderv0.BuildImageInput{Role: role, Reference: ref, Variant: variant}
}

// The recipe owns compatibility, even when a caller selects another registry
// or a different digest. A selection must never silently become the default.
func TestBuildImageBindingsRefuseIncompatibleOrAmbiguousSelections(t *testing.T) {
	ref := "docker.io/library/alpine@sha256:" + strings.Repeat("a", 64)
	defaults := []*builderv0.BuildImageInput{testImage("runtime", ref, "alpine3.23-musl"), testImage("empty", "scratch", "scratch")}
	for _, tc := range []struct {
		name     string
		bindings []*builderv0.BuildImageInput
		refusal  string
	}{
		{"unknown", []*builderv0.BuildImageInput{testImage("typo", ref, "alpine3.23-musl")}, "unsupported image role"},
		{"variant", []*builderv0.BuildImageInput{testImage("runtime", ref, "debian-glibc")}, "unsupported recipe variant"},
		{"mutable", []*builderv0.BuildImageInput{testImage("runtime", "docker.io/library/alpine:3.23", "alpine3.23-musl")}, "sha256-pinned"},
		{"implicit-registry", []*builderv0.BuildImageInput{testImage("runtime", "alpine@sha256:"+strings.Repeat("a", 64), "alpine3.23-musl")}, "fully qualified"},
		{"scratch", []*builderv0.BuildImageInput{testImage("runtime", "scratch", "alpine3.23-musl")}, "cannot substitute scratch"},
		{"duplicate", []*builderv0.BuildImageInput{defaults[0], defaults[0]}, "duplicate image binding"},
		{"nil", []*builderv0.BuildImageInput{nil}, "role name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveBuildImages(defaults, tc.bindings)
			require.ErrorContains(t, err, tc.refusal)
		})
	}
	selected := testImage("runtime", "registry.example/approved/alpine@sha256:"+strings.Repeat("b", 64), "alpine3.23-musl")
	actual, err := ResolveBuildImages(defaults, []*builderv0.BuildImageInput{selected})
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
	require.Error(t, RequireBuildImageBindings(&builderv0.BuildCapabilitiesResponse{ImageInputs: []*builderv0.BuildImageInput{input}}))
	require.ErrorContains(t, RequireBuildImageBindings(&builderv0.BuildCapabilitiesResponse{ExecutionContracts: []string{BuildImageBindingsContract}}), "no image inputs")
	require.NoError(t, RequireBuildImageBindings(&builderv0.BuildCapabilitiesResponse{ExecutionContracts: []string{"unknown-contract/v99", BuildImageBindingsContract}, ImageInputs: []*builderv0.BuildImageInput{input}}))
}
