package solutionhost

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/internal/conditions"
	"github.com/codefly-dev/core/internal/wire"
	runtimelanguages "github.com/codefly-dev/core/languages"
)

// TestEveryRuleIsProtectedByAFixture is the kit's self-check for the rule
// table: every rule is named by at least one refused fixture, every fixture
// naming a rule names one that exists, and deleting any one rule lets a fixture
// naming it through — accepted, or refused by another rule with another
// message. A rule this test cannot falsify is a rule the kit does not protect.
// Decoding rules are deleted in the decoder, node rules before typed decoding,
// model rules in validation; parse takes the one name and threads it to all
// three.
func TestEveryRuleIsProtectedByAFixture(t *testing.T) {
	names := ruleNames()
	protected := map[string][]Fixture{}
	for _, fixture := range Fixtures() {
		if fixture.Rule == "" {
			continue
		}
		if fixture.Type == DocumentTypeAuthority {
			// Held by TestEveryAuthorityRuleIsProtectedByAFixture, against the
			// authority rule table and the authority reader.
			continue
		}
		if fixture.Type != DocumentTypePresence || fixture.Outcome != OutcomeRejected {
			t.Errorf("fixture %s/%s names rule %s but is not a rejected presence document", fixture.Type, fixture.Name, fixture.Rule)
		}
		if !slices.Contains(names, fixture.Rule) {
			t.Errorf("fixture %s names rule %q, which does not exist", fixture.Name, fixture.Rule)
		}
		if fixture.Message == "" {
			t.Errorf("fixture %s names rule %s without the message its refusal carries", fixture.Name, fixture.Rule)
		}
		protected[fixture.Rule] = append(protected[fixture.Rule], fixture)
	}
	for _, r := range rules() {
		fixtures := protected[r.name]
		if len(fixtures) == 0 {
			t.Errorf("rule %s is protected by no fixture: a reader could drop it and pass the kit", r.name)
			continue
		}
		for _, fixture := range fixtures {
			// With every rule in place, the fixture is refused BY this rule.
			_, err := parse(fixture.Document, "")
			if err == nil || !(errors.Is(err, ErrInvalid) || errors.Is(err, ErrSchema)) || !strings.Contains(err.Error(), fixture.Message) {
				t.Errorf("fixture %s must be refused naming %q (rule %s), got: %v", fixture.Name, fixture.Message, r.name, err)
			}
		}
		if r.inherent {
			continue
		}
		noticed := false
		for _, fixture := range fixtures {
			// With this rule deleted, it is not refused by it.
			_, err := parse(fixture.Document, r.name)
			if err == nil || !strings.Contains(err.Error(), fixture.Message) {
				noticed = true
			} else {
				t.Errorf("fixture %s is still refused naming %q with rule %s deleted: the refusal comes from somewhere else", fixture.Name, fixture.Message, r.name)
			}
		}
		if !noticed {
			t.Errorf("rule %s can be deleted and the kit still passes: %d fixture(s) name it but none notices", r.name, len(fixtures))
		}
	}
}

// TestDeletingARuleAdmitsItsWitness: for the rules whose witness is a repair —
// a count yaml would silently fix — deleting the rule must ACCEPT the fixture,
// not merely refuse it differently. That is the finding the node rules exist
// for: with the rule gone, the repaired document validates with totals that
// agree with rows the file never wrote.
func TestDeletingARuleAdmitsItsWitness(t *testing.T) {
	// Not listed: build-size-count-fractional (with the whole-number rule
	// deleted the decimal node rule still refuses "12416.9"),
	// build-size-count-too-large (with the fit rule deleted the typed decoder
	// refuses an explicit !!int past uint64) and build-size-count-negative
	// (the typed decoder refuses a negative uint64) — each is defence in depth
	// and is held by TestEveryRuleIsProtectedByAFixture as refused differently.
	for fixture, rule := range map[string]string{
		"build-size-binary-key":               ruleKeyIsAName,
		"build-size-merge-key":                ruleNoMergeKeys,
		"merge-key-at-top-level":              ruleNoMergeKeys,
		"build-size-count-leading-zero":       ruleBuildSizeCountsDecimal,
		"build-size-row-frontend-omitted":     ruleBuildSizeFieldsDeclared,
		"build-size-count-hex":                ruleBuildSizeCountsDecimal,
		"build-size-backend-sum-overflows":    ruleBuildSizeBackendSumFits,
		"build-size-frontend-sum-overflows":   ruleBuildSizeFrontendSumFits,
		"build-size-total-overflows":          ruleBuildSizeTotalFits,
		"build-size-unknown-field":            "", // refused by vendored-declared once the unknown field is let through
		"two-documents":                       ruleOneDocument,
		"tombstone-with-build-size":           ruleBuildSizeAbsentWhenRemoved,
		"build-size-vendored-not-utf8":        ruleBuildSizeVendoredCanonical,
		"build-size-language-twice":           ruleBuildSizeLanguageUnique,
		"build-size-unknown-language":         ruleBuildSizeLanguageKnown,
		"build-size-empty-language":           ruleBuildSizeLanguageCounts,
		"build-size-languages-omitted":        ruleBuildSizeLanguagesDeclared,
		"build-size-vendored-omitted":         ruleBuildSizeVendoredDeclared,
		"build-size-vendored-trailing-slash":  ruleBuildSizeVendoredCanonical,
		"build-size-vendored-twice":           ruleBuildSizeVendoredUnique,
		"build-size-vendored-nested":          ruleBuildSizeVendoredDisjoint,
		"build-size-backend-total-disagrees":  ruleBuildSizeBackendTotal,
		"build-size-frontend-total-disagrees": ruleBuildSizeFrontendTotal,
		"build-size-total-disagrees":          ruleBuildSizeTotal,
	} {
		if rule == "" {
			continue
		}
		t.Run(fixture, func(t *testing.T) {
			document := mustFixture(DocumentTypePresence, fixture)
			if _, err := parse(document, rule); err != nil {
				t.Fatalf("with rule %s deleted the fixture must be ACCEPTED — that is the repair the rule refuses — but it was refused: %v", rule, err)
			}
		})
	}
}

func checkFuncName(check any) string {
	full := runtime.FuncForPC(reflect.ValueOf(check).Pointer()).Name()
	return full[strings.LastIndex(full, ".")+1:]
}

// TestEveryTableRuleHoldsExactlyOneCondition is the construction that keeps
// "every condition has a witness" true going forward: a node or model rule is
// a function in buildsize.go holding exactly one refusal, so a second
// condition added inside an existing rule fails here, and a refusal written
// in a function that is no rule's check fails too, because no fixture could
// be required to protect it. The one refusal outside the table in that file
// is Validate's nil check, named and held to exactly one.
func TestEveryTableRuleHoldsExactlyOneCondition(t *testing.T) {
	sites, err := conditions.Sites(".", "ErrInvalid", "ErrSchema")
	if err != nil {
		t.Fatal(err)
	}
	inFile := map[string][]conditions.Site{}
	for _, site := range sites {
		if site.File != "buildsize.go" {
			continue
		}
		if site.Func == "" {
			t.Errorf("buildsize.go:%d is a refusal outside any function", site.Line)
			continue
		}
		inFile[site.Func] = append(inFile[site.Func], site)
	}
	if len(inFile) == 0 {
		t.Fatal("no refusal sites found in buildsize.go: the enumeration itself is broken")
	}
	const outsideTheTable = "Validate"
	if held := len(inFile[outsideTheTable]); held != 1 {
		t.Errorf("%s holds %d refusal conditions; it is exempt for exactly one, the nil section, and anything else it refuses needs a rule and a witness", outsideTheTable, held)
	}
	delete(inFile, outsideTheTable)

	isRule := map[string]string{}
	for _, r := range rules() {
		if r.node != nil {
			isRule[checkFuncName(r.node)] = r.name
		}
		if r.check != nil {
			isRule[checkFuncName(r.check)] = r.name
		}
	}
	for function, found := range inFile {
		rule, governed := isRule[function]
		if !governed {
			t.Errorf("%s holds %d refusal condition(s) but is not the check of any rule, so no fixture can be required to protect it", function, len(found))
			continue
		}
		if len(found) > 1 {
			literals := make([]string, 0, len(found))
			for _, site := range found {
				literals = append(literals, site.Literal)
			}
			t.Errorf("rule %s (%s) holds %d conditions, not one: %q", rule, function, len(found), literals)
		}
	}
	for _, r := range rules() {
		for _, fn := range []any{r.node, r.check} {
			if fn == nil || reflect.ValueOf(fn).IsNil() {
				continue
			}
			if len(inFile[checkFuncName(fn)]) == 0 {
				t.Errorf("rule %s (%s) refuses nothing in buildsize.go: either its condition moved and the row is stale, or the row never had one", r.name, checkFuncName(fn))
			}
		}
	}
}

// TestEveryNewRefusalIsReachedByAFixture holds the refusals this change added
// to the SOURCE rather than to a list: every refusal in buildsize.go, and
// every refusal in the decoder (decodeStrict), must be the refusal some
// rejected presence fixture actually receives. A condition added without a
// counterexample fails here at its own line.
func TestEveryNewRefusalIsReachedByAFixture(t *testing.T) {
	sites, err := conditions.Sites(".", "ErrInvalid", "ErrSchema")
	if err != nil {
		t.Fatal(err)
	}
	var refusals []string
	for _, fixture := range FixturesOf(DocumentTypePresence) {
		if _, err := Parse(fixture.Document); err != nil {
			refusals = append(refusals, err.Error())
		}
	}
	defensive := map[string]string{
		"build size is required":               "Validate(nil); forced in TestABuildSizeIsValidatedOnItsOwn, a Go call no document can make",
		": trailing input after the document:": "a decoder error after the first document that is not a second document; two-documents reaches the sibling condition, and yaml.v3 yields no other error there for well-formed input",
	}
	for _, site := range sites {
		if site.File != "buildsize.go" && site.Func != "decodeStrict" {
			continue
		}
		if site.Literal == "" {
			t.Errorf("%s:%d carries no literal text to match on", site.File, site.Line)
			continue
		}
		reached := slices.ContainsFunc(refusals, func(refusal string) bool { return strings.Contains(refusal, site.Literal) })
		_, declared := defensive[site.Literal]
		switch {
		case !reached && !declared:
			t.Errorf("%s:%d is reached by no fixture and is not declared defensive: %q", site.File, site.Line, site.Literal)
		case reached && declared:
			t.Errorf("%q is declared defensive but a fixture reaches it; the declaration is stale", site.Literal)
		}
	}
}

// TestTheBuildSizeVocabulariesAreExactlyThese guards the closed sets WHOLE,
// which a fixture cannot do for an unnamed added value: a refusal fixture
// names a value that is still absent after the set widens. The language set
// is the set of languages codefly services are written in, spelled as the
// languages package spells them — that package is imported by this TEST only,
// so a presence document's readers never link the runtime toolchain. The two
// sides a line is counted on are exactly the two the catalogue shows, and are
// the artifact surfaces of the same names.
func TestTheBuildSizeVocabulariesAreExactlyThese(t *testing.T) {
	want := []Language{"go", "javascript", "python", "rust", "typescript"}
	if !slices.Equal(languages, want) {
		t.Fatalf("the language vocabulary is %v; the build's size counts %v, sorted", languages, want)
	}
	if !slices.Equal(Languages(), want) {
		t.Fatalf("Languages() answers %v, not the vocabulary", Languages())
	}
	runtimes := []Language{}
	for _, runtime := range []string{"go", "python", "typescript", "javascript", "rust"} {
		resolved := runtimelanguages.FromString(runtime)
		if resolved == runtimelanguages.NotSupported {
			t.Fatalf("the languages package no longer knows %q", runtime)
		}
		runtimes = append(runtimes, Language(resolved))
	}
	slices.Sort(runtimes)
	// The languages package exposes no enumeration, so this holds that every
	// runtime language is counted under its own spelling; a runtime added
	// there without a row here is caught by the whole-set assertion above
	// when the row is added, and by review until then.
	if !slices.Equal(runtimes, want) {
		t.Fatalf("the runtime languages are %v and the build's size counts %v; every runtime language is counted as that package spells it", runtimes, want)
	}
	if !slices.Equal(sizeSurfaces, []Surface{SurfaceBackend, SurfaceFrontend}) {
		t.Fatalf("a counted line is backend or frontend; the sides are %v", sizeSurfaces)
	}
	if string(SurfaceBackend) != "backend" || string(SurfaceFrontend) != "frontend" {
		t.Fatal("a side's spelling moved")
	}
	extensionsSeen := make([]string, 0, len(extensions))
	for extension, language := range extensions {
		if !slices.Contains(languages, language) {
			t.Errorf("extension %s maps to %q, which is not in the vocabulary", extension, language)
		}
		extensionsSeen = append(extensionsSeen, extension)
	}
	slices.Sort(extensionsSeen)
	if wantExtensions := []string{".cjs", ".cts", ".go", ".js", ".jsx", ".mjs", ".mts", ".py", ".rs", ".ts", ".tsx"}; !slices.Equal(extensionsSeen, wantExtensions) {
		t.Fatalf("the counted extensions are %v; documented are %v", extensionsSeen, wantExtensions)
	}
}

// TestTheWriterGuardRefusesAnEncodingThatDoesNotReadBack is the witness the
// writer's read-back guard had been missing: no model Validate accepts is
// known whose real encoding changes on re-read, so the guard is driven here
// over injected encoders instead — one whose bytes the reader refuses, one
// that fails outright, and one whose output drifts between the write and the
// re-read — and each is refused by the branch that exists for it. Deleting the
// read-back or the comparison in marshalWith fails this test.
func TestTheWriterGuardRefusesAnEncodingThatDoesNotReadBack(t *testing.T) {
	document, err := Parse(mustFixture(DocumentTypePresence, "build-size"))
	if err != nil {
		t.Fatal(err)
	}
	// The real encoder passes, which is the control.
	if _, err := marshalWith(document, yaml.Marshal); err != nil {
		t.Fatalf("the real encoding must pass the guard: %v", err)
	}
	// Bytes the reader refuses: the schema line dropped.
	refused := func(value any) ([]byte, error) {
		out, err := yaml.Marshal(value)
		return bytes.Replace(out, []byte("schema: "), []byte("schema_was: "), 1), err
	}
	if _, err := marshalWith(document, refused); err == nil || !strings.Contains(err.Error(), "refused by its own reader") {
		t.Fatalf("an encoding the reader refuses must be refused before it is written: %v", err)
	}
	// An encoder that fails.
	failing := func(any) ([]byte, error) { return nil, errors.New("disk full") }
	if _, err := marshalWith(document, failing); err == nil || !strings.Contains(err.Error(), "cannot be encoded") {
		t.Fatalf("an encoder error must be refused: %v", err)
	}
	// Output that drifts: each call writes a different generation, so what
	// was written and what the re-read document writes are not the same
	// bytes — a document that parses but is not the one that was encoded.
	calls := 0
	drifting := func(value any) ([]byte, error) {
		calls++
		copied := *value.(*SolutionHostBinding)
		copied.Generation += uint64(calls)
		return yaml.Marshal(&copied)
	}
	_, err = marshalWith(document, drifting)
	if err == nil || !strings.Contains(err.Error(), "reads back as a different document") {
		t.Fatalf("an encoding that does not read back must be refused: %v", err)
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("the refusal must carry ErrInvalid: %v", err)
	}
}

// aliasBomb is the document that made the walk exponential before alias
// targets were walked once: x0 anchors a one-element list, and each level
// anchors a list of ten aliases to the level below, so N levels name 10^N
// nodes through 579 bytes. prefix is prepended so the document is otherwise
// one the reader starts on; the bomb's keys are unknown to every model, so a
// reader that survives the walk refuses them.
func aliasBomb(prefix string, levels int) []byte {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString("x0: &a0 [lol]\n")
	for n := 1; n <= levels; n++ {
		b.WriteString(fmt.Sprintf("x%d: &a%d [", n, n))
		for i := 0; i < 10; i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(fmt.Sprintf("*a%d", n-1))
		}
		b.WriteString("]\n")
	}
	return []byte(b.String())
}

// TestAnAliasBombIsAnsweredAtOnce: twelve levels of fan-ten aliases — ten
// trillion node visits if every alias were followed — are answered by
// wire.Check and refused by both readers of this package in well under a
// second, because an alias target is walked once, at its anchor.
func TestAnAliasBombIsAnsweredAtOnce(t *testing.T) {
	const levels = 12
	start := time.Now()
	var tree yaml.Node
	if err := yaml.Unmarshal(aliasBomb("", levels), &tree); err != nil {
		t.Fatal(err)
	}
	if defect := wire.Check(&tree); defect.Kind != wire.NoDefect {
		t.Fatalf("the bomb carries no defect the walk reports; got %v", defect)
	}
	if _, err := Parse(aliasBomb(string(mustFixture(DocumentTypePresence, "build-size")), levels)); err == nil {
		t.Fatal("the bomb's keys are unknown to the presence document and must be refused")
	}
	if _, err := ParseAuthority(aliasBomb(string(mustFixture(DocumentTypeAuthority, "valid")), levels)); err == nil {
		t.Fatal("the bomb's keys are unknown to the authority document and must be refused")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the walk took %s over a %d-level alias bomb; an alias target is walked once, so this is linear", elapsed, levels)
	}
}

// TestSameFileRefusesAFileReplacedBetweenLookAndOpen is os.SameFile's own
// witness: through the seam, a regular file is replaced by ANOTHER regular
// file between being looked at and being opened. Every other check passes —
// the entry was a regular file at Lstat and what was opened is a regular
// file — and only the identity comparison refuses it. Deleting the SameFile
// term fails this test.
func TestSameFileRefusesAFileReplacedBetweenLookAndOpen(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("services/api/main.go", "package main\n")
	write("elsewhere/big.go", strings.Repeat("x\n", 40))
	swapped := false
	betweenLookAndOpen = func(path string) {
		if path == "services/api/main.go" && !swapped {
			swapped = true
			if err := os.Rename(filepath.Join(root, "elsewhere", "big.go"), filepath.Join(root, "services", "api", "main.go")); err != nil {
				t.Fatal(err)
			}
		}
	}
	defer func() { betweenLookAndOpen = nil }()
	_, err := MeasureDir(root, nil, func(string) Surface { return SurfaceBackend })
	if !swapped {
		t.Fatal("the seam was not reached")
	}
	if err == nil || !strings.Contains(err.Error(), "services/api/main.go") || !strings.Contains(err.Error(), "not the regular file that was looked at") {
		t.Fatalf("a file replaced between Lstat and Open must be refused by identity: %v", err)
	}
}

// TestEveryAuthorityRuleIsProtectedByAFixture is the kit's self-check for the
// authority rule table, and it is the stronger form of the presence one:
// deleting any rule must ADMIT its witness, not merely refuse it differently.
//
// It can be the stronger form because each of these rules is the only thing
// standing between its witness and acceptance — which is the property worth
// pinning. The checks these rules replace were inline in Validate, where
// nothing could say which one refused a document and a reader could drop any
// of them and still pass the kit.
func TestEveryAuthorityRuleIsProtectedByAFixture(t *testing.T) {
	names := authorityRuleNames()
	protected := map[string][]Fixture{}
	for _, fixture := range FixturesOf(DocumentTypeAuthority) {
		if fixture.Rule == "" {
			continue
		}
		if fixture.Outcome != OutcomeRejected {
			t.Errorf("fixture %s names rule %s but is accepted", fixture.Name, fixture.Rule)
		}
		if fixture.Message == "" {
			t.Errorf("fixture %s names rule %s without the message its refusal carries", fixture.Name, fixture.Rule)
		}
		// The schema rule is the decoder's and is shared with the presence
		// reader; the rest are this table's.
		if fixture.Rule != ruleSchema && !slices.Contains(names, fixture.Rule) {
			t.Errorf("fixture %s names rule %q, which no authority rule declares", fixture.Name, fixture.Rule)
		}
		protected[fixture.Rule] = append(protected[fixture.Rule], fixture)
	}
	for _, r := range authorityRules() {
		fixtures := protected[r.name]
		if len(fixtures) == 0 {
			t.Errorf("authority rule %s is protected by no fixture: a reader could drop it and pass the kit", r.name)
			continue
		}
		for _, fixture := range fixtures {
			if _, err := parseAuthority(fixture.Document, ""); err == nil ||
				!errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), fixture.Message) {
				t.Errorf("fixture %s must be refused naming %q (rule %s), got: %v", fixture.Name, fixture.Message, r.name, err)
			}
			if _, err := parseAuthority(fixture.Document, r.name); err != nil {
				t.Errorf("fixture %s is still refused with rule %s deleted, so something else refuses it and the rule is not what the fixture protects: %v",
					fixture.Name, r.name, err)
			}
		}
	}
}
