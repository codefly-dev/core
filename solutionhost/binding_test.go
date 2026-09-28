package solutionhost_test

import (
	"strings"
	"testing"

	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func valid(t *testing.T) *solutionhost.SolutionHostBinding {
	t.Helper()
	document, err := solutionhost.Parse(fixture(t, "valid"))
	require.NoError(t, err)
	return document
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := solutionhost.FixtureDocument(name)
	require.NoError(t, err)
	return data
}

func TestValidDocumentCarriesEveryDeclaredField(t *testing.T) {
	document := valid(t)

	require.Equal(t, solutionhost.SchemaV1, document.Schema)
	require.Equal(t, solutionhost.FixtureBindingID, document.Binding)
	require.Equal(t, uint64(4), document.Generation)
	require.Equal(t, solutionhost.FixtureCoordinate, document.Host.Coordinate)
	require.Equal(t, "saas-host", document.Host.Component)
	require.Equal(t, "obin/crm@1.4.0", document.Release.Identity())
	require.False(t, document.Removed)
	require.Equal(t, []string{"crm"}, document.Aliases())
	require.Len(t, document.Modules, 2)
	require.Len(t, document.Endpoints, 2)
	require.NotEmpty(t, document.Workload.Audience)
	require.NotEmpty(t, document.Workload.Subject)

	// Every rendered surface is pinned: this is the v1 requirement.
	surfaces := map[solutionhost.Surface]string{}
	for _, artifact := range document.Artifacts {
		surfaces[artifact.Surface] = artifact.Digest
	}
	require.Len(t, surfaces, 3)
	for _, surface := range []solutionhost.Surface{solutionhost.SurfaceFrontend, solutionhost.SurfaceBackend, solutionhost.SurfaceClient} {
		require.Regexp(t, `^sha256:[0-9a-f]{64}$`, surfaces[surface], "surface %s", surface)
	}
}

// The design point this type exists to protect: a render is pinned today, a
// signature is not, so v1 requires one digest and leaves the other optional.
func TestReleaseDigestIsOptionalInV1AndArtifactDigestsAreNot(t *testing.T) {
	document := valid(t)
	require.NotEmpty(t, document.Release.Digest, "the fixture carries one, to show the field is read")

	document.Release.Digest = ""
	require.NoError(t, document.Validate(), "declared presence must ship before signed releases exist")

	document.Release.Digest = "not-a-digest"
	require.ErrorIs(t, document.Validate(), solutionhost.ErrInvalid)

	document = valid(t)
	document.Artifacts[1].Digest = ""
	err := document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "rendered digest")
}

func TestMixedReleaseGenerationIsInvalidOnItsOwn(t *testing.T) {
	_, err := solutionhost.Parse(fixture(t, "mixed-release"))
	require.ErrorIs(t, err, solutionhost.ErrMixedRelease)
	require.Contains(t, err.Error(), "obin/pim@1.9.0")
}

func TestTombstoneIsAGenerationThatDeclaresNothingPresent(t *testing.T) {
	document, err := solutionhost.Parse(fixture(t, "tombstone"))
	require.NoError(t, err)
	require.True(t, document.Removed)
	require.Empty(t, document.Routes)
	require.Empty(t, document.Artifacts)
	require.Equal(t, uint64(5), document.Generation)
	// It still names what it removes.
	require.Equal(t, "obin/crm@1.4.0", document.Release.Identity())

	document.Routes = []solutionhost.Route{{Alias: "crm", Surface: solutionhost.SurfaceFrontend}}
	require.ErrorIs(t, document.Validate(), solutionhost.ErrInvalid)
}

func TestUnknownSchemaIsVersionSkewNotAMalformedDocument(t *testing.T) {
	document := valid(t)
	document.Schema = "codefly/solution-host-binding/v2"
	err := document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrSchema)
	require.NotErrorIs(t, err, solutionhost.ErrInvalid)

	document.Schema = ""
	require.ErrorIs(t, document.Validate(), solutionhost.ErrSchema)
}

func TestUnknownFieldIsRejectedSoANewFieldIsAVersionStep(t *testing.T) {
	_, err := solutionhost.Parse(append(fixture(t, "valid"), []byte("\nsignature: whatever\n")...))
	require.Error(t, err)
	require.Contains(t, err.Error(), "signature")
}

func TestSecondYAMLDocumentIsRejected(t *testing.T) {
	data := append(fixture(t, "valid"), []byte("\n---\nschema: codefly/solution-host-binding/v1\n")...)
	_, err := solutionhost.Parse(data)
	require.ErrorContains(t, err, "multiple YAML documents")
}

func TestValidationRejectsEachWayTheDocumentCanLie(t *testing.T) {
	for name, mutate := range map[string]func(*solutionhost.SolutionHostBinding){
		"no binding ID":            func(d *solutionhost.SolutionHostBinding) { d.Binding = "" },
		"binding ID with a space":  func(d *solutionhost.SolutionHostBinding) { d.Binding = "crm 01" },
		"reserved binding ID":      func(d *solutionhost.SolutionHostBinding) { d.Binding = "base" },
		"generation zero":          func(d *solutionhost.SolutionHostBinding) { d.Generation = 0 },
		"no coordinate":            func(d *solutionhost.SolutionHostBinding) { d.Host.Coordinate = "" },
		"no component":             func(d *solutionhost.SolutionHostBinding) { d.Host.Component = "" },
		"no publisher":             func(d *solutionhost.SolutionHostBinding) { d.Release.Publisher = "" },
		"release version range":    func(d *solutionhost.SolutionHostBinding) { d.Release.Version = ">=1.4.0" },
		"no artifacts":             func(d *solutionhost.SolutionHostBinding) { d.Artifacts = nil },
		"unknown artifact surface": func(d *solutionhost.SolutionHostBinding) { d.Artifacts[0].Surface = "sidecar" },
		"duplicate artifact": func(d *solutionhost.SolutionHostBinding) {
			d.Artifacts = append(d.Artifacts, d.Artifacts[0])
		},
		"duplicate route alias": func(d *solutionhost.SolutionHostBinding) {
			d.Routes = append(d.Routes, d.Routes[0])
		},
		"route to an unrendered surface": func(d *solutionhost.SolutionHostBinding) {
			d.Artifacts = d.Artifacts[1:]
		},
		"module pin is a constraint": func(d *solutionhost.SolutionHostBinding) { d.Modules[0].Version = "^1.4.0" },
		"duplicate module pin": func(d *solutionhost.SolutionHostBinding) {
			d.Modules = append(d.Modules, d.Modules[0])
		},
		"duplicate endpoint": func(d *solutionhost.SolutionHostBinding) {
			d.Endpoints = append(d.Endpoints, d.Endpoints[0])
		},
		"endpoint without an api": func(d *solutionhost.SolutionHostBinding) { d.Endpoints[0].API = "" },
		"no workload audience":    func(d *solutionhost.SolutionHostBinding) { d.Workload.Audience = "" },
		"multi-line workload subject": func(d *solutionhost.SolutionHostBinding) {
			d.Workload.Subject = "-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----"
		},
	} {
		t.Run(name, func(t *testing.T) {
			document := valid(t)
			mutate(document)
			require.Error(t, document.Validate())
		})
	}
}

func TestNilDocumentValidates(t *testing.T) {
	var document *solutionhost.SolutionHostBinding
	require.ErrorIs(t, document.Validate(), solutionhost.ErrInvalid)
}

func TestCanonicalDigestIgnoresDeclarationOrder(t *testing.T) {
	document := valid(t)
	digest, err := document.Digest()
	require.NoError(t, err)
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, digest)

	shuffled := valid(t)
	shuffled.Artifacts = []solutionhost.Artifact{document.Artifacts[2], document.Artifacts[0], document.Artifacts[1]}
	shuffled.Modules = []solutionhost.ModulePin{document.Modules[1], document.Modules[0]}
	shuffled.Endpoints = []solutionhost.Endpoint{document.Endpoints[1], document.Endpoints[0]}
	shuffledDigest, err := shuffled.Digest()
	require.NoError(t, err)
	require.Equal(t, digest, shuffledDigest)

	// Content, not order, moves the digest: this is what separates a re-read of
	// an applied generation from a rewrite of it.
	changed := valid(t)
	changed.Artifacts[0].Digest = strings.Replace(changed.Artifacts[0].Digest, "sha256:3", "sha256:4", 1)
	changedDigest, err := changed.Digest()
	require.NoError(t, err)
	require.NotEqual(t, digest, changedDigest)

	// An invalid document has no digest at all, so nothing can be recorded as
	// applied without having passed validation.
	changed.Schema = "codefly/solution-host-binding/v9"
	_, err = changed.Digest()
	require.ErrorIs(t, err, solutionhost.ErrSchema)
}

func TestMarshalRoundTripsAndRefusesAnInvalidDocument(t *testing.T) {
	document := valid(t)
	data, err := solutionhost.Marshal(document)
	require.NoError(t, err)

	reparsed, err := solutionhost.Parse(data)
	require.NoError(t, err)
	require.Equal(t, document, reparsed)

	document.Generation = 0
	_, err = solutionhost.Marshal(document)
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
}

// A tombstone must not grow an empty routes/artifacts key on the way out: a
// consumer reading the rendered YAML should see absence, not an empty list.
func TestMarshalledTombstoneOmitsWhatItWithdraws(t *testing.T) {
	document, err := solutionhost.Parse(fixture(t, "tombstone"))
	require.NoError(t, err)
	data, err := solutionhost.Marshal(document)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, yaml.Unmarshal(data, &raw))
	for _, key := range []string{"routes", "artifacts", "modules", "endpoints"} {
		require.NotContains(t, raw, key)
	}
	require.Equal(t, true, raw["removed"])
}
