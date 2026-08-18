package dockerrun

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type fakeDistributionInspector struct {
	platforms []ocispec.Platform
	err       error
}

func (f fakeDistributionInspector) DistributionInspect(context.Context, string, string) (registry.DistributionInspect, error) {
	if f.err != nil {
		return registry.DistributionInspect{}, f.err
	}
	return registry.DistributionInspect{Platforms: f.platforms}, nil
}

func linux(arch string) ocispec.Platform { return ocispec.Platform{OS: "linux", Architecture: arch} }

// foreignArch is a linux architecture the current host does not run natively,
// so the "host absent" branches are exercised regardless of where the suite runs.
func foreignArch() string {
	if runtime.GOARCH == "amd64" {
		return "arm64"
	}
	return "amd64"
}

// TestResolveImagePlatform pins the arch-aware fallback: a host-native image
// runs natively (nil), a foreign-only image is targeted at an emulatable
// platform (preferring amd64), and anything ambiguous falls back to the host
// default so the normal pull path still surfaces a real error.
func TestResolveImagePlatform(t *testing.T) {
	cases := []struct {
		name      string
		inspector fakeDistributionInspector
		want      *ocispec.Platform
	}{
		{
			name:      "host arch present runs natively",
			inspector: fakeDistributionInspector{platforms: []ocispec.Platform{linux(runtime.GOARCH), linux(foreignArch())}},
			want:      nil,
		},
		{
			name:      "foreign-only image emulates the foreign arch",
			inspector: fakeDistributionInspector{platforms: []ocispec.Platform{linux(foreignArch())}},
			want:      &ocispec.Platform{OS: "linux", Architecture: foreignArch()},
		},
		{
			name:      "attestation and non-linux entries are ignored",
			inspector: fakeDistributionInspector{platforms: []ocispec.Platform{{OS: "unknown", Architecture: "unknown"}, {OS: "windows", Architecture: "amd64"}, linux(foreignArch())}},
			want:      &ocispec.Platform{OS: "linux", Architecture: foreignArch()},
		},
		{
			name:      "no linux platform falls back to host default",
			inspector: fakeDistributionInspector{platforms: []ocispec.Platform{{OS: "windows", Architecture: "amd64"}}},
			want:      nil,
		},
		{
			name:      "inspect failure falls back to host default",
			inspector: fakeDistributionInspector{err: errors.New("registry unreachable")},
			want:      nil,
		},
		{
			name:      "empty manifest falls back to host default",
			inspector: fakeDistributionInspector{},
			want:      nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveImagePlatform(context.Background(), tc.inspector, &resources.DockerImage{Name: "codeflydev/proto", Tag: "0.0.12"})
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("nil-ness mismatch: got %+v, want %+v", got, tc.want)
			}
			if got != nil && (got.OS != tc.want.OS || got.Architecture != tc.want.Architecture) {
				t.Fatalf("platform mismatch: got %s/%s, want %s/%s", got.OS, got.Architecture, tc.want.OS, tc.want.Architecture)
			}
		})
	}
}

// TestResolveImagePlatformPrefersAmd64 locks the amd64 preference when the host
// arch is absent and several emulatable linux arches are published. It is only
// observable off amd64 hosts, where amd64 is itself foreign.
func TestResolveImagePlatformPrefersAmd64(t *testing.T) {
	if runtime.GOARCH == "amd64" {
		t.Skip("amd64 host runs amd64 natively; preference is unobservable here")
	}
	inspector := fakeDistributionInspector{platforms: []ocispec.Platform{linux("ppc64le"), linux("amd64")}}
	got := resolveImagePlatform(context.Background(), inspector, &resources.DockerImage{Name: "codeflydev/proto", Tag: "0.0.12"})
	if got == nil || got.Architecture != "amd64" {
		t.Fatalf("expected amd64 fallback, got %+v", got)
	}
}

func TestImagePlatformRef(t *testing.T) {
	if ref := imagePlatformRef(&ocispec.Platform{OS: "linux", Architecture: "amd64"}); ref != "linux/amd64" {
		t.Fatalf("got %q, want linux/amd64", ref)
	}
	if ref := imagePlatformRef(&ocispec.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}); ref != "linux/arm64/v8" {
		t.Fatalf("got %q, want linux/arm64/v8", ref)
	}
}
