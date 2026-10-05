package cell

import (
	"slices"
	"testing"

	"github.com/codefly-dev/core/resources"
)

// TestTheVisibilityVocabularyIsCoreOwn: the cell carries a service's declared
// visibility verbatim, so the set this reader accepts must be the set core
// declares — no wider, and no narrower. core#703's cold cutover deleted the
// "module" and "external" spellings, and this test is what caught the cell
// still accepting them when that landed on main.
//
// resources is imported HERE and not by the package: a wire model the
// platform's loader links must not pull core's resource tree in with it. That
// is why the vocabulary is a literal in rules.go, and why this test exists to
// hold the literal and core's own predicate together.
func TestTheVisibilityVocabularyIsCoreOwn(t *testing.T) {
	for _, visibility := range visibilities {
		if !resources.KnownVisibility(visibility) {
			t.Errorf("this reader accepts visibility %q, which core does not declare", visibility)
		}
	}
	for _, declared := range []string{resources.VisibilityPrivate, resources.VisibilityInternal, resources.VisibilityPublic} {
		if !slices.Contains(visibilities, declared) {
			t.Errorf("core declares visibility %q, which a cell may carry and this reader refuses", declared)
		}
	}
	// The spellings the cutover deleted: a service declaring one no longer
	// loads, so a cell carrying one describes a service that cannot exist.
	for _, gone := range []string{"module", "external"} {
		if resources.KnownVisibility(gone) {
			t.Errorf("core declares %q again; a cell may then carry it and this reader refuses it", gone)
		}
		if slices.Contains(visibilities, gone) {
			t.Errorf("this reader still accepts the deleted spelling %q", gone)
		}
	}
	if allowAllModules != resources.AllowAllModules {
		t.Errorf("the allow-list wildcard is %q here and %q in core", allowAllModules, resources.AllowAllModules)
	}
	if visibilityInternal != resources.VisibilityInternal {
		t.Errorf("the allow-list's visibility is %q here and %q in core", visibilityInternal, resources.VisibilityInternal)
	}
	// An allow-list is only read for "internal", and core refuses one
	// anywhere else at the declaration; the cell's rule must agree.
	if err := resources.ValidateEndpointDeclaration("api", "rest", resources.VisibilityPublic, "", []string{"billing"}); err == nil {
		t.Error("core now permits an allow-list on a public endpoint; the cell's allow-modules-need-internal rule contradicts it")
	}
}

// TestTheCellPackageDoesNotLinkResources: the rules above are a test-only
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
