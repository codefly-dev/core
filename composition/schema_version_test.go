package composition

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every document this package reads has one schema, v1. None of them ever had
// an earlier format, so there is no old content to refuse: what is held here
// is that a document stating any other schema is refused by name and its
// author is told which one to state.
func TestADocumentStatingAnotherSchemaIsRefusedByName(t *testing.T) {
	const stated = "codefly/some-later-format"

	t.Run("module package manifest", func(t *testing.T) {
		root := newPackageRoot(t, "0.1.0")
		path := filepath.Join(root, PackageManifestFileName)
		manifest := readFile(t, path)
		require.Contains(t, manifest, "schema: "+PackageSchema)
		writeFile(t, path, strings.Replace(manifest, "schema: "+PackageSchema, "schema: "+stated, 1))
		_, err := LoadPackageManifest(root)
		require.ErrorContains(t, err, `"`+stated+`", which is unknown`)
		require.ErrorContains(t, err, "state the schema as "+PackageSchema)
	})

	t.Run("module lock", func(t *testing.T) {
		lock := validLock()
		lock.Schema = stated
		err := lock.Validate()
		require.ErrorContains(t, err, `"`+stated+`", which is unknown`)
		require.ErrorContains(t, err, "state the schema as "+LockSchema)
	})

	t.Run("module lock artifact media type", func(t *testing.T) {
		lock := validLock()
		lock.Artifact.MediaType = "application/vnd.codefly.module.later+tar"
		err := lock.Validate()
		require.ErrorContains(t, err, "which is unknown")
		require.ErrorContains(t, err, "state the media type as "+ArtifactMediaType)
	})

	provenance := func() Provenance {
		return Provenance{
			Schema: ProvenanceSchema, Package: testPackage, Version: "0.1.0", Repository: testRepository,
			Ref: "v0.1.0", Commit: strings.Repeat("a", 40), ArtifactMediaType: ArtifactMediaType,
			ArtifactDigest: "sha256:" + strings.Repeat("0", 64), SignatureIdentity: testSigner,
		}
	}

	t.Run("module provenance", func(t *testing.T) {
		current := provenance()
		data, err := json.Marshal(current)
		require.NoError(t, err)
		_, err = ParseProvenance(data)
		require.NoError(t, err)

		current.Schema = stated
		data, err = json.Marshal(current)
		require.NoError(t, err)
		_, err = ParseProvenance(data)
		require.ErrorContains(t, err, `"`+stated+`", which is unknown`)
		require.ErrorContains(t, err, "state the schema as "+ProvenanceSchema)
	})

	t.Run("module provenance artifact media type", func(t *testing.T) {
		current := provenance()
		current.ArtifactMediaType = "application/vnd.codefly.module.later+tar"
		data, err := json.Marshal(current)
		require.NoError(t, err)
		_, err = ParseProvenance(data)
		require.ErrorContains(t, err, "state the media type as "+ArtifactMediaType)
	})

	t.Run("composition catalog", func(t *testing.T) {
		projection := t.TempDir()
		path := filepath.Join(projection, filepath.FromSlash(CompositionCatalogName))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		writeFile(t, path, `{"schema":"`+catalogSchema+`"}`)
		_, err := LoadCatalog(projection)
		require.NoError(t, err)

		writeFile(t, path, `{"schema":"`+stated+`"}`)
		_, err = LoadCatalog(projection)
		require.ErrorContains(t, err, `"`+stated+`", which is unknown`)
		require.ErrorContains(t, err, "state the schema as "+catalogSchema)
	})

	t.Run("local module develop override", func(t *testing.T) {
		project := t.TempDir()
		path := developOverridePath(project, "accounts")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		override := DevelopOverride{Schema: developOverrideSchema, Module: "accounts", Package: testPackage, Source: project}
		data, err := json.Marshal(override)
		require.NoError(t, err)
		writeFile(t, path, string(data))
		_, err = LoadDevelopOverride(project, "accounts")
		require.NoError(t, err)

		override.Schema = stated
		data, err = json.Marshal(override)
		require.NoError(t, err)
		writeFile(t, path, string(data))
		_, err = LoadDevelopOverride(project, "accounts")
		require.ErrorContains(t, err, `"`+stated+`", which is unknown`)
		require.ErrorContains(t, err, "state the schema as "+developOverrideSchema)
	})
}
