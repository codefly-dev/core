package python

// ARCHITECTURE: Runtime diagnostics cross the Codefly boundary and become
// durable model context, cassette identity, and operator evidence. Python and
// pytest routinely embed sandbox roots, pytest temp ordinals, and object
// addresses in otherwise deterministic failures. Those values describe the
// runtime container, not the project defect, so the Python runner removes them
// before returning its typed TestResponse. Mind never learns path heuristics.

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	pythonObjectAddressPattern = regexp.MustCompile(`\b0x[0-9a-fA-F]{6,}\b`)
	pytestTempPathPattern      = regexp.MustCompile(`(?i)(?:[a-z]:)?(?:[/\\][^/\\\s:"'<>]+)*[/\\]pytest-of-[^/\\\s:"'<>]+[/\\](?:pytest-[0-9]+|pytest-current)`)
)

// normalizeDiagnostics removes runtime-owned identities from every diagnostic
// surface that can reach TestResponse. Project-relative filenames, assertion
// values, traceback structure, and failure text remain intact.
func (r *StructuredTestRun) normalizeDiagnostics(runtimeRoots ...string) {
	if r == nil {
		return
	}
	normalize := newPythonDiagnosticNormalizer(runtimeRoots...)
	r.RawOutput = normalize(r.RawOutput)
	if r.EnvError != nil {
		r.EnvError.Detail = normalize(r.EnvError.Detail)
	}
	for _, suite := range r.Suites {
		if suite == nil {
			continue
		}
		suite.Name = normalize(suite.Name)
		suite.File = normalize(suite.File)
		for _, testCase := range suite.Cases {
			if testCase == nil {
				continue
			}
			testCase.Name = normalize(testCase.Name)
			testCase.FullName = normalize(testCase.FullName)
			testCase.File = normalize(testCase.File)
			testCase.Output = normalize(testCase.Output)
			if testCase.Failure != nil {
				testCase.Failure.Message = normalize(testCase.Failure.Message)
				testCase.Failure.Detail = normalize(testCase.Failure.Detail)
			}
		}
	}
}

func newPythonDiagnosticNormalizer(runtimeRoots ...string) func(string) string {
	roots := pythonDiagnosticRootSpellings(runtimeRoots...)
	return func(value string) string {
		for _, root := range roots {
			value = strings.ReplaceAll(value, root, "<workspace>")
		}
		value = pytestTempPathPattern.ReplaceAllString(value, "<pytest-tmp>")
		return pythonObjectAddressPattern.ReplaceAllString(value, "0xADDR")
	}
}

func pythonDiagnosticRootSpellings(runtimeRoots ...string) []string {
	unique := make(map[string]struct{})
	add := func(root string) {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" || root == "." || root == string(filepath.Separator) {
			return
		}
		for _, spelling := range []string{root, filepath.ToSlash(root)} {
			unique[spelling] = struct{}{}
			if strings.HasPrefix(spelling, "/var/") {
				unique["/private"+spelling] = struct{}{}
			}
			if strings.HasPrefix(spelling, "/private/var/") {
				unique[strings.TrimPrefix(spelling, "/private")] = struct{}{}
			}
		}
	}
	for _, root := range runtimeRoots {
		add(root)
		if absolute, err := filepath.Abs(root); err == nil {
			add(absolute)
		}
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			add(resolved)
		}
	}
	roots := make([]string, 0, len(unique))
	for root := range unique {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool {
		if len(roots[i]) != len(roots[j]) {
			return len(roots[i]) > len(roots[j])
		}
		return roots[i] < roots[j]
	})
	return roots
}
