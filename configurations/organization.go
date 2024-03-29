package configurations

import (
	"context"
	"fmt"
	"strings"

	"github.com/bufbuild/protovalidate-go"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type Organization struct {
	// ID must be globally unique
	ID string `yaml:"id"`

	Name string `yaml:"name"`
}

func (organization *Organization) Proto() *basev0.Organization {
	return &basev0.Organization{
		Name: organization.Name,
	}
}

func ToOrganizationSourceVersionControl(name string) string {
	domain := strings.ReplaceAll(name, " ", ".")
	domain = strings.ReplaceAll(domain, ".", "-")
	return fmt.Sprintf("github.com/%s", strings.ToLower(domain))
}

func ToOrganizationName(svc string) string {
	return strings.TrimPrefix(svc, "github.com/")
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
		Name: m.Name,
	}, nil
}
