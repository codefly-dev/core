package module

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/codefly-dev/core/internal/conditions"
)

// checkFunc is the name of the function a rule row runs, as the binary
// records it. An anonymous row — one whose condition lives in the parser
// rather than in rules.go — reports a generated name, which is how the two
// kinds are told apart without a second list to maintain.
func checkFunc(check func(*Contract) error) string {
	full := runtime.FuncForPC(reflect.ValueOf(check).Pointer()).Name()
	name := full[strings.LastIndex(full, ".")+1:]
	return name
}

// TestEveryRuleHoldsExactlyOneCondition is the construction that makes
// "every condition has a witness" true going forward, rather than true today.
//
// TestEveryRuleIsProtectedByAFixture already deletes each rule and requires a
// fixture to notice. That proves a RULE is protected; it says nothing about a
// condition sharing a rule with another. Nine rounds of review found that
// difference five times, and twice at the end in one function: an overlap
// predicate gated on `network.IP.To4() != nil` and a canonical-spelling check
// run only `while len(networks) == 0` each accepted an invalid document while
// every fixture went on refusing — because a SIBLING condition in the same
// rule still reached the same refusal site.
//
// So a rule holds one condition. A refusal added inside an existing rule
// fails here, at the function that grew it; a refusal added as a new rule has
// no fixture and fails the test above. There is no third place to put one.
func TestEveryRuleHoldsExactlyOneCondition(t *testing.T) {
	sites, err := conditions.Sites(".", "ErrInvalid", "ErrSchema")
	if err != nil {
		t.Fatal(err)
	}
	inRules := map[string][]conditions.Site{}
	for _, site := range sites {
		if site.File != "rules.go" {
			continue
		}
		if site.Func == "" {
			t.Errorf("rules.go:%d is a refusal outside any function", site.Line)
			continue
		}
		inRules[site.Func] = append(inRules[site.Func], site)
	}
	if len(inRules) == 0 {
		t.Fatal("no refusal sites found in rules.go: the enumeration itself is broken")
	}

	// The one refusal in rules.go that is not a rule: the loop itself
	// refuses a nil contract before any rule runs, so no rule row could hold
	// it and no document fixture can reach it (TestANilModelIsRefused
	// forces it). Named with its reason, and held to exactly one condition,
	// so the exemption cannot grow quietly.
	const outsideTheTable = "validate"
	if held := len(inRules[outsideTheTable]); held != 1 {
		t.Errorf("%s holds %d refusal conditions; it is exempt from needing a rule row for exactly one, the nil contract, and anything else it refuses needs a rule and a witness", outsideTheTable, held)
	}
	delete(inRules, outsideTheTable)

	isRule := map[string]string{}
	for _, r := range rules() {
		if name := checkFunc(r.check); !strings.Contains(name, "func") {
			isRule[name] = r.name
		}
	}

	for function, found := range inRules {
		rule, governed := isRule[function]
		if !governed {
			t.Errorf("%s holds %d refusal condition(s) but is not the check of any rule, so no fixture can be required to protect it:\n\tgive it a rule row of its own, or move its refusal into the rule that calls it",
				function, len(found))
			continue
		}
		if len(found) > 1 {
			lines := make([]string, 0, len(found))
			for _, site := range found {
				lines = append(lines, site.Literal)
			}
			t.Errorf("rule %s (%s) holds %d conditions, not one: %q\n\tone rule is one condition, because one witness proves one predicate — split it into a rule per condition, each with its own fixture",
				rule, function, len(found), lines)
		}
	}

	// And the other direction: a rule whose check is a named function in
	// this package refuses something, or it is not a rule at all.
	for _, r := range rules() {
		name := checkFunc(r.check)
		if strings.Contains(name, "func") || r.inherent {
			continue
		}
		if len(inRules[name]) == 0 {
			t.Errorf("rule %s (%s) refuses nothing in rules.go: either its condition moved and the row is stale, or the row never had one", r.name, name)
		}
	}
	t.Logf("%d rules, one condition each, in %d refusal sites across rules.go", len(isRule), len(inRules))
}
