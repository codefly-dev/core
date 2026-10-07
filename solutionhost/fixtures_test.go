package solutionhost_test

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

// A fixture that is shipped but not described, or described but not shipped, is
// worse than a missing one: a consumer testing against it would be testing
// against something this repository does not claim.
//
// Only the document types with files on disk are walked. A signed carrier is a
// mechanical transform of a document in one of those directories, so it is
// built on demand rather than stored — storing it would mean a change to a
// document silently leaving its carrier describing the old one.
func TestEveryShippedFileIsADescribedFixture(t *testing.T) {
	onDisk := map[string]struct{}{}
	require.NoError(t, fs.WalkDir(solutionhost.FixtureFS(), ".", func(file string, entry fs.DirEntry, err error) error {
		require.NoError(t, err)
		if entry.IsDir() {
			return nil
		}
		documentType := solutionhost.DocumentType(path.Dir(file))
		name, found := strings.CutSuffix(path.Base(file), ".codefly.yaml")
		require.Truef(t, found, "%s is not a document", file)
		onDisk[string(documentType)+"/"+name] = struct{}{}
		return nil
	}))

	described := map[string]struct{}{}
	for _, shipped := range solutionhost.Fixtures() {
		if shipped.Type == solutionhost.DocumentTypeSigned {
			// Derived, not stored: covered by TestSignedFixturesReachTheirOutcome.
			require.NotEmptyf(t, shipped.Document, "%s/%s", shipped.Type, shipped.Name)
			require.NotEmptyf(t, shipped.Reason, "%s/%s", shipped.Type, shipped.Name)
			continue
		}
		require.NotEmptyf(t, shipped.Document, "%s/%s", shipped.Type, shipped.Name)
		require.NotEmptyf(t, shipped.Reason, "%s/%s", shipped.Type, shipped.Name)
		described[string(shipped.Type)+"/"+shipped.Name] = struct{}{}
		// Only a presence fixture reaches Host.Admit, so only a presence
		// fixture has a decision to declare. An authority or signed fixture
		// carrying one would be describing an outcome nothing produces.
		if shipped.Outcome == solutionhost.OutcomeAccepted && shipped.Type == solutionhost.DocumentTypePresence {
			require.NotEmptyf(t, shipped.Decision, "%s must say which decision it expects", shipped.Name)
		} else {
			require.Emptyf(t, shipped.Decision, "%s/%s reaches no admission decision", shipped.Type, shipped.Name)
		}
		// A rejection says what its refusal carries, so a consumer asserts
		// the reason and not only the outcome; an acceptance carries none,
		// and only a rejected presence document can protect a build-size
		// rule.
		if shipped.Outcome == solutionhost.OutcomeRejected {
			require.NotEmptyf(t, shipped.Message, "%s/%s is rejected and says nothing about why", shipped.Type, shipped.Name)
		} else {
			require.Emptyf(t, shipped.Message, "%s/%s is accepted and carries a refusal message", shipped.Type, shipped.Name)
			require.Emptyf(t, shipped.Rule, "%s/%s is accepted and names a rule", shipped.Type, shipped.Name)
		}
		if shipped.Rule != "" {
			require.Equalf(t, solutionhost.DocumentTypePresence, shipped.Type, "%s names a build-size rule", shipped.Name)
		}
	}
	require.Equal(t, onDisk, described)
}

// Each type's fixtures are reachable on their own, because each is driven
// against a different piece of fixture state.
func TestFixturesOfSplitsByType(t *testing.T) {
	var total int
	for _, documentType := range []solutionhost.DocumentType{
		solutionhost.DocumentTypePresence,
		solutionhost.DocumentTypeAuthority,
		solutionhost.DocumentTypeSigned,
	} {
		of := solutionhost.FixturesOf(documentType)
		require.NotEmpty(t, of)
		for _, shipped := range of {
			require.Equal(t, documentType, shipped.Type)
		}
		total += len(of)
	}
	require.Len(t, solutionhost.Fixtures(), total, "every fixture belongs to exactly one type")
}

func TestFixtureDocumentNamesAMissingFixture(t *testing.T) {
	_, err := solutionhost.FixtureDocument(solutionhost.DocumentTypePresence, "no-such-fixture")
	require.ErrorContains(t, err, "no-such-fixture")

	_, err = solutionhost.FixtureDocument("invented", "valid")
	require.ErrorContains(t, err, "invented")
}

// Every document fixture targets the one coordinate FixtureHost reconciles, so
// a consumer never has to guess which host state a fixture is written against.
// The signed ones carry it inside their payload, which is the same assertion
// one encoding down.
func TestEveryFixtureTargetsTheFixtureHost(t *testing.T) {
	for _, shipped := range solutionhost.Fixtures() {
		require.Containsf(t, string(shipped.Document), solutionhost.FixtureCoordinate, "%s/%s", shipped.Type, shipped.Name)
	}

	host, err := solutionhost.FixtureHost()
	require.NoError(t, err)
	require.Equal(t, solutionhost.FixtureCoordinate, host.Coordinate)
	require.Equal(t, []string{solutionhost.FixtureDomain}, host.Domains)
	require.Len(t, host.Applied, 1)
	require.Equal(t, solutionhost.FixtureBindingID, host.Applied[0].Binding)
	require.Equal(t, uint64(4), host.Applied[0].Generation)
	require.Equal(t, solutionhost.FixtureDomain, host.Applied[0].Domain)
}

// The envelope is assembled rather than read from a file. A fixture envelope on
// disk, next to the documents it bounds, would be a ceiling delivered alongside
// the thing it is supposed to bound — which is the one shape an envelope must
// never have.
func TestFixtureEnvelopeIsNotShippedAsADocument(t *testing.T) {
	envelope := solutionhost.FixtureEnvelope()
	require.Equal(t, uint64(solutionhost.FixtureEnvelopeRevision), envelope.Revision)
	require.Len(t, envelope.Grants, 3)
	require.Len(t, envelope.ApprovedBuilds, 2)

	for _, shipped := range solutionhost.Fixtures() {
		require.NotContainsf(t, string(shipped.Document), "approved_builds",
			"%s/%s carries an envelope", shipped.Type, shipped.Name)
	}
}
