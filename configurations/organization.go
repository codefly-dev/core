package configurations

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/bufbuild/protovalidate-go"
	"github.com/codefly-dev/core/shared"

	basev1 "github.com/codefly-dev/core/generated/go/base/v1"
)

type Organization struct {
	Name   string `yaml:"name"`
	Domain string `yaml:"domain"`
}

func (organization *Organization) Proto() *basev1.Organization {
	return &basev1.Organization{
		Name:   organization.Name,
		Domain: organization.Domain,
	}
}

func ExtraValidOrganizationName(name string) bool {
	// Regular expression to match valid organization names
	// ^[a-zA-Z] : starts with a letter
	// [a-zA-Z0-9-\.]* : followed by any number of letters, numbers, hyphens, or spaces
	re := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-\.]*$`)
	return re.MatchString(name)
}

func ToOrganizationDomain(name string) string {
	domain := strings.Replace(name, " ", ".", -1)
	domain = strings.Replace(domain, ".", "-", -1)
	return fmt.Sprintf("github.com/%s", strings.ToLower(domain))
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
