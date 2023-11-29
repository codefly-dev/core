package configurations

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type Case struct {
	LowerCase string
	SnakeCase string
	CamelCase string
	KebabCase string
	DnsCase   string
	Title     string
}

type ServiceWithCase struct {
	Name      Case
	Unique    Case
	Domain    string
	Namespace string
}

// toSnakeCase converts a string to snake_case
func toSnakeCase(s string) string {
	var buf bytes.Buffer
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				buf.WriteRune('_')
			}
			buf.WriteRune(unicode.ToLower(r))
		} else {
			buf.WriteRune(r)
		}
	}
	return buf.String()
}

// toCamelCase converts a string to camelCase
func toCamelCase(s string) string {
	var buf bytes.Buffer
	toUpper := false
	for _, r := range s {
		if r == '_' || r == '-' {
			toUpper = true
			continue
		}
		if toUpper {
			buf.WriteRune(unicode.ToUpper(r))
			toUpper = false
		} else {
			buf.WriteRune(r)
		}
	}
	return buf.String()
}

// toKebabCase converts a string to kebab-case
func toKebabCase(str string) string {
	return strings.ReplaceAll(toSnakeCase(str), "_", "-")
}

func toDnsCase(s string) string {
	// Unique is of the convention /app/service
	// For DNS we invert and follow a subdomain convention service-app
	tokens := strings.Split(s, "/")
	if len(tokens) == 1 {
		return strings.ToLower(s)
	}
	app := tokens[0]
	svc := tokens[1]
	return strings.ToLower(fmt.Sprintf("%s-%s", svc, app))
}

func toCase(s string) Case {
	return Case{
		LowerCase: toLowerCase(s),
		DnsCase:   toDnsCase(s),
		SnakeCase: toSnakeCase(s),
		CamelCase: toCamelCase(s),
		KebabCase: toKebabCase(s),
		Title:     cases.Title(language.English).String(s),
	}
}

func toLowerCase(s string) string {
	return strings.ToLower(s)
}

func ToServiceWithCase(svc *Service) *ServiceWithCase {
	return &ServiceWithCase{
		Name:      toCase(svc.Name),
		Unique:    toCase(svc.Unique()),
		Domain:    svc.Domain,
		Namespace: svc.Namespace,
	}
}
