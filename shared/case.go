package shared

import (
	"bytes"
	"fmt"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"strings"
	"unicode"
)

type Case struct {
	LowerCase string
	SnakeCase string
	CamelCase string
	KebabCase string
	DNSCase   string
	Title     string
}

// ToSnakeCase converts a string to snake_case
func ToSnakeCase(s string) string {
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

// ToCamelCase converts a string to camelCase
func ToCamelCase(s string) string {
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

// ToKebabCase converts a string to kebab-case
func ToKebabCase(str string) string {
	return strings.ReplaceAll(ToSnakeCase(str), "_", "-")
}

func ToDNSCase(s string) string {
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

func ToCase(s string) Case {
	return Case{
		LowerCase: ToLowerCase(s),
		DNSCase:   ToDNSCase(s),
		SnakeCase: ToSnakeCase(s),
		CamelCase: ToCamelCase(s),
		KebabCase: ToKebabCase(s),
		Title:     ToTitle(s),
	}
}

func ToTitle(s string) string {
	return ToCamelCase(cases.Title(language.English).String(s))
}

func ToLowerCase(s string) string {
	return strings.ToLower(s)
}
