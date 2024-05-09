package resources_test

import (
	"testing"

	"github.com/bufbuild/protovalidate-go"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestValidOrganization(t *testing.T) {
	tcs := []struct {
		name string
		*basev0.Organization
		err error
	}{
		{"too short", &basev0.Organization{Name: "c"}, &protovalidate.ValidationError{}},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			err := resources.ValidOrganization(tc.Organization)
			if tc.err == nil {
				require.NoError(t, err, tc.name)
			} else {
				require.Error(t, err, tc.name)
				require.IsType(t, tc.err, err, tc.name)
			}
		})
	}
}

func TestToOrganizationDomain(t *testing.T) {
	tcs := []struct {
		name string
		in   string
		out  string
	}{
		{"normal", "org", "github.com/org"},
		{"normal", "Org", "github.com/org"},
		{"normal", "My Org", "github.com/my-org"},
		{"normal", "org.io", "github.com/org-io"},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.out, resources.ToOrganizationSourceVersionControl(tc.in))
		})
	}
}

func TestOrganizationNameFromDomain(t *testing.T) {
	domain := "github.com/org"
	require.Equal(t, "org", resources.ToOrganizationName(domain))
}
