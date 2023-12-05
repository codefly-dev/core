package configurations

import (
	"context"
	"net/url"
	"regexp"

	basev1 "github.com/codefly-dev/core/proto/v1/go/base"
)

type Organization struct {
	Name   string
	Domain string
}

func ValidOrganizationName(name string) bool {
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

func OrganizationFromProto(_ context.Context, m *basev1.Organization) (*Organization, error) {
	return &Organization{
		Name:   m.Name,
		Domain: m.Domain,
	}, nil
}
