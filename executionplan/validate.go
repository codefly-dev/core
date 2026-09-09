package executionplan

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ErrInvalid is returned for a plan that is malformed or internally inconsistent.
var ErrInvalid = errors.New("invalid Codefly execution plan")

// Validate checks every invariant a consumer of a plan is entitled to assume.
// It is pure: it reads nothing outside the plan.
func (plan *Plan) Validate() error {
	if plan == nil {
		return fmt.Errorf("%w: plan is required", ErrInvalid)
	}
	if plan.Schema != SchemaV1 {
		return fmt.Errorf("%w: schema %q is not %q", ErrInvalid, plan.Schema, SchemaV1)
	}
	if err := plan.Requested.validate(); err != nil {
		return err
	}
	nodes, err := plan.validateNodes()
	if err != nil {
		return err
	}
	if _, selected := nodes[plan.Requested.Service]; !selected {
		return fmt.Errorf("%w: requested service %q is not in the closure", ErrInvalid, plan.Requested.Service)
	}
	if err := plan.validateEdges(nodes); err != nil {
		return err
	}
	if err := plan.validateConfigurations(nodes); err != nil {
		return err
	}
	if err := plan.validateSchemaSteps(nodes); err != nil {
		return err
	}
	if err := plan.StatePolicy.validate(); err != nil {
		return err
	}
	return plan.validateAcyclic()
}

func (target *Target) validate() error {
	if target.Workspace == "" {
		return fmt.Errorf("%w: requested workspace is required", ErrInvalid)
	}
	if target.Service == "" {
		return fmt.Errorf("%w: requested service is required", ErrInvalid)
	}
	switch target.Phase {
	case PhaseBuild, PhaseRun, PhaseTest, PhaseDeploy:
	default:
		return fmt.Errorf("%w: requested phase %q is not a known phase", ErrInvalid, target.Phase)
	}
	return nil
}

func (plan *Plan) validateNodes() (map[string]struct{}, error) {
	if len(plan.Nodes) == 0 {
		return nil, fmt.Errorf("%w: plan selects no node", ErrInvalid)
	}
	nodes := make(map[string]struct{}, len(plan.Nodes))
	var unresolved []string
	for _, node := range plan.Nodes {
		if node.ID == "" {
			return nil, fmt.Errorf("%w: node id is required", ErrInvalid)
		}
		if _, exists := nodes[node.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate node %q", ErrInvalid, node.ID)
		}
		nodes[node.ID] = struct{}{}
		if node.Kind != NodeService {
			return nil, fmt.Errorf("%w: node %q has unknown kind %q", ErrInvalid, node.ID, node.Kind)
		}
		switch node.Resolution {
		case Resolved:
			if node.Unresolved != "" {
				return nil, fmt.Errorf("%w: resolved node %q carries an unresolved reason", ErrInvalid, node.ID)
			}
		case Unresolved:
			if node.Unresolved == "" {
				return nil, fmt.Errorf("%w: unresolved node %q must state why", ErrInvalid, node.ID)
			}
			unresolved = append(unresolved, fmt.Sprintf("%s: %s", node.ID, node.Unresolved))
		default:
			return nil, fmt.Errorf("%w: node %q has unknown resolution %q", ErrInvalid, node.ID, node.Resolution)
		}
		if err := node.Selection.validate(node.ID); err != nil {
			return nil, err
		}
		for _, artifact := range node.Artifacts {
			if err := artifact.validate(node.ID); err != nil {
				return nil, err
			}
		}
	}
	if len(unresolved) > 0 {
		sort.Strings(unresolved)
		return nil, fmt.Errorf("%w: the closure has unresolved nodes:\n  %s",
			ErrInvalid, strings.Join(unresolved, "\n  "))
	}
	return nodes, nil
}

func (artifact *Artifact) validate(node string) error {
	if artifact.Reference == "" {
		return fmt.Errorf("%w: node %q artifact reference is required", ErrInvalid, node)
	}
	switch artifact.Verification {
	case VerificationLocal, VerificationVerified, VerificationUnverified:
	default:
		return fmt.Errorf("%w: node %q artifact %q has unknown verification %q",
			ErrInvalid, node, artifact.Reference, artifact.Verification)
	}
	if artifact.Digest != "" && !digestPattern.MatchString(artifact.Digest) {
		return fmt.Errorf("%w: node %q artifact %q digest %q is not a sha256 digest",
			ErrInvalid, node, artifact.Reference, artifact.Digest)
	}
	return artifact.Selection.validate(node + " artifact " + artifact.Reference)
}

func (plan *Plan) validateEdges(nodes map[string]struct{}) error {
	seen := make(map[string]struct{}, len(plan.Edges))
	for _, edge := range plan.Edges {
		switch edge.Kind {
		case KindDeclared, KindBuild, KindRuntime, KindCompletion, KindSchema, KindExternal:
		default:
			return fmt.Errorf("%w: edge %s -> %s has unknown kind %q", ErrInvalid, edge.From, edge.To, edge.Kind)
		}
		if edge.From == edge.To {
			return fmt.Errorf("%w: node %q depends on itself", ErrInvalid, edge.From)
		}
		for _, endpoint := range []string{edge.From, edge.To} {
			if _, exists := nodes[endpoint]; !exists {
				return fmt.Errorf("%w: edge %s -> %s references unselected node %q",
					ErrInvalid, edge.From, edge.To, endpoint)
			}
		}
		key := string(edge.Kind) + "\x00" + edge.From + "\x00" + edge.To
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate %s edge %s -> %s", ErrInvalid, edge.Kind, edge.From, edge.To)
		}
		seen[key] = struct{}{}
		if err := edge.Selection.validate(fmt.Sprintf("edge %s -> %s", edge.From, edge.To)); err != nil {
			return err
		}
	}
	return nil
}

func (plan *Plan) validateConfigurations(nodes map[string]struct{}) error {
	seen := make(map[string]struct{}, len(plan.Configurations))
	for _, origin := range plan.Configurations {
		if _, exists := nodes[origin.Consumer]; !exists {
			return fmt.Errorf("%w: configuration %q consumer %q is not in the closure",
				ErrInvalid, origin.Key, origin.Consumer)
		}
		if origin.Key == "" {
			return fmt.Errorf("%w: configuration consumed by %q has no key", ErrInvalid, origin.Consumer)
		}
		switch origin.Origin {
		case OriginServiceEndpoint, OriginServiceConfiguration:
			if _, exists := nodes[origin.Producer]; !exists {
				return fmt.Errorf("%w: configuration %q producer %q is not in the closure",
					ErrInvalid, origin.Key, origin.Producer)
			}
		case OriginWorkspaceConfiguration:
			if origin.Producer != "" {
				return fmt.Errorf("%w: workspace configuration %q cannot name a service producer",
					ErrInvalid, origin.Key)
			}
		default:
			return fmt.Errorf("%w: configuration %q has unknown origin %q", ErrInvalid, origin.Key, origin.Origin)
		}
		if origin.Secret && origin.SecretRef == "" {
			return fmt.Errorf("%w: secret configuration %q must carry a reference", ErrInvalid, origin.Key)
		}
		if !origin.Secret && (origin.SecretRef != "" || origin.SecretVersion != "") {
			return fmt.Errorf("%w: non-secret configuration %q carries a secret reference", ErrInvalid, origin.Key)
		}
		key := origin.Consumer + "\x00" + origin.Key
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: %q receives configuration %q from more than one origin",
				ErrInvalid, origin.Consumer, origin.Key)
		}
		seen[key] = struct{}{}
		if err := origin.Selection.validate("configuration " + origin.Key); err != nil {
			return err
		}
	}
	return nil
}

func (plan *Plan) validateSchemaSteps(nodes map[string]struct{}) error {
	seen := make(map[string]struct{}, len(plan.SchemaSteps))
	for _, step := range plan.SchemaSteps {
		if step.ID == "" {
			return fmt.Errorf("%w: schema step id is required", ErrInvalid)
		}
		if _, exists := seen[step.ID]; exists {
			return fmt.Errorf("%w: duplicate schema step %q", ErrInvalid, step.ID)
		}
		seen[step.ID] = struct{}{}
		for _, target := range step.Targets {
			if _, exists := nodes[target]; !exists {
				return fmt.Errorf("%w: schema step %q targets unselected node %q", ErrInvalid, step.ID, target)
			}
		}
		if err := step.Selection.validate("schema step " + step.ID); err != nil {
			return err
		}
	}
	return nil
}

func (policy *StatePolicy) validate() error {
	switch policy.Lifecycle {
	case LifecycleStop, LifecycleKeepRunning, LifecycleReset:
		return nil
	default:
		return fmt.Errorf("%w: state policy lifecycle %q is not known", ErrInvalid, policy.Lifecycle)
	}
}

func (selection *Selection) validate(subject string) error {
	switch selection.Reason {
	case ReasonRequestedTarget, ReasonDeclaredDependency, ReasonTransitiveDependency,
		ReasonSchemaPrerequisite, ReasonLocalOverlay, ReasonCommittedPin,
		ReasonWorkspaceLayout, ReasonInvocationOverride:
		return nil
	default:
		return fmt.Errorf("%w: %s has unknown selection reason %q", ErrInvalid, subject, selection.Reason)
	}
}

// validateAcyclic rejects a plan whose edges cannot be ordered. A plan is the
// thing an executor walks, so a cycle here is not a diagnostic — it is a plan
// that cannot be run.
func (plan *Plan) validateAcyclic() error {
	inDegree := make(map[string]int, len(plan.Nodes))
	outgoing := make(map[string][]string, len(plan.Nodes))
	for _, node := range plan.Nodes {
		inDegree[node.ID] = 0
	}
	for _, edge := range plan.Edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge.To)
		inDegree[edge.To]++
	}
	var ready []string
	for id, degree := range inDegree {
		if degree == 0 {
			ready = append(ready, id)
		}
	}
	slices.Sort(ready)
	ordered := 0
	for len(ready) > 0 {
		current := ready[0]
		ready = ready[1:]
		ordered++
		for _, next := range outgoing[current] {
			inDegree[next]--
			if inDegree[next] == 0 {
				ready = append(ready, next)
			}
		}
		slices.Sort(ready)
	}
	if ordered != len(plan.Nodes) {
		var cyclic []string
		for id, degree := range inDegree {
			if degree > 0 {
				cyclic = append(cyclic, id)
			}
		}
		sort.Strings(cyclic)
		return fmt.Errorf("%w: dependency cycle among %s", ErrInvalid, strings.Join(cyclic, ", "))
	}
	return nil
}
