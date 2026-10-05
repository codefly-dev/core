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

// value is a result the evaluator either knows or does not.
type value struct {
	known bool
	v     any // string, bool or float64
}

var unknownValue = value{}

func knownBool(b bool) value { return value{known: true, v: b} }

// truth coerces a value to three-valued truth the way GitHub coerces it to
// boolean: an empty string, a zero and null are false.
func truth(v value) tri {
	if !v.known {
		return triUnknown
	}
	switch x := v.v.(type) {
	case bool:
		if x {
			return triTrue
		}
		return triFalse
	case string:
		if x != "" {
			return triTrue
		}
		return triFalse
	case float64:
		if x != 0 {
			return triTrue
		}
		return triFalse
	case nil:
		return triFalse
	}
	return triUnknown
}

// scenario binds the context values a situation fixes. Everything absent is
// unknown, which is what keeps the evaluation honest: an unbound
// `needs.build.result` makes a condition depending on it unknown rather than
// conveniently false.
type scenario struct {
	name   string
	bound  map[string]string
	always map[string]tri // function results this situation fixes
}

func (s scenario) eval(n node) value {
	switch t := n.(type) {
	case literalNode:
		return value{known: true, v: t.value}

	case contextNode:
		if bound, ok := s.bound[strings.ToLower(strings.Join(t.path, "."))]; ok {
			return value{known: true, v: bound}
		}
		return unknownValue

	case indexNode:
		return unknownValue

	case callNode:
		return s.evalCall(t)

	case unaryNode:
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

	// String predicates are computable when both arguments are.
	if len(t.args) == 2 {
		left, right := s.eval(t.args[0]), s.eval(t.args[1])
		ls, lok := left.v.(string)
		rs, rok := right.v.(string)
		if left.known && right.known && lok && rok {
			ls, rs = strings.ToLower(ls), strings.ToLower(rs)
			switch t.name {
			case "startswith":
				return knownBool(strings.HasPrefix(ls, rs))
			case "endswith":
				return knownBool(strings.HasSuffix(ls, rs))
			case "contains":
				return knownBool(strings.Contains(ls, rs))
			}
		}
	}
	return unknownValue
}

func (s scenario) evalBinary(t binaryNode) value {
	switch t.op {
	case "&&":
		// False if either side is false, whatever the other is -- which is why
		// an unknown beside a false still yields false, and a negation around
		// the pair yields TRUE rather than being read as a conjunction.
		lt := truth(s.eval(t.l))
		if lt == triFalse {
			return knownBool(false)
		}
		rt := truth(s.eval(t.r))
		if rt == triFalse {
			return knownBool(false)
		}
		if lt == triTrue && rt == triTrue {
			return knownBool(true)
		}
		return unknownValue

	case "||":
		lt := truth(s.eval(t.l))
		if lt == triTrue {
			return knownBool(true)
		}
		rt := truth(s.eval(t.r))
		if rt == triTrue {
			return knownBool(true)
		}
		if lt == triFalse && rt == triFalse {
			return knownBool(false)
		}
		return unknownValue
	}

	left, right := s.eval(t.l), s.eval(t.r)
	if !left.known || !right.known {
		return unknownValue
	}
	equal := looseEqual(left.v, right.v)
	switch t.op {
	case "==":
		return knownBool(equal)
	case "!=":
		return knownBool(!equal)
	}
	// Ordering comparisons are not needed by any condition here, and guessing
	// would be worse than declining to answer.
	return unknownValue
}

// looseEqual compares the way GitHub does: strings case-insensitively.
func looseEqual(a, b any) bool {
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return strings.EqualFold(as, bs)
	}
	return a == b
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
	for _, body := range expressionsIn(trimmed, true) {
		tree, err := parseExpression(body)
		if err != nil {
			return triUnknown, fmt.Errorf("cannot parse condition %q: %w", trimmed, err)
		}
		// A condition is one expression; several interpolations in one `if:`
		// would be a string, which GitHub treats as truthy, so the first is the
		// one that decides.
		return truth(s.eval(tree)), nil
	}
	return triTrue, nil
}
