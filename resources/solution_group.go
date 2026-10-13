package resources

import (
	"fmt"
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
	// Title is what a host labels the group with: one non-empty line.
	Title string `yaml:"title"`
	// Order is where the group sorts among groups. Declared, zero included.
	Order int64 `yaml:"order"`
	// Solutions names the group's members by the name the composition carries
	// each under (Member.Name): every one an effective solution of this
	// composition, each in one group at most.
	Solutions []string `yaml:"solutions"`
}

var solutionGroupIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidateSolutionGroupID holds a group id to its grammar: lowercase letters
// and digits separated by single dashes, the shape a host keys a navigation
// group by. A presence document's frontend.group.id is held to this same
// function, so what a composition authors is what a host reads.
func ValidateSolutionGroupID(id string) error {
	if !solutionGroupIDPattern.MatchString(id) {
		return fmt.Errorf("solution group id %q must be lowercase letters and digits separated by dashes", id)
	}
	return nil
}

// validateSolutionGroups runs after composition, since a member is judged
// against the composed set: every group has an id of the grammar, once, a
// one-line title and at least one member, and every member is an effective
// solution of this composition that no other group names.
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
		if strings.TrimSpace(group.Title) == "" || strings.ContainsFunc(group.Title, unicode.IsControl) {
			return fmt.Errorf("solution group %q requires a one-line title", group.ID)
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
// what a renderer projects into the solution's presence as frontend.group,
// once the composition is resolved; the record is the workspace's own.
func (workspace *Workspace) SolutionGroupOf(solution string) (*SolutionGroup, bool) {
	for _, group := range workspace.SolutionGroups {
		if group != nil && slices.Contains(group.Solutions, solution) {
			return group, true
		}
	}
	return nil, false
}
