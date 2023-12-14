package configurations_test

import (
	"testing"

	"github.com/bufbuild/protovalidate-go"
	basev1 "github.com/codefly-dev/core/generated/v1/go/proto/base"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

//
//func TestValidOrganizationName(t *testing.T) {
//	tcs := []struct {
//		name  string
//		valid bool
//	}{
//		{"codefly.ai", true},
//		{"codefly", true},
//		{"01codefly", false},
//	}
//	for _, tc := range tcs {
//		t.Run(tc.name, func(t *testing.T) {
//			assert.Equal(t, tc.valid, configurations.ExtraValidOrganizationNameCheck(tc.name), tc.name)
//		})
//	}
//}
//
//func TestValidOrganizationDomain(t *testing.T) {
//	tcs := []struct {
//		name  string
//		valid bool
//	}{
//		{"https://codefly.ai", true},
//		{"https://codefly.ai/", true},
//		{"codefly.ai/organization", false},
//	}
//	for _, tc := range tcs {
//		t.Run(tc.name, func(t *testing.T) {
//			assert.Equal(t, tc.valid, configurations.ValidOrganizationDomain(tc.name))
//		})
//	}
//}

func TestValidOrganization(t *testing.T) {
	tcs := []struct {
		name string
		*basev1.Organization
		err error
	}{
		{"nil", nil, shared.NewNilError[basev1.Organization]()},
		{"too short", &basev1.Organization{Name: "c"}, &protovalidate.ValidationError{}},
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
			assert.Equal(t, tc.out, configurations.ToOrganizationDomain(tc.in))
		})
	}
}
