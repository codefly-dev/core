package cell

import (
	"slices"
	"testing"

	"github.com/codefly-dev/core/resources"
)

// TestTheVisibilityVocabularyIsCoreOwn: the cell carries a service's declared
// visibility verbatim, so the set this reader accepts must be the set core
// declares — including the two spellings core marks deprecated, since core
// itself still assigns "module" (resources/module.go) and the render copies
// the field through. A reader refusing those would refuse a cell a real
// publish writes.
//
// resources is imported HERE and not by the package: a wire model the
// platform's loader links must not pull core's resource tree in with it. That
// is also why the vocabulary is a literal in rules.go rather than a reference,
// and why this test exists to catch the two drifting apart.
func TestTheVisibilityVocabularyIsCoreOwn(t *testing.T) {
	declared := []string{
		resources.VisibilityPrivate, resources.VisibilityInternal,
		resources.VisibilityModule, resources.VisibilityPublic, resources.VisibilityExternal,
	}
	for _, visibility := range declared {
		if !slices.Contains(visibilities, visibility) {
			t.Errorf("core declares visibility %q, which a cell may carry and this reader refuses", visibility)
		}
	}
	for _, visibility := range visibilities {
		if !slices.Contains(declared, visibility) {
			t.Errorf("this reader accepts visibility %q, which core does not declare", visibility)
		}
	}
	if allowAllModules != resources.AllowAllModules {
		t.Errorf("the allow-list wildcard is %q here and %q in core", allowAllModules, resources.AllowAllModules)
	}
}

// TestTheCellPackageDoesNotLinkResources: the rule above is a test-only
// import. If the package itself ever imports resources, the platform's loader
// starts linking core's protos and resource tree to read a cell file, which is
// the cost the literal vocabulary exists to avoid.
func TestTheCellPackageDoesNotLinkResources(t *testing.T) {
	for _, path := range packageImports(t) {
		if path == "github.com/codefly-dev/core/resources" {
			t.Fatalf("the cell package imports resources; keep it to the test binary (see TestTheVisibilityVocabularyIsCoreOwn)")
		}
	}
}
