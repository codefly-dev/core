package resources

import "path"

func ValidateDomain() {
}

func ExtendDomain(domain string, extension string) string {
	return path.Join(domain, extension)
}
