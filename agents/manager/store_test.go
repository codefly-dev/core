package manager

import (
	"testing"
)

func TestExtractFirstLayerDigest(t *testing.T) {
	// OCI Image Manifest v1
	ociManifest := `{
		"schemaVersion": 2,
		"mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": {
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest": "sha256:configdigest",
			"size": 123
		},
		"layers": [
			{
				"mediaType": "application/octet-stream",
				"digest": "sha256:abc123def456",
				"size": 42000000
			}
		]
	}`

	digest, err := extractFirstLayerDigest([]byte(ociManifest))
	if err != nil {
		t.Fatalf("extractFirstLayerDigest: %v", err)
	}
	if digest != "sha256:abc123def456" {
		t.Errorf("expected sha256:abc123def456, got %s", digest)
	}
}

func TestExtractFirstLayerDigest_DockerV2(t *testing.T) {
	dockerManifest := `{
		"schemaVersion": 2,
		"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
		"config": {
			"digest": "sha256:configdigest"
		},
		"layers": [
			{
				"digest": "sha256:docker789layer",
				"size": 35000000
			}
		]
	}`

	digest, err := extractFirstLayerDigest([]byte(dockerManifest))
	if err != nil {
		t.Fatalf("extractFirstLayerDigest: %v", err)
	}
	if digest != "sha256:docker789layer" {
		t.Errorf("expected sha256:docker789layer, got %s", digest)
	}
}

func TestExtractFirstLayerDigest_NoLayers(t *testing.T) {
	_, err := extractFirstLayerDigest([]byte(`{"schemaVersion": 2}`))
	if err == nil {
		t.Error("expected error for manifest with no layers")
	}
}

func TestPlatformSuffix(t *testing.T) {
	s := PlatformSuffix()
	if s == "" {
		t.Error("expected non-empty platform suffix")
	}
	t.Logf("platform: %s", s)
}

func TestNewOCIStoreFromEnv_Empty(t *testing.T) {
	t.Setenv("AGENT_REGISTRY", "")
	store := NewOCIStoreFromEnv(nil)
	if store != nil {
		t.Error("expected nil store when AGENT_REGISTRY is empty")
	}
}

func TestNewOCIStoreFromEnv_Localhost(t *testing.T) {
	t.Setenv("AGENT_REGISTRY", "localhost:5111")
	store := NewOCIStoreFromEnv(nil)
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if store.scheme != "http" {
		t.Errorf("expected http for localhost, got %s", store.scheme)
	}
}

func TestNewOCIStoreFromEnv_Remote(t *testing.T) {
	t.Setenv("AGENT_REGISTRY", "ghcr.io/codefly-dev")
	store := NewOCIStoreFromEnv(nil)
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if store.scheme != "https" {
		t.Errorf("expected https for ghcr.io, got %s", store.scheme)
	}
}
