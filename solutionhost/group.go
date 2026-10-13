package solutionhost

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/resources"
)

// SolutionGroup is the presentation group a solution is organised under on
// its host — in the navigation, after the host's own entitlement filter, and
// in the administrative catalogue, where a headless solution is organised
// too, which is why it is a block of the document and not of the frontend.
// It is GENERATED: the renderer projects it from the composing product's
// solution groups (resources.SolutionGroup, through Workspace.SolutionGroupOf)
// and an author writes it nowhere else. It grants nothing and installs
// nothing: a group is a heading, where it sorts, and the id the solutions
// under it share, and a solution's own identity is unchanged by it. Solutions
// only — a module is grouped by nothing — and a tombstone declares none.
//
// The block is OPTIONAL, for BuildSize's reason: a document without it encodes
// as it did before the block existed, so every stored digest holds, and one
// that carries it is held to every group rule.
type SolutionGroup struct {
	// ID is the group's id as the composing workspace spells it: lowercase
	// letters and digits, dash-separated (resources.ValidateSolutionGroupID).
	ID string `yaml:"id" json:"id"`

	// Title is what the host labels the group with: one non-empty line.
	Title string `yaml:"title" json:"title"`

	// Order is where the group sorts among groups. Declared, zero included:
	// an omitted order would decode as zero and read as a choice.
	Order int64 `yaml:"order" json:"order"`
}

// GroupTextMaxBytes bounds a group's id and title, in UTF-8 bytes: the
// frontend text bound, since a host persists and projects both the same way.
const GroupTextMaxBytes = FrontendTextMaxBytes

// The group rules, by name; see rule.
const (
	ruleGroupFieldsDeclared    = "group-fields-declared"
	ruleGroupOrderDecimal      = "group-order-decimal"
	ruleGroupOrderFits         = "group-order-fits"
	ruleGroupAbsentWhenRemoved = "group-absent-when-removed"
	ruleGroupSolutionOnly      = "group-solution-only"
	ruleGroupTextBounded       = "group-text-bounded"
	ruleGroupID                = "group-id"
	ruleGroupTitle             = "group-title"
)

// groupNodeRules run over the group node before it is typed, and
// groupModelRules over the decoded block; both are rows of the one table in
// rules(), which fixes their order among the other rules.
func groupNodeRules() []rule {
	return []rule{
		{name: ruleGroupFieldsDeclared, section: sectionGroup, node: checkGroupFieldsDeclared},
		{name: ruleGroupOrderDecimal, section: sectionGroup, node: checkGroupOrderDecimal},
		{name: ruleGroupOrderFits, section: sectionGroup, node: checkGroupOrderFits},
	}
}

func groupModelRules() []rule {
	return []rule{
		{name: ruleGroupAbsentWhenRemoved, section: sectionGroup, check: checkGroupAbsentWhenRemoved},
		{name: ruleGroupSolutionOnly, section: sectionGroup, check: checkGroupSolutionOnly},
		{name: ruleGroupTextBounded, section: sectionGroup, check: checkGroupTextBounded},
		{name: ruleGroupID, section: sectionGroup, check: checkGroupID},
		{name: ruleGroupTitle, section: sectionGroup, check: checkGroupTitle},
	}
}

// groupFields lists the fields of the block by the path a refusal names, the
// order apart as its one number, which is signed. A block that is not a
// mapping is left to the typed decoder, which refuses its shape.
func groupFields(section *yaml.Node) (order countField, others []countField) {
	for _, name := range []string{"id", "title"} {
		others = append(others, countField{path: "group." + name, node: mappingValue(section, name)})
	}
	return countField{path: "group.order", node: mappingValue(section, "order")}, others
}

func checkGroupFieldsDeclared(section *yaml.Node) error {
	order, others := groupFields(section)
	for _, field := range append(others, order) {
		if field.node == nil {
			return fmt.Errorf("%w: %s must be declared; an absent field decodes as zero or empty, and a value nobody wrote would be signed as a choice", ErrInvalid, field.path)
		}
	}
	return nil
}

func checkGroupOrderDecimal(section *yaml.Node) error {
	if order, _ := groupFields(section); order.node != nil && !isDecimalInteger(order.node, true) {
		return fmt.Errorf("%w: %s is %q, not a whole number written in decimal digits; a number has one spelling here, and yaml would otherwise read a base prefix, a leading zero, a plus sign or a string into a number nobody wrote", ErrInvalid, order.path, order.node.Value)
	}
	return nil
}

func checkGroupOrderFits(section *yaml.Node) error {
	if order, _ := groupFields(section); order.node != nil && isDecimalInteger(order.node, true) && !fitsInteger(order.node, true) {
		return fmt.Errorf("%w: %s is %s, more than the field can hold", ErrInvalid, order.path, order.node.Value)
	}
	return nil
}

func checkGroupAbsentWhenRemoved(document *SolutionHostBinding) error {
	if document.Removed {
		return fmt.Errorf("%w: a removed generation declares no group; a group is where a host shows a solution, and this generation declares that there is nothing to show", ErrInvalid)
	}
	return nil
}

func checkGroupSolutionOnly(document *SolutionHostBinding) error {
	if document.Kind != KindSolution {
		return fmt.Errorf("%w: group is declared on a %q; a group organises solutions, and a module is grouped by nothing", ErrInvalid, document.Kind)
	}
	return nil
}

func (group *SolutionGroup) texts() []frontendText {
	return []frontendText{{path: "group.id", value: group.ID}, {path: "group.title", value: group.Title}}
}

func checkGroupTextBounded(document *SolutionHostBinding) error {
	for _, text := range document.Group.texts() {
		if len(text.value) > GroupTextMaxBytes {
			return fmt.Errorf("%w: group text exceeds %d bytes at %s", ErrInvalid, GroupTextMaxBytes, text.path)
		}
	}
	return nil
}

func checkGroupID(document *SolutionHostBinding) error {
	if err := resources.ValidateSolutionGroupID(document.Group.ID); err != nil {
		return fmt.Errorf("%w: group.id %q is invalid: %v", ErrInvalid, document.Group.ID, err)
	}
	return nil
}

func checkGroupTitle(document *SolutionHostBinding) error {
	if !isText(document.Group.Title) {
		return fmt.Errorf("%w: group.title must be a non-empty single-line text, got %q", ErrInvalid, document.Group.Title)
	}
	return nil
}
