// Package sensitive centralizes defense-in-depth recognition and redaction of
// credential-shaped keys before values reach diagnostics or persistent logs.
package sensitive

import "strings"

var keyMarkers = []string{
	"PASSWORD",
	"PASSWD",
	"SECRET",
	"TOKEN",
	"CREDENTIAL",
	"API_KEY",
	"PRIVATE_KEY",
	"ACCESS_KEY",
	"UNSEAL_KEY",
	"AUTH",
	"DATABASE_URL",
	"CONNECTION",
	"DSN",
	"COOKIE",
	"SESSION",
}

var keyCanonicalizer = strings.NewReplacer(
	" ", "_",
	"-", "_",
	".", "_",
	"/", "_",
)

// Key reports whether a label conventionally carries credential material.
// Separators are canonicalized so human-readable process output such as
// "Unseal Key" receives the same protection as UNSEAL_KEY configuration.
func Key(key string) bool {
	canonical := keyCanonicalizer.Replace(strings.ToUpper(key))
	for _, marker := range keyMarkers {
		if strings.Contains(canonical, marker) {
			return true
		}
	}
	return false
}

// RedactText removes values from sensitive key/value lines while retaining the
// label and delimiter for useful diagnostics. It operates on arbitrary text so
// stdout/stderr forwarded through the logging layer receives the same guarantee
// as structured fields.
func RedactText(text string) string {
	lines := strings.SplitAfter(text, "\n")
	for index, line := range lines {
		ending := ""
		body := line
		if strings.HasSuffix(body, "\n") {
			body = strings.TrimSuffix(body, "\n")
			ending = "\n"
		}
		if strings.HasSuffix(body, "\r") {
			body = strings.TrimSuffix(body, "\r")
			ending = "\r" + ending
		}

		separator := firstSeparator(body)
		if separator < 0 || !Key(strings.TrimSpace(body[:separator])) {
			continue
		}
		value := body[separator+1:]
		leading := len(value) - len(strings.TrimLeft(value, " \t"))
		if strings.TrimSpace(value) == "" {
			continue
		}
		lines[index] = body[:separator+1] + value[:leading] + "****" + ending
	}
	return strings.Join(lines, "")
}

func firstSeparator(line string) int {
	colon := strings.IndexByte(line, ':')
	equals := strings.IndexByte(line, '=')
	switch {
	case colon < 0:
		return equals
	case equals < 0:
		return colon
	case colon < equals:
		return colon
	default:
		return equals
	}
}
