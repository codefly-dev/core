package solutionhost_test

import (
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func valid(t *testing.T) *solutionhost.SolutionHostBinding {
	t.Helper()
	document, err := solutionhost.Parse(presence(t, "valid"))
	require.NoError(t, err)
	return document
}

func presence(t *testing.T, name string) []byte {
	t.Helper()
	data, err := solutionhost.FixtureDocument(solutionhost.DocumentTypePresence, name)
	require.NoError(t, err)
	return data
}

func authority(t *testing.T, name string) []byte {
	t.Helper()
	data, err := solutionhost.FixtureDocument(solutionhost.DocumentTypeAuthority, name)
	require.NoError(t, err)
	return data
}

func TestValidDocumentCarriesEveryDeclaredField(t *testing.T) {
	document := valid(t)

	require.Equal(t, solutionhost.SchemaPresenceV1, document.Schema)
	require.Equal(t, solutionhost.KindSolution, document.Kind)
	require.Equal(t, solutionhost.FixtureBindingID, document.Binding)
	require.Equal(t, uint64(4), document.Generation)
	require.Equal(t, solutionhost.FixtureDomain, document.OwnershipDomain)
	require.Equal(t, uint64(solutionhost.FixtureEnvelopeRevision), document.EnvelopeRevision)
	require.Equal(t, solutionhost.FixtureCoordinate, document.Host.Coordinate)
	require.Equal(t, "solution-host", document.Host.Component)
	require.Equal(t, "example/alpha@1.4.0", document.Release.Identity())
	require.NotEmpty(t, document.Release.Digest)
	require.False(t, document.Removed)
	require.Equal(t, []string{"alpha"}, document.Aliases())
	require.Len(t, document.Modules, 2)

	// An endpoint is named and never addressed, and declares two things about
	// one: who may call it, and whether an address reachable from outside the
	// workspace was allocated for it. The second is stated where it can
	// differ, which is on the endpoint reachable from outside.
	require.Equal(t, []solutionhost.Endpoint{
		{Name: "api", Service: "alpha", Module: "alpha", API: "grpc", Visibility: "internal"},
		{Name: "web", Service: "alpha", Module: "alpha", API: "http", Visibility: "public", Exposure: "public"},
	}, document.Endpoints)

	// Every rendered artifact is pinned.
	surfaces := map[solutionhost.Surface]solutionhost.RenderedDigest{}
	for _, artifact := range document.Artifacts {
		surfaces[artifact.Surface] = artifact.Digest
	}
	require.Len(t, surfaces, 3)
	for _, surface := range []solutionhost.Surface{solutionhost.SurfaceFrontend, solutionhost.SurfaceBackend, solutionhost.SurfaceClient} {
		require.Regexp(t, `^sha256:[0-9a-f]{64}$`, string(surfaces[surface]), "surface %s", surface)
	}

	// The workload names what the host must find true of it before issuing
	// anything: one authenticating container, an exact build, an identity.
	require.Len(t, document.Workloads, 1)
	workload := document.Workloads[0]
	require.Equal(t, "alpha-api", workload.Name)
	require.Equal(t, "api", workload.Artifact)
	require.Equal(t, "api", workload.Container)
	require.Equal(t, "registry.example/alpha-api", workload.Image.Repository)
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, string(workload.Image.Digest))
	require.NotNil(t, workload.NonAuthenticating, "the exclusion list is declared, not inferred")
	require.Equal(t, []string{"envoy", "migrate"}, *workload.NonAuthenticating)
	require.Equal(t, "spiffe://prod.region-a.example/ns/alpha-region-a-01/sa/alpha-api", workload.Identity.SPIFFEID)
	require.Equal(t, []solutionhost.ImageDigest{workload.Image.Digest}, document.Builds())
}

// Presence covers both kinds, and the kind is declared rather than inferred:
// a host that guessed would be deciding what may install a thing from the
// shape of the thing, and the two kinds differ in exactly that.
func TestPresenceCoversModulesAsWellAsSolutions(t *testing.T) {
	document, err := solutionhost.Parse(presence(t, "module-presence"))
	require.NoError(t, err)
	require.Equal(t, solutionhost.KindModule, document.Kind)
	require.Equal(t, solutionhost.FixtureModuleBindingID, document.Binding)
	require.Len(t, document.Workloads, 1)
	require.Empty(t, document.Routes, "a module fronts no surface of its own here")

	for _, kind := range []solutionhost.Kind{"", "service", "Module", "solutions"} {
		document.Kind = kind
		err := document.Validate()
		require.ErrorIsf(t, err, solutionhost.ErrInvalid, "kind %q", kind)
		require.Contains(t, err.Error(), "kind")
	}
}

func TestWrongKindFixtureIsRefused(t *testing.T) {
	_, err := solutionhost.Parse(presence(t, "wrong-kind"))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "kind")
}

// The release digest is required now. A generation without one can be matched
// to no authority document, because the approved release is what a host holds
// it against.
func TestEveryDigestIsRequired(t *testing.T) {
	document := valid(t)
	document.Release.Digest = ""
	err := document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "release digest")

	document = valid(t)
	document.Release.Digest = "not-a-digest"
	require.ErrorIs(t, document.Validate(), solutionhost.ErrInvalid)

	document = valid(t)
	document.Artifacts[1].Digest = ""
	err = document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "rendered digest")

	document = valid(t)
	document.Workloads[0].Image.Digest = ""
	err = document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "image manifest digest")
}

// The three digests describe three different objects, and nothing in the model
// compares one to another. Equality between two of different kinds would need
// a SHA-256 collision, so it is evidence of a confusion rather than a
// coincidence — and left to run, the symptom is a credential refusal on a
// healthy pod with no stated cause.
func TestOneDigestForTwoObjectsIsRefused(t *testing.T) {
	_, err := solutionhost.Parse(presence(t, "digest-confusion"))
	require.ErrorIs(t, err, solutionhost.ErrDigestConfusion)

	document := valid(t)
	document.Workloads[0].Image.Digest = solutionhost.ImageDigest(document.Release.Digest)
	require.ErrorIs(t, document.Validate(), solutionhost.ErrDigestConfusion)

	// Two artifacts legitimately rendering identical bytes are the SAME kind of
	// digest, so that is not confusion and must still validate.
	document = valid(t)
	document.Artifacts[2].Digest = document.Artifacts[0].Digest
	require.NoError(t, document.Validate())
}

// Workloads if and only if there is something to run. The biconditional is
// deliberate: one direction stops a generation that renders a backend from
// leaving the host with no build and no identity to hold a container to, and
// the other stops a workload being declared for bytes this generation never
// rendered.
func TestWorkloadsAreDeclaredExactlyWhenSomethingRunsThem(t *testing.T) {
	document := valid(t)
	document.Workloads = nil
	err := document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "declares the workloads that run it")

	// A frontend-only generation runs nothing, so it declares no workload —
	// and declaring one is refused rather than ignored.
	frontendOnly := valid(t)
	frontendOnly.Artifacts = frontendOnly.Artifacts[:1]
	frontendOnly.Routes = frontendOnly.Routes[:1]
	frontendOnly.Workloads = nil
	require.NoError(t, frontendOnly.Validate())

	frontendOnly.Workloads = valid(t).Workloads
	err = frontendOnly.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
}

// A workload names the artifact that renders it, by NAME. That is the verified
// relationship between the rendered bytes and the image, and it is
// deliberately not one digest compared against another.
func TestWorkloadMustNameABackendArtifactItDeclares(t *testing.T) {
	document := valid(t)
	document.Workloads[0].Artifact = "nowhere"
	err := document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "does not declare")

	document = valid(t)
	document.Workloads[0].Artifact = "web"
	err = document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "rendered by a backend artifact")
}

// One container authenticates, and the containers that must never authenticate
// are named explicitly. A document asserting both of one container says
// nothing a host can act on, and the safe reading is not obvious enough to
// pick one.
func TestTheAuthenticatingContainerCannotAlsoBeExcluded(t *testing.T) {
	document := valid(t)
	withContainer := append(*document.Workloads[0].NonAuthenticating, document.Workloads[0].Container)
	document.Workloads[0].NonAuthenticating = &withContainer
	err := document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "must never authenticate")
}

// A repository carrying its own tag or digest would give one container two
// answers about what it runs, and the mutable one usually wins.
func TestImageRepositoryCarriesNeitherTagNorDigest(t *testing.T) {
	for _, repository := range []string{
		"registry.example/alpha-api:1.4.0",
		"registry.example/alpha-api@sha256:3880ab5504a3f436fead6e19fb23b641443747ab55faa3f63c7b7f91b610e28f",
	} {
		document := valid(t)
		document.Workloads[0].Image.Repository = repository
		require.Errorf(t, document.Validate(), "repository %q", repository)
	}
}

// A SPIFFE ID is validated as a SPIFFE ID and not as a name: a subject that
// happens to parse as a URL is not an SVID, and a host that accepted one would
// verify a connection against something no workload can present.
func TestSPIFFEIDIsValidatedAsAnSVIDName(t *testing.T) {
	for name, id := range map[string]string{
		"absent":              "",
		"wrong scheme":        "https://prod.example.example/ns/alpha/sa/api",
		"no scheme":           "prod.example.example/ns/alpha/sa/api",
		"trust domain only":   "spiffe://prod.example.example",
		"trailing slash only": "spiffe://prod.example.example/",
		"uppercase domain":    "spiffe://Prod.Region-A.Example/ns/alpha/sa/api",
		"with a port":         "spiffe://prod.example.example:8443/ns/alpha/sa/api",
		"with a query":        "spiffe://prod.example.example/ns/alpha/sa/api?x=1",
		"with a fragment":     "spiffe://prod.example.example/ns/alpha/sa/api#x",
		"with user info":      "spiffe://user@prod.example.example/ns/alpha/sa/api",
		"relative segment":    "spiffe://prod.example.example/ns/../sa/api",
		"empty segment":       "spiffe://prod.example.example/ns//sa/api",
	} {
		t.Run(name, func(t *testing.T) {
			document := valid(t)
			document.Workloads[0].Identity.SPIFFEID = id
			err := document.Validate()
			require.ErrorIs(t, err, solutionhost.ErrInvalid)
			require.Contains(t, err.Error(), "SPIFFE")
		})
	}
}

func TestMissingIdentityFixtureIsRefused(t *testing.T) {
	_, err := solutionhost.Parse(presence(t, "missing-identity"))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "SPIFFE")
}

func TestMixedReleaseGenerationIsInvalidOnItsOwn(t *testing.T) {
	_, err := solutionhost.Parse(presence(t, "mixed-release"))
	require.ErrorIs(t, err, solutionhost.ErrMixedRelease)
	require.Contains(t, err.Error(), "example/beta@1.9.0")
}

func TestTombstoneIsAGenerationThatDeclaresNothingPresent(t *testing.T) {
	document, err := solutionhost.Parse(presence(t, "tombstone"))
	require.NoError(t, err)
	require.True(t, document.Removed)
	require.Empty(t, document.Routes)
	require.Empty(t, document.Artifacts)
	require.Empty(t, document.Workloads)
	require.Equal(t, uint64(5), document.Generation)
	// It still names what it removes, and the domain it was applied under.
	require.Equal(t, "example/alpha@1.4.0", document.Release.Identity())
	require.Equal(t, solutionhost.FixtureDomain, document.OwnershipDomain)

	document.Routes = []solutionhost.Route{{Alias: "alpha", Surface: solutionhost.SurfaceFrontend}}
	require.ErrorIs(t, document.Validate(), solutionhost.ErrInvalid)

	document = mustParse(t, presence(t, "tombstone"))
	document.Workloads = valid(t).Workloads
	err = document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "no workloads")
}

func mustParse(t *testing.T, data []byte) *solutionhost.SolutionHostBinding {
	t.Helper()
	document, err := solutionhost.Parse(data)
	require.NoError(t, err)
	return document
}

// Each reader reads exactly ONE schema string and refuses every other as a
// version skew rather than an invalid document, because the two call for
// different responses: a document the reader does not read is not malformed,
// and the fix is a re-render by whoever wrote it.
//
// There is no reader for any other string — no older one, no newer one, and
// not the other document type's. The string is inside the signed canonical
// encoding, so it is what binds a signature to the document TYPE: a presence
// document declaring the authority string is refused here rather than admitted
// under a signature that verifies.
func TestOnlyOneSchemaStringIsReadAndEveryOtherIsVersionSkew(t *testing.T) {
	_, err := solutionhost.Parse(presence(t, "other-document-type"))
	require.ErrorIs(t, err, solutionhost.ErrSchema)
	require.NotErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), solutionhost.SchemaPresenceV1)

	document := valid(t)
	for _, schema := range []string{
		"", "codefly/solution-host-binding", "codefly/solution-host-binding/v4",
		solutionhost.SchemaAuthorityV1, solutionhost.SchemaSignedV1,
	} {
		document.Schema = schema
		require.ErrorIsf(t, document.Validate(), solutionhost.ErrSchema, "schema %q", schema)
	}
}

func TestUnknownFieldIsRejectedSoANewFieldIsAVersionStep(t *testing.T) {
	_, err := solutionhost.Parse(append(presence(t, "valid"), []byte("\npublic_key: whatever\n")...))
	require.Error(t, err)
	require.Contains(t, err.Error(), "public_key")
}

func TestSecondYAMLDocumentIsRejected(t *testing.T) {
	data := append(presence(t, "valid"), []byte("\n---\nschema: "+solutionhost.SchemaPresenceV1+"\n")...)
	_, err := solutionhost.Parse(data)
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.ErrorContains(t, err, "holds more than one document")
}

func TestValidationRejectsEachWayTheDocumentCanLie(t *testing.T) {
	for name, mutate := range map[string]func(*solutionhost.SolutionHostBinding){
		"no binding ID":           func(d *solutionhost.SolutionHostBinding) { d.Binding = "" },
		"binding ID with a space": func(d *solutionhost.SolutionHostBinding) { d.Binding = "alpha 01" },
		"reserved binding ID":     func(d *solutionhost.SolutionHostBinding) { d.Binding = "base" },
		"generation zero":         func(d *solutionhost.SolutionHostBinding) { d.Generation = 0 },
		"no ownership domain":     func(d *solutionhost.SolutionHostBinding) { d.OwnershipDomain = "" },
		"ownership domain with a space": func(d *solutionhost.SolutionHostBinding) {
			d.OwnershipDomain = "two words"
		},
		"envelope revision zero":   func(d *solutionhost.SolutionHostBinding) { d.EnvelopeRevision = 0 },
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
		"no workload name":        func(d *solutionhost.SolutionHostBinding) { d.Workloads[0].Name = "" },
		"duplicate workload": func(d *solutionhost.SolutionHostBinding) {
			d.Workloads = append(d.Workloads, d.Workloads[0])
		},
		"no authenticating container": func(d *solutionhost.SolutionHostBinding) { d.Workloads[0].Container = "" },
		"duplicate excluded container": func(d *solutionhost.SolutionHostBinding) {
			d.Workloads[0].NonAuthenticating = &[]string{"envoy", "envoy"}
		},
		"undeclared exclusion list": func(d *solutionhost.SolutionHostBinding) {
			d.Workloads[0].NonAuthenticating = nil
		},
		"no image repository":  func(d *solutionhost.SolutionHostBinding) { d.Workloads[0].Image.Repository = "" },
		"no workload audience": func(d *solutionhost.SolutionHostBinding) { d.Workloads[0].Identity.Audience = "" },
		"multi-line workload subject": func(d *solutionhost.SolutionHostBinding) {
			d.Workloads[0].Identity.Subject = "-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----"
		},
	} {
		t.Run(name, func(t *testing.T) {
			document := valid(t)
			mutate(document)
			require.Error(t, document.Validate())
		})
	}
}

// An endpoint's reach is a declaration the platform derives policy from, so a
// value the resource model does not define must not survive in a rendered
// document: the model refuses one at load, at selection and at dependency
// wiring, and a presence document that admitted a fourth spelling would
// describe an endpoint no service can declare.
//
// Each refusal is asserted by its own message, not by ErrInvalid alone: every
// later rule in validateEndpoints returns the same sentinel, so an assertion
// on it passes even when the reach check is gone and the duplicate-key check
// refuses instead.
func TestEndpointReachIsHeldToTheModelsVocabulary(t *testing.T) {
	for _, refused := range []string{"module", "external", "application", "pubilc", "PUBLIC"} {
		t.Run(refused, func(t *testing.T) {
			document := valid(t)
			document.Endpoints[0].Visibility = refused
			err := document.Validate()
			require.ErrorIs(t, err, solutionhost.ErrInvalid)
			require.ErrorContains(t, err, `endpoint "api" visibility "`+refused+`" is none of`)
			require.ErrorContains(t, err, "no service can declare")
		})
	}

	// The model admits an omission and resolves it to private. A rendered
	// document gets no such resolution, so it states the reach rather than
	// leaving that default to be derived a second time.
	t.Run("omitted", func(t *testing.T) {
		document := valid(t)
		document.Endpoints[0].Visibility = ""
		err := document.Validate()
		require.ErrorIs(t, err, solutionhost.ErrInvalid)
		require.ErrorContains(t, err, `endpoint "api" states no visibility`)
	})

	for _, known := range []string{"private", "internal", "public"} {
		t.Run(known, func(t *testing.T) {
			document := valid(t)
			document.Endpoints[0].Visibility = known
			if known == "public" {
				document.Endpoints[0].Exposure = "none"
			}
			require.NoError(t, document.Validate())
		})
	}
}

// Addressing is the endpoint's second axis, and the one v2 could not state: a
// public endpoint says whether an address reachable from outside the workspace
// was allocated for it, since reach admits both answers and a reader that took
// the reach for the answer is exactly the conflation the split removed.
//
// Each refusal is asserted by its own message rather than by ErrInvalid alone:
// every later rule in validateEndpoints returns the same sentinel, so an
// assertion on it passes with the exposure checks gone and the duplicate-key
// check refusing instead.
func TestAPublicEndpointStatesWhetherAnAddressWasAllocated(t *testing.T) {
	t.Run("omitted", func(t *testing.T) {
		document := valid(t)
		document.Endpoints[1].Exposure = ""
		err := document.Validate()
		require.ErrorIs(t, err, solutionhost.ErrInvalid)
		require.ErrorContains(t, err, `endpoint "web" declares visibility "public" and states no exposure`)
		require.ErrorContains(t, err, "no field left to tell whether an address was allocated")
	})

	for _, stated := range []string{"public", "none"} {
		t.Run(stated, func(t *testing.T) {
			document := valid(t)
			document.Endpoints[1].Exposure = stated
			require.NoError(t, document.Validate())
		})
	}

	for _, refused := range []string{"ingress", "route", "internet", "pubilc", "PUBLIC"} {
		t.Run("unknown "+refused, func(t *testing.T) {
			document := valid(t)
			document.Endpoints[1].Exposure = refused
			err := document.Validate()
			require.ErrorIs(t, err, solutionhost.ErrInvalid)
			require.ErrorContains(t, err, `endpoint "web" exposure "`+refused+`" is neither "public" nor "none"`)
		})
	}
}

// An address reachable from outside the workspace is declared only on an
// endpoint reachable from outside it. The axes are read on their own, which is
// not the same as being independent: addressing past the reach describes an
// address whose every holder the endpoint would refuse.
//
// "none" is admitted on a reach that stops at the workspace, and that is the
// resource model's judgment rather than a gap here: nothing else is true of
// such an endpoint, so a renderer may write it or leave it out, and neither
// spelling makes the document say something the model did not.
func TestAnOutwardAddressIsDeclaredOnlyWithinTheReach(t *testing.T) {
	for _, visibility := range []string{"private", "internal"} {
		t.Run(visibility, func(t *testing.T) {
			document := valid(t)
			document.Endpoints[0].Visibility = visibility
			document.Endpoints[0].Exposure = "public"
			err := document.Validate()
			require.ErrorIs(t, err, solutionhost.ErrInvalid)
			require.ErrorContains(t, err, `endpoint "api" states exposure "public" with visibility "`+visibility+`"`)

			document.Endpoints[0].Exposure = "none"
			require.NoError(t, document.Validate())
			document.Endpoints[0].Exposure = ""
			require.NoError(t, document.Validate())
		})
	}
}

// Both vocabularies and both cross-field rules are the resource model's,
// read from it rather than copied here: this package already links resources
// through composition, so a literal would be a second list to keep in step for
// no gain. Holding the two together at the document's own loader is what makes
// a widened model unable to leave the document behind, and a widened document
// unable to admit what no service can declare.
//
// It also means a renderer projects a declaration the model admitted straight
// through, without normalizing it first — and a normalizing renderer is where
// the fact would be lost. The one difference is the reach itself, which the
// model resolves from an omission and a rendered document states (see
// TestEndpointReachIsHeldToTheModelsVocabulary).
//
// The model's third exposure rule has nothing to bind to here: it refuses an
// address allocated for an endpoint that lives outside the system, and this
// document carries no location. Such an endpoint is refused at its source,
// where the location is declared.
func TestTheEndpointAxesAreJudgedAsTheModelJudgesThem(t *testing.T) {
	for _, visibility := range []string{"private", "internal", "public", "module", "external", "everyone", ""} {
		for _, exposure := range []string{"", "none", "public", "ingress"} {
			document := valid(t)
			document.Endpoints[0].Visibility = visibility
			document.Endpoints[0].Exposure = exposure
			accepted := document.Validate() == nil

			declaration := resources.EndpointDeclaration{
				Service:    document.Endpoints[0].Service,
				Name:       document.Endpoints[0].Name,
				Visibility: visibility,
				Exposure:   exposure,
			}
			byTheModel := resources.ValidateEndpointDeclaration(declaration) == nil && visibility != ""
			require.Equalf(t, byTheModel, accepted,
				"the document and the resource model disagree about visibility %q with exposure %q", visibility, exposure)
		}
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
	// The exclusion list is a set, so its delivered order must not reach the
	// digest. A renderer emitting it from a Go map would otherwise produce a
	// different digest per process, and the host would read every pass as a
	// rewritten generation.
	shuffled.Workloads[0].NonAuthenticating = &[]string{"migrate", "envoy"}
	shuffledDigest, err := shuffled.Digest()
	require.NoError(t, err)
	require.Equal(t, digest, shuffledDigest)

	// Content, not order, moves the digest: this is what separates a re-read of
	// an applied generation from a rewrite of it.
	changed := valid(t)
	changed.Artifacts[0].Digest = solutionhost.RenderedDigest(
		strings.Replace(string(changed.Artifacts[0].Digest), "sha256:a", "sha256:b", 1))
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
	document := mustParse(t, presence(t, "tombstone"))
	data, err := solutionhost.Marshal(document)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, yaml.Unmarshal(data, &raw))
	for _, key := range []string{"routes", "artifacts", "workloads", "modules", "endpoints"} {
		require.NotContains(t, raw, key)
	}
	require.Equal(t, true, raw["removed"])
}

// Artifact names are unique across the whole document, not per surface, and
// this is the test for a round-trip bug rather than a style preference.
//
// A workload names the artifact that renders it by name alone. With per-surface
// uniqueness, "frontend/api" and "backend/api" gave that resolution one key and
// two answers, and which answer won depended on iteration order: the document
// validated as delivered, and its OWN canonical bytes — which sort backend
// before frontend — then failed to reparse. A document whose canonical encoding
// does not round-trip cannot be signed and verified, which is the property
// every other rule in this package rests on.
func TestArtifactNamesAreUniqueAcrossTheDocumentSoTheRoundTripHolds(t *testing.T) {
	document := valid(t)
	// The collision the old rule allowed: two artifacts named "api".
	document.Artifacts = append(document.Artifacts, solutionhost.Artifact{
		Surface: solutionhost.SurfaceFrontend,
		Name:    "api",
		Release: document.Release.Identity(),
		Digest:  "sha256:1111111111111111111111111111111111111111111111111111111111111111",
	})
	err := document.Validate()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "declared twice")
	require.Contains(t, err.Error(), "unique across the document")

	// And the same collision in the other input order is refused identically,
	// which is the point: the answer must not depend on order.
	reordered := valid(t)
	reordered.Artifacts = append([]solutionhost.Artifact{{
		Surface: solutionhost.SurfaceFrontend,
		Name:    "api",
		Release: reordered.Release.Identity(),
		Digest:  "sha256:1111111111111111111111111111111111111111111111111111111111111111",
	}}, reordered.Artifacts...)
	require.ErrorIs(t, reordered.Validate(), solutionhost.ErrInvalid)
}

// Every shipped document's canonical bytes reparse, and reparse to the same
// digest. This is the invariant the signature scheme rests on, so it is held
// for the whole kit rather than for one example.
func TestEveryDocumentsCanonicalBytesReparse(t *testing.T) {
	for _, shipped := range solutionhost.FixturesOf(solutionhost.DocumentTypePresence) {
		t.Run(shipped.Name, func(t *testing.T) {
			document, err := solutionhost.Parse(shipped.Document)
			if err != nil {
				return // a fixture its own rules refuse has no canonical form
			}
			canonical, err := document.CanonicalBytes()
			require.NoError(t, err)

			reparsed, err := solutionhost.Parse(canonical)
			require.NoError(t, err, "the canonical bytes must reparse")

			again, err := reparsed.CanonicalBytes()
			require.NoError(t, err)
			require.Equal(t, canonical, again, "canonicalization must be idempotent")
		})
	}
}

// The exclusion list is declared, never inferred. A plain slice could not tell
// "there are none" from "nobody said" — both decode to nil — so the field's own
// promise was unenforceable and a renderer could sign a workload that had
// declared nothing.
func TestTheExclusionListMustBeDeclaredExplicitly(t *testing.T) {
	// Omitted entirely.
	omitted := strings.Replace(string(presence(t, "valid")),
		"    non_authenticating:\n      - envoy\n      - migrate\n", "", 1)
	require.NotEqual(t, string(presence(t, "valid")), omitted, "the fixture must have had the key")
	_, err := solutionhost.Parse([]byte(omitted))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "explicitly")

	// Written as null.
	nulled := strings.Replace(string(presence(t, "valid")),
		"    non_authenticating:\n      - envoy\n      - migrate\n", "    non_authenticating: null\n", 1)
	_, err = solutionhost.Parse([]byte(nulled))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)

	// Written as an empty list: valid, and means there are none.
	empty := strings.Replace(string(presence(t, "valid")),
		"    non_authenticating:\n      - envoy\n      - migrate\n", "    non_authenticating: []\n", 1)
	document, err := solutionhost.Parse([]byte(empty))
	require.NoError(t, err)
	require.NotNil(t, document.Workloads[0].NonAuthenticating)
	require.Empty(t, *document.Workloads[0].NonAuthenticating)

	// And an empty list survives canonicalization as an empty list rather than
	// collapsing to null — otherwise the canonical bytes would say "nobody
	// declared this", which Validate refuses, and the document's own canonical
	// encoding would not round-trip.
	canonical, err := document.CanonicalBytes()
	require.NoError(t, err)
	require.Contains(t, string(canonical), `"non_authenticating":[]`)
	_, err = solutionhost.Parse(canonical)
	require.NoError(t, err)
}
