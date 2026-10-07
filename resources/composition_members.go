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
	// MemberRoleSolution is a module declared under `solutions:` — a product's
	// addition to the host it composes. It keeps that role, and the workspace
	// that declared it, however many compositions deep it is carried.
	MemberRoleSolution MemberRole = "solution"
)

// Member is one module of a composition with its provenance: the role it was
// declared in and the workspace that declared it. It is what a join over the
// composition judges an edge with, which a bare module name cannot carry.
type Member struct {
	Name      string
	Role      MemberRole
	Workspace string
}

// Provenance answers who a module is to the composition that carries it. The
// composition — *Workspace — is the one implementation. Every verdict on an
// edge takes one, so a reader that holds no composition cannot ask the verdict
// and get an answer that a composition would have refused: it is a caller's
// provenance, not a precheck beside the verdict.
type Provenance interface {
	Member(name string) (Member, bool)
}

// ErrSolutionReachesThroughHost is returned for a dependency through which a
// solution would reach a module's endpoint directly. A solution reaches the
// host's modules only through the host — the gateway it registers its own
// upstream with — never by a route of its own, so the edge is refused inside
// the one verdict every reader consults, whatever the endpoint's visibility
// grants.
var ErrSolutionReachesThroughHost = errors.New("a solution reaches modules only through the host")

// ErrUnjudgedProvenance is returned when an edge is judged without a
// composition that carries both of its ends: there is nothing to judge the
// edge with, and this package does not answer on the assumption that whoever
// asked is a module.
var ErrUnjudgedProvenance = errors.New("the edge's provenance is not judged: the composition does not carry it")

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
// judged by visibility alone. No provenance, or an end the composition does
// not carry, is ErrUnjudgedProvenance.
//
// It is the first step of every verdict on an edge (ConsumedDependencyEndpoints
// and what builds on it), so no reader reaches the endpoint's visibility
// without passing it.
func JudgeCompositionEdge(provenance Provenance, consumerModule, producerModule string, dependency *ServiceDependency) error {
	if dependency == nil {
		return fmt.Errorf("composition edge from %q to %q names no dependency", consumerModule, producerModule)
	}
	if provenance == nil {
		return fmt.Errorf("%w: dependency %s of module %q is judged with no composition", ErrUnjudgedProvenance, dependency.Unique(), consumerModule)
	}
	consumer, ok := provenance.Member(consumerModule)
	if !ok {
		return fmt.Errorf("%w: dependency %s of module %q, which the composition does not carry", ErrUnjudgedProvenance, dependency.Unique(), consumerModule)
	}
	producer, ok := provenance.Member(producerModule)
	if !ok {
		return fmt.Errorf("%w: dependency %s of module %q on module %q, which the composition does not carry", ErrUnjudgedProvenance, dependency.Unique(), consumerModule, producerModule)
	}
	if consumer.Role == MemberRoleSolution && producer.Role == MemberRoleModule && dependency.Kind.ReachesEndpoints() {
		return fmt.Errorf("%w: solution %q (of workspace %q) declares %s on module %q (of workspace %q), a direct route to its endpoints",
			ErrSolutionReachesThroughHost, consumer.Name, consumer.Workspace, dependency.Unique(), producer.Name, producer.Workspace)
	}
	return nil
}
