// Package usage is the ONE implementation of the usage contract —
// codefly/usage/v1, the file an owner publishes as usage.codefly.yaml beside
// its agent or module manifest: its Go model, its strict decoder and every
// refusal rule. Consumers import it as usagecontract.
//
// A usage contract is what an owner tells the code that CONSUMES it. A
// repository's AGENTS.md is written for whoever changes that repository; this
// file is written for whoever calls it — the service that depends on a
// database agent, the solution that consumes a module — and it travels with
// the owner's release, so a consumer reads the rules of the version it pinned.
//
// Each rule is written once and read at up to two strengths. Its text
// (rationale, do, dont) is what a coding agent or a person is told. Its
// detect clauses, when it has any, are what a linter checks: a declarative
// list of symbols a consumer must not call or import, per language. A rule
// with no detect clause is one that takes judgement, and is legitimate.
//
// Three things a contract never carries. A script, a command or a pattern
// over arbitrary text: detection is a closed vocabulary, so reading a
// contract runs nothing. A reference to fetch: docs is a path inside the
// owner's own release. And the consumer's verdict: a contract states rules;
// which of them apply to one service, and what a violation costs it, is the
// reader's to decide from the dependencies that service declares.
//
// The text of a contract reaches a model's context, so it is bounded: a
// document, each list and each field has a size no rule may exceed. The
// bounds are part of the schema, not a reader's preference.
//
// # Rules
//
// Every refusal is one named rule (rules.go), applied in a fixed order, so a
// document is refused for one reason whichever reader refused it. Each rule
// is protected by a fixture the kit ships (Fixtures), and the package's
// self-check deletes each rule in turn and proves a fixture notices.
package usage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/wire"
)

const (
	// FileName is the contract's path relative to the directory of the
	// manifest it is published beside.
	FileName = "usage.codefly.yaml"

	// SchemaV1 is the only usage schema this package reads.
	SchemaV1 = "codefly/usage/v1"
)

// The two severities a rule may declare. An owner introduces a rule as a
// warning and may raise it in a later release, so pinning a newer owner never
// fails a consumer that had no release in which to hear about the rule.
const (
	SeverityError = "error"
	SeverityWarn  = "warn"
)

var severities = []string{SeverityError, SeverityWarn}

// The bounds of a contract. They are the schema's: a document that exceeds
// one is refused by every reader.
const (
	// MaxDocumentBytes bounds the file.
	MaxDocumentBytes = 256 << 10
	// MaxRules bounds how many rules one owner states.
	MaxRules = 64
	// MaxSummaryRunes bounds the one-paragraph summary.
	MaxSummaryRunes = 512
	// MaxTitleRunes bounds a rule's one-line title.
	MaxTitleRunes = 120
	// MaxTextRunes bounds each of a rule's rationale, do and dont.
	MaxTextRunes = 2048
	// MaxDetectClauses bounds a rule's detect clauses: one per language.
	MaxDetectClauses = 16
	// MaxSymbols bounds each symbol list of a detect clause.
	MaxSymbols = 64
	// MaxExceptPaths bounds a rule's excepted paths.
	MaxExceptPaths = 32
)

var (
	// ErrSchema means the file does not declare a schema this package reads: a
	// version skew, which a reader reports differently from a malformed file.
	ErrSchema = errors.New("usage contract schema is not supported")
	// ErrInvalid means the contract violates its own rules.
	ErrInvalid = errors.New("usage contract is invalid")
)

// Contract is one owner's usage contract.
type Contract struct {
	// Schema is SchemaV1.
	Schema string `yaml:"schema" json:"schema"`
	// Summary is one paragraph saying what consuming this owner correctly
	// comes down to. It is what a reader shows when it shows one thing.
	Summary string `yaml:"summary" json:"summary"`
	// Rules are the rules, in the order the owner wrote them.
	Rules []Rule `yaml:"rules" json:"rules"`
}

// Rule is one thing a consumer must or must not do.
type Rule struct {
	// ID identifies the rule for the life of the owner: it is what an
	// exception names and what a finding reports, so it is never renumbered
	// and never reused for another rule.
	ID string `yaml:"id" json:"id"`
	// Title is the rule in one line.
	Title string `yaml:"title" json:"title"`
	// Severity is SeverityError or SeverityWarn.
	Severity string `yaml:"severity" json:"severity"`
	// Since is the owner's release that introduced the rule, as a semantic
	// version without a leading v.
	Since string `yaml:"since" json:"since"`
	// Rationale says why the rule exists: what goes wrong without it.
	Rationale string `yaml:"rationale" json:"rationale"`
	// Do says what to do instead. Every rule carries one: a rule that only
	// forbids leaves its reader to guess the alternative.
	Do string `yaml:"do" json:"do"`
	// Dont says what the rule forbids, when stating it helps.
	Dont string `yaml:"dont,omitempty" json:"dont,omitempty"`
	// Docs is a path, relative to the owner's release, of a document that
	// says more.
	Docs string `yaml:"docs,omitempty" json:"docs,omitempty"`
	// Detect is how a linter recognises a violation, one clause per
	// language. Empty for a rule that takes judgement.
	Detect []Detect `yaml:"detect,omitempty" json:"detect,omitempty"`
	// ExceptPaths are globs, relative to the consuming service, where the
	// detect clauses do not apply. Meaningful only beside Detect.
	ExceptPaths []string `yaml:"except-paths,omitempty" json:"except_paths,omitempty"`
}

// Detect is one language's detection of a rule: the symbols a consumer
// written in that language must not reach.
type Detect struct {
	// Language names the source language the clause applies to, as the
	// agents spell it (go, python, typescript). It is a name and not an
	// enumeration: a language no reader of this package has heard of is a
	// clause that reader has no linter for, never a malformed contract.
	Language string `yaml:"language" json:"language"`
	// ForbidCall lists fully qualified functions or methods that must not be
	// called.
	ForbidCall []string `yaml:"forbid-call,omitempty" json:"forbid_call,omitempty"`
	// ForbidImport lists packages or modules that must not be imported.
	ForbidImport []string `yaml:"forbid-import,omitempty" json:"forbid_import,omitempty"`
	// Names says what a package is called in a call when its path does not
	// end in its name, keyed by the owner of a forbidden call: a Go package
	// imported as example.com/store/libs/go and written postgres.Open. The
	// owner of a package is the one party that knows this; a reader has only
	// the consumer's source, which does not say.
	Names map[string]string `yaml:"names,omitempty" json:"names,omitempty"`
}

// Detects returns the rule's clause for a language, or nil when the rule has
// none: the rule's text still applies to that language, only no linter checks
// it.
func (rule Rule) Detects(language string) *Detect {
	for i := range rule.Detect {
		if rule.Detect[i].Language == language {
			return &rule.Detect[i]
		}
	}
	return nil
}

// Rule returns the rule with the given ID, or nil.
func (contract *Contract) Rule(id string) *Rule {
	if contract == nil {
		return nil
	}
	for i := range contract.Rules {
		if contract.Rules[i].ID == id {
			return &contract.Rules[i]
		}
	}
	return nil
}

// Load reads and validates the usage contract published in dir. An owner with
// no contract returns os.ErrNotExist, which a caller distinguishes from a
// contract that cannot be read: the first is an owner with nothing to say,
// the second is an owner whose rules are unreadable.
func Load(dir string) (*Contract, error) {
	// The file is read through the directory rather than by a path, so a
	// FileName that resolved outside it would be refused.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(FileName)
	if err != nil {
		return nil, err
	}
	contract, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.ToSlash(filepath.Join(dir, FileName)), err)
	}
	return contract, nil
}

// Parse decodes and validates one contract. Decoding is strict: an unknown
// field is an error rather than a silently ignored intention, which is what
// refuses a contract written for a later schema, or one trying to carry a
// command. The schema is checked first and leniently, so a contract of
// another version is reported as a version skew rather than as malformed.
func Parse(data []byte) (*Contract, error) { return parse(data, "") }

// parse is Parse with one rule deleted — the self-check's entrypoint, which
// proves every rule is protected by a fixture. "" deletes none.
func parse(data []byte, without string) (*Contract, error) {
	if len(data) > MaxDocumentBytes && without != ruleDocumentBounded {
		return nil, fmt.Errorf("%w: the usage contract is %d bytes; a contract is at most %d", ErrInvalid, len(data), MaxDocumentBytes)
	}
	var tree yaml.Node
	if err := yaml.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("%w: the usage contract is not a YAML document: %v", ErrInvalid, err)
	}
	// The schema is read off the tree rather than through a typed decode, so
	// a file of another version is a version skew whatever else it carries,
	// and a key repeated at the top is refused by the rule that owns it
	// rather than by the decoder, as a file that is not YAML.
	if declared := declaredSchema(&tree); declared != SchemaV1 && without != ruleSchema {
		return nil, fmt.Errorf("%w: %q (this reader reads %q)", ErrSchema, declared, SchemaV1)
	}
	switch defect := wire.Check(&tree); {
	case defect.Kind == wire.DuplicateKey && without != ruleMappingKeysOnce:
		return nil, fmt.Errorf("%w: the mapping at %s names the key %q twice; which value a reader keeps would depend on the reader", ErrInvalid, defect.Path, defect.Detail)
	case defect.Kind == wire.MergeKey && without != ruleNoMergeKeys:
		return nil, fmt.Errorf("%w: the mapping at %s uses the merge key <<; a merged field is one no rule over the document saw", ErrInvalid, defect.Path)
	case defect.Kind == wire.NullNode && without != ruleNoNulls:
		return nil, fmt.Errorf("%w: the document carries an explicit null at %s; a null decodes as an absent field, so write the field or leave it out", ErrInvalid, defect.Path)
	case defect.Kind == wire.FractionalNumber && without != ruleWholeNumbers:
		return nil, fmt.Errorf("%w: the number %s at %s is not text; quote a version such as since: \"1.2\"", ErrInvalid, defect.Detail, defect.Path)
	case defect.Kind == wire.KeyNotAName && without != ruleKeyIsAName:
		return nil, fmt.Errorf("%w: the mapping at %s carries a key that is not a name (%q)", ErrInvalid, defect.Path, defect.Detail)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(without != ruleKnownFields)
	var contract Contract
	if err := decoder.Decode(&contract); err != nil {
		return nil, fmt.Errorf("%w: the usage contract carries a field no contract declares, or a value of the wrong shape: %v", ErrInvalid, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) && without != ruleOneDocument {
		return nil, fmt.Errorf("%w: the file holds more than one document", ErrInvalid)
	}
	if err := contract.validate(without); err != nil {
		return nil, err
	}
	return &contract, nil
}

// declaredSchema is the text of the document's top-level schema field, or ""
// when it has none.
func declaredSchema(tree *yaml.Node) string {
	if len(tree.Content) == 0 {
		return ""
	}
	root := wire.Resolve(tree.Content[0])
	if root == nil || root.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if key := wire.Resolve(root.Content[i]); key != nil && key.Kind == yaml.ScalarNode && key.Value == "schema" {
			if value := wire.Resolve(root.Content[i+1]); value != nil && value.Kind == yaml.ScalarNode {
				return value.Value
			}
			return ""
		}
	}
	return ""
}

// Validate checks a contract built in memory against every rule that does
// not belong to the decoder, in order, the first refusal named.
func (contract *Contract) Validate() error { return contract.validate("") }

// Encode writes the contract as the YAML document Parse reads.
func (contract *Contract) Encode() ([]byte, error) {
	if err := contract.Validate(); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(contract); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
