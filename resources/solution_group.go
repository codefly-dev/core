package resources

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// SolutionGroup organises several of a composition's solutions under one
// heading of a host's navigation. It is PRESENTATION ONLY: a group grants no
// access, installs nothing, and is not an identity anything is granted or
// installed under; a host groups a navigation AFTER each solution's own
// entitlements are judged, and a solution's stable identity is unchanged by
// the group it is shown in. Groups are optional and flat: a solution is in at
// most one, and a group holds solutions, never groups.
//
// It is authored by the composing product — the workspace that lists many
// solutions and so has something to organise — never by a solution's own
// package, and it is not inherited: a composed workspace's groups organise
// that workspace's own view and say nothing about the product's.
type SolutionGroup struct {
	// ID is the group's id: lowercase letters and digits, dash-separated,
	// unique within the workspace. See ValidateSolutionGroupID.
	ID string `yaml:"id"`
	// Title is what a host labels the group with: one non-blank line, no
	// control characters (ValidateSolutionGroupTitle).
	Title string `yaml:"title"`
	// Order is where the group sorts among groups. Declared, zero included,
	// and within SolutionGroupOrderMin..SolutionGroupOrderMax
	// (ValidateSolutionGroupOrder): the field is wide, the admitted range is
	// a signed 32-bit integer, so every host wire and every client number
	// represents it exactly.
	Order int64 `yaml:"order"`
	// Solutions names the group's members by the name the composition carries
	// each under (Member.Name): every one an effective solution of this
	// composition, each in one group at most.
	Solutions []string `yaml:"solutions"`
}

// The bounds a solution group is authored within and carried within: the
// presence document's group block is held to these same values, so what a
// composition authors is what a host reads, and a host parser aligns with one
// definition rather than two.
const (
	// SolutionGroupTextMaxBytes bounds a group's id and title, in UTF-8 bytes.
	SolutionGroupTextMaxBytes = 1024
	// SolutionGroupOrderMin and SolutionGroupOrderMax are the admitted order
	// range: a signed 32-bit integer, which every host wire (an int32) and
	// every client number represents exactly. Nothing casts: a value outside
	// is refused by name wherever it is read.
	SolutionGroupOrderMin int64 = math.MinInt32
	SolutionGroupOrderMax int64 = math.MaxInt32
)

var solutionGroupIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidateSolutionGroupID holds a group id to its grammar: lowercase letters
// and digits separated by single dashes, the shape a host keys a group by. A
// presence document's group.id is held to this same function.
func ValidateSolutionGroupID(id string) error {
	if !solutionGroupIDPattern.MatchString(id) {
		return fmt.Errorf("solution group id %q must be lowercase letters and digits separated by dashes", id)
	}
	return nil
}

// ValidateSolutionGroupTitle holds a group title to one line a host can show:
// not blank, and free of control characters (C0 and C1 alike), so a heading
// cannot be empty to the eye or carry a newline. A presence document's
// group.title is held to this same function; the byte bound is held apart.
func ValidateSolutionGroupTitle(title string) error {
	if strings.TrimSpace(title) == "" || strings.ContainsFunc(title, unicode.IsControl) {
		return fmt.Errorf("solution group title %q must be one non-blank line without control characters", title)
	}
	return nil
}

// ValidateSolutionGroupOrder holds a group order to the admitted range. A
// presence document's group.order is held to this same function, on the
// written number before it is typed.
func ValidateSolutionGroupOrder(order int64) error {
	if order < SolutionGroupOrderMin || order > SolutionGroupOrderMax {
		return fmt.Errorf("solution group order %d is outside %d..%d, the range every host represents exactly", order, SolutionGroupOrderMin, SolutionGroupOrderMax)
	}
	return nil
}

// validateSolutionGroups runs after composition, since a member is judged
// against the composed set: every group has an id of the grammar, once, a
// one-line title, both within the text bound, an order within the admitted
// range, and at least one member, and every member is an effective solution
// of this composition that no other group names.
func (workspace *Workspace) validateSolutionGroups() error {
	ids := make(map[string]bool, len(workspace.SolutionGroups))
	grouped := make(map[string]string)
	for _, group := range workspace.SolutionGroups {
		if group == nil {
			return fmt.Errorf("workspace %q declares an empty solution group entry", workspace.Name)
		}
		if err := ValidateSolutionGroupID(group.ID); err != nil {
			return err
		}
		if ids[group.ID] {
			return fmt.Errorf("solution group %q is declared twice", group.ID)
		}
		ids[group.ID] = true
		if err := ValidateSolutionGroupTitle(group.Title); err != nil {
			return fmt.Errorf("solution group %q: %w", group.ID, err)
		}
		if len(group.ID) > SolutionGroupTextMaxBytes || len(group.Title) > SolutionGroupTextMaxBytes {
			return fmt.Errorf("solution group %q: id and title are each at most %d bytes", group.ID, SolutionGroupTextMaxBytes)
		}
		if err := ValidateSolutionGroupOrder(group.Order); err != nil {
			return fmt.Errorf("solution group %q: %w", group.ID, err)
		}
		if len(group.Solutions) == 0 {
			return fmt.Errorf("solution group %q organises no solution", group.ID)
		}
		for _, name := range group.Solutions {
			member, ok := workspace.Member(name)
			if !ok || member.Role != MemberRoleSolution {
				return fmt.Errorf("solution group %q names %q, which is not a solution of this composition", group.ID, name)
			}
			if other, taken := grouped[name]; taken {
				return fmt.Errorf("solution %q is in groups %q and %q; a solution is in at most one", name, other, group.ID)
			}
			grouped[name] = group.ID
		}
	}
	return nil
}

// SolutionGroupOf answers the group a solution of this composition is
// organised under, by the name the composition carries it under, and false
// when it is in none — a module always is, since nothing groups one. It is
// what a renderer projects into the solution's presence as its top-level
// group block, once the composition is resolved; the record is the
// workspace's own.
func (workspace *Workspace) SolutionGroupOf(solution string) (*SolutionGroup, bool) {
	for _, group := range workspace.SolutionGroups {
		if group != nil && slices.Contains(group.Solutions, solution) {
			return group, true
		}
	}
	return nil, false
}
