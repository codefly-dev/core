package resources

import (
	"errors"
	"fmt"
)

// MemberRole is what a module is to the composition that carries it.
type MemberRole string

const (
	// MemberRoleModule is a module declared under `modules:` — of the product
	// itself, or of a platform workspace the product composes: the host.
	MemberRoleModule MemberRole = "module"
	// MemberRoleSolution is a module declared under `solutions:` — the
	// product's addition to the host it composes.
	MemberRoleSolution MemberRole = "solution"
)

// Member is one module of a composition with its provenance: the role it was
// declared in and the workspace that declared it. It is what a join over the
// composition judges an edge with, which a bare service identity cannot carry.
type Member struct {
	Name      string
	Role      MemberRole
	Workspace string
}

// ErrSolutionReachesThroughHost is returned for a dependency through which a
// solution would reach a module's endpoint directly. A solution reaches the
// host's modules only through the host — the gateway it registers its own
// upstream with — never by a route of its own, so the edge is refused where
// the composition is judged: the static pass and the derivation of the
// allow-list, never left to a caller's precheck.
var ErrSolutionReachesThroughHost = errors.New("a solution reaches modules only through the host")

// Member reports the provenance of a module of this composition, and whether
// the composition carries it at all.
func (workspace *Workspace) Member(name string) (Member, bool) {
	if workspace == nil {
		return Member{}, false
	}
	if workspace.moduleReference(name) == nil {
		return Member{}, false
	}
	member := Member{Name: name, Role: MemberRoleModule, Workspace: workspace.Name}
	if role, ok := workspace.memberRoles[name]; ok {
		member.Role = role
	}
	if owner, ok := workspace.memberOwners[name]; ok {
		member.Workspace = owner
	}
	return member, true
}

// Members lists every module of the composition with its provenance, in
// declaration order.
func (workspace *Workspace) Members() []Member {
	members := make([]Member, 0, len(workspace.Modules))
	for _, ref := range workspace.Modules {
		member, _ := workspace.Member(ref.Name)
		members = append(members, member)
	}
	return members
}

// JudgeCompositionEdge judges a declared dependency by the provenance of its
// two ends, before the export boundary judges the endpoint: a solution's edge
// that would reach a module's endpoints at run time is refused
// (ErrSolutionReachesThroughHost), whatever the endpoint's visibility grants.
// A build or schema edge reads the module's contract and calls nothing, so it
// is not that route; an edge between two solutions, or from a module, is
// judged by visibility alone. A consumer or producer the composition does not
// carry is unjudged provenance, and refused as such.
func (workspace *Workspace) JudgeCompositionEdge(consumerModule, producerModule string, dependency *ServiceDependency) error {
	if dependency == nil {
		return fmt.Errorf("composition edge from %q to %q names no dependency", consumerModule, producerModule)
	}
	consumer, ok := workspace.Member(consumerModule)
	if !ok {
		return fmt.Errorf("dependency %s of a service in module %q: the composition does not carry module %q, so the edge cannot be judged", dependency.Unique(), consumerModule, consumerModule)
	}
	producer, ok := workspace.Member(producerModule)
	if !ok {
		return fmt.Errorf("dependency %s of a service in module %q: the composition does not carry module %q, so the edge cannot be judged", dependency.Unique(), consumerModule, producerModule)
	}
	if consumer.Role == MemberRoleSolution && producer.Role == MemberRoleModule && dependency.Kind.ReachesEndpoints() {
		return fmt.Errorf("%w: solution %q (of workspace %q) declares %s on module %q (of workspace %q), a direct route to its endpoints",
			ErrSolutionReachesThroughHost, consumer.Name, consumer.Workspace, dependency.Unique(), producer.Name, producer.Workspace)
	}
	return nil
}
