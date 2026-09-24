// Package sensitive centralizes defense-in-depth recognition and redaction of
// credential-shaped keys before values reach diagnostics or persistent logs.
package sensitive

import "strings"

// keyMarkers are matched as substrings of the canonical key: any occurrence,
// inside a word or across words, marks the key sensitive. AUTH is deliberately
// NOT here — see authWordSensitive.
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
	return authWordSensitive(strings.Split(canonical, "_"))
}

// publicAuthWords are whole words that contain AUTH but name public,
// non-credential concepts: an OIDC issuer/audience ("authority") and an
// authorization endpoint ("authorize"). The list is closed on purpose: every
// other word containing AUTH — AUTH, OAUTH, NEXTAUTH, AUTHN, AUTHKEY,
// AUTHORIZATION (the header that carries a bearer credential),
// AUTHENTICATION, and anything not yet seen — stays sensitive. A false
// positive costs a value its public reader; a false negative publishes a
// credential, so a new public word is added here with a test, never inferred.
var publicAuthWords = map[string]struct{}{
	"AUTHOR":      {},
	"AUTHORS":     {},
	"AUTHORITY":   {},
	"AUTHORITIES": {},
	"AUTHORIZE":   {},
}

// credentialWords keep a key sensitive even when its only AUTH occurrence is a
// public word: CERTIFICATE_AUTHORITY_KEY is a private key, not an issuer.
var credentialWords = map[string]struct{}{
	"KEY":        {},
	"KEYS":       {},
	"SEED":       {},
	"PASS":       {},
	"PASSPHRASE": {},
	"PIN":        {},
	"SALT":       {},
	"HMAC":       {},
	"PEM":        {},
	"P12":        {},
	"PFX":        {},
	"JKS":        {},
}

// authWordSensitive reports whether any word containing AUTH names credential
// material. Matching is per word (separator-delimited), so a public word such
// as AUTHORITY does not taint AUTHORITY_ISSUER, while a bare AUTH, BASIC_AUTH,
// X_AUTH or AUTHORIZATION stays sensitive.
func authWordSensitive(words []string) bool {
	sawAuth := false
	for _, word := range words {
		if !strings.Contains(word, "AUTH") {
			continue
		}
		if _, public := publicAuthWords[word]; !public {
			return true
		}
		sawAuth = true
	}
	if !sawAuth {
		return false
	}
	for _, word := range words {
		if _, credential := credentialWords[word]; credential {
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
