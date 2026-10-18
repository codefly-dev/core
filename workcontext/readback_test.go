package workcontext_test

import (
	"context"
	"crypto/ed25519"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/workcontext"
)

// The issuer reads back a capability it issued, to answer a question its holder
// asks about it. These hold the two halves of that: it spends nothing, and it
// refuses everything Verify refuses.

func (h *harness) readBack(aud, token string) (*workcontext.ReadBackCapability, error) {
	return h.authority.ReadBack(context.Background(), token, workcontext.ReadBackCheck{Audience: aud, Grants: h})
}

func (h *harness) mintSingleUse() string {
	h.t.Helper()
	token, err := h.startIn(workcontext.StartInput{ReplayPolicy: workcontext.ReplaySingleUse})
	require.NoError(h.t, err)
	return token
}

// Asking twice spends nothing, and the consumer's Verify still consumes the
// capability exactly once afterwards.
func TestReadBack_DoesNotConsumeASingleUseCapability(t *testing.T) {
	h := newHarness(t)
	single := h.mintSingleUse()

	for range 2 {
		read, err := h.readBack(audience, single)
		require.NoError(t, err)
		require.Equal(t, workcontext.ReplaySingleUse, read.Context().GetReplayPolicy())
	}

	_, err := h.verify(audience, single)
	require.NoError(t, err, "the read-backs left the capability for its consumer")
	_, err = h.verify(audience, single)
	require.ErrorIs(t, err, workcontext.ErrReplayed, "and the consumer's Verify still consumes it once")
}

// The question a read-back exists for: the consumer verified (and so consumed)
// the capability first, and still asks the issuer about it.
func TestReadBack_AnswersAboutACapabilityItsConsumerAlreadyConsumed(t *testing.T) {
	h := newHarness(t)
	single := h.mintSingleUse()
	_, err := h.verify(audience, single)
	require.NoError(t, err)

	for range 2 {
		_, err := h.readBack(audience, single)
		require.NoError(t, err, "a consumed capability can still be asked about, on every page")
	}
}

// Everything but replay is Verify's check, by the same body: a capability Verify
// refuses, ReadBack refuses with the same error.
func TestReadBack_RefusesWhatVerifyRefusesWithTheSameError(t *testing.T) {
	for name, tc := range map[string]struct {
		audience string
		alter    func(h *harness)
		want     error
	}{
		"another audience":       {audience: "someone-else", want: workcontext.ErrInvalid},
		"a revision since moved": {audience: audience, alter: func(h *harness) { h.revision++ }, want: workcontext.ErrRevoked},
		"an expired window":      {audience: audience, alter: func(h *harness) { h.clock = h.clock.Add(2 * time.Hour) }, want: workcontext.ErrInvalid},
		"a seal since superseded": {audience: audience, alter: func(h *harness) {
			require.NoError(h.t, h.seals.Put(ownerID, workcontext.Seal{InstallationID: installation, InstallationRevision: 4}))
		}, want: workcontext.ErrRevoked},
		"a principal epoch since advanced": {audience: audience, alter: func(h *harness) {
			require.NoError(h.t, h.seals.PutEpoch(ownerID, 3))
		}, want: workcontext.ErrRevoked},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			single := h.mintSingleUse()
			if tc.alter != nil {
				tc.alter(h)
			}
			_, readErr := h.readBack(tc.audience, single)
			_, verifyErr := h.verify(tc.audience, single)
			require.ErrorIs(t, readErr, tc.want)
			require.ErrorIs(t, verifyErr, tc.want)
			require.Equal(t, verifyErr.Error(), readErr.Error(), "one body, one answer")
		})
	}
}

// The signature is checked under THIS authority's key: a capability another
// authority signed, under the same issuer name and key id, is not read back.
func TestReadBack_ReadsBackOnlyWhatThisAuthoritySigned(t *testing.T) {
	h := newHarness(t)
	_, other, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	impostor := *h.authority
	impostor.Key = other
	token, _, err := impostor.Start(context.Background(), workcontext.StartInput{
		TenantID: tenant, OwnerPrincipalID: ownerID, OwnerPrincipalKind: "human", OrganizationID: organization,
		TaskID: taskID, Audience: audience, InstallationID: installation, TTL: time.Hour,
	})
	require.NoError(t, err)

	_, err = h.readBack(audience, token)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "signature does not verify")
}

// A read-back is a full-strength question or it is nothing: without the live
// state or the grant records it would answer, it refuses to answer.
func TestReadBack_NeedsWhatItChecksAgainst(t *testing.T) {
	h := newHarness(t)
	single := h.mintSingleUse()
	ctx := context.Background()

	_, err := h.authority.ReadBack(ctx, single, workcontext.ReadBackCheck{Audience: audience})
	require.ErrorContains(t, err, "grant source")
	_, err = h.authority.ReadBack(ctx, single, workcontext.ReadBackCheck{Grants: h})
	require.ErrorContains(t, err, "names no audience")

	noSeals := *h.authority
	noSeals.Seals = nil
	_, err = noSeals.ReadBack(ctx, single, workcontext.ReadBackCheck{Audience: audience, Grants: h})
	require.ErrorContains(t, err, "seal source")

	noRevisions := *h.authority
	noRevisions.Revisions = nil
	_, err = noRevisions.ReadBack(ctx, single, workcontext.ReadBackCheck{Audience: audience, Grants: h})
	require.ErrorContains(t, err, "revision source")

	// None of the refusals touched the capability.
	_, err = h.verify(audience, single)
	require.NoError(t, err)
}

// A read-back answers a question; it is not a credential. There is no exported
// declaration taking a ReadBackCapability and returning a *Verified, so one can
// never be derived from (Child, Grant) or rechecked (Recheck) as a capability
// somebody presented. The same guard as the Authenticated one, for the same
// reason: one such function would be the silent upgrade the type exists to
// make unreachable.
func TestNoDeclarationTurnsAReadBackCapabilityIntoAVerifiedOne(t *testing.T) {
	set := token.NewFileSet()
	packages, err := parser.ParseDir(set, ".", func(file os.FileInfo) bool {
		return !strings.HasSuffix(file.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)
	require.Contains(t, packages, "workcontext")

	mentions := func(fields *ast.FieldList) []string {
		var found []string
		if fields == nil {
			return found
		}
		for _, field := range fields.List {
			found = append(found, strings.TrimSpace(typeName(field.Type)))
		}
		return found
	}
	for _, file := range packages["workcontext"].Files {
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || !function.Name.IsExported() {
				continue
			}
			takesReadBack := false
			for _, input := range append(mentions(function.Recv), mentions(function.Type.Params)...) {
				if input == "*ReadBackCapability" || input == "ReadBackCapability" {
					takesReadBack = true
				}
			}
			if !takesReadBack {
				continue
			}
			for _, result := range mentions(function.Type.Results) {
				require.NotContains(t, []string{"*Verified", "Verified"}, result,
					"%s at %s converts a read-back into a Verified capability; see this test's comment",
					function.Name.Name, set.Position(function.Pos()))
			}
		}
	}
}
