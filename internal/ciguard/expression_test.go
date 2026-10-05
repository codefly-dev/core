package ciguard

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Every guard in this package used to ask its question of the TEXT of a
// workflow expression: does this condition CONTAIN `github.event_name ==
// 'push'`, does this value MATCH `secrets\.NAME`. Both are answerable without
// understanding the expression, and both are therefore answerable wrongly:
//
//	if: github.event_name == 'push' && github.ref == 'refs/heads/main' || true
//	if: ${{ !(always() && github.event_name == 'push' && always()) }}
//	env: { PROBE: "${{ secrets['SLACK_WEBHOOK_URL'] }}" }
//	env: { PROBE: "${{ toJSON(secrets) }}" }
//
// The first two contain every required clause and run on a pull request. The
// second two reference a secret and match no `secrets.NAME` pattern. A guard
// that reads text cannot tell any of them from the safe form, so this file
// reads the expression instead: it tokenises and parses GitHub's expression
// grammar, then answers two questions of the syntax tree.
//
// **Can this condition be true when the input is hostile?** Answered by
// evaluating it in three-valued logic against a scenario that binds the
// adversarial facts. Anything the evaluator cannot know -- `needs.build.result`,
// `success()`, a `format()` call -- is UNKNOWN, and unknown is not false, so a
// job is only accepted when its condition is *definitely* false. Judging by
// meaning this way makes `|| true` true, makes a negated conjunction true, and
// needs no list of blessed clause spellings.
//
// **Does this expression read the secrets context?** Answered by walking for
// any access to `secrets`, in each of the forms GitHub resolves: a property,
// an index with a literal or a computed key, or the whole context handed to a
// function.
//
// An expression this file cannot parse fails the guard that asked. A parser
// that silently returns "nothing found" for what it does not understand is the
// same failure as the regex it replaces.

// ---------------------------------------------------------------- tokenising

type exprTokenKind int

const (
	exprEOF exprTokenKind = iota
	exprIdent
	exprString
	exprNumber
	exprOperator
	exprPunctuation
)

type exprToken struct {
	kind exprTokenKind
	text string
}

// lex turns an expression body into tokens. GitHub's grammar has no comments
// and no statements, so this is a flat scan.
func lex(input string) ([]exprToken, error) {
	var out []exprToken
	runes := []rune(input)

	for i := 0; i < len(runes); {
		r := runes[i]

		switch {
		case unicode.IsSpace(r):
			i++

		// A string literal is single-quoted, and a doubled quote is a literal
		// quote -- 'it''s'. Missing that would end the string early and leave
		// its tail to be lexed as code.
		case r == '\'':
			i++
			var sb strings.Builder
			closed := false
			for i < len(runes) {
				if runes[i] == '\'' {
					if i+1 < len(runes) && runes[i+1] == '\'' {
						sb.WriteRune('\'')
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				sb.WriteRune(runes[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated string literal")
			}
			out = append(out, exprToken{kind: exprString, text: sb.String()})

		case unicode.IsDigit(r) || (r == '-' && i+1 < len(runes) && unicode.IsDigit(runes[i+1]) && expectsOperand(out)):
			start := i
			if runes[i] == '-' {
				i++
			}
			for i < len(runes) && (unicode.IsDigit(runes[i]) || runes[i] == '.') {
				i++
			}
			out = append(out, exprToken{kind: exprNumber, text: string(runes[start:i])})

		// Context and function names. Hyphens are legal in a property name --
		// `inputs.setup-run` -- so they belong in an identifier, which is why
		// a leading `-` is only a number's sign where an operand is expected.
		case unicode.IsLetter(r) || r == '_':
			start := i
			for i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || runes[i] == '_' || runes[i] == '-') {
				i++
			}
			out = append(out, exprToken{kind: exprIdent, text: string(runes[start:i])})

		default:
			two := ""
			if i+1 < len(runes) {
				two = string(runes[i : i+2])
			}
			switch two {
			case "&&", "||", "==", "!=", "<=", ">=":
				out = append(out, exprToken{kind: exprOperator, text: two})
				i += 2
				continue
			}
			switch r {
			case '!', '<', '>':
				out = append(out, exprToken{kind: exprOperator, text: string(r)})
			case '(', ')', '[', ']', '.', ',', '*':
				out = append(out, exprToken{kind: exprPunctuation, text: string(r)})
			default:
				return nil, fmt.Errorf("unexpected character %q", r)
			}
			i++
		}
	}
	return append(out, exprToken{kind: exprEOF}), nil
}

// expectsOperand reports whether the next token begins an operand, which is
// what decides whether a `-` is a sign or an operator.
func expectsOperand(sofar []exprToken) bool {
	if len(sofar) == 0 {
		return true
	}
	last := sofar[len(sofar)-1]
	if last.kind == exprOperator {
		return true
	}
	return last.kind == exprPunctuation && (last.text == "(" || last.text == "," || last.text == "[")
}

// ------------------------------------------------------------------- parsing

type node interface{}

type literalNode struct{ value any } // string, bool, float64 or nil
type contextNode struct{ path []string }
type indexNode struct {
	target node
	key    node // nil for `.*`
}
type callNode struct {
	name string
	args []node
}
type unaryNode struct{ operand node }
type binaryNode struct {
	op   string
	l, r node
}

type exprParser struct {
	tokens []exprToken
	at     int
}

func parseExpression(body string) (node, error) {
	tokens, err := lex(body)
	if err != nil {
		return nil, err
	}
	p := &exprParser{tokens: tokens}
	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != exprEOF {
		return nil, fmt.Errorf("unexpected trailing %q", p.peek().text)
	}
	return n, nil
}

func (p *exprParser) peek() exprToken { return p.tokens[p.at] }

func (p *exprParser) take(kind exprTokenKind, text string) bool {
	if t := p.peek(); t.kind == kind && (text == "" || t.text == text) {
		p.at++
		return true
	}
	return false
}

func (p *exprParser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.take(exprOperator, "||") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = binaryNode{op: "||", l: left, r: right}
	}
	return left, nil
}

func (p *exprParser) parseAnd() (node, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.take(exprOperator, "&&") {
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = binaryNode{op: "&&", l: left, r: right}
	}
	return left, nil
}

func (p *exprParser) parseComparison() (node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for _, op := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if p.take(exprOperator, op) {
			right, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			return binaryNode{op: op, l: left, r: right}, nil
		}
	}
	return left, nil
}

func (p *exprParser) parseUnary() (node, error) {
	if p.take(exprOperator, "!") {
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return unaryNode{operand: operand}, nil
	}
	return p.parsePrimary()
}

func (p *exprParser) parsePrimary() (node, error) {
	t := p.peek()

	switch {
	case t.kind == exprString:
		p.at++
		return p.parsePostfix(literalNode{value: t.text})

	case t.kind == exprNumber:
		p.at++
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, fmt.Errorf("bad number %q", t.text)
		}
		return literalNode{value: f}, nil

	case t.kind == exprPunctuation && t.text == "(":
		p.at++
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.take(exprPunctuation, ")") {
			return nil, fmt.Errorf("missing )")
		}
		return p.parsePostfix(inner)

	case t.kind == exprIdent:
		p.at++
		switch strings.ToLower(t.text) {
		case "true":
			return literalNode{value: true}, nil
		case "false":
			return literalNode{value: false}, nil
		case "null":
			return literalNode{value: nil}, nil
		}
		// A function call, or a context the postfix loop walks into.
		if p.take(exprPunctuation, "(") {
			call := callNode{name: strings.ToLower(t.text)}
			if !p.take(exprPunctuation, ")") {
				for {
					arg, err := p.parseOr()
					if err != nil {
						return nil, err
					}
					call.args = append(call.args, arg)
					if p.take(exprPunctuation, ",") {
						continue
					}
					if p.take(exprPunctuation, ")") {
						break
					}
					return nil, fmt.Errorf("missing ) in call to %s", call.name)
				}
			}
			return p.parsePostfix(call)
		}
		return p.parsePostfix(contextNode{path: []string{t.text}})
	}
	return nil, fmt.Errorf("unexpected %q", t.text)
}

// parsePostfix consumes `.name`, `.*` and `[key]` after a primary. Both
// property forms land in the same shape, which is the point: `secrets.X` and
// `secrets['X']` are one access as far as the walkers below are concerned.
func (p *exprParser) parsePostfix(target node) (node, error) {
	for {
		switch {
		case p.take(exprPunctuation, "."):
			if p.take(exprPunctuation, "*") {
				target = indexNode{target: target}
				continue
			}
			name := p.peek()
			if name.kind != exprIdent {
				return nil, fmt.Errorf("expected a property name after . , got %q", name.text)
			}
			p.at++
			if ctx, ok := target.(contextNode); ok {
				target = contextNode{path: append(append([]string{}, ctx.path...), name.text)}
				continue
			}
			target = indexNode{target: target, key: literalNode{value: name.text}}

		case p.take(exprPunctuation, "["):
			key, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			if !p.take(exprPunctuation, "]") {
				return nil, fmt.Errorf("missing ]")
			}
			// A literal index is the same thing as a property: fold it into the
			// path so `secrets['X']` reads as `secrets.X`.
			if ctx, ok := target.(contextNode); ok {
				if lit, isLit := key.(literalNode); isLit {
					if s, isString := lit.value.(string); isString {
						target = contextNode{path: append(append([]string{}, ctx.path...), s)}
						continue
					}
				}
			}
			target = indexNode{target: target, key: key}

		default:
			return target, nil
		}
	}
}

// interpolation finds each `${{ ... }}` in a YAML string. A value may hold
// several, or none, or be a bare expression in an `if:`.
var interpolation = regexp.MustCompile(`(?s)\$\{\{(.*?)\}\}`)

// expressionsIn returns the expression bodies in a string: each interpolation,
// or the whole string when it carries none (how `if:` is usually written).
func expressionsIn(value string, bareIsAnExpression bool) []string {
	matches := interpolation.FindAllStringSubmatch(value, -1)
	if len(matches) > 0 {
		out := make([]string, 0, len(matches))
		for _, m := range matches {
			out = append(out, m[1])
		}
		return out
	}
	if bareIsAnExpression && strings.TrimSpace(value) != "" {
		return []string{value}
	}
	return nil
}

// ------------------------------------------------------- the secrets context

// wholeSecretsContext is reported when an expression consumes the secrets
// context without naming one secret -- `toJSON(secrets)`, or an index computed
// at run time. It is the widest possible reference and must never read as none.
const wholeSecretsContext = "(the whole secrets context)"

// secretReferences walks a syntax tree for any read of the secrets context.
func secretReferences(n node, into map[string]bool) {
	switch t := n.(type) {
	case contextNode:
		if len(t.path) > 0 && strings.EqualFold(t.path[0], "secrets") {
			if len(t.path) >= 2 {
				into[t.path[1]] = true
			} else {
				// Bare `secrets`, reached as a context rather than a property.
				into[wholeSecretsContext] = true
			}
		}
	case indexNode:
		// A computed index into secrets names no secret statically, so it has
		// to count as all of them.
		if ctx, ok := t.target.(contextNode); ok &&
			len(ctx.path) == 1 && strings.EqualFold(ctx.path[0], "secrets") {
			into[wholeSecretsContext] = true
		}
		secretReferences(t.target, into)
		if t.key != nil {
			secretReferences(t.key, into)
		}
	case callNode:
		for _, arg := range t.args {
			secretReferences(arg, into)
		}
	case unaryNode:
		secretReferences(t.operand, into)
	case binaryNode:
		secretReferences(t.l, into)
		secretReferences(t.r, into)
	}
}

// secretsReferencedIn returns every secret name a YAML string reads, and an
// error if any expression in it cannot be parsed -- because a reference inside
// an expression this cannot read is a reference it cannot report.
func secretsReferencedIn(value string) ([]string, error) {
	found := map[string]bool{}
	for _, body := range expressionsIn(value, false) {
		tree, err := parseExpression(body)
		if err != nil {
			return nil, fmt.Errorf("cannot parse ${{%s}}: %w", body, err)
		}
		secretReferences(tree, found)
	}
	out := make([]string, 0, len(found))
	for name := range found {
		out = append(out, name)
	}
	return out, nil
}

// contextReadsUnder returns the paths an expression reads beneath a context
// prefix -- `contextReadsUnder(ref, "github.event.workflow_run")` answers
// whether a checkout's ref comes from the triggering run's payload at all,
// rather than whether it mentions one of a handful of field names somebody
// thought to list.
func contextReadsUnder(value, prefix string) ([]string, error) {
	want := strings.Split(strings.ToLower(prefix), ".")
	var out []string

	var walk func(n node)
	walk = func(n node) {
		switch t := n.(type) {
		case contextNode:
			if len(t.path) < len(want) {
				return
			}
			for i, segment := range want {
				if !strings.EqualFold(t.path[i], segment) {
					return
				}
			}
			out = append(out, strings.Join(t.path, "."))
		case indexNode:
			walk(t.target)
			if t.key != nil {
				walk(t.key)
			}
		case callNode:
			for _, arg := range t.args {
				walk(arg)
			}
		case unaryNode:
			walk(t.operand)
		case binaryNode:
			walk(t.l)
			walk(t.r)
		}
	}

	for _, body := range expressionsIn(value, false) {
		tree, err := parseExpression(body)
		if err != nil {
			return nil, fmt.Errorf("cannot parse ${{%s}}: %w", body, err)
		}
		walk(tree)
	}
	return out, nil
}

// ------------------------------------------------- three-valued evaluation

type tri int

const (
	triFalse tri = iota
	triTrue
	triUnknown
)

func (t tri) String() string {
	switch t {
	case triFalse:
		return "false"
	case triTrue:
		return "true"
	}
	return "unknown"
}

func triOf(b bool) tri {
	if b {
		return triTrue
	}
	return triFalse
}

// value is what an expression evaluates to. Three kinds, and the middle one is
// what makes the evaluation both sound and precise:
//
//   - KNOWN: the concrete value.
//   - PREFIXED: the value is not known, but every value it could be begins with
//     a prefix the EVENT fixes. `github.ref` on a pull request is the case
//     this exists for: GitHub forces `refs/pull/<n>/merge`, and the number is
//     the author's. Binding one sample (`refs/pull/7/merge`) made
//     `github.ref == 'refs/pull/8/merge'` definitely-false and accepted a job
//     that is reachable on pull request #8. Leaving it wholly unknown would be
//     sound but would then demand an ancestry proof from jobs that plainly
//     cannot run on a tag. A prefix answers both: a comparison outside the
//     domain is FALSE, one inside it is UNKNOWN.
//   - UNKNOWN: nothing is known. Everything a triggering party chooses freely
//     lands here, and unknown is never accepted as false.
//
// Truthiness travels with the value, because `&&` and `||` can determine it
// without determining which operand was selected.
type valueKind int

const (
	valueUnknown valueKind = iota
	valueKnown
	valuePrefixed
)

type value struct {
	kind   valueKind
	v      any    // valueKnown: string, bool, float64 or nil
	prefix string // valuePrefixed
	t      tri
}

var unknownValue = value{kind: valueUnknown, t: triUnknown}

func knownValue(v any) value {
	return value{kind: valueKnown, v: v, t: truthOf(v)}
}

func knownBool(b bool) value { return knownValue(b) }

// prefixedValue is a string whose leading characters the event fixes. A
// non-empty prefix means the string is non-empty, hence truthy.
func prefixedValue(prefix string) value {
	return value{kind: valuePrefixed, prefix: prefix, t: triOf(prefix != "")}
}

// truthinessOnly carries a truthiness that is determined while the value is
// not -- `unknown && <falsy>` is falsy whichever operand is selected.
func truthinessOnly(t tri) value {
	return value{kind: valueUnknown, t: t}
}

// truthOf coerces a concrete value to boolean the way GitHub does: an empty
// string, a zero and null are false.
func truthOf(v any) tri {
	switch x := v.(type) {
	case bool:
		return triOf(x)
	case string:
		return triOf(x != "")
	case float64:
		return triOf(x != 0)
	case nil:
		return triFalse
	}
	return triUnknown
}

func truth(v value) tri { return v.t }

// toNumber is GitHub's numeric coercion, used when `==` compares across types.
// A string that is not a number becomes NaN, which equals nothing.
func toNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case nil:
		return 0, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case float64:
		return x, true
	case string:
		trimmed := strings.TrimSpace(x)
		if trimmed == "" {
			return 0, true
		}
		f, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, false // NaN
		}
		return f, true
	}
	return 0, false
}

// looseEqual implements GitHub's `==`: same-typed strings compare
// case-insensitively, and operands of different types are both converted to
// numbers. `” == 0` is TRUE there, which Go's `==` on `any` reports as false
// -- a difference a condition can be built out of.
func looseEqual(a, b any) tri {
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			return triOf(strings.EqualFold(as, bs))
		}
	}
	if ab, ok := a.(bool); ok {
		if bb, ok := b.(bool); ok {
			return triOf(ab == bb)
		}
	}
	if an, ok := a.(float64); ok {
		if bn, ok := b.(float64); ok {
			return triOf(an == bn)
		}
	}
	if a == nil && b == nil {
		return triTrue
	}
	an, aok := toNumber(a)
	bn, bok := toNumber(b)
	if !aok || !bok {
		return triFalse // NaN equals nothing
	}
	return triOf(an == bn)
}

// equality compares two values, including the prefixed kind: a literal outside
// the domain can never be equal, one inside it might be.
func equality(a, b value) tri {
	if a.kind == valueKnown && b.kind == valueKnown {
		return looseEqual(a.v, b.v)
	}
	known, prefixed := a, b
	if a.kind == valuePrefixed && b.kind == valueKnown {
		known, prefixed = b, a
	}
	if known.kind == valueKnown && prefixed.kind == valuePrefixed {
		if literal, ok := known.v.(string); ok {
			if !strings.HasPrefix(strings.ToLower(literal), strings.ToLower(prefixed.prefix)) {
				return triFalse
			}
		}
	}
	return triUnknown
}

// scenario binds the context values a situation fixes. Everything absent is
// unknown, which is what keeps the evaluation honest: an unbound
// `needs.build.result` makes a condition depending on it unknown rather than
// conveniently false.
//
// WHICH values may be bound is the whole soundness argument, and getting it
// wrong is silent. A scenario must answer for EVERY instance of its situation,
// not for one; binding a value the triggering party CHOOSES answers for the one
// instance that was guessed. `github.head_ref` is the example that bit: bound to
// some branch name, `github.head_ref == 'release'` evaluates to definitely-false
// and a credential-bearing job gated on it is accepted -- while being perfectly
// reachable from a pull request whose branch is named `release`.
//
// So there are three maps, and the differences are not cosmetic:
//
//   - `fixed` holds values the EVENT determines, identically for every
//     instance. Nobody chooses them, so one binding answers for all.
//   - `prefixed` holds values whose LEADING PART the event fixes while the rest
//     is chosen -- `github.ref` is `refs/pull/<n>/merge` for every pull
//     request. A comparison outside the domain is false; one inside it is
//     unknown.
//   - `adversarial` holds values the triggering party does choose, pinned to
//     the value that most favours them. That is sound for what these scenarios
//     are for -- obliging one specific condition -- and every entry carries its
//     reason.
//
// Anything else stays unknown. TestNoScenarioBindsAValueTheTriggeringPartyChooses
// makes an unsound binding impossible to add by adding a line.
type scenario struct {
	name        string
	fixed       map[string]string
	prefixed    map[string]string
	adversarial map[string]string
	always      map[string]tri // function results this situation fixes
}

// lookup resolves a context path.
func (s scenario) lookup(path string) (value, bool) {
	if v, ok := s.fixed[path]; ok {
		return knownValue(v), true
	}
	if v, ok := s.adversarial[path]; ok {
		return knownValue(v), true
	}
	if p, ok := s.prefixed[path]; ok {
		return prefixedValue(p), true
	}
	return unknownValue, false
}

func (s scenario) eval(n node) value {
	switch t := n.(type) {
	case literalNode:
		return knownValue(t.value)

	case contextNode:
		if v, ok := s.lookup(strings.ToLower(strings.Join(t.path, "."))); ok {
			return v
		}
		return unknownValue

	case indexNode:
		return unknownValue

	case callNode:
		return s.evalCall(t)

	case unaryNode:
		// `!` returns a boolean, unlike `&&` and `||`.
		switch truth(s.eval(t.operand)) {
		case triTrue:
			return knownBool(false)
		case triFalse:
			return knownBool(true)
		}
		return unknownValue

	case binaryNode:
		return s.evalBinary(t)
	}
	return unknownValue
}

func (s scenario) evalCall(t callNode) value {
	if fixed, ok := s.always[t.name]; ok {
		switch fixed {
		case triTrue:
			return knownBool(true)
		case triFalse:
			return knownBool(false)
		}
		return unknownValue
	}

	if len(t.args) == 2 {
		left, right := s.eval(t.args[0]), s.eval(t.args[1])
		literal, literalOK := right.v.(string)
		if right.kind != valueKnown {
			literalOK = false
		}

		if subject, ok := left.v.(string); ok && left.kind == valueKnown && literalOK {
			subject, literal := strings.ToLower(subject), strings.ToLower(literal)
			switch t.name {
			case "startswith":
				return knownBool(strings.HasPrefix(subject, literal))
			case "endswith":
				return knownBool(strings.HasSuffix(subject, literal))
			case "contains":
				return knownBool(strings.Contains(subject, literal))
			}
		}

		// A prefixed subject decides some of these outright.
		if left.kind == valuePrefixed && literalOK {
			domain, literal := strings.ToLower(left.prefix), strings.ToLower(literal)
			switch t.name {
			case "startswith":
				if strings.HasPrefix(domain, literal) {
					return knownBool(true) // every value in the domain does
				}
				if strings.HasPrefix(literal, domain) {
					return unknownValue // some might
				}
				return knownBool(false) // none can
			case "contains":
				if strings.Contains(domain, literal) {
					return knownBool(true)
				}
			}
		}
	}
	return unknownValue
}

func (s scenario) evalBinary(t binaryNode) value {
	switch t.op {
	case "&&", "||":
		// GitHub's `&&` and `||` return the SELECTED OPERAND, not a boolean.
		// `(a && 'run' || '') == 'run'` is a condition built out of that: read
		// as a boolean it compares `true` with `'run'` and is false; read
		// correctly it selects `'run'` and is true.
		left := s.eval(t.l)
		lt := truth(left)

		selectsRight := lt == triTrue
		if t.op == "||" {
			selectsRight = lt == triFalse
		}
		if lt != triUnknown {
			if selectsRight {
				return s.eval(t.r)
			}
			return left
		}

		// Which operand is selected is unknown, but the TRUTHINESS can still be
		// determined: `unknown && <falsy>` is falsy either way, and
		// `unknown || <truthy>` is truthy either way.
		rt := truth(s.eval(t.r))
		if t.op == "&&" && rt == triFalse {
			return truthinessOnly(triFalse)
		}
		if t.op == "||" && rt == triTrue {
			return truthinessOnly(triTrue)
		}
		return unknownValue
	}

	left, right := s.eval(t.l), s.eval(t.r)
	switch t.op {
	case "==":
		result := equality(left, right)
		if result == triUnknown {
			return unknownValue
		}
		return knownBool(result == triTrue)
	case "!=":
		result := equality(left, right)
		if result == triUnknown {
			return unknownValue
		}
		return knownBool(result == triFalse)
	}
	// Ordering comparisons are not needed by any condition here, and guessing
	// would be worse than declining to answer.
	return unknownValue
}

// canRunUnder answers the only question the guards ask of a condition: could
// this job run in this situation? A missing condition means "always".
//
// The answer is triUnknown whenever the condition turns on something the
// scenario does not fix, and a caller must treat unknown as "yes, it could" --
// a guard that accepted unknown as safe would accept every condition mentioning
// `needs` or `success()`.
func canRunUnder(gate string, s scenario) (tri, error) {
	trimmed := strings.TrimSpace(gate)
	if trimmed == "" {
		return triTrue, nil
	}
	// A condition is one expression -- either bare, or one `${{ }}` spanning the
	// whole value. Anything else is string CONCATENATION, and GitHub treats a
	// non-empty string as true: `${{ false }}x` runs. Reading only the first
	// interpolation of such a value would report false and accept the job.
	bodies := expressionsIn(trimmed, true)
	whole := strings.HasPrefix(trimmed, "${{") && strings.HasSuffix(trimmed, "}}")
	if len(bodies) > 1 || (len(bodies) == 1 && strings.Contains(trimmed, "${{") && !whole) {
		return triTrue, nil
	}
	for _, body := range bodies {
		tree, err := parseExpression(body)
		if err != nil {
			return triUnknown, fmt.Errorf("cannot parse condition %q: %w", trimmed, err)
		}
		return truth(s.eval(tree)), nil
	}
	return triTrue, nil
}
