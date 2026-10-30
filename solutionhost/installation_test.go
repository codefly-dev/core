package solutionhost_test

import (
	"context"
	"os"
	"testing"

	modulecontract "github.com/codefly-dev/core/contracts/module"
	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

func TestInstallationDeclarationsSurviveSignedDelivery(t *testing.T) {
	for _, role := range []string{"", "none", "editor"} {
		t.Run(role, func(t *testing.T) {
			manifest := valid(t)
			manifest.Authority.InstallationRole = role
			manifest.Authority.Setup = []modulecontract.ResolvedSetup{{Binding: "model", Methods: []string{"/model.v1.Model/Generate"}, Scopes: []string{"model:invoke"}}}
			manifest.Authority.OwnerAwareResourceKinds = []string{"example.items"}
			// Deliberately not sorted; order is part of the authored declaration.
			manifest.Authority.EdgeDependencies = []modulecontract.EdgeDependency{
				{Module: "z.catalogue", Presence: "required"},
				{Module: "a.catalogue", Presence: "optional"},
			}
			written, err := solutionhost.Marshal(manifest)
			require.NoError(t, err)
			read, err := solutionhost.Parse(written)
			require.NoError(t, err)
			payload, err := solutionhost.SignedPayload(read)
			require.NoError(t, err)
			require.Contains(t, string(payload), `"module":"z.catalogue","presence":"required"`)
			require.NotContains(t, string(payload), `"Module"`)
			envelope, err := solutionhost.SignEnvelope(payload, signerFor(t, testKeyID))
			require.NoError(t, err)
			carrier, err := solutionhost.Carrier(payload, envelope)
			require.NoError(t, err)
			encoded, err := solutionhost.MarshalSigned(carrier)
			require.NoError(t, err)
			signed, err := solutionhost.ParseSigned(encoded)
			require.NoError(t, err)
			delivered, err := solutionhost.VerifyDelivered(context.Background(), signed, testVerifier(t))
			require.NoError(t, err)
			got, err := delivered.Manifest()
			require.NoError(t, err)
			require.Equal(t, role, got.Authority.InstallationRole)
			require.Equal(t, manifest.Authority.Setup, got.Authority.Setup)
			require.Equal(t, manifest.Authority.OwnerAwareResourceKinds, got.Authority.OwnerAwareResourceKinds)
			require.Equal(t, manifest.Authority.EdgeDependencies, got.Authority.EdgeDependencies)
			require.Equal(t, "model", got.Authority.Bindings[0].ID)

			// Changing either declaration invalidates the original signature.
			for _, mutate := range []func(*solutionhost.Authority){
				func(a *solutionhost.Authority) { a.InstallationRole = "another-role" },
				func(a *solutionhost.Authority) { a.Setup[0].Methods[0] = "/model.v1.Model/Other" },
				func(a *solutionhost.Authority) { a.OwnerAwareResourceKinds = []string{"different.kind"} },
				func(a *solutionhost.Authority) { a.EdgeDependencies[0].Presence = "optional" },
			} {
				changed, err := solutionhost.Parse(payload)
				require.NoError(t, err)
				mutate(changed.Authority)
				changedBytes, err := changed.CanonicalBytes()
				require.NoError(t, err)
				_, err = solutionhost.VerifyDelivered(context.Background(), &solutionhost.Signed{Document: changedBytes, Envelope: envelope}, testVerifier(t))
				require.ErrorIs(t, err, solutionhost.ErrUnsigned)
			}
		})
	}
}

func TestInstallationCarrierPreservesLegacyCanonicalBytes(t *testing.T) {
	// Captured at aeee38fd before extending the signed carrier.
	want, err := os.ReadFile("testdata/legacy-installation-manifest.json")
	require.NoError(t, err)
	for _, edges := range [][]modulecontract.EdgeDependency{nil, {}} {
		manifest := valid(t)
		manifest.Authority.EdgeDependencies = edges
		got, err := manifest.CanonicalBytes()
		require.NoError(t, err)
		require.Equal(t, string(want), string(got))
		require.NotContains(t, string(got), "installation_role")
		require.NotContains(t, string(got), "edge_dependencies")
	}
}
