package solutionhost

import (
	"fmt"
	"math"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/internal/wire"
	"github.com/codefly-dev/core/resources/names"
)

// Language is one language the build's size is counted in.
//
// The set is CLOSED and is read from here. A producer that keeps its own copy
// drifts the moment this one moves, and a reader refuses a language it does
// not know rather than displaying a number under a name it cannot place. It is
// the set of languages codefly services are written in — the five the
// languages package names as runtimes, spelled as that package spells them,
// which TestTheBuildSizeVocabulariesAreExactlyThese holds without importing it
// here: that package reaches the runtime toolchain and a presence document is
// read by binaries that must not.
type Language string

const (
	// LanguageGo counts .go files.
	LanguageGo Language = "go"
	// LanguageJavaScript counts .js, .jsx, .mjs and .cjs files.
	LanguageJavaScript Language = "javascript"
	// LanguagePython counts .py files.
	LanguagePython Language = "python"
	// LanguageRust counts .rs files.
	LanguageRust Language = "rust"
	// LanguageTypeScript counts .ts, .tsx, .mts and .cts files.
	LanguageTypeScript Language = "typescript"
)

// languages is the vocabulary, in the order a document's rows are sorted.
var languages = []Language{LanguageGo, LanguageJavaScript, LanguagePython, LanguageRust, LanguageTypeScript}

// Languages returns the closed vocabulary, sorted. A consumer reads the set
// from here and never keeps a copy.
func Languages() []Language { return slices.Clone(languages) }

// sizeSurfaces are the two sides a counted line is attributed to. The build's
// size knows backend and frontend and nothing else — a client artifact's
// source is counted under whichever side its producer attributes it to —
// because that is what the catalogue shows and what the manifest's author
// can say about a path. It is a subset of the artifact surfaces, asserted
// whole by test.
var sizeSurfaces = []Surface{SurfaceBackend, SurfaceFrontend}

// extensions maps a file extension, as spelled, to the language it names. A
// file whose extension is absent here is not code the build's size counts: no
// line of it reaches any total, whatever it contains. The set agrees with
// code/semantic's grammars for every language both know; it is kept here
// rather than imported because that package is the one cgo surface in core.
var extensions = map[string]Language{
	".go":  LanguageGo,
	".py":  LanguagePython,
	".ts":  LanguageTypeScript,
	".tsx": LanguageTypeScript,
	".mts": LanguageTypeScript,
	".cts": LanguageTypeScript,
	".js":  LanguageJavaScript,
	".jsx": LanguageJavaScript,
	".mjs": LanguageJavaScript,
	".cjs": LanguageJavaScript,
	".rs":  LanguageRust,
}

// LanguageOf returns the language a file's extension names, and whether it
// names one. The match is on the extension as spelled: "main.go" is Go, and
// "README.md" is no language at all.
func LanguageOf(file string) (Language, bool) {
	language, known := extensions[path.Ext(file)]
	return language, known
}

// BuildSize is the build's size: lines of code per language, each line counted
// backend or frontend, the totals, and the paths the manifest declared vendored,
// which the producer excluded.
//
// It is a BUILD FACT. The producer counts it over the same tree it digests as
// the release, when it renders the generation, and it travels inside the signed
// canonical bytes beside the release digest. A host never counts: it has no
// source, and a number computed later would describe something other than the
// build it sits beside.
//
// The section is OPTIONAL in the v2 schema. A document without it is an older
// producer's and is accepted — the facts here are read by a catalogue, not held
// by a host against a running container, so their absence weakens no
// verification and is not the v1 case the schema step exists for. A document
// that carries the section is held to every rule in buildSizeRules.
type BuildSize struct {
	// Languages is one row per language with at least one counted line, each
	// row carrying its backend and its frontend lines. Declared, empty when
	// no file in any known language was found; absent is refused, because an
	// absent list and "nothing was found" must not look the same.
	Languages *[]LanguageSize `yaml:"languages" json:"languages"`

	// Backend is the sum of every row's backend lines.
	Backend uint64 `yaml:"backend" json:"backend"`

	// Frontend is the sum of every row's frontend lines.
	Frontend uint64 `yaml:"frontend" json:"frontend"`

	// Total is Backend plus Frontend.
	Total uint64 `yaml:"total" json:"total"`

	// Vendored are the path prefixes the producer excluded, exactly as the
	// manifest declared them (resources.Module.Vendored): canonical relative
	// paths in names.IsPathPrefix's spelling, each once, none under another.
	// Declared, empty when the manifest declares none; absent is refused.
	Vendored *[]string `yaml:"vendored" json:"vendored"`
}

// LanguageSize is one language's counted lines, by the side they belong to.
type LanguageSize struct {
	// Language is the row's language, one of Languages.
	Language Language `yaml:"language" json:"language"`
	// Backend is the lines attributed to the backend.
	Backend uint64 `yaml:"backend" json:"backend"`
	// Frontend is the lines attributed to the frontend.
	Frontend uint64 `yaml:"frontend" json:"frontend"`
}

// A rule is one refusal this reader enforces, named: a conformance fixture
// says which rule refuses it, and the package's self-check deletes each rule
// in turn and proves a fixture notices. Three kinds share one table, applied
// in this order, so a document is refused for one reason, named — the same
// reason whichever reader refused it:
//
//   - a DECODING rule is enforced where the bytes are read (decodeStrict),
//     before any typed field exists, and is listed here with no check of its
//     own so the fixture table and the self-check cover it like any other;
//   - a NODE rule runs over the build_size node of the YAML tree before the
//     typed decoder sees it, because yaml.v3 REPAIRS what it cannot represent
//     — a fraction is truncated, a null and an absent field become zero, a
//     number past uint64 saturates — and a count repaired before validation
//     is a total that agrees with rows it was never written to agree with;
//   - a MODEL rule runs over the decoded document.
//
// The table holds the build-size section's rules and the decoding rules every
// field of the document shares. The presence document's older rules — kind,
// identifiers, digests, the workload biconditional — predate the table and are
// not in it; moving them is a change to a package whose digests are pinned.
type rule struct {
	name string
	// decoding marks a rule decodeStrict enforces by name; it has no check.
	decoding bool
	// node is a rule over the build_size node, run before typed decoding.
	node func(section *yaml.Node) error
	// check is a rule over the decoded document.
	check func(document *SolutionHostBinding) error
	// inherent marks a rule the self-check cannot delete: YAML syntax is not
	// a rule of this package, only a precondition of reading anything.
	inherent bool
}

// The rules, by name. A refused fixture names one of these; a reader that
// drops one fails the kit on that fixture.
const (
	ruleWellFormed      = "well-formed"
	ruleSchema          = "schema"
	ruleMappingKeysOnce = "mapping-keys-once"
	ruleNoMergeKeys     = "no-merge-keys"
	ruleNoNulls         = "no-explicit-nulls"
	ruleWholeNumbers    = "numbers-are-whole"
	ruleKeyIsAName      = "key-is-a-name"
	ruleKnownFields     = "known-fields"
	ruleOneDocument     = "one-document"

	ruleBuildSizeFieldsDeclared = "build-size-fields-declared"
	ruleBuildSizeCountsDecimal  = "build-size-counts-decimal"
	ruleBuildSizeCountsFit      = "build-size-counts-fit"

	ruleBuildSizeAbsentWhenRemoved = "build-size-absent-when-removed"
	ruleBuildSizeLanguagesDeclared = "build-size-languages-declared"
	ruleBuildSizeLanguageKnown     = "build-size-language-known"
	ruleBuildSizeLanguageUnique    = "build-size-language-unique"
	ruleBuildSizeLanguageCounts    = "build-size-language-counts"
	ruleBuildSizeVendoredDeclared  = "build-size-vendored-declared"
	ruleBuildSizeVendoredCanonical = "build-size-vendored-canonical"
	ruleBuildSizeVendoredUnique    = "build-size-vendored-unique"
	ruleBuildSizeVendoredDisjoint  = "build-size-vendored-disjoint"
	ruleBuildSizeBackendSumFits    = "build-size-backend-sum-fits"
	ruleBuildSizeBackendTotal      = "build-size-backend-total"
	ruleBuildSizeFrontendSumFits   = "build-size-frontend-sum-fits"
	ruleBuildSizeFrontendTotal     = "build-size-frontend-total"
	ruleBuildSizeTotalFits         = "build-size-total-fits"
	ruleBuildSizeTotal             = "build-size-total"
)

// rules is the order a document is held to: how the bytes read, then what the
// build_size node says before it is typed, then the decoded section — each
// list's presence, then its entries, then the totals over them, a sum that
// does not fit refused before one that disagrees.
func rules() []rule {
	return []rule{
		{name: ruleWellFormed, decoding: true, inherent: true},
		{name: ruleSchema, decoding: true},
		{name: ruleMappingKeysOnce, decoding: true},
		{name: ruleNoMergeKeys, decoding: true},
		{name: ruleNoNulls, decoding: true},
		{name: ruleWholeNumbers, decoding: true},
		{name: ruleKeyIsAName, decoding: true},
		{name: ruleBuildSizeFieldsDeclared, node: checkBuildSizeFieldsDeclared},
		{name: ruleBuildSizeCountsDecimal, node: checkBuildSizeCountsDecimal},
		{name: ruleBuildSizeCountsFit, node: checkBuildSizeCountsFit},
		{name: ruleKnownFields, decoding: true},
		{name: ruleOneDocument, decoding: true},
		{name: ruleBuildSizeAbsentWhenRemoved, check: checkBuildSizeAbsentWhenRemoved},
		{name: ruleBuildSizeLanguagesDeclared, check: checkBuildSizeLanguagesDeclared},
		{name: ruleBuildSizeLanguageKnown, check: checkBuildSizeLanguagesKnown},
		{name: ruleBuildSizeLanguageUnique, check: checkBuildSizeLanguagesUnique},
		{name: ruleBuildSizeLanguageCounts, check: checkBuildSizeLanguagesCount},
		{name: ruleBuildSizeVendoredDeclared, check: checkBuildSizeVendoredDeclared},
		{name: ruleBuildSizeVendoredCanonical, check: checkBuildSizeVendoredCanonical},
		{name: ruleBuildSizeVendoredUnique, check: checkBuildSizeVendoredUnique},
		{name: ruleBuildSizeVendoredDisjoint, check: checkBuildSizeVendoredDisjoint},
		{name: ruleBuildSizeBackendSumFits, check: checkBuildSizeBackendSumFits},
		{name: ruleBuildSizeBackendTotal, check: checkBuildSizeBackendTotal},
		{name: ruleBuildSizeFrontendSumFits, check: checkBuildSizeFrontendSumFits},
		{name: ruleBuildSizeFrontendTotal, check: checkBuildSizeFrontendTotal},
		{name: ruleBuildSizeTotalFits, check: checkBuildSizeTotalFits},
		{name: ruleBuildSizeTotal, check: checkBuildSizeTotal},
	}
}

// ruleNames lists every rule, in order.
func ruleNames() []string {
	all := rules()
	out := make([]string, 0, len(all))
	for _, r := range all {
		out = append(out, r.name)
	}
	return out
}

// checkBuildSizeNodes runs every node rule but the one named over the
// build_size node of a parsed tree, before the typed decoder reads it; ""
// deletes none. A document without the section has nothing to check.
func checkBuildSizeNodes(tree *yaml.Node, without string) error {
	section := buildSizeNode(tree)
	if section == nil {
		return nil
	}
	for _, r := range rules() {
		if r.node == nil || r.name == without {
			continue
		}
		if err := r.node(section); err != nil {
			return err
		}
	}
	return nil
}

// validateBuildSize runs every model rule but the one named, in order; ""
// deletes none. The rules read the section through the document, so a rule
// about the section's place in the document — a tombstone declaring one — is
// a rule like the others.
func (document *SolutionHostBinding) validateBuildSize(without string) error {
	for _, r := range rules() {
		if r.check == nil || r.name == without {
			continue
		}
		// A document without the section is an older producer's: nothing
		// here applies to it, and the rules read the section unguarded.
		if document.BuildSize == nil && r.name != ruleBuildSizeAbsentWhenRemoved {
			continue
		}
		if err := r.check(document); err != nil {
			return err
		}
	}
	return nil
}

// Validate holds a section to every model rule, so a producer can check what
// it is about to write before it builds the document around it. The node rules
// have nothing to say about a Go value: a uint64 is already whole, decimal and
// in range.
func (size *BuildSize) Validate() error {
	if size == nil {
		return fmt.Errorf("%w: build size is required", ErrInvalid)
	}
	return (&SolutionHostBinding{BuildSize: size}).validateBuildSize("")
}

// buildSizeNode finds the build_size value of the document's top-level
// mapping, aliases resolved, or nil when the document carries none.
func buildSizeNode(tree *yaml.Node) *yaml.Node {
	root := wire.Resolve(tree)
	if root != nil && root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = wire.Resolve(root.Content[0])
	}
	return mappingValue(root, "build_size")
}

// mappingValue returns the value a mapping holds under key, aliases resolved
// on both the key and the value, or nil when the mapping has no such key or
// node is not a mapping. It matches the key's WRITTEN text, which is sound
// only because the shared check has already refused every key not tagged
// !!str: a `!!binary` spelling of build_size decodes to the same name in the
// typed decoder and would otherwise hide the section from every rule here.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	node = wire.Resolve(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		name := wire.Resolve(node.Content[index])
		if name != nil && name.Kind == yaml.ScalarNode && name.Value == key {
			return wire.Resolve(node.Content[index+1])
		}
	}
	return nil
}

// countField is one count the section declares, by the path a refusal names
// and the node carrying it — nil when the field is absent.
type countField struct {
	path string
	node *yaml.Node
}

// buildSizeFields lists the required scalar fields of the section and of every
// row that is a mapping: the three totals, and each row's language and two
// counts. A section or row that is not a mapping, and a languages value that
// is not a list, are left to the typed decoder, which refuses their shape.
func buildSizeFields(section *yaml.Node) (counts, others []countField) {
	for _, name := range []string{"backend", "frontend", "total"} {
		counts = append(counts, countField{path: "build_size." + name, node: mappingValue(section, name)})
	}
	rows := wire.Resolve(mappingValue(section, "languages"))
	if rows == nil || rows.Kind != yaml.SequenceNode {
		return counts, others
	}
	for index, row := range rows.Content {
		row = wire.Resolve(row)
		if row == nil || row.Kind != yaml.MappingNode {
			continue
		}
		at := fmt.Sprintf("build_size.languages[%d]", index)
		others = append(others, countField{path: at + ".language", node: mappingValue(row, "language")})
		for _, name := range []string{"backend", "frontend"} {
			counts = append(counts, countField{path: at + "." + name, node: mappingValue(row, name)})
		}
	}
	return counts, others
}

func checkBuildSizeFieldsDeclared(section *yaml.Node) error {
	counts, others := buildSizeFields(section)
	for _, field := range append(counts, others...) {
		if field.node == nil {
			return fmt.Errorf("%w: %s must be declared; an absent count decodes as zero, and a zero nobody wrote is a total that agrees with nothing", ErrInvalid, field.path)
		}
	}
	return nil
}

// isDecimalCount reports whether a present count node is a whole number
// written in decimal digits: an integer scalar — plain, or tagged !!int, which
// names the same number — with no sign, no base prefix and no leading zero. A
// string is refused, since it is not a number. A fraction is refused earlier,
// document-wide, as not whole; so is a plain integer past uint64, which yaml
// tags as a float. A leading zero is refused because yaml reads 012 as OCTAL
// ten, so a count written that way would be validated as a number nobody
// wrote.
func isDecimalCount(node *yaml.Node) bool {
	node = wire.Resolve(node)
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" || node.Value == "" {
		return false
	}
	if strings.Trim(node.Value, "0123456789") != "" {
		return false
	}
	return node.Value == "0" || node.Value[0] != '0'
}

func checkBuildSizeCountsDecimal(section *yaml.Node) error {
	counts, _ := buildSizeFields(section)
	for _, field := range counts {
		if field.node != nil && !isDecimalCount(field.node) {
			return fmt.Errorf("%w: %s is %q, not a whole number written in decimal digits; a count has one spelling, and yaml would otherwise read a sign, a base prefix, a leading zero or a string into a number nobody wrote", ErrInvalid, field.path, wire.Resolve(field.node).Value)
		}
	}
	return nil
}

func checkBuildSizeCountsFit(section *yaml.Node) error {
	counts, _ := buildSizeFields(section)
	for _, field := range counts {
		if field.node == nil || !isDecimalCount(field.node) {
			continue
		}
		if _, err := strconv.ParseUint(wire.Resolve(field.node).Value, 10, 64); err != nil {
			return fmt.Errorf("%w: %s is %s, more than a count can hold; yaml would otherwise saturate it to the largest count silently", ErrInvalid, field.path, wire.Resolve(field.node).Value)
		}
	}
	return nil
}

func checkBuildSizeAbsentWhenRemoved(document *SolutionHostBinding) error {
	if document.Removed && document.BuildSize != nil {
		return fmt.Errorf("%w: a removed generation declares no build_size; a size describes a build, and this generation declares that there is none", ErrInvalid)
	}
	return nil
}

// languages and vendored read the declared lists nil-safely, so a rule that
// runs with the declaration rule deleted (the self-check does that) reads an
// absent list as empty rather than dereferencing nothing.
func (size *BuildSize) languages() []LanguageSize {
	if size.Languages == nil {
		return nil
	}
	return *size.Languages
}

func (size *BuildSize) vendored() []string {
	if size.Vendored == nil {
		return nil
	}
	return *size.Vendored
}

func checkBuildSizeLanguagesDeclared(document *SolutionHostBinding) error {
	if size := document.BuildSize; size != nil && size.Languages == nil {
		return fmt.Errorf("%w: build_size.languages must be declared, as a list that is empty when no file in a known language was counted; an absent list and \"nothing was found\" must not look the same", ErrInvalid)
	}
	return nil
}

func checkBuildSizeLanguagesKnown(document *SolutionHostBinding) error {
	for _, row := range document.BuildSize.languages() {
		if !slices.Contains(languages, row.Language) {
			return fmt.Errorf("%w: build_size language %q is not one of %v", ErrInvalid, row.Language, languages)
		}
	}
	return nil
}

func checkBuildSizeLanguagesUnique(document *SolutionHostBinding) error {
	seen := make(map[Language]struct{}, len(document.BuildSize.languages()))
	for _, row := range document.BuildSize.languages() {
		if _, exists := seen[row.Language]; exists {
			return fmt.Errorf("%w: build_size language %q is declared twice; one language is one row, carrying both its backend and its frontend lines", ErrInvalid, row.Language)
		}
		seen[row.Language] = struct{}{}
	}
	return nil
}

func checkBuildSizeLanguagesCount(document *SolutionHostBinding) error {
	for _, row := range document.BuildSize.languages() {
		if row.Backend == 0 && row.Frontend == 0 {
			return fmt.Errorf("%w: build_size language %q counts no line; a language that was not found is not written", ErrInvalid, row.Language)
		}
	}
	return nil
}

func checkBuildSizeVendoredDeclared(document *SolutionHostBinding) error {
	if size := document.BuildSize; size != nil && size.Vendored == nil {
		return fmt.Errorf("%w: build_size.vendored must be declared, as a list that is empty when the manifest declares no vendored path; an absent list and \"nothing was excluded\" must not look the same", ErrInvalid)
	}
	return nil
}

func checkBuildSizeVendoredCanonical(document *SolutionHostBinding) error {
	for _, entry := range document.BuildSize.vendored() {
		if !names.IsPathPrefix(entry) {
			return fmt.Errorf("%w: build_size vendored path %q is not a canonical relative path: it is slash-separated, relative to the module directory, with no leading ./ or /, no .. and no trailing slash", ErrInvalid, entry)
		}
	}
	return nil
}

func checkBuildSizeVendoredUnique(document *SolutionHostBinding) error {
	seen := make(map[string]struct{}, len(document.BuildSize.vendored()))
	for _, entry := range document.BuildSize.vendored() {
		if _, exists := seen[entry]; exists {
			return fmt.Errorf("%w: build_size vendored path %q is declared twice", ErrInvalid, entry)
		}
		seen[entry] = struct{}{}
	}
	return nil
}

func checkBuildSizeVendoredDisjoint(document *SolutionHostBinding) error {
	declared := document.BuildSize.vendored()
	for index, entry := range declared {
		for _, earlier := range declared[:index] {
			if earlier != entry && (names.PathWithin(earlier, entry) || names.PathWithin(entry, earlier)) {
				return fmt.Errorf("%w: build_size vendored paths %q and %q overlap; a path inside an excluded one excludes nothing more, so the outer one is declared alone", ErrInvalid, earlier, entry)
			}
		}
	}
	return nil
}

func checkBuildSizeBackendSumFits(document *SolutionHostBinding) error {
	if _, held := sumLines(document.BuildSize.languages(), func(row LanguageSize) uint64 { return row.Backend }); !held {
		return fmt.Errorf("%w: the languages' backend lines sum to more than a count can hold; a sum that wraps could agree with a stated total by accident", ErrInvalid)
	}
	return nil
}

func checkBuildSizeBackendTotal(document *SolutionHostBinding) error {
	size := document.BuildSize
	if sum, held := sumLines(size.languages(), func(row LanguageSize) uint64 { return row.Backend }); held && sum != size.Backend {
		return fmt.Errorf("%w: build_size.backend is %d and its languages' backend lines sum to %d; a total equals the sum of its parts", ErrInvalid, size.Backend, sum)
	}
	return nil
}

func checkBuildSizeFrontendSumFits(document *SolutionHostBinding) error {
	if _, held := sumLines(document.BuildSize.languages(), func(row LanguageSize) uint64 { return row.Frontend }); !held {
		return fmt.Errorf("%w: the languages' frontend lines sum to more than a count can hold; a sum that wraps could agree with a stated total by accident", ErrInvalid)
	}
	return nil
}

func checkBuildSizeFrontendTotal(document *SolutionHostBinding) error {
	size := document.BuildSize
	if sum, held := sumLines(size.languages(), func(row LanguageSize) uint64 { return row.Frontend }); held && sum != size.Frontend {
		return fmt.Errorf("%w: build_size.frontend is %d and its languages' frontend lines sum to %d; a total equals the sum of its parts", ErrInvalid, size.Frontend, sum)
	}
	return nil
}

func checkBuildSizeTotalFits(document *SolutionHostBinding) error {
	if _, held := addLines(document.BuildSize.Backend, document.BuildSize.Frontend); !held {
		return fmt.Errorf("%w: backend plus frontend is more than a count can hold; a total that wraps could agree with the parts by accident", ErrInvalid)
	}
	return nil
}

func checkBuildSizeTotal(document *SolutionHostBinding) error {
	size := document.BuildSize
	if sum, held := addLines(size.Backend, size.Frontend); held && sum != size.Total {
		return fmt.Errorf("%w: build_size.total is %d and backend plus frontend is %d; a total equals the sum of its parts", ErrInvalid, size.Total, sum)
	}
	return nil
}

// sumLines adds one side of every row, reporting false when the sum does not
// fit a count.
func sumLines(rows []LanguageSize, side func(LanguageSize) uint64) (uint64, bool) {
	var sum uint64
	for _, row := range rows {
		var held bool
		if sum, held = addLines(sum, side(row)); !held {
			return 0, false
		}
	}
	return sum, true
}

func addLines(left, right uint64) (uint64, bool) {
	if left > math.MaxUint64-right {
		return 0, false
	}
	return left + right, true
}

// canonical returns a copy with both collections in the document's canonical
// order — rows by language, excluded paths by name — and an empty list kept an
// empty list rather than collapsed to null, for the reason CanonicalBytes gives
// for the non-authenticating list. A producer emitting either from a Go map
// would otherwise move the digest per process.
func (size *BuildSize) canonical() *BuildSize {
	normalized := *size
	rows := slices.Clone(size.languages())
	if rows == nil {
		rows = []LanguageSize{}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Language < rows[j].Language })
	normalized.Languages = &rows
	excluded := slices.Sorted(slices.Values(size.vendored()))
	if excluded == nil {
		excluded = []string{}
	}
	normalized.Vendored = &excluded
	return &normalized
}
