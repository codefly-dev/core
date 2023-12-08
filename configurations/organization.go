package configurations

import (
	"context"
	"github.com/bufbuild/protovalidate-go"
	"github.com/codefly-dev/core/shared"
	"net/url"
	"regexp"

	basev1 "github.com/codefly-dev/core/proto/v1/go/base"
)

type Organization struct {
	Name   string
	Domain string
}

func ExtraValidOrganizationName(name string) bool {
	// Regular expression to match valid organization names
	// ^[a-zA-Z] : starts with a letter
	// [a-zA-Z0-9-\.]* : followed by any number of letters, numbers, hyphens, or spaces
	re := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-\.]*$`)
	return re.MatchString(name)
}

func ValidOrganizationDomain(domain string) bool {
	// Domain is URL - think about github organization
	u, err := url.ParseRequestURI(domain)
	if err != nil {
		return false
	}
	return u.Scheme != "" && u.Host != ""
}

func ValidOrganization(org *basev1.Organization) error {
	if org == nil {
		return shared.NewNilError[basev1.Organization]()
	}

	v, err := protovalidate.New()
	if err != nil {
		return err
	}
	if err = v.Validate(org); err != nil {
		return err
	}
	return nil
}

func OrganizationFromProto(_ context.Context, m *basev1.Organization) (*Organization, error) {
	return &Organization{
		Name:   m.Name,
		Domain: m.Domain,
	}, nil
}
