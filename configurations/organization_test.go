package configurations_test

import (
	"testing"

	"github.com/bufbuild/protovalidate-go"
	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/stretchr/testify/assert"
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
			err := configurations.ValidOrganization(tc.Organization)
			if tc.err == nil {
				assert.NoError(t, err, tc.name)
			} else {
				assert.Error(t, err, tc.name)
				assert.IsType(t, tc.err, err, tc.name)
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
			assert.Equal(t, tc.out, configurations.ToOrganizationSourceVersionControl(tc.in))
		})
	}
}

func TestOrganizationNameFromDomain(t *testing.T) {
	domain := "github.com/org"
	assert.Equal(t, "org", configurations.ToOrganizationName(domain))
}
