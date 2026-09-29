package configurations_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
)

// deployedWorkspace is the shape this issue is about: ONE declared set of groups
// (the shared profile) and two profiles that name their situation, each supplying
// only what differs. Nothing deployed claims to be local, and the authority
// address is declared once as a reference the network model resolves rather than
// typed per profile.
func deployedWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeConfigurationFile(t, root, "workspace.codefly.yaml", `name: solution
layout: modules
environments:
  - name: local
    configuration-profile: local
  - name: deployed
    configuration-profile: deployed
`)
	writeConfigurationFile(t, root, "configurations/shared/work-context.env",
		"authority-jwks-url=${endpoint:saas/auth/http}/v1/auth/.well-known/jwks.json\n"+
			"audience=${profile}\n"+
			"issuer-clock-skew=30s\n")
	writeConfigurationFile(t, root, "configurations/local/profile.codefly.yaml", "derives-from: shared\n")
	writeConfigurationFile(t, root, "configurations/local/work-context.env", "audience=http://localhost:8080\n")
	writeConfigurationFile(t, root, "configurations/deployed/profile.codefly.yaml", "derives-from: shared\n")
	writeConfigurationFile(t, root, "configurations/deployed/work-context.env", "audience=https://api.example.com\n")
	return root
}

func profileValue(t *testing.T, provided *configurations.ProfileConfigurations, group, key string) string {
	t.Helper()
	for _, info := range provided.Infos {
		if !resources.Match(info.GetName(), group) {
			continue
		}
		for _, value := range info.GetConfigurationValues() {
			if resources.Match(value.GetKey(), key) {
				return value.GetValue()
			}
		}
	}
	t.Fatalf("no value %s/%s in %v", group, key, provided.Infos)
	return ""
}

// One declared set of groups, the deployed difference expressed as a value: the
// deployed profile restates one key and inherits the rest, so the two profiles
// cannot drift apart key by key the way two hand-copied directories do.
func TestAProfileDerivesTheGroupsItDoesNotRestate(t *testing.T) {
	ctx := context.Background()
	root := deployedWorkspace(t)

	for _, tc := range []struct {
		profile  string
		audience string
	}{
		{profile: "local", audience: "http://localhost:8080"},
		{profile: "deployed", audience: "https://api.example.com"},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			provided, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{tc.profile})
			require.NoError(t, err)
			require.True(t, provided.Exists)
			require.Empty(t, provided.Unsupplied, "both profiles supply the value declared per profile")

			assert.Equal(t, tc.audience, profileValue(t, provided, "work-context", "audience"))
			assert.Equal(t, "30s", profileValue(t, provided, "work-context", "issuer-clock-skew"),
				"a key the profile does not restate is inherited, not lost")
			assert.Equal(t, "${endpoint:saas/auth/http}/v1/auth/.well-known/jwks.json",
				profileValue(t, provided, "work-context", "authority-jwks-url"),
				"the address stays a reference the run resolves, identical in both profiles")

			require.Len(t, provided.Layers, 2)
			assert.Equal(t, filepath.Join(root, "configurations", "shared"), provided.Layers[0], "the base layer is read first")
			assert.Equal(t, filepath.Join(root, "configurations", tc.profile), provided.Layers[1])
		})
	}
}

// The declaration that makes an environment-specific difference reviewable: the
// group says the value is supplied per profile, and a profile that supplies none
// is reported rather than handed the base profile's development value.
func TestAValueDeclaredPerProfileIsOwedByAProfileThatSuppliesNone(t *testing.T) {
	ctx := context.Background()
	root := deployedWorkspace(t)
	// A third profile that names a deployed situation and forgets the one value
	// that cannot be shared.
	writeConfigurationFile(t, root, "configurations/staging/profile.codefly.yaml", "derives-from: shared\n")
	writeConfigurationFile(t, root, "configurations/staging/work-context.env", "issuer-clock-skew=5s\n")

	provided, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{"staging"})
	require.NoError(t, err)
	require.Len(t, provided.Unsupplied, 1)
	requirement := provided.Unsupplied[0]
	assert.Equal(t, "work-context", requirement.Group)
	assert.Equal(t, "audience", requirement.Key)
	assert.Equal(t, "staging", requirement.Profile)
	assert.Equal(t, filepath.Join(root, "configurations", "shared"), requirement.DeclaredIn,
		"the requirement names the file a reader must look at, not the profile that owes it")
	assert.Contains(t, (&configurations.UnsuppliedProfileValuesError{Requirements: provided.Unsupplied}).Error(), "work-context/audience")
}

// A load is a render: an owed value fails it, before anything is built, started
// or rendered — the whole point being that this does not surface at runtime as
// one wrong address.
func TestALoadFailsOnAValueTheSelectedProfileOwes(t *testing.T) {
	ctx := context.Background()
	root := deployedWorkspace(t)
	writeConfigurationFile(t, root, "configurations/staging/profile.codefly.yaml", "derives-from: shared\n")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)

	err = loader.Load(ctx, &resources.Environment{Name: "staging"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "work-context/audience")
	assert.Contains(t, err.Error(), "supplied per profile")

	// The same workspace under a profile that supplies it loads.
	require.NoError(t, loader.Load(ctx, &resources.Environment{Name: "deployed"}))
}

// An operator supplying the value for one invocation has supplied it: the
// declaration is a requirement, not a demand that it be committed.
func TestAnInvocationOverrideDischargesAPerProfileValue(t *testing.T) {
	ctx := context.Background()
	root := deployedWorkspace(t)
	writeConfigurationFile(t, root, "configurations/staging/profile.codefly.yaml", "derives-from: shared\n")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)

	encoded, err := resources.EncodeWorkspaceConfigurationOverrides([]resources.WorkspaceConfigurationOverride{
		{Name: "work-context", Key: "audience", Value: "https://staging.example.com"},
	})
	require.NoError(t, err)
	t.Setenv(resources.WorkspaceConfigurationOverridesEnvironment, encoded)

	require.NoError(t, loader.Load(ctx, &resources.Environment{Name: "staging"}))
}

// A derivation that cannot be honoured is an error, never a quiet read of the
// profile alone: that would hand a deployed environment a group stripped of
// everything its author expected it to start from.
func TestADerivationThatCannotBeHonouredFailsTheRead(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		write func(t *testing.T, root string)
		says  string
	}{
		{
			name: "the profile it derives from is not there",
			write: func(t *testing.T, root string) {
				writeConfigurationFile(t, root, "configurations/deployed/profile.codefly.yaml", "derives-from: absent\n")
			},
			says: "has no directory there",
		},
		{
			name: "the chain closes on itself",
			write: func(t *testing.T, root string) {
				writeConfigurationFile(t, root, "configurations/shared/profile.codefly.yaml", "derives-from: deployed\n")
			},
			says: "already in the chain",
		},
		{
			name: "the declaration names a directory outside the location",
			write: func(t *testing.T, root string) {
				writeConfigurationFile(t, root, "configurations/deployed/profile.codefly.yaml", "derives-from: ../local\n")
			},
			says: "single path component",
		},
		{
			name: "the declaration carries a key nothing reads",
			write: func(t *testing.T, root string) {
				writeConfigurationFile(t, root, "configurations/deployed/profile.codefly.yaml", "derives_from: shared\n")
			},
			says: "field derives_from not found",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := deployedWorkspace(t)
			tc.write(t, root)
			_, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{"deployed"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.says)
		})
	}
}

// A profile that derives nothing reads exactly as it did before: one directory,
// no layering, and a location that holds no such profile still contributes
// nothing rather than failing.
func TestAProfileWithoutDerivationReadsOneDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "configurations/local/auth.env", "client-id=public\n")

	provided, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{"local"})
	require.NoError(t, err)
	require.True(t, provided.Exists)
	require.Len(t, provided.Layers, 1)
	assert.Equal(t, "public", profileValue(t, provided, "auth", "client-id"))

	absent, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{"deployed"})
	require.NoError(t, err)
	assert.False(t, absent.Exists)
	assert.Empty(t, absent.Infos)
}

// The environment's chain and a profile's derivation are different declarations
// with different owners — the consuming workspace's and the profile author's —
// and they compose: the chain picks which profile a location reads, and that
// profile's derivation says what it starts from.
func TestTheEnvironmentChainSelectsTheProfileWhoseDerivationIsThenRead(t *testing.T) {
	ctx := context.Background()
	root := deployedWorkspace(t)

	// This location holds no "audit" profile, so the chain falls through to
	// deployed, whose own derivation is then honoured.
	provided, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{"audit", "deployed"})
	require.NoError(t, err)
	require.Len(t, provided.Layers, 2)
	assert.Equal(t, "https://api.example.com", profileValue(t, provided, "work-context", "audience"))
	assert.Equal(t, "30s", profileValue(t, provided, "work-context", "issuer-clock-skew"))
}

// A derived profile overrides a value whole, not only its text: a profile that
// makes a shared default secret, or replaces it with an assembly, says so here.
func TestADerivedProfileReplacesTheWholeValue(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "configurations/shared/db.env", "password=dev-only\n")
	writeConfigurationFile(t, root, "configurations/deployed/profile.codefly.yaml", "derives-from: shared\n")
	writeConfigurationFile(t, root, "configurations/deployed/db.secret.env", "password=from-the-store\n")

	provided, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{"deployed"})
	require.NoError(t, err)
	require.Len(t, provided.Infos, 1)
	require.Len(t, provided.Infos[0].GetConfigurationValues(), 1, "the derived declaration replaces the shared one")
	value := provided.Infos[0].GetConfigurationValues()[0]
	assert.Equal(t, "from-the-store", value.GetValue())
	assert.True(t, value.GetSecret(), "a profile that makes a value secret is not merged with the plaintext default")
}

// A structured document has no keys to overlay, so a derived profile's document
// replaces the one it derives from — and a layer that would turn a document into
// key/value pairs is a conflict rather than a silent choice between the two.
func TestADerivedProfileReplacesAStructuredDocument(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "configurations/shared/policy.yaml", "limits:\n  requests: 10\n")
	writeConfigurationFile(t, root, "configurations/deployed/profile.codefly.yaml", "derives-from: shared\n")
	writeConfigurationFile(t, root, "configurations/deployed/policy.yaml", "limits:\n  requests: 1000\n")

	provided, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{"deployed"})
	require.NoError(t, err)
	require.Len(t, provided.Infos, 1)
	assert.Contains(t, string(provided.Infos[0].GetData().GetContent()), "1000")

	conflicting := t.TempDir()
	writeConfigurationFile(t, conflicting, "configurations/shared/policy.yaml", "limits:\n  requests: 10\n")
	writeConfigurationFile(t, conflicting, "configurations/deployed/profile.codefly.yaml", "derives-from: shared\n")
	writeConfigurationFile(t, conflicting, "configurations/deployed/policy.env", "requests=1000\n")
	_, err = configurations.LoadProfileConfigurations(ctx, conflicting, "configurations", []string{"deployed"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "structured document")
}

// A single-file declaration has no keys either: the most derived profile that
// ships one wins, and a derived profile that ships none keeps the declaration it
// derives from rather than silently having none.
func TestASingleFileDeclarationComesFromTheMostDerivedProfileThatShipsOne(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "dns/shared/dns.codefly.yaml", "- host: shared.example.com\n")
	writeConfigurationFile(t, root, "dns/deployed/profile.codefly.yaml", "derives-from: shared\n")
	writeConfigurationFile(t, root, "dns/overridden/profile.codefly.yaml", "derives-from: shared\n")
	writeConfigurationFile(t, root, "dns/overridden/dns.codefly.yaml", "- host: deployed.example.com\n")

	inherited, exists, err := configurations.ProfileFile(ctx, root, "dns", []string{"deployed"}, "dns.codefly.yaml")
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, filepath.Join(root, "dns", "shared", "dns.codefly.yaml"), inherited)

	own, exists, err := configurations.ProfileFile(ctx, root, "dns", []string{"overridden"}, "dns.codefly.yaml")
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, filepath.Join(root, "dns", "overridden", "dns.codefly.yaml"), own)

	_, exists, err = configurations.ProfileFile(ctx, root, "dns", []string{"absent"}, "dns.codefly.yaml")
	require.NoError(t, err)
	assert.False(t, exists)
}

// The two halves together, as the render sees them: one declared group serves a
// local run and a deployed one, the authority address is a reference rather than
// a literal per profile, and the value that genuinely cannot be shared is
// declared per profile. The only difference between the two renders is the
// mappings each is given — which is the point: a second copy of the group cannot
// drift from the first if there is no second copy.
func TestOneDeclaredGroupServesALocalRunAndADeployedRender(t *testing.T) {
	ctx := context.Background()
	root := deployedWorkspace(t)
	writeConfigurationFile(t, root, "modules/saas/module.codefly.yaml", "name: saas\n")
	writeConfigurationFile(t, root, "modules/saas/services/auth/service.codefly.yaml", `name: auth
module: saas
agent:
  name: go
endpoints:
  - name: http
    api: http
`)
	writeConfigurationFile(t, root, "workspace.codefly.yaml", `name: solution
layout: modules
modules:
  - name: saas
environments:
  - name: local
    configuration-profile: local
  - name: deployed
    configuration-profile: deployed
`)

	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)

	mappings := func(address string) []*basev0.NetworkMapping {
		return []*basev0.NetworkMapping{{
			Endpoint:  &basev0.Endpoint{Module: "saas", Service: "auth", Name: "http", Api: standards.HTTP},
			Instances: []*basev0.NetworkInstance{{Address: address, Access: resources.NewNativeNetworkAccess()}},
		}}
	}
	inRun := func(unique string) bool { return unique == "saas/auth" }

	for _, tc := range []struct {
		profile  string
		address  string
		audience string
	}{
		{profile: "local", address: "http://localhost:45123", audience: "http://localhost:8080"},
		{profile: "deployed", address: "https://auth.example.com", audience: "https://api.example.com"},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
			require.NoError(t, err)
			manager, err := configurations.NewManager(ctx, workspace)
			require.NoError(t, err)
			manager.WithLoader(loader)
			require.NoError(t, manager.Load(ctx, &resources.Environment{Name: tc.profile}))

			confs, err := manager.
				ForConsumer(mappings(tc.address), resources.NewNativeNetworkAccess()).
				WithRunProducers(inRun).
				GetWorkspaceDependenciesConfigurations(ctx, "work-context")
			require.NoError(t, err)
			require.Len(t, confs, 1)

			jwks, err := resources.GetConfigurationValue(ctx, confs[0], "work-context", "authority-jwks-url")
			require.NoError(t, err)
			assert.Equal(t, tc.address+"/v1/auth/.well-known/jwks.json", jwks,
				"the authority address comes from the run's mappings, not from a value typed per profile")

			audience, err := resources.GetConfigurationValue(ctx, confs[0], "work-context", "audience")
			require.NoError(t, err)
			assert.Equal(t, tc.audience, audience, "the value that cannot be shared is declared per profile")

			skew, err := resources.GetConfigurationValue(ctx, confs[0], "work-context", "issuer-clock-skew")
			require.NoError(t, err)
			assert.Equal(t, "30s", skew, "and everything else is declared once")
		})
	}
}

// Two composed modules vendoring the same group both collect its per-profile
// requirement — identical definitions are deliberately not a conflict — and the
// value is owed once, not once per offer.
func TestAValueIsOwedOnceHoweverManyPlacesOfferedTheGroup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "workspace.codefly.yaml", `name: solution
layout: modules
modules:
  - name: first
  - name: second
`)
	for _, module := range []string{"first", "second"} {
		writeConfigurationFile(t, root, "modules/"+module+"/module.codefly.yaml", "name: "+module+"\n")
	}
	// Each module is its own repository root, so each carries the group it ships —
	// the same declaration, from two places.
	for _, module := range []string{"first", "second"} {
		dir := "modules/" + module
		writeConfigurationFile(t, root, dir+"/workspace.codefly.yaml", "name: "+module+"\n")
		writeConfigurationFile(t, root, dir+"/configurations/local/vendored.env", "audience=${profile}\n")
	}

	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	provided, err := configurations.ReadWorkspaceConfigurations(ctx, workspace, resources.LocalEnvironment())
	require.NoError(t, err)
	require.Len(t, provided.Unsupplied, 2, "both offers of the vendored group collect the requirement")

	owed := configurations.StillUnsupplied(provided.Unsupplied, func(string) []*basev0.ConfigurationInformation {
		return provided.Infos
	})
	require.Len(t, owed, 1, "one value, one line to fix")
	assert.Equal(t, "vendored", owed[0].Group)
	assert.Equal(t, "audience", owed[0].Key)
}

// The base profile is the declared set of a group's keys. A key a derived profile
// introduces into a group the base declares is a difference between environments
// that the declared set never mentions: every profile that does not carry it
// renders the group without it, and a missing key reads as the empty string, with
// no error anywhere. That is the failure this whole row is about, so the
// declaration is refused where it is written.
func TestADerivedProfileCannotIntroduceAKeyTheBaseDoesNotDeclare(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "workspace.codefly.yaml", "name: solution\nlayout: modules\n")
	writeConfigurationFile(t, root, "configurations/shared/work-context.env", "issuer-clock-skew=30s\n")
	writeConfigurationFile(t, root, "configurations/local/profile.codefly.yaml", "derives-from: shared\n")
	writeConfigurationFile(t, root, "configurations/local/work-context.env", "audience=http://localhost:8080\n")
	writeConfigurationFile(t, root, "configurations/deployed/profile.codefly.yaml", "derives-from: shared\n")

	// The key exists only in local. Reading local says so, naming the key and the
	// remedy — rather than reading deployed and finding nothing wrong with it.
	_, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{"local"})
	require.Error(t, err)
	require.ErrorIs(t, err, configurations.ErrUndeclaredProfileKey)
	assert.Contains(t, err.Error(), "work-context/audience")
	assert.Contains(t, err.Error(), configurations.ProfileValueMarker, "the diagnostic names the remedy")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	require.Error(t, loader.Load(ctx, &resources.Environment{Name: "local"}),
		"the load fails too, so no render can reach a workload from this composition")

	// Declared in the base as supplied per profile, it is a difference the declared
	// set names: local supplies it, and deployed is now told it owes one.
	writeConfigurationFile(t, root, "configurations/shared/work-context.env",
		"issuer-clock-skew=30s\naudience=${profile}\n")
	local, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{"local"})
	require.NoError(t, err)
	assert.Empty(t, local.Unsupplied)
	deployed, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{"deployed"})
	require.NoError(t, err)
	require.Len(t, deployed.Unsupplied, 1)
	assert.Equal(t, "audience", deployed.Unsupplied[0].Key)
}

// A group a derived profile introduces WHOLE is not the same case: it is absent
// from every profile that does not carry it, and a consumer that declares a group
// nothing provides is told so by name rather than reading an empty key.
func TestADerivedProfileMayIntroduceAWholeGroup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "configurations/shared/work-context.env", "issuer-clock-skew=30s\n")
	writeConfigurationFile(t, root, "configurations/deployed/profile.codefly.yaml", "derives-from: shared\n")
	writeConfigurationFile(t, root, "configurations/deployed/scaling.env", "replicas=3\n")

	provided, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{"deployed"})
	require.NoError(t, err)
	assert.Equal(t, "3", profileValue(t, provided, "scaling", "replicas"))
	assert.Equal(t, "30s", profileValue(t, provided, "work-context", "issuer-clock-skew"))
}

// A structured document has no key model — a derived profile replaces it whole —
// so the marker anywhere in its content declares that each profile supplies the
// whole document. Scanned rather than skipped: an author who writes the marker has
// declared something, and ignoring it shipped "${profile}" to the workload as the
// literal it is, which is the declaration silently doing nothing.
func TestAStructuredDocumentDeclaredPerProfileIsOwedWhole(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "workspace.codefly.yaml", "name: solution\nlayout: modules\n")
	writeConfigurationFile(t, root, "configurations/shared/policy.yaml", "limits:\n  audience: ${profile}\n")
	writeConfigurationFile(t, root, "configurations/deployed/profile.codefly.yaml", "derives-from: shared\n")

	provided, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{"deployed"})
	require.NoError(t, err)
	require.Len(t, provided.Unsupplied, 1)
	assert.Equal(t, "policy", provided.Unsupplied[0].Group)
	assert.Empty(t, provided.Unsupplied[0].Key, "the whole document is the unit, so no key is invented")
	assert.Contains(t, (&configurations.UnsuppliedProfileValuesError{Requirements: provided.Unsupplied}).Error(),
		"the whole document")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	require.Error(t, loader.Load(ctx, &resources.Environment{Name: "deployed"}),
		"the marker must never reach a workload as the literal it is")

	// A profile that supplies the whole document owes nothing.
	writeConfigurationFile(t, root, "configurations/deployed/policy.yaml", "limits:\n  audience: https://api.example.com\n")
	require.NoError(t, loader.Load(ctx, &resources.Environment{Name: "deployed"}))
}

// The derivation file exists for one purpose, so an empty or absent value is a
// malformed declaration rather than "this profile derives from nothing": read that
// way, a profile holding only this file loads as a clean, empty profile and every
// group its author meant to start from is gone with no error at all.
func TestAnEmptyDerivationDeclarationFailsTheRead(t *testing.T) {
	ctx := context.Background()
	for _, content := range []string{"derives-from:\n", "derives-from: \"\"\n", "", "\n"} {
		root := t.TempDir()
		writeConfigurationFile(t, root, "configurations/shared/config.env", "required=value\n")
		writeConfigurationFile(t, root, "configurations/deployed/profile.codefly.yaml", content)

		_, err := configurations.LoadProfileConfigurations(ctx, root, "configurations", []string{"deployed"})
		require.Error(t, err, "content %q", content)
		require.ErrorIs(t, err, configurations.ErrProfileDerivation)
	}
}

// The depth bound is the number of directories one location is read from, checked
// before a layer is appended: the largest accepted chain is exactly that many.
func TestTheDerivationDepthBoundIsTheNumberOfProfilesRead(t *testing.T) {
	ctx := context.Background()
	// profile-0 is the base; profile-N derives from profile-(N-1).
	chain := func(t *testing.T, length int) string {
		t.Helper()
		root := t.TempDir()
		writeConfigurationFile(t, root, "configurations/profile-0/base.env", "shared=yes\n")
		for i := 1; i < length; i++ {
			writeConfigurationFile(t, root, fmt.Sprintf("configurations/profile-%d/profile.codefly.yaml", i),
				fmt.Sprintf("derives-from: profile-%d\n", i-1))
		}
		return root
	}

	accepted, err := configurations.LoadProfileConfigurations(ctx, chain(t, 8), "configurations", []string{"profile-7"})
	require.NoError(t, err, "eight profiles is the advertised bound and must be accepted")
	require.Len(t, accepted.Layers, 8)

	_, err = configurations.LoadProfileConfigurations(ctx, chain(t, 9), "configurations", []string{"profile-8"})
	require.Error(t, err, "the ninth profile is one past the bound")
	require.ErrorIs(t, err, configurations.ErrProfileDerivation)
}
