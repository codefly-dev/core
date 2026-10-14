package solutionhost_test

import (
	"strings"
	"testing"

	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

// A solution's group is a block of the document, not of its frontend: a
// headless solution is organised in the catalogue too. It is generated, signed
// with the generation, read back by the writer, and absent from the bytes of a
// document that declares none.
func TestAHeadlessSolutionIsOrganisedUnderAGroup(t *testing.T) {
	document := parse(t, "group")
	require.Nil(t, document.Frontend, "the fixture is headless")
	require.Equal(t, &solutionhost.SolutionGroup{ID: "operations", Title: "Operations", Order: 2}, document.Group)

	canonical, err := document.CanonicalBytes()
	require.NoError(t, err)
	require.Contains(t, string(canonical), `,"group":{"id":"operations","order":2,"title":"Operations"},`)
	verified, err := solutionhost.PresenceFromVerified(canonical)
	require.NoError(t, err)
	require.Equal(t, document.Group, verified.Group)
	written, err := solutionhost.Marshal(document)
	require.NoError(t, err)
	again, err := solutionhost.Parse(written)
	require.NoError(t, err)
	require.Equal(t, document.Group, again.Group)

	signed, err := document.Digest()
	require.NoError(t, err)
	document.Group.Order = 3
	moved, err := document.Digest()
	require.NoError(t, err)
	require.NotEqual(t, signed, moved, "a changed group changes what was signed")

	ungrouped := valid(t)
	require.Nil(t, ungrouped.Group)
	bytes, err := ungrouped.CanonicalBytes()
	require.NoError(t, err)
	require.NotContains(t, string(bytes), `"group"`)
}

// A group is a solution's and a present generation's: a module is grouped by
// nothing, a tombstone declares none, and a solution with a frontend carries
// both blocks, each held to its own rules.
func TestAGroupIsASolutionsAndAPresentGenerations(t *testing.T) {
	_, err := solutionhost.Parse(presence(t, "module-with-group"))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), `group is declared on a "module"`)
	_, err = solutionhost.Parse(presence(t, "tombstone-with-group"))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "a removed generation declares no group")

	withFrontend := parse(t, "frontend-generated")
	withFrontend.Group = parse(t, "group").Group
	require.NoError(t, withFrontend.Validate())
	withFrontend.Group.Title = strings.Repeat("x", solutionhost.GroupTextMaxBytes+1)
	require.ErrorIs(t, withFrontend.Validate(), solutionhost.ErrInvalid)
}
