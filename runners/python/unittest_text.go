package python

import (
	"regexp"
	"sort"
	"strings"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// unittest-text is a FORMAT parser, not a framework. Python's unittest
// TextTestRunner (which django's runtests.py, plain `python -m unittest`, and
// nose all drive) prints a stable verbose format:
//
//	test_method (pkg.mod.Class) ... ok
//	test_other  (pkg.mod.Class) ... FAIL
//	======================================================================
//	FAIL: test_other (pkg.mod.Class)
//	----------------------------------------------------------------------
//	Traceback (most recent call last): ...
//
// ParseUnittestText turns that into the SAME StructuredTestRun shape
// ParsePytestJUnit produces, so the runtime plugin emits one structured
// TestResponse regardless of which runner produced the output. The plugin
// (allowed framework knowledge) selects this parser by the test formula's
// output format; Mind only reads the structured result.
var (
	// verbosity-2 single-line result: "test_x (a.b.Class) ... ok".
	reUnittestResult = regexp.MustCompile(`^(test[\w.]*) \(([\w.]+)\)[^\n]*?\.\.\. (ok|OK|FAIL|ERROR|skipped|expected failure)`)
	// docstring-bearing tests print on TWO lines: a bare id line, then the
	// docstring text followed by the result.
	reUnittestBareID    = regexp.MustCompile(`^(test[\w.]*) \(([\w.]+)\)\s*$`)
	reUnittestDocResult = regexp.MustCompile(`^(.*?) \.\.\. (ok|OK|FAIL|ERROR|skipped|expected failure)`)
	// failure/error block header: "FAIL: test_x (a.b.Class)".
	reUnittestBlockHeader = regexp.MustCompile(`^(FAIL|ERROR): ([\w.]+) \(([\w.]+)\)`)
	// aggregate summary lines every unittest runner prints.
	reUnittestRan      = regexp.MustCompile(`(?m)^Ran (\d+) tests? in `)
	reUnittestSkipped  = regexp.MustCompile(`skipped=(\d+)`)
	reUnittestFailures = regexp.MustCompile(`failures=(\d+)`)
	reUnittestErrors   = regexp.MustCompile(`errors=(\d+)`)
)

// unittestState maps a raw unittest result token to a structured case state.
func unittestState(raw string) (runtimev0.TestCaseState, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ok", "expected failure":
		return runtimev0.TestCaseState_TEST_CASE_STATE_PASSED, true
	case "fail":
		return runtimev0.TestCaseState_TEST_CASE_STATE_FAILED, true
	case "error":
		return runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED, true
	case "skipped":
		return runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED, true
	default:
		return runtimev0.TestCaseState_TEST_CASE_STATE_PASSED, false
	}
}

// utCase is the parser's working record for one test.
type utCase struct {
	method string
	class  string // dotted, e.g. "pkg.mod.Class"
	doc    string // docstring key, when present (unittest prints it inline)
	state  runtimev0.TestCaseState
	detail string
	order  int
}

// ParseUnittestText parses unittest TextTestRunner verbose output into a
// StructuredTestRun. Result lines set each test's state; FAIL:/ERROR: block
// headers override it (a test can print "... ok" then error during teardown)
// and attach the traceback as the case's captured output. Cases are grouped
// into one suite per test class. Empty/garbage input yields zero suites — the
// caller decides whether that's an environment block.
func ParseUnittestText(output string) *StructuredTestRun {
	run := &StructuredTestRun{}
	if strings.TrimSpace(output) == "" {
		return run
	}
	lines := strings.Split(output, "\n")

	cases := map[string]*utCase{} // key: "class.method"
	order := 0
	get := func(method, class string) *utCase {
		key := class + "." + method
		c, ok := cases[key]
		if !ok {
			c = &utCase{method: method, class: class, order: order, state: runtimev0.TestCaseState_TEST_CASE_STATE_PASSED}
			cases[key] = c
			order++
		}
		return c
	}

	// Pass 1 — per-test result lines (single-line + two-line docstring form).
	for i, line := range lines {
		if m := reUnittestResult.FindStringSubmatch(line); m != nil {
			if st, ok := unittestState(m[3]); ok {
				get(m[1], m[2]).state = st
			}
			continue
		}
		if m := reUnittestBareID.FindStringSubmatch(line); m != nil && i+1 < len(lines) {
			if dm := reUnittestDocResult.FindStringSubmatch(lines[i+1]); dm != nil {
				if st, ok := unittestState(dm[2]); ok {
					c := get(m[1], m[2])
					c.state = st
					if doc := strings.TrimSpace(dm[1]); doc != "" {
						c.doc = doc
					}
				}
			}
		}
	}

	// Pass 2 — FAIL:/ERROR: blocks override state + capture the traceback.
	for i, line := range lines {
		m := reUnittestBlockHeader.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		kind, method, class := m[1], m[2], m[3]
		st := runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED
		if kind == "FAIL" {
			st = runtimev0.TestCaseState_TEST_CASE_STATE_FAILED
		}
		c := get(method, class)
		c.state = st
		c.detail = captureUnittestBlock(lines, i+1)
	}

	run.Suites = buildUnittestSuites(cases)

	// Aggregate summary — every unittest TextTestRunner (django's runtests.py,
	// `python -m unittest`) ends with "Ran N tests in Xs" then a status line.
	// At DEFAULT verbosity there are NO per-test result lines (just dots), so
	// the passes above find zero cases and a healthy run of all-skipped or
	// all-passing tests would be MISCLASSIFIED as "no tests executed" / env
	// blocked. Parse the summary so the count is right regardless of verbosity;
	// named FAIL:/ERROR: cases (parsed above) still carry the failing test ids
	// the grader needs. Framework-agnostic: it's the unittest FORMAT, not django.
	if m := reUnittestRan.FindStringSubmatch(output); m != nil {
		total := atoiSafe(m[1])
		if total > 0 {
			failed, errored := 0, 0
			for _, c := range cases {
				switch c.state {
				case runtimev0.TestCaseState_TEST_CASE_STATE_FAILED:
					failed++
				case runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED:
					errored++
				}
			}
			skipped := firstIntIn(reUnittestSkipped, output)
			// Prefer explicit failures=/errors= from the status line when present.
			if f := firstIntIn(reUnittestFailures, output); f > failed {
				failed = f
			}
			if e := firstIntIn(reUnittestErrors, output); e > errored {
				errored = e
			}
			passed := total - failed - errored - skipped
			if passed < 0 {
				passed = 0
			}
			run.Summary = &UnittestSummary{Total: total, Passed: passed, Failed: failed, Errored: errored, Skipped: skipped}
		}
	}
	return run
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func firstIntIn(re *regexp.Regexp, s string) int {
	if m := re.FindStringSubmatch(s); m != nil {
		return atoiSafe(m[1])
	}
	return 0
}

// captureUnittestBlock collects a failure block body starting at `from` until
// the next "====" separator (unittest's block delimiter) or EOF.
func captureUnittestBlock(lines []string, from int) string {
	var b strings.Builder
	for j := from; j < len(lines); j++ {
		l := lines[j]
		if strings.HasPrefix(l, "======") {
			break
		}
		// skip the leading "------" rule unittest prints under the header
		if strings.HasPrefix(l, "------") && b.Len() == 0 {
			continue
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// buildUnittestSuites groups cases into one StructuredSuite per class, with
// deterministic ordering (by class name, then by first-seen order within).
func buildUnittestSuites(cases map[string]*utCase) []*StructuredSuite {
	byClass := map[string][]*utCase{}
	for _, c := range cases {
		byClass[c.class] = append(byClass[c.class], c)
	}
	classes := make([]string, 0, len(byClass))
	for cls := range byClass {
		classes = append(classes, cls)
	}
	sort.Strings(classes)

	suites := make([]*StructuredSuite, 0, len(classes))
	for _, cls := range classes {
		cs := byClass[cls]
		sort.Slice(cs, func(i, j int) bool { return cs[i].order < cs[j].order })
		suite := &StructuredSuite{Name: cls}
		for _, c := range cs {
			full := c.class + "." + c.method
			sc := &StructuredCase{
				Name:     c.method,
				FullName: full,
				State:    c.state,
				Output:   c.detail,
			}
			if c.state == runtimev0.TestCaseState_TEST_CASE_STATE_FAILED ||
				c.state == runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED {
				sc.Failure = &StructuredFailure{
					Message: strings.SplitN(c.detail, "\n", 2)[0],
					Detail:  c.detail,
					Kind:    runtimev0.TestFailureKind_TEST_FAILURE_KIND_ASSERTION,
				}
			}
			suite.Cases = append(suite.Cases, sc)
		}
		suites = append(suites, suite)
	}
	return suites
}
