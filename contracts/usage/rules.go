package usage

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The rules a usage contract is held to. Each is one function with one
// name, run in the order rules() lists them, so a document is refused for one
// reason, named — the same reason whichever reader refused it.
//
// The decoder's refusals and the inherent one (ruleWellFormed: a document
// that is not YAML has nothing to validate) are listed here so the fixture
// table and the self-check cover them like any other.
type rule struct {
	name  string
	check func(contract *Contract) error
	// inherent marks a rule the self-check cannot delete: YAML syntax is not
	// a rule of this package, only a precondition of reading anything.
	inherent bool
}

// The rules, by name. A refused fixture names one of these; a reader that
// drops one fails the kit on that fixture.
const (
	ruleWellFormed      = "well-formed"
	ruleDocumentBounded = "document-bounded"
	ruleSchema          = "schema"
	ruleMappingKeysOnce = "mapping-keys-once"
	ruleNoMergeKeys     = "no-merge-keys"
	ruleNoNulls         = "no-nulls"
	ruleWholeNumbers    = "whole-numbers"
	ruleKeyIsAName      = "key-is-a-name"
	ruleKnownFields     = "known-fields"
	ruleOneDocument     = "one-document"

	ruleSummary           = "summary"
	ruleRulesDeclared     = "rules-declared"
	ruleRulesBounded      = "rules-bounded"
	ruleIDShape           = "rule-id-shape"
	ruleIDsUnique         = "rule-ids-unique"
	ruleTitle             = "rule-title"
	ruleSeverity          = "rule-severity"
	ruleSince             = "rule-since"
	ruleRationale         = "rule-rationale"
	ruleDo                = "rule-do"
	ruleTextBounded       = "rule-text-bounded"
	ruleTextPrintable     = "rule-text-printable"
	ruleDocsRelative      = "rule-docs-relative"
	ruleDetectBounded     = "detect-bounded"
	ruleDetectLanguage    = "detect-language"
	ruleDetectOnePerLang  = "detect-one-per-language"
	ruleDetectForbids     = "detect-forbids-something"
	ruleSymbolsBounded    = "detect-symbols-bounded"
	ruleCallShape         = "detect-call-shape"
	ruleImportShape       = "detect-import-shape"
	ruleSymbolsUnique     = "detect-symbols-unique"
	ruleExceptNeedsDetect = "except-paths-need-detect"
	ruleExceptBounded     = "except-paths-bounded"
	ruleExceptRelative    = "except-paths-relative"
	ruleExceptUnique      = "except-paths-unique"
)

func decoders() func(*Contract) error { return func(*Contract) error { return nil } }

func rules() []rule {
	return []rule{
		{name: ruleWellFormed, inherent: true, check: decoders()},
		{name: ruleDocumentBounded, check: decoders()},
		{name: ruleSchema, check: checkSchema},
		{name: ruleMappingKeysOnce, check: decoders()},
		{name: ruleNoMergeKeys, check: decoders()},
		{name: ruleNoNulls, check: decoders()},
		{name: ruleWholeNumbers, check: decoders()},
		{name: ruleKeyIsAName, check: decoders()},
		{name: ruleKnownFields, check: decoders()},
		{name: ruleOneDocument, check: decoders()},
		{name: ruleSummary, check: checkSummary},
		{name: ruleRulesDeclared, check: checkRulesDeclared},
		{name: ruleRulesBounded, check: checkRulesBounded},
		{name: ruleIDShape, check: checkIDShape},
		{name: ruleIDsUnique, check: checkIDsUnique},
		{name: ruleTitle, check: checkTitle},
		{name: ruleSeverity, check: checkSeverity},
		{name: ruleSince, check: checkSince},
		{name: ruleRationale, check: checkRationale},
		{name: ruleDo, check: checkDo},
		{name: ruleTextBounded, check: checkTextBounded},
		{name: ruleTextPrintable, check: checkTextPrintable},
		{name: ruleDocsRelative, check: checkDocsRelative},
		{name: ruleDetectBounded, check: checkDetectBounded},
		{name: ruleDetectLanguage, check: checkDetectLanguage},
		{name: ruleDetectOnePerLang, check: checkDetectOnePerLanguage},
		{name: ruleDetectForbids, check: checkDetectForbids},
		{name: ruleSymbolsBounded, check: checkSymbolsBounded},
		{name: ruleCallShape, check: checkCallShape},
		{name: ruleImportShape, check: checkImportShape},
		{name: ruleSymbolsUnique, check: checkSymbolsUnique},
		{name: ruleExceptNeedsDetect, check: checkExceptNeedsDetect},
		{name: ruleExceptBounded, check: checkExceptBounded},
		{name: ruleExceptRelative, check: checkExceptRelative},
		{name: ruleExceptUnique, check: checkExceptUnique},
	}
}

func ruleNames() []string {
	all := rules()
	names := make([]string, 0, len(all))
	for _, r := range all {
		names = append(names, r.name)
	}
	return names
}

// validate runs every rule but the one named, in order; "" deletes none.
func (contract *Contract) validate(without string) error {
	if contract == nil {
		return fmt.Errorf("%w: contract is required", ErrInvalid)
	}
	for _, r := range rules() {
		if r.name == without {
			continue
		}
		if err := r.check(contract); err != nil {
			return err
		}
	}
	return nil
}

func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...)
}

// label names a rule in a refusal: by its ID when it has a usable one, by its
// position otherwise.
func label(index int, r Rule) string {
	if ruleID.MatchString(r.ID) {
		return "rule " + r.ID
	}
	return fmt.Sprintf("rule %d", index+1)
}

func checkSchema(contract *Contract) error {
	if contract.Schema != SchemaV1 {
		return fmt.Errorf("%w: %q (this reader reads %q)", ErrSchema, contract.Schema, SchemaV1)
	}
	return nil
}

func checkSummary(contract *Contract) error {
	if strings.TrimSpace(contract.Summary) == "" {
		return refuse("summary is required: one paragraph saying what consuming this owner correctly comes down to")
	}
	return nil
}

func checkRulesDeclared(contract *Contract) error {
	if len(contract.Rules) == 0 {
		return refuse("a usage contract states at least one rule; an owner with nothing to say publishes no contract")
	}
	return nil
}

func checkRulesBounded(contract *Contract) error {
	if len(contract.Rules) > MaxRules {
		return refuse("the contract states %d rules; a contract states at most %d", len(contract.Rules), MaxRules)
	}
	return nil
}

// ruleID is an upper-case prefix and one or more upper-case or numeric parts:
// PG-CONN-001, RUNTIME-TASK-7.
var ruleID = regexp.MustCompile(`^[A-Z][A-Z0-9]*(-[A-Z0-9]+)+$`)

func checkIDShape(contract *Contract) error {
	for i, r := range contract.Rules {
		if !ruleID.MatchString(r.ID) {
			return refuse("rule %d has the id %q; a rule id is upper-case parts joined by hyphens, such as PG-CONN-001", i+1, r.ID)
		}
	}
	return nil
}

func checkIDsUnique(contract *Contract) error {
	seen := map[string]bool{}
	for _, r := range contract.Rules {
		if seen[r.ID] {
			return refuse("the rule id %s is declared twice; an id names one rule for the life of the owner", r.ID)
		}
		seen[r.ID] = true
	}
	return nil
}

func checkTitle(contract *Contract) error {
	for i, r := range contract.Rules {
		if strings.TrimSpace(r.Title) == "" {
			return refuse("%s has no title", label(i, r))
		}
		if strings.ContainsAny(r.Title, "\n\r") {
			return refuse("%s has a title of more than one line", label(i, r))
		}
	}
	return nil
}

func checkSeverity(contract *Contract) error {
	for i, r := range contract.Rules {
		if !slices.Contains(severities, r.Severity) {
			return refuse("%s has the severity %q; a severity is one of %s", label(i, r), r.Severity, strings.Join(severities, ", "))
		}
	}
	return nil
}

// version is a semantic version without a leading v, as a release is spelled
// in an agent manifest.
var version = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

func checkSince(contract *Contract) error {
	for i, r := range contract.Rules {
		if !version.MatchString(r.Since) {
			return refuse("%s has since %q; since is the owner's release that introduced the rule, as a semantic version such as 1.4.0", label(i, r), r.Since)
		}
	}
	return nil
}

func checkRationale(contract *Contract) error {
	for i, r := range contract.Rules {
		if strings.TrimSpace(r.Rationale) == "" {
			return refuse("%s has no rationale: say what goes wrong without the rule", label(i, r))
		}
	}
	return nil
}

func checkDo(contract *Contract) error {
	for i, r := range contract.Rules {
		if strings.TrimSpace(r.Do) == "" {
			return refuse("%s has no do: a rule that only forbids leaves its reader to guess the alternative", label(i, r))
		}
	}
	return nil
}

// text is one free-text field of a contract and the bound it is held to.
type text struct {
	where, value string
	limit        int
}

// texts is every free-text field of the contract, so the two text rules read
// one list.
func (contract *Contract) texts() []text {
	out := []text{{"summary", contract.Summary, MaxSummaryRunes}}
	for i, r := range contract.Rules {
		at := label(i, r)
		out = append(out,
			text{at + " title", r.Title, MaxTitleRunes},
			text{at + " rationale", r.Rationale, MaxTextRunes},
			text{at + " do", r.Do, MaxTextRunes},
			text{at + " dont", r.Dont, MaxTextRunes},
		)
	}
	return out
}

func checkTextBounded(contract *Contract) error {
	for _, field := range contract.texts() {
		if n := utf8.RuneCountInString(field.value); n > field.limit {
			return refuse("%s is %d characters; it is at most %d, because a contract's text is read into a model's context", field.where, n, field.limit)
		}
	}
	return nil
}

// checkTextPrintable refuses a control character other than a line break or a
// tab, and invalid UTF-8: text a reader shows must be text a person could
// have read in the file.
func checkTextPrintable(contract *Contract) error {
	for _, field := range contract.texts() {
		if !utf8.ValidString(field.value) {
			return refuse("%s is not valid UTF-8", field.where)
		}
		for _, r := range field.value {
			if r == '\n' || r == '\t' {
				continue
			}
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return refuse("%s carries the non-printing character %U; a contract's text is what a person could read in the file", field.where, r)
			}
		}
	}
	return nil
}

// relative is whether p is a slash path that stays inside the directory it
// is relative to.
func relative(p string) bool {
	if p == "" || strings.Contains(p, "\\") || strings.Contains(p, "://") || path.IsAbs(p) {
		return false
	}
	return !slices.Contains(strings.Split(p, "/"), "..")
}

func checkDocsRelative(contract *Contract) error {
	for i, r := range contract.Rules {
		if r.Docs != "" && (!relative(r.Docs) || path.Clean(r.Docs) != r.Docs) {
			return refuse("%s has docs %q; docs is a clean path inside the owner's release, never a URL or a path that leaves it", label(i, r), r.Docs)
		}
	}
	return nil
}

func checkDetectBounded(contract *Contract) error {
	for i, r := range contract.Rules {
		if len(r.Detect) > MaxDetectClauses {
			return refuse("%s has %d detect clauses; a rule has at most %d", label(i, r), len(r.Detect), MaxDetectClauses)
		}
	}
	return nil
}

var languageName = regexp.MustCompile(`^[a-z][a-z0-9+#-]*$`)

func checkDetectLanguage(contract *Contract) error {
	for i, r := range contract.Rules {
		for _, clause := range r.Detect {
			if !languageName.MatchString(clause.Language) {
				return refuse("%s has a detect clause for the language %q; a language is a lower-case name such as go or python", label(i, r), clause.Language)
			}
		}
	}
	return nil
}

func checkDetectOnePerLanguage(contract *Contract) error {
	for i, r := range contract.Rules {
		seen := map[string]bool{}
		for _, clause := range r.Detect {
			if seen[clause.Language] {
				return refuse("%s has two detect clauses for %s; a rule has one clause per language", label(i, r), clause.Language)
			}
			seen[clause.Language] = true
		}
	}
	return nil
}

func checkDetectForbids(contract *Contract) error {
	for i, r := range contract.Rules {
		for _, clause := range r.Detect {
			if len(clause.ForbidCall) == 0 && len(clause.ForbidImport) == 0 {
				return refuse("%s has a detect clause for %s that forbids nothing; a clause lists forbid-call or forbid-import", label(i, r), clause.Language)
			}
		}
	}
	return nil
}

func checkSymbolsBounded(contract *Contract) error {
	for i, r := range contract.Rules {
		for _, clause := range r.Detect {
			if n := max(len(clause.ForbidCall), len(clause.ForbidImport)); n > MaxSymbols {
				return refuse("%s has a detect clause for %s listing %d symbols; a list has at most %d", label(i, r), clause.Language, n, MaxSymbols)
			}
		}
	}
	return nil
}

// A symbol is a literal name and never a pattern: path-like parts joined by
// dots, slashes, colons or hyphens. A call is qualified — it names what owns
// the function — because a bare name (connect, Open) would match every
// function so named in a consumer.
var (
	importName = regexp.MustCompile(`^[A-Za-z0-9_@][A-Za-z0-9_@~-]*([./:][A-Za-z0-9_@~-]+)*$`)
	callName   = regexp.MustCompile(`^[A-Za-z0-9_@][A-Za-z0-9_@~-]*([./:][A-Za-z0-9_@~-]+)*[.:][A-Za-z_][A-Za-z0-9_]*$`)
)

func checkCallShape(contract *Contract) error {
	for i, r := range contract.Rules {
		for _, clause := range r.Detect {
			for _, symbol := range clause.ForbidCall {
				if !callName.MatchString(symbol) {
					return refuse("%s forbids the %s call %q; a call is a literal, qualified name such as database/sql.Open or psycopg.connect, never a bare name or a pattern", label(i, r), clause.Language, symbol)
				}
			}
		}
	}
	return nil
}

func checkImportShape(contract *Contract) error {
	for i, r := range contract.Rules {
		for _, clause := range r.Detect {
			for _, symbol := range clause.ForbidImport {
				if !importName.MatchString(symbol) {
					return refuse("%s forbids the %s import %q; an import is a literal package or module name, never a pattern", label(i, r), clause.Language, symbol)
				}
			}
		}
	}
	return nil
}

func firstDuplicate(values []string) (string, bool) {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return value, true
		}
		seen[value] = true
	}
	return "", false
}

func checkSymbolsUnique(contract *Contract) error {
	for i, r := range contract.Rules {
		for _, clause := range r.Detect {
			for _, list := range [][]string{clause.ForbidCall, clause.ForbidImport} {
				if symbol, twice := firstDuplicate(list); twice {
					return refuse("%s lists %q twice in its detect clause for %s", label(i, r), symbol, clause.Language)
				}
			}
		}
	}
	return nil
}

func checkExceptNeedsDetect(contract *Contract) error {
	for i, r := range contract.Rules {
		if len(r.ExceptPaths) > 0 && len(r.Detect) == 0 {
			return refuse("%s has except-paths and no detect clause; an excepted path is where detection does not apply, and this rule detects nothing", label(i, r))
		}
	}
	return nil
}

func checkExceptBounded(contract *Contract) error {
	for i, r := range contract.Rules {
		if len(r.ExceptPaths) > MaxExceptPaths {
			return refuse("%s has %d except-paths; a rule has at most %d", label(i, r), len(r.ExceptPaths), MaxExceptPaths)
		}
	}
	return nil
}

func checkExceptRelative(contract *Contract) error {
	for i, r := range contract.Rules {
		for _, glob := range r.ExceptPaths {
			if !relative(glob) {
				return refuse("%s excepts the path %q; an excepted path is a glob relative to the consuming service that stays inside it", label(i, r), glob)
			}
			if _, err := path.Match(strings.ReplaceAll(glob, "**", "*"), ""); err != nil {
				return refuse("%s excepts the path %q, which is not a glob: %v", label(i, r), glob, err)
			}
		}
	}
	return nil
}

func checkExceptUnique(contract *Contract) error {
	for i, r := range contract.Rules {
		if glob, twice := firstDuplicate(r.ExceptPaths); twice {
			return refuse("%s excepts the path %q twice", label(i, r), glob)
		}
	}
	return nil
}
