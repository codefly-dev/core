package usage

import (
	"regexp"
	"sort"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// The two kinds of finding, one per detect clause field.
const (
	// FindingCall is a call a rule forbids.
	FindingCall = "call"
	// FindingImport is an import a rule forbids.
	FindingImport = "import"
)

// Finding is one place in a consumer where a rule's detect clause matched.
type Finding struct {
	// RuleID is the rule that matched; the rule itself carries what to tell
	// whoever wrote the code.
	RuleID string `json:"rule_id"`
	// Severity is the rule's severity when it matched.
	Severity string `json:"severity"`
	// Language is the language of the file and of the clause.
	Language string `json:"language"`
	// Kind is FindingCall or FindingImport.
	Kind string `json:"kind"`
	// Symbol is the forbidden symbol, as the contract spells it.
	Symbol string `json:"symbol"`
	// Path is the file, relative to the root the index was taken from.
	Path string `json:"path"`
	// Line and Column locate a call, 1-based. They are zero for an import:
	// the index records which modules a file imports, not where.
	Line   int32 `json:"line,omitempty"`
	Column int32 `json:"column,omitempty"`
}

// Detect reports every place in a consumer's source where one of the
// contract's detect clauses matches. The source is read through its semantic
// index — the body-free projection a language agent already produces — so
// this function parses no language and reads no file.
//
// It reports what it can be sure of and nothing else:
//
//   - An import matches when a file imports the forbidden package or module,
//     or anything beneath it.
//   - A call matches when the file imports what owns the function and calls
//     it the way that import is ordinarily named: pgxpool.New for
//     github.com/jackc/pgx/v5/pgxpool.New, psycopg.connect for
//     psycopg.connect.
//
// A package whose path does not end in its name is matched by the name its
// owner declares for it (Detect.Names).
//
// What that leaves out, because the index records calls as written and does
// not say what a name is bound to: a call through an import alias (import pg
// "…/pgxpool"; import psycopg as pg), a call of a name imported on its own
// (from psycopg import connect; import { connect }), and a call whose
// qualifier another import of the same file is also ordinarily written as (a
// consumer's own package named like the owner's). All are missed, never
// guessed: a finding that might be the consumer's own function would teach
// people to stop reading findings. An owner that needs the module kept out
// entirely says so with forbid-import, which has neither gap.
//
// A file matching one of the rule's except-paths is skipped for that rule.
// Findings are sorted by path, position, rule and symbol.
func (contract *Contract) Detect(index *basev0.SemanticIndex) []Finding {
	if contract == nil || index == nil {
		return nil
	}
	calls := map[string][]*basev0.SemanticUse{}
	for _, symbol := range index.GetSymbols() {
		for _, call := range symbol.GetCalls() {
			if path := call.GetLocation().GetPath(); path != "" {
				calls[path] = append(calls[path], call)
			}
		}
	}
	var findings []Finding
	for _, rule := range contract.Rules {
		if len(rule.Detect) == 0 {
			continue
		}
		excepted := exceptions(rule.ExceptPaths)
		for _, file := range index.GetFiles() {
			clause := rule.Detects(file.GetLanguage())
			if clause == nil || excepted(file.GetPath()) {
				continue
			}
			finding := Finding{RuleID: rule.ID, Severity: rule.Severity, Language: clause.Language, Path: file.GetPath()}
			for _, forbidden := range clause.ForbidImport {
				if imports(file, forbidden) {
					found := finding
					found.Kind, found.Symbol = FindingImport, forbidden
					findings = append(findings, found)
				}
			}
			for _, forbidden := range clause.ForbidCall {
				owner, function := splitCall(forbidden)
				if !reaches(file, owner) {
					continue
				}
				written := spellings(owner, function)
				if name := clause.Names[owner]; name != "" {
					// The owner said what its package is called: that is the
					// spelling, and the path's last element is not.
					written = map[string]bool{name + "." + function: true}
				}
				// Another import of the file that is ordinarily written the
				// same way makes the call ambiguous: postgres.NewStore may be
				// the consumer's own package named postgres. Ambiguous is
				// missed, like an alias, never reported.
				for _, imported := range file.GetImports() {
					if imported == owner {
						continue
					}
					for spelling := range spellings(imported, function) {
						delete(written, spelling)
					}
				}
				seen := map[[2]int32]bool{}
				for _, call := range calls[file.GetPath()] {
					at := [2]int32{call.GetLocation().GetStartLine(), call.GetLocation().GetStartColumn()}
					if !written[call.GetName()] || seen[at] {
						continue
					}
					seen[at] = true
					found := finding
					found.Kind, found.Symbol, found.Line, found.Column = FindingCall, forbidden, at[0], at[1]
					findings = append(findings, found)
				}
			}
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		switch {
		case a.Path != b.Path:
			return a.Path < b.Path
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.Column != b.Column:
			return a.Column < b.Column
		case a.RuleID != b.RuleID:
			return a.RuleID < b.RuleID
		}
		return a.Symbol < b.Symbol
	})
	return findings
}

// separators are what joins the parts of a package or module path, in the
// languages a clause may name.
var separators = []string{"/", ".", "::"}

// beneath is whether imported is forbidden itself or something under it.
func beneath(imported, forbidden string) bool {
	if imported == forbidden {
		return true
	}
	for _, separator := range separators {
		if strings.HasPrefix(imported, forbidden+separator) {
			return true
		}
	}
	return false
}

func imports(file *basev0.SemanticFile, forbidden string) bool {
	for _, imported := range file.GetImports() {
		if beneath(imported, forbidden) {
			return true
		}
	}
	return false
}

// reaches is whether a file imports what owns a function: the owner itself,
// or a parent module through which a dotted call reaches it (import asyncpg,
// then asyncpg.pool.create_pool).
func reaches(file *basev0.SemanticFile, owner string) bool {
	for _, imported := range file.GetImports() {
		if imported == owner || strings.HasPrefix(owner, imported+".") {
			return true
		}
	}
	return false
}

// splitCall divides a forbidden call into what owns the function and the
// function. The contract's rules guarantee the last separator is there.
func splitCall(symbol string) (owner, function string) {
	at := strings.LastIndexAny(symbol, ".:")
	return symbol[:at], symbol[at+1:]
}

var (
	// majorVersion is the path element Go appends for a module's major
	// version, which is not the package's name.
	majorVersion = regexp.MustCompile(`^v[0-9]+$`)
	// versionSuffix is the gopkg.in spelling of the same thing.
	versionSuffix = regexp.MustCompile(`\.v[0-9]+$`)
)

// spellings are the ways a call of owner's function is ordinarily written
// when owner is imported without an alias: by the owner's last path element
// (pgxpool.New), and by its whole dotted path (asyncpg.pool.create_pool).
func spellings(owner, function string) map[string]bool {
	out := map[string]bool{}
	if !strings.Contains(owner, "/") {
		out[owner+"."+function] = true
	}
	parts := strings.Split(owner, "/")
	local := parts[len(parts)-1]
	if majorVersion.MatchString(local) && len(parts) > 1 {
		local = parts[len(parts)-2]
	}
	local = versionSuffix.ReplaceAllString(local, "")
	if at := strings.LastIndex(local, "."); at >= 0 && strings.Contains(owner, "/") {
		// A path element with a dot that is not a version (example.com/a.b)
		// names its package by convention only; leave it to forbid-import.
		return out
	} else if at >= 0 {
		local = local[at+1:]
	}
	out[local+"."+function] = true
	return out
}

// exceptions compiles a rule's except-paths into one test. A glob is matched
// against the whole path: "**" crosses directories, "*" and "?" do not.
func exceptions(globs []string) func(path string) bool {
	patterns := make([]*regexp.Regexp, 0, len(globs))
	for _, glob := range globs {
		patterns = append(patterns, globPattern(glob))
	}
	return func(path string) bool {
		for _, pattern := range patterns {
			if pattern.MatchString(path) {
				return true
			}
		}
		return false
	}
}

func globPattern(glob string) *regexp.Regexp {
	var out strings.Builder
	out.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch {
		case strings.HasPrefix(glob[i:], "**/"):
			out.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			out.WriteString(".*")
			i++
		case glob[i] == '*':
			out.WriteString("[^/]*")
		case glob[i] == '?':
			out.WriteString("[^/]")
		default:
			out.WriteString(regexp.QuoteMeta(string(glob[i])))
		}
	}
	out.WriteString("$")
	return regexp.MustCompile(out.String())
}
