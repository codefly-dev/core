package executionplan_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/executionplan"
	"github.com/stretchr/testify/require"
)

func valid() *executionplan.Plan {
	return &executionplan.Plan{
		Schema: executionplan.SchemaV1,
		Requested: executionplan.Target{
			Workspace:   "commerce",
			Service:     "api/orders",
			Phase:       executionplan.PhaseRun,
			Environment: "local",
		},
		Nodes: []executionplan.Node{
			{
				ID:         "api/orders",
				Kind:       executionplan.NodeService,
				Module:     "api",
				Name:       "orders",
				Resolution: executionplan.Resolved,
				Selection:  executionplan.Selection{Reason: executionplan.ReasonRequestedTarget},
			},
			{
				ID:         "data/postgres",
				Kind:       executionplan.NodeService,
				Module:     "data",
				Name:       "postgres",
				Resolution: executionplan.Resolved,
				Artifacts: []executionplan.Artifact{{
					Reference:    "codefly-dev/module-data",
					Version:      "1.2.3",
					Verification: executionplan.VerificationVerified,
					Selection:    executionplan.Selection{Reason: executionplan.ReasonCommittedPin},
				}},
				Selection: executionplan.Selection{
					Reason: executionplan.ReasonDeclaredDependency,
					Via:    []string{"api/orders"},
				},
			},
		},
		Edges: []executionplan.Edge{{
			From:      "data/postgres",
			To:        "api/orders",
			Kind:      executionplan.KindDeclared,
			Selection: executionplan.Selection{Reason: executionplan.ReasonDeclaredDependency},
		}},
		Configurations: []executionplan.ConfigurationOrigin{{
			Consumer:  "api/orders",
			Key:       "CODEFLY__ENDPOINT__DATA__POSTGRES__TCP__TCP",
			Origin:    executionplan.OriginServiceEndpoint,
			Producer:  "data/postgres",
			Selection: executionplan.Selection{Reason: executionplan.ReasonDeclaredDependency},
		}},
		StatePolicy: executionplan.StatePolicy{Reuse: true, Lifecycle: executionplan.LifecycleStop},
	}
}

func TestValidateAcceptsAResolvedPlan(t *testing.T) {
	require.NoError(t, valid().Validate())
}

func TestValidateRejectsAnotherSchema(t *testing.T) {
	plan := valid()
	plan.Schema = "codefly.execution-plan/v2"
	require.ErrorIs(t, plan.Validate(), executionplan.ErrInvalid)
}

// An unresolved node is a planning failure, not a node to skip.
func TestValidateRejectsUnresolvedNodes(t *testing.T) {
	plan := valid()
	plan.Nodes[1].Resolution = executionplan.Unresolved
	plan.Nodes[1].Unresolved = "cannot load module <data>"

	err := plan.Validate()
	require.ErrorIs(t, err, executionplan.ErrInvalid)
	require.Contains(t, err.Error(), "data/postgres: cannot load module <data>")

	plan.Nodes[1].Unresolved = ""
	require.ErrorContains(t, plan.Validate(), "must state why")
}

func TestValidateRejectsACycle(t *testing.T) {
	plan := valid()
	plan.Edges = append(plan.Edges, executionplan.Edge{
		From:      "api/orders",
		To:        "data/postgres",
		Kind:      executionplan.KindDeclared,
		Selection: executionplan.Selection{Reason: executionplan.ReasonDeclaredDependency},
	})
	require.ErrorContains(t, plan.Validate(), "dependency cycle among api/orders, data/postgres")
}

func TestValidateRejectsReferencesOutsideTheClosure(t *testing.T) {
	plan := valid()
	plan.Edges[0].From = "vault/secrets"
	require.ErrorContains(t, plan.Validate(), "references unselected node \"vault/secrets\"")

	plan = valid()
	plan.Configurations[0].Producer = "vault/secrets"
	require.ErrorContains(t, plan.Validate(), "producer \"vault/secrets\" is not in the closure")

	plan = valid()
	plan.Requested.Service = "vault/secrets"
	require.ErrorContains(t, plan.Validate(), "is not in the closure")

	plan = valid()
	plan.SchemaSteps = []executionplan.SchemaStep{{
		ID:        "data/db-migration",
		Module:    "data",
		Name:      "db-migration",
		After:     []string{"vault/secrets"},
		Selection: executionplan.Selection{Reason: executionplan.ReasonSchemaPrerequisite},
	}}
	require.ErrorContains(t, plan.Validate(), "runs after unselected node \"vault/secrets\"")

	plan = valid()
	plan.SchemaSteps = []executionplan.SchemaStep{{
		ID:        "data/db-migration",
		Module:    "data",
		Name:      "db-migration",
		After:     []string{"data/postgres"},
		Before:    []string{"vault/secrets"},
		Selection: executionplan.Selection{Reason: executionplan.ReasonSchemaPrerequisite},
	}}
	require.ErrorContains(t, plan.Validate(), "runs before unselected node \"vault/secrets\"")
}

// A step cannot both wait on a node and gate it.
func TestValidateRejectsAContradictorySchemaStep(t *testing.T) {
	plan := valid()
	plan.SchemaSteps = []executionplan.SchemaStep{{
		ID:        "data/db-migration",
		Module:    "data",
		Name:      "db-migration",
		After:     []string{"data/postgres"},
		Before:    []string{"data/postgres"},
		Selection: executionplan.Selection{Reason: executionplan.ReasonSchemaPrerequisite},
	}}
	require.ErrorContains(t, plan.Validate(), "runs both after and before node \"data/postgres\"")
}

func TestValidateGuardsSecretReferences(t *testing.T) {
	plan := valid()
	plan.Configurations[0].Secret = true
	require.ErrorContains(t, plan.Validate(), "must carry a reference")

	plan.Configurations[0].SecretRef = "vault://commerce/stripe"
	plan.Configurations[0].SecretVersion = "4"
	require.NoError(t, plan.Validate())

	plan.Configurations[0].Secret = false
	require.ErrorContains(t, plan.Validate(), "carries a secret reference")
}

func TestValidateRejectsOneKeyFromTwoOrigins(t *testing.T) {
	plan := valid()
	plan.Configurations = append(plan.Configurations, executionplan.ConfigurationOrigin{
		Consumer:  "api/orders",
		Key:       plan.Configurations[0].Key,
		Origin:    executionplan.OriginWorkspaceConfiguration,
		Selection: executionplan.Selection{Reason: executionplan.ReasonDeclaredDependency},
	})
	require.ErrorContains(t, plan.Validate(), "from more than one origin")
}

// slices.SortFunc is not stable, so a partial sort key let the same logical
// plan hash two ways depending on the order the producer emitted its elements.
func TestCanonicalIsIndependentOfInputOrder(t *testing.T) {
	verifications := []executionplan.Verification{
		executionplan.VerificationLocal,
		executionplan.VerificationVerified,
		executionplan.VerificationUnverified,
	}
	visibilities := []string{"public", "private", "internal"}

	build := func(reverse bool) *executionplan.Plan {
		plan := valid()
		var artifacts []executionplan.Artifact
		var endpoints []executionplan.EndpointRequirement
		// Enough elements that pdqsort leaves its insertion-sort path, where a
		// tie on a partial key is silently resolved by input order.
		for i := 0; i < 14; i++ {
			artifacts = append(artifacts, executionplan.Artifact{
				Reference:    "acme/module",
				Version:      "1.0.0",
				Verification: verifications[i%len(verifications)],
				Selection:    executionplan.Selection{Reason: executionplan.ReasonCommittedPin},
			})
			endpoints = append(endpoints, executionplan.EndpointRequirement{
				Name: "grpc", API: "grpc", Visibility: visibilities[i%len(visibilities)],
			})
		}
		if reverse {
			slices.Reverse(artifacts)
			slices.Reverse(endpoints)
		}
		plan.Nodes[1].Artifacts = artifacts
		plan.Nodes[1].Endpoints = endpoints
		return plan
	}

	forward := build(false).Canonical()
	reversed := build(true).Canonical()
	require.Equal(t, forward.Nodes[1].Artifacts, reversed.Nodes[1].Artifacts)
	require.Equal(t, forward.Nodes[1].Endpoints, reversed.Nodes[1].Endpoints)
}

// The same artifact cannot resolve two ways, and one endpoint cannot carry two
// visibilities: such a plan is self-contradictory, not merely ambiguous to sort.
func TestValidateRejectsAContradictoryNode(t *testing.T) {
	plan := valid()
	plan.Nodes[1].Artifacts = append(plan.Nodes[1].Artifacts, executionplan.Artifact{
		Reference:    plan.Nodes[1].Artifacts[0].Reference,
		Version:      plan.Nodes[1].Artifacts[0].Version,
		Verification: executionplan.VerificationLocal,
		Selection:    executionplan.Selection{Reason: executionplan.ReasonLocalOverlay},
	})
	require.ErrorContains(t, plan.Validate(), "pins artifact \"codefly-dev/module-data\" at one version more than once")

	plan = valid()
	plan.Nodes[1].Endpoints = []executionplan.EndpointRequirement{
		{Name: "tcp", API: "tcp", Visibility: "internal"},
		{Name: "tcp", API: "tcp", Visibility: "public"},
	}
	require.ErrorContains(t, plan.Validate(), "requires endpoint \"tcp\" more than once")

	plan = valid()
	plan.Nodes[1].Endpoints = []executionplan.EndpointRequirement{{API: "tcp"}}
	require.ErrorContains(t, plan.Validate(), "endpoint requirement with no name")
}

func TestValidateRejectsAMalformedDigest(t *testing.T) {
	plan := valid()
	plan.Nodes[1].Artifacts[0].Digest = "deadbeef"
	require.ErrorContains(t, plan.Validate(), "is not a sha256 digest")
}

// Sets are sorted; a migration sequence is not a set.
func TestCanonicalSortsSetsAndKeepsSchemaOrder(t *testing.T) {
	plan := valid()
	plan.Nodes[1].Selection.Via = []string{"web/portal", "api/orders"}
	plan.Nodes[0], plan.Nodes[1] = plan.Nodes[1], plan.Nodes[0]
	plan.Edges[0].Endpoints = []string{"tcp", "grpc"}
	plan.SchemaSteps = []executionplan.SchemaStep{
		{ID: "data/create-schema", Module: "data", Name: "create-schema", Selection: executionplan.Selection{Reason: executionplan.ReasonSchemaPrerequisite}},
		{ID: "data/add-index", Module: "data", Name: "add-index", Selection: executionplan.Selection{Reason: executionplan.ReasonSchemaPrerequisite}},
	}

	canonical := plan.Canonical()
	require.Equal(t, "api/orders", canonical.Nodes[0].ID)
	require.Equal(t, "data/postgres", canonical.Nodes[1].ID)
	require.Equal(t, []string{"grpc", "tcp"}, canonical.Edges[0].Endpoints)
	require.Equal(t, []string{"web/portal", "api/orders"}, canonical.Nodes[1].Selection.Via,
		"a dependency path is ordered, not sorted")
	require.Equal(t, "data/create-schema", canonical.SchemaSteps[0].ID)
	require.Equal(t, "data/add-index", canonical.SchemaSteps[1].ID)

	require.Equal(t, "data/postgres", plan.Nodes[0].ID, "Canonical does not mutate its receiver")
}

func TestSemanticFingerprintIgnoresInvocation(t *testing.T) {
	plan := valid()
	bare := regexp.MustCompile(`^[0-9a-f]{64}$`)

	withoutInvocation, err := plan.SemanticFingerprint()
	require.NoError(t, err)
	require.True(t, bare.MatchString(withoutInvocation), withoutInvocation)

	plan.Invocation = &executionplan.Invocation{
		ID:           "01JX",
		StartedAt:    time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
		WorkspaceDir: "/tmp/checkout",
		Actor:        "ci",
	}
	withInvocation, err := plan.SemanticFingerprint()
	require.NoError(t, err)
	require.Equal(t, withoutInvocation, withInvocation)

	semantic, err := plan.SemanticBytes()
	require.NoError(t, err)
	require.NotContains(t, string(semantic), "/tmp/checkout")

	serialized, err := plan.MarshalCanonical()
	require.NoError(t, err)
	require.Contains(t, string(serialized), "/tmp/checkout")
}

// A fingerprint over artifacts with no content digest cannot stand in for input
// equality: the same fingerprint covers two different working trees.
func TestUncoveredContentNamesNodesTheFingerprintCannotPin(t *testing.T) {
	plan := valid()
	require.Equal(t, []string{"api/orders", "data/postgres"}, plan.UncoveredContent(),
		"api/orders has no artifact; data/postgres pins one with no digest")

	digest := "sha256:" + strings.Repeat("ab", 32)
	plan.Nodes[1].Artifacts[0].Digest = digest
	require.Equal(t, []string{"api/orders"}, plan.UncoveredContent())

	plan.Nodes[0].Artifacts = []executionplan.Artifact{{
		Reference:    "codefly-dev/module-api",
		Version:      "2.0.0",
		Digest:       digest,
		Verification: executionplan.VerificationVerified,
		Selection:    executionplan.Selection{Reason: executionplan.ReasonCommittedPin},
	}}
	require.Empty(t, plan.UncoveredContent(),
		"every node content-addressed: fingerprint equality now implies input equality")
}

// The hash format is part of the hashed bytes, so changing how the digest is
// computed cannot collide with a fingerprint produced by the previous format.
func TestSemanticFingerprintIsFormatVersioned(t *testing.T) {
	plan := valid()
	semantic, err := plan.SemanticBytes()
	require.NoError(t, err)
	unversioned := sha256.Sum256(semantic)

	actual, err := plan.SemanticFingerprint()
	require.NoError(t, err)
	require.NotEqual(t, hex.EncodeToString(unversioned[:]), actual)

	framed := sha256.Sum256(append(append([]byte(executionplan.SemanticHashFormatV1), 0), semantic...))
	require.Equal(t, hex.EncodeToString(framed[:]), actual)
}

func TestSemanticFingerprintTracksResolvedFacts(t *testing.T) {
	baseline, err := valid().SemanticFingerprint()
	require.NoError(t, err)

	for name, mutate := range map[string]func(*executionplan.Plan){
		"backend": func(plan *executionplan.Plan) {
			plan.Nodes[0].Backend = &executionplan.Backend{Kind: "runtime::service", Publisher: "codefly.ai", Name: "go-grpc", Version: "0.0.17"}
		},
		"artifact version": func(plan *executionplan.Plan) { plan.Nodes[1].Artifacts[0].Version = "1.2.4" },
		"verification": func(plan *executionplan.Plan) {
			plan.Nodes[1].Artifacts[0].Verification = executionplan.VerificationUnverified
		},
		"fixture":       func(plan *executionplan.Plan) { plan.Requested.Environment = "staging" },
		"state policy":  func(plan *executionplan.Plan) { plan.StatePolicy.Reuse = false },
		"configuration": func(plan *executionplan.Plan) { plan.Configurations[0].Producer = "api/orders" },
	} {
		t.Run(name, func(t *testing.T) {
			plan := valid()
			mutate(plan)
			mutated, err := plan.SemanticFingerprint()
			require.NoError(t, err)
			require.NotEqual(t, baseline, mutated)
		})
	}
}

func TestUnmarshalRoundTrips(t *testing.T) {
	serialized, err := valid().MarshalCanonical()
	require.NoError(t, err)

	decoded, err := executionplan.Unmarshal(serialized)
	require.NoError(t, err)

	again, err := decoded.MarshalCanonical()
	require.NoError(t, err)
	require.Equal(t, string(serialized), string(again))
}

// The contract has no place to put a configuration value, so a producer cannot
// smuggle one past a consumer.
func TestUnmarshalRefusesAConfigurationValue(t *testing.T) {
	serialized, err := valid().MarshalCanonical()
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(serialized, &raw))
	origins := raw["configurations"].([]any)
	origins[0].(map[string]any)["value"] = "postgres://user:hunter2@localhost:5432"
	smuggled, err := json.Marshal(raw)
	require.NoError(t, err)

	_, err = executionplan.Unmarshal(smuggled)
	require.ErrorIs(t, err, executionplan.ErrInvalid)
	require.True(t, strings.Contains(err.Error(), "unknown field"), err.Error())
}
