package conditions

import (
	"os"
	"path/filepath"
	"testing"
)

// The enumeration is what decides whether a refusal condition is protected,
// so a defect in it reads as "everything is protected". Both defects it has
// had were of exactly that shape and both were found by a caller, not here:
//
//   - the enclosing function was tracked on a stack popped when ast.Inspect
//     reported the end of a node — which it does for EVERY node, not only a
//     declaration, so the stack emptied at the first leaf and every site was
//     reported as belonging to no function;
//   - a format string written as a CONCATENATION was not read at all, so a
//     refusal whose message is spelled across two lines was invisible: not an
//     unprotected condition, but a condition no check for protection saw.
//
// Each case below is one of those, plus the boundaries either fix could move.
func TestSitesFindsEveryRefusalAndNamesItsFunction(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "rules.go", `package p

import "fmt"

var ErrInvalid = fmt.Errorf("invalid")
var ErrOther = fmt.Errorf("other")

func single() error {
	return fmt.Errorf("%w: a single literal", ErrInvalid)
}

func concatenated() error {
	return fmt.Errorf("%w: the first half "+
		"and the second half", ErrInvalid)
}

func parenthesised() error {
	return fmt.Errorf(("%w: inside parentheses"), ErrInvalid)
}

func twoConditions() error {
	if true {
		return fmt.Errorf("%w: the first condition", ErrInvalid)
	}
	return fmt.Errorf("%w: the second condition", ErrInvalid)
}

func anotherSentinel() error {
	return fmt.Errorf("%w: not the sentinel asked for", ErrOther)
}

func notWrapped() error {
	return fmt.Errorf("plain, no verb for the sentinel")
}

func insideAClosure() error {
	refuse := func() error {
		return fmt.Errorf("%w: written in a function literal", ErrInvalid)
	}
	return refuse()
}
`)
	// A _test.go file is not the package's behaviour and must not be counted.
	write(t, dir, "rules_test.go", `package p

import "fmt"

func fromATest() error {
	return fmt.Errorf("%w: written in a test", ErrInvalid)
}
`)

	sites, err := Sites(dir, "ErrInvalid")
	if err != nil {
		t.Fatal(err)
	}

	byFunc := map[string][]string{}
	for _, site := range sites {
		if site.File != "rules.go" {
			t.Errorf("site from %s: a test file is not the package's behaviour", site.File)
		}
		byFunc[site.Func] = append(byFunc[site.Func], site.Literal)
	}

	for _, want := range []struct {
		function string
		count    int
		literal  string
	}{
		{"single", 1, "a single literal"},
		// The whole concatenation is read: the literal spans both operands,
		// so a fix that read only the first would fail here rather than pass
		// a count-only assertion.
		{"concatenated", 1, "the first half and the second half"},
		{"parenthesised", 1, "inside parentheses"},
		{"twoConditions", 2, ""},
		// A refusal in a function literal belongs to the declaration that
		// holds it, which is what makes "one rule, one condition" answerable
		// for a rule whose check closes over something.
		{"insideAClosure", 1, "written in a function literal"},
	} {
		found := byFunc[want.function]
		if len(found) != want.count {
			t.Errorf("%s: %d site(s), want %d (%q)", want.function, len(found), want.count, found)
			continue
		}
		if want.literal != "" && found[0] != want.literal {
			t.Errorf("%s: literal %q, want %q", want.function, found[0], want.literal)
		}
	}
	for _, unwanted := range []string{"anotherSentinel", "notWrapped", "fromATest"} {
		if found, ok := byFunc[unwanted]; ok {
			t.Errorf("%s must not be a site: %q", unwanted, found)
		}
	}
	if _, ok := byFunc[""]; ok {
		t.Errorf("a site belongs to no function: the enclosing declaration is not being tracked")
	}
}

// Sites reports the directory's own failure rather than an empty list: a
// caller asserting "more than zero sites" would otherwise read a wrong path
// as a package with nothing to refuse.
func TestSitesRefusesADirectoryItCannotRead(t *testing.T) {
	if _, err := Sites(filepath.Join(t.TempDir(), "absent"), "ErrInvalid"); err == nil {
		t.Fatal("a missing directory must be an error, not an empty enumeration")
	}
	dir := t.TempDir()
	write(t, dir, "broken.go", "package p\nfunc (")
	if _, err := Sites(dir, "ErrInvalid"); err == nil {
		t.Fatal("a file that does not parse must be an error, not an empty enumeration")
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
