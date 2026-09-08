package resources

import "testing"

func TestDockerImageFullNamePrefersDigest(t *testing.T) {
	image := &DockerImage{
		Repository: "registry.example.com/team",
		Name:       "runtime",
		Tag:        "1.2.3",
		Digest:     "sha256:abcdef",
	}
	if got, want := image.FullName(), "registry.example.com/team/runtime@sha256:abcdef"; got != want {
		t.Fatalf("FullName() = %q, want %q", got, want)
	}
}

func TestDockerImageFullNameFallsBackToTag(t *testing.T) {
	image := &DockerImage{Name: "runtime", Tag: "1.2.3"}
	if got, want := image.FullName(), "runtime:1.2.3"; got != want {
		t.Fatalf("FullName() = %q, want %q", got, want)
	}
}

func TestPublishedImageAddressesTheCanonicalRegistry(t *testing.T) {
	image := PublishedImage("proto", "0.0.13")

	if got, want := image.Repository, ImageRegistry; got != want {
		t.Fatalf("Repository = %q, want %q", got, want)
	}
	if got, want := image.FullName(), "ghcr.io/codefly-dev/proto:0.0.13"; got != want {
		t.Fatalf("FullName() = %q, want %q", got, want)
	}
	if got, want := image.String(), image.FullName(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestPublishedImageDigestWinsOverTag(t *testing.T) {
	image := PublishedImage("proto", "0.0.13")
	image.Digest = "sha256:abcdef"

	if got, want := image.FullName(), "ghcr.io/codefly-dev/proto@sha256:abcdef"; got != want {
		t.Fatalf("FullName() = %q, want %q", got, want)
	}
}
