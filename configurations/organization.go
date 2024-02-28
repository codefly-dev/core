package configurations

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/bufbuild/protovalidate-go"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type Organization struct {
	// ID must be globally unique
	ID string `yaml:"id"`

	Name                 string `yaml:"name"`
	SourceVersionControl string `yaml:"domain"`
}

func (organization *Organization) Proto() *basev0.Organization {
	return &basev0.Organization{
		Name:                 organization.Name,
		SourceVersionControl: organization.SourceVersionControl,
	}
}

func ExtraValidOrganizationName(name string) bool {
	// Regular expression to match valid organization names
	// ^[a-zA-Z] : starts with a letter
	// [a-zA-Z0-9-\.]* : followed by any number of letters, numbers, hyphens, or spaces
	re := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-\.]*$`)
	return re.MatchString(name)
}

func ToOrganizationSourceVersionControl(name string) string {
	domain := strings.Replace(name, " ", ".", -1)
	domain = strings.Replace(domain, ".", "-", -1)
	return fmt.Sprintf("github.com/%s", strings.ToLower(domain))
}

func ToOrganizationName(svc string) string {
	return strings.TrimPrefix(svc, "github.com/")
}

func ValidOrganizationSourceVersionControl(domain string) bool {
	// SourceVersionControl is URL - think about github organization
	u, err := url.ParseRequestURI(domain)
	if err != nil {
		return false
	}
	return u.Scheme != "" && u.Host != ""
}

func ValidOrganization(org *basev0.Organization) error {
	v, err := protovalidate.New()
	if err != nil {
		return err
	}
	if err = v.Validate(org); err != nil {
		return err
	}
	return nil
}

func OrganizationFromProto(_ context.Context, m *basev0.Organization) (*Organization, error) {
	return &Organization{
		Name:                 m.Name,
		SourceVersionControl: m.SourceVersionControl,
	}, nil
}
