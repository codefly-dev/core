// Package runnable validates and digests the immutable installation facts of a
// Runnable: the package descriptor a build produces and the binding an
// installation on one execution facility produces. Both are content-addressed
// so identical registrations are recognizable and changed content under the
// same release identity is a conflict, never a silent replacement.
package runnable

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"buf.build/go/protovalidate"
	"github.com/Masterminds/semver/v3"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

const (
	// PackageSchemaV1 is the only package descriptor schema this package accepts.
	PackageSchemaV1 = "codefly.runnable-package/v1"
	// BindingSchemaV1 is the only binding schema this package accepts.
	BindingSchemaV1 = "codefly.runnable-binding/v1"
	// TargetSchemaV1 is the only execution-target schema this package accepts.
	TargetSchemaV1 = "codefly.runnable-target/v1"
	// ProtocolV1 is the launcher/harness invocation framing this package
	// implements, and the only protocol a loaded contract may name.
	ProtocolV1 = resources.RunnableProtocolV1
)

var (
	// ErrInvalid is returned for a malformed or internally inconsistent
	// descriptor or binding.
	ErrInvalid = errors.New("invalid Codefly runnable")
	// ErrConflict is returned when content differs under the same release
	// identity.
	ErrConflict = errors.New("runnable release conflict")

	validator protovalidate.Validator
)

func init() {
	var err error
	validator, err = protovalidate.New()
	if err != nil {
		panic(err)
	}
}

// artifactFacility is the facility an artifact form executes on. A binding may
// only pair the two this way.
var artifactFacility = map[basev0.RunnableArtifact_Kind]basev0.RunnableFacility_Kind{
	basev0.RunnableArtifact_NATIVE: basev0.RunnableFacility_NATIVE,
	basev0.RunnableArtifact_IMAGE:  basev0.RunnableFacility_KUBERNETES,
}

// signalableFacilities are the dispatch forms whose execution a launcher owns
// and can therefore interrupt. Nothing signals a method running inside a
// process its owner operates, or a function its provider runs.
var signalableFacilities = map[basev0.RunnableFacility_Kind]struct{}{
	basev0.RunnableFacility_NATIVE:     {},
	basev0.RunnableFacility_KUBERNETES: {},
}

// PreparePackage clones pkg, validates it, canonicalizes its sets and
// populates the digest. A non-empty caller-supplied digest must already match:
// conflicting immutable bytes are never silently repaired.
func PreparePackage(pkg *basev0.RunnablePackage) (*basev0.RunnablePackage, error) {
	if pkg == nil {
		return nil, fmt.Errorf("%w: package is required", ErrInvalid)
	}
	prepared := &basev0.RunnablePackage{}
	proto.Merge(prepared, pkg)
	supplied := prepared.GetDigest()
	prepared.Digest = ""
	if err := validatePackage(prepared); err != nil {
		return nil, err
	}
	canonicalizePackage(prepared)
	digest, err := digestOf(PackageDigestFormatV1, prepared)
	if err != nil {
		return nil, err
	}
	if supplied != "" && supplied != digest {
		return nil, fmt.Errorf("%w: digest %s does not match descriptor bytes (%s)", ErrInvalid, supplied, digest)
	}
	prepared.Digest = digest
	return prepared, nil
}

// VerifyPackage checks that pkg carries the digest of its own canonical bytes.
func VerifyPackage(pkg *basev0.RunnablePackage) error {
	if pkg.GetDigest() == "" {
		return fmt.Errorf("%w: package carries no digest", ErrInvalid)
	}
	prepared, err := PreparePackage(pkg)
	if err != nil {
		return err
	}
	if !proto.Equal(prepared, pkg) {
		return fmt.Errorf("%w: package is not in canonical form", ErrInvalid)
	}
	return nil
}

// CompareRelease decides what registering incoming means next to existing,
// both verified packages of the same release identity: nil is an identical,
// idempotent registration; ErrConflict is changed content under the same
// immutable identity.
func CompareRelease(existing, incoming *basev0.RunnablePackage) error {
	if err := VerifyPackage(existing); err != nil {
		return fmt.Errorf("existing: %w", err)
	}
	if err := VerifyPackage(incoming); err != nil {
		return fmt.Errorf("incoming: %w", err)
	}
	if !proto.Equal(existing.GetIdentity(), incoming.GetIdentity()) {
		return fmt.Errorf("%w: packages have different release identities", ErrInvalid)
	}
	if existing.GetDigest() != incoming.GetDigest() {
		return fmt.Errorf("%w: release %s/%s@%s is already registered with digest %s; incoming digest is %s",
			ErrConflict, existing.GetIdentity().GetModule(), existing.GetIdentity().GetName(),
			existing.GetIdentity().GetVersion(), existing.GetDigest(), incoming.GetDigest())
	}
	return nil
}

// CompareBinding decides what installing incoming means next to existing, both
// verified bindings of pkg: nil is an identical, idempotent installation, and
// ErrConflict is a changed implementation or target under the same release and
// the same target identity. An installation is identified by its release, its
// facility and the environment and revision of the target it was installed
// onto, so a second environment or a re-provisioned target coexists instead of
// replacing what is already installed.
func CompareBinding(existing, incoming *basev0.RunnableBinding, pkg *basev0.RunnablePackage) error {
	if err := VerifyBinding(existing, pkg); err != nil {
		return fmt.Errorf("existing: %w", err)
	}
	if err := VerifyBinding(incoming, pkg); err != nil {
		return fmt.Errorf("incoming: %w", err)
	}
	if !proto.Equal(existing.GetIdentity(), incoming.GetIdentity()) {
		return fmt.Errorf("%w: bindings install different releases", ErrInvalid)
	}
	if existing.GetFacility().GetKind() != incoming.GetFacility().GetKind() ||
		existing.GetTarget().GetEnvironment() != incoming.GetTarget().GetEnvironment() ||
		existing.GetTarget().GetRevision() != incoming.GetTarget().GetRevision() {
		return fmt.Errorf("%w: bindings install onto different targets", ErrInvalid)
	}
	if existing.GetDigest() != incoming.GetDigest() {
		return fmt.Errorf("%w: release %s/%s@%s is already installed on %s revision %s of %s with digest %s; incoming digest is %s",
			ErrConflict, existing.GetIdentity().GetModule(), existing.GetIdentity().GetName(),
			existing.GetIdentity().GetVersion(), existing.GetFacility().GetKind(), existing.GetTarget().GetRevision(),
			existing.GetTarget().GetEnvironment(), existing.GetDigest(), incoming.GetDigest())
	}
	return nil
}

// PrepareBinding clones binding, validates it against the verified package it
// installs, canonicalizes its sets and populates the digest.
func PrepareBinding(binding *basev0.RunnableBinding, pkg *basev0.RunnablePackage) (*basev0.RunnableBinding, error) {
	if binding == nil {
		return nil, fmt.Errorf("%w: binding is required", ErrInvalid)
	}
	if err := VerifyPackage(pkg); err != nil {
		return nil, err
	}
	prepared := &basev0.RunnableBinding{}
	proto.Merge(prepared, binding)
	supplied := prepared.GetDigest()
	prepared.Digest = ""
	if err := validateBinding(prepared, pkg); err != nil {
		return nil, err
	}
	canonicalizeBinding(prepared)
	digest, err := digestOf(BindingDigestFormatV1, prepared)
	if err != nil {
		return nil, err
	}
	if supplied != "" && supplied != digest {
		return nil, fmt.Errorf("%w: digest %s does not match binding bytes (%s)", ErrInvalid, supplied, digest)
	}
	prepared.Digest = digest
	return prepared, nil
}

// VerifyBinding checks that binding installs pkg and carries the digest of its
// own canonical bytes.
func VerifyBinding(binding *basev0.RunnableBinding, pkg *basev0.RunnablePackage) error {
	if binding.GetDigest() == "" {
		return fmt.Errorf("%w: binding carries no digest", ErrInvalid)
	}
	prepared, err := PrepareBinding(binding, pkg)
	if err != nil {
		return err
	}
	if !proto.Equal(prepared, binding) {
		return fmt.Errorf("%w: binding is not in canonical form", ErrInvalid)
	}
	return nil
}

func validatePackage(pkg *basev0.RunnablePackage) error {
	if pkg.GetSchema() != PackageSchemaV1 {
		return fmt.Errorf("%w: package schema %q is not supported; expected %s", ErrInvalid, pkg.GetSchema(), PackageSchemaV1)
	}
	if err := validator.Validate(pkg); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := semver.StrictNewVersion(pkg.GetIdentity().GetVersion()); err != nil {
		return fmt.Errorf("%w: version %q is not a strict semantic version", ErrInvalid, pkg.GetIdentity().GetVersion())
	}
	agent, err := resources.AgentFromProto(pkg.GetAgent())
	if err != nil {
		return fmt.Errorf("%w: agent: %v", ErrInvalid, err)
	}
	if !agent.IsRunnable() {
		return fmt.Errorf("%w: agent kind %s is not %s", ErrInvalid, agent.Kind, resources.RunnableAgent)
	}
	if agent.Version == "latest" {
		return fmt.Errorf("%w: agent version must be pinned, not latest", ErrInvalid)
	}
	if _, err = resources.RunnableContractFromProto(pkg.GetContract()); err != nil {
		return fmt.Errorf("%w: contract: %v", ErrInvalid, err)
	}
	facilities, err := validateExecution(pkg.GetExecution())
	if err != nil {
		return err
	}
	if err := validatePackageBuild(pkg); err != nil {
		return err
	}
	if err := validateDependencies(pkg.GetServiceDependencies()); err != nil {
		return err
	}
	if err := validateUniqueNames("workspace configuration dependency", pkg.GetWorkspaceConfigurationDependencies()); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(pkg.GetArtifacts()))
	for _, artifact := range pkg.GetArtifacts() {
		if err := validateArtifact(artifact); err != nil {
			return err
		}
		key := artifact.GetKind().String() + " " + artifact.GetPlatform()
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: artifact %s is declared twice", ErrInvalid, key)
		}
		seen[key] = struct{}{}
		if _, allowed := facilities[artifactFacility[artifact.GetKind()]]; !allowed {
			return fmt.Errorf("%w: %s artifact for %s needs facility %s, which the package does not declare",
				ErrInvalid, artifact.GetKind(), artifact.GetPlatform(), artifactFacility[artifact.GetKind()])
		}
	}
	if err := validateServiceOperations(pkg.GetServiceOperations(), facilities); err != nil {
		return err
	}
	if err := validateFunctions(pkg.GetFunctions(), facilities); err != nil {
		return err
	}
	if len(pkg.GetArtifacts())+len(pkg.GetServiceOperations())+len(pkg.GetFunctions()) == 0 {
		return fmt.Errorf("%w: package declares no implementation of its operation", ErrInvalid)
	}
	return nil
}

// validatePackageBuild ties the pinned build inputs to there being built bytes
// to pin. An owner handler is built by the owner's own service agent, so a
// release that ships one has no harness, toolchain or configuration digest of
// its own, and inventing them would assert a package nobody produced.
func validatePackageBuild(pkg *basev0.RunnablePackage) error {
	if len(pkg.GetArtifacts()) == 0 {
		if pkg.GetBuild() != nil {
			return fmt.Errorf("%w: package with no artifact pins build inputs it did not produce", ErrInvalid)
		}
		return nil
	}
	if pkg.GetBuild() == nil {
		return fmt.Errorf("%w: package with an artifact must pin the build inputs it was built from", ErrInvalid)
	}
	return validateBuild(pkg.GetBuild())
}

func validateServiceOperations(operations []*basev0.RunnableServiceOperation, facilities map[basev0.RunnableFacility_Kind]struct{}) error {
	seen := make(map[string]struct{}, len(operations))
	for _, operation := range operations {
		key := serviceOperationKey(operation)
		if operation.GetAdaptation() != basev0.RunnableServiceOperation_ADAPTATION_BOUNDED_JSON_V1 {
			return fmt.Errorf("%w: service operation %s adaptation %s is not supported: the bounded profile does not cover arbitrary protobuf, streaming or dynamic values, and core will not coerce them into it",
				ErrInvalid, key, operation.GetAdaptation())
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: service operation %s is declared twice", ErrInvalid, key)
		}
		seen[key] = struct{}{}
		if _, allowed := facilities[basev0.RunnableFacility_SERVICE]; !allowed {
			return fmt.Errorf("%w: service operation %s needs facility %s, which the package does not declare",
				ErrInvalid, key, basev0.RunnableFacility_SERVICE)
		}
	}
	return nil
}

func validateFunctions(functions []*basev0.RunnableFunction, facilities map[basev0.RunnableFacility_Kind]struct{}) error {
	seen := make(map[string]struct{}, len(functions))
	for _, function := range functions {
		key := functionKey(function)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: function %s is declared twice", ErrInvalid, key)
		}
		seen[key] = struct{}{}
		if _, allowed := facilities[basev0.RunnableFacility_FUNCTION]; !allowed {
			return fmt.Errorf("%w: function %s needs facility %s, which the package does not declare",
				ErrInvalid, key, basev0.RunnableFacility_FUNCTION)
		}
	}
	return nil
}

func serviceOperationKey(operation *basev0.RunnableServiceOperation) string {
	return strings.Join([]string{operation.GetModule(), operation.GetName(), operation.GetEndpoint(), operation.GetOperation()}, "/")
}

func functionKey(function *basev0.RunnableFunction) string {
	return function.GetProvider() + "/" + function.GetEntrypoint()
}

func validateExecution(execution *basev0.RunnableExecution) (map[basev0.RunnableFacility_Kind]struct{}, error) {
	facilities := make(map[basev0.RunnableFacility_Kind]struct{}, len(execution.GetFacilities()))
	for _, facility := range execution.GetFacilities() {
		kind := facility.GetKind()
		if _, known := basev0.RunnableFacility_Kind_name[int32(kind)]; !known || kind == basev0.RunnableFacility_UNKNOWN {
			return nil, fmt.Errorf("%w: execution facility %s is not supported", ErrInvalid, kind)
		}
		if _, exists := facilities[kind]; exists {
			return nil, fmt.Errorf("%w: execution facility %s is declared twice", ErrInvalid, kind)
		}
		facilities[kind] = struct{}{}
	}
	if err := execution.GetTimeout().CheckValid(); err != nil || execution.GetTimeout().AsDuration() <= 0 {
		return nil, fmt.Errorf("%w: execution timeout must be positive", ErrInvalid)
	}
	if execution.GetCancellation() == basev0.RunnableExecution_CANCELLATION_UNKNOWN {
		return nil, fmt.Errorf("%w: execution cancellation is required", ErrInvalid)
	}
	if execution.GetRecovery() == basev0.RunnableExecution_RECOVERY_UNKNOWN {
		return nil, fmt.Errorf("%w: execution recovery is required", ErrInvalid)
	}
	if execution.GetCancellation() == basev0.RunnableExecution_CANCELLATION_SIGNAL {
		for _, facility := range execution.GetFacilities() {
			if _, signalable := signalableFacilities[facility.GetKind()]; !signalable {
				return nil, fmt.Errorf("%w: facility %s cannot honor cancellation %s: nothing signals a method its owner runs inside its own process, or a function its provider runs",
					ErrInvalid, facility.GetKind(), execution.GetCancellation())
			}
		}
	}
	return facilities, nil
}

func validateDependencies(dependencies []*basev0.RunnableDependency) error {
	seen := make(map[string]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		unique := dependency.GetModule() + "/" + dependency.GetName()
		if err := resources.DependencyKind(dependency.GetKind()).Validate(); err != nil {
			return fmt.Errorf("%w: dependency %s: %v", ErrInvalid, unique, err)
		}
		if _, exists := seen[unique]; exists {
			return fmt.Errorf("%w: dependency %s is declared twice", ErrInvalid, unique)
		}
		seen[unique] = struct{}{}
		if err := validateUniqueNames("dependency "+unique+" endpoint", dependency.GetEndpoints()); err != nil {
			return err
		}
	}
	return nil
}

func validateUniqueNames(kind string, names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%w: %s cannot be empty", ErrInvalid, kind)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("%w: %s %q is declared twice", ErrInvalid, kind, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateBuild(build *basev0.RunnableBuild) error {
	if err := validateConfinedPath("handler", build.GetHandler().GetPath()); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(build.GetInputs()))
	for _, input := range build.GetInputs() {
		if err := validateConfinedPath("build input", input.GetPath()); err != nil {
			return err
		}
		if _, exists := seen[input.GetPath()]; exists {
			return fmt.Errorf("%w: build input %q is pinned twice", ErrInvalid, input.GetPath())
		}
		seen[input.GetPath()] = struct{}{}
	}
	return nil
}

// validateConfinedPath applies the declaration's path rule to a path carried
// on the wire, so a descriptor cannot pin content outside the runnable tree.
func validateConfinedPath(kind, p string) error {
	if !filepath.IsLocal(p) || strings.ContainsAny(p, "\x00\\") {
		return fmt.Errorf("%w: %s path %q must stay within the runnable directory", ErrInvalid, kind, p)
	}
	return nil
}

func validateArtifact(artifact *basev0.RunnableArtifact) error {
	os, arch, ok := strings.Cut(artifact.GetPlatform(), "/")
	if !ok || os == "" || arch == "" || strings.Contains(arch, "/") {
		return fmt.Errorf("%w: artifact platform %q must be os/arch", ErrInvalid, artifact.GetPlatform())
	}
	switch artifact.GetKind() {
	case basev0.RunnableArtifact_NATIVE:
		if len(artifact.GetCommand()) == 0 {
			return fmt.Errorf("%w: native artifact for %s declares no launch command", ErrInvalid, artifact.GetPlatform())
		}
	case basev0.RunnableArtifact_IMAGE:
		if len(artifact.GetCommand()) > 0 {
			return fmt.Errorf("%w: image artifact for %s must not declare a launch command; the image carries its entrypoint", ErrInvalid, artifact.GetPlatform())
		}
		if !strings.HasSuffix(artifact.GetReference(), "@"+artifact.GetDigest()) {
			return fmt.Errorf("%w: image reference %q is not pinned to its digest %s", ErrInvalid, artifact.GetReference(), artifact.GetDigest())
		}
	default:
		return fmt.Errorf("%w: artifact kind %s is not supported", ErrInvalid, artifact.GetKind())
	}
	return nil
}

func validateBinding(binding *basev0.RunnableBinding, pkg *basev0.RunnablePackage) error {
	if binding.GetSchema() != BindingSchemaV1 {
		return fmt.Errorf("%w: binding schema %q is not supported; expected %s", ErrInvalid, binding.GetSchema(), BindingSchemaV1)
	}
	if err := validator.Validate(binding); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if binding.GetPackageDigest() != pkg.GetDigest() {
		return fmt.Errorf("%w: binding installs package digest %s but was given package %s", ErrInvalid, binding.GetPackageDigest(), pkg.GetDigest())
	}
	if !proto.Equal(binding.GetIdentity(), pkg.GetIdentity()) {
		return fmt.Errorf("%w: binding identity does not match the package release identity", ErrInvalid)
	}
	facility := binding.GetFacility().GetKind()
	if !slices.ContainsFunc(pkg.GetExecution().GetFacilities(), func(f *basev0.RunnableFacility) bool { return f.GetKind() == facility }) {
		return fmt.Errorf("%w: package does not allow execution facility %s", ErrInvalid, facility)
	}
	if err := validateImplementation(binding, pkg); err != nil {
		return err
	}
	if err := validateTarget(binding); err != nil {
		return err
	}
	if err := validateBindingMappings(binding.GetDependencyNetworkMappings(), pkg.GetServiceDependencies()); err != nil {
		return err
	}
	if err := validateUniqueNames("credential reference", binding.GetCredentialReferences()); err != nil {
		return err
	}
	if err := validateUniqueNames("configuration reference", binding.GetConfigurationReferences()); err != nil {
		return err
	}
	required := slices.Sorted(slices.Values(pkg.GetWorkspaceConfigurationDependencies()))
	bound := slices.Sorted(slices.Values(binding.GetConfigurationReferences()))
	if !slices.Equal(required, bound) {
		return fmt.Errorf("%w: binding resolves configurations %v but the package declares %v", ErrInvalid, bound, required)
	}
	return nil
}

// validateImplementation checks that the installation selected one of the
// implementations the release actually declares, in the form its facility
// dispatches. Keeping the forms apart here is what stops an owner handler
// from being installed as though a native package had been built for it.
func validateImplementation(binding *basev0.RunnableBinding, pkg *basev0.RunnablePackage) error {
	facility := binding.GetFacility().GetKind()
	switch implementation := binding.GetImplementation().(type) {
	case *basev0.RunnableBinding_Artifact:
		if !slices.ContainsFunc(pkg.GetArtifacts(), func(a *basev0.RunnableArtifact) bool { return proto.Equal(a, implementation.Artifact) }) {
			return fmt.Errorf("%w: binding artifact is not one of the package artifacts", ErrInvalid)
		}
		if artifactFacility[implementation.Artifact.GetKind()] != facility {
			return fmt.Errorf("%w: %s artifact cannot execute on facility %s", ErrInvalid, implementation.Artifact.GetKind(), facility)
		}
	case *basev0.RunnableBinding_ServiceOperation:
		if facility != basev0.RunnableFacility_SERVICE {
			return fmt.Errorf("%w: a service operation cannot execute on facility %s", ErrInvalid, facility)
		}
		if !slices.ContainsFunc(pkg.GetServiceOperations(), func(o *basev0.RunnableServiceOperation) bool {
			return proto.Equal(o, implementation.ServiceOperation)
		}) {
			return fmt.Errorf("%w: binding service operation is not one of the package service operations", ErrInvalid)
		}
	case *basev0.RunnableBinding_Function:
		if facility != basev0.RunnableFacility_FUNCTION {
			return fmt.Errorf("%w: a remote function cannot execute on facility %s", ErrInvalid, facility)
		}
		if !slices.ContainsFunc(pkg.GetFunctions(), func(f *basev0.RunnableFunction) bool { return proto.Equal(f, implementation.Function) }) {
			return fmt.Errorf("%w: binding function is not one of the package functions", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: binding selects no implementation for facility %s", ErrInvalid, facility)
	}
	return nil
}

// validateTarget checks the coordinates an adapter dispatches to. The variant
// is what keeps the dispatch forms apart: a NATIVE binding names a launcher
// and the directory its artifact was unpacked into, and an owner handler
// running inside a service its owner already operates has neither.
func validateTarget(binding *basev0.RunnableBinding) error {
	target, facility := binding.GetTarget(), binding.GetFacility().GetKind()
	if target.GetSchema() != TargetSchemaV1 {
		return fmt.Errorf("%w: target schema %q is not supported; expected %s", ErrInvalid, target.GetSchema(), TargetSchemaV1)
	}
	switch coordinates := target.GetCoordinates().(type) {
	case *basev0.RunnableTarget_Host:
		if facility != basev0.RunnableFacility_NATIVE {
			return fmt.Errorf("%w: facility %s does not dispatch to a host target", ErrInvalid, facility)
		}
		installPath := coordinates.Host.GetInstallPath()
		if !strings.HasPrefix(installPath, "/") || strings.ContainsRune(installPath, 0) {
			return fmt.Errorf("%w: host install path %q must be absolute", ErrInvalid, installPath)
		}
	case *basev0.RunnableTarget_Cluster:
		if facility != basev0.RunnableFacility_KUBERNETES {
			return fmt.Errorf("%w: facility %s does not dispatch to a cluster target", ErrInvalid, facility)
		}
	case *basev0.RunnableTarget_Service:
		if facility != basev0.RunnableFacility_SERVICE {
			return fmt.Errorf("%w: facility %s does not dispatch to a service target", ErrInvalid, facility)
		}
		return validateServiceTarget(coordinates.Service, binding.GetServiceOperation())
	case *basev0.RunnableTarget_Function:
		if facility != basev0.RunnableFacility_FUNCTION {
			return fmt.Errorf("%w: facility %s does not dispatch to a function target", ErrInvalid, facility)
		}
		if coordinates.Function.GetProvider() != binding.GetFunction().GetProvider() {
			return fmt.Errorf("%w: function target is on provider %q but the selected implementation is for %q",
				ErrInvalid, coordinates.Function.GetProvider(), binding.GetFunction().GetProvider())
		}
	default:
		return fmt.Errorf("%w: binding carries no target coordinates for facility %s", ErrInvalid, facility)
	}
	return nil
}

func validateServiceTarget(target *basev0.RunnableServiceTarget, operation *basev0.RunnableServiceOperation) error {
	endpoint := target.GetEndpoint().GetEndpoint()
	if endpoint.GetModule() != operation.GetModule() || endpoint.GetService() != operation.GetName() || endpoint.GetName() != operation.GetEndpoint() {
		return fmt.Errorf("%w: service target addresses %s/%s/%s, not the %s/%s/%s endpoint the selected operation is published on",
			ErrInvalid, endpoint.GetModule(), endpoint.GetService(), endpoint.GetName(),
			operation.GetModule(), operation.GetName(), operation.GetEndpoint())
	}
	return validateNetworkInstances(networkMappingKey(target.GetEndpoint()), target.GetEndpoint().GetInstances())
}

func validateBindingMappings(mappings []*basev0.NetworkMapping, dependencies []*basev0.RunnableDependency) error {
	declared := make(map[string]*basev0.RunnableDependency, len(dependencies))
	for _, dependency := range dependencies {
		declared[dependency.GetModule()+"/"+dependency.GetName()] = dependency
	}
	seen := make(map[string]struct{}, len(mappings))
	bound := make(map[string]map[string]struct{}, len(dependencies))
	for _, mapping := range mappings {
		endpoint := mapping.GetEndpoint()
		if endpoint == nil {
			return fmt.Errorf("%w: binding network mapping requires an endpoint", ErrInvalid)
		}
		unique := endpoint.GetModule() + "/" + endpoint.GetService()
		dependency, ok := declared[unique]
		if !ok {
			return fmt.Errorf("%w: binding maps %s, which the package does not declare as a dependency", ErrInvalid, unique)
		}
		if len(dependency.GetEndpoints()) > 0 && !slices.Contains(dependency.GetEndpoints(), endpoint.GetName()) {
			return fmt.Errorf("%w: binding maps endpoint %s/%s, which dependency %s does not consume", ErrInvalid, unique, endpoint.GetName(), unique)
		}
		key := networkMappingKey(mapping)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: binding maps endpoint %s twice; put its instances in one mapping", ErrInvalid, key)
		}
		seen[key] = struct{}{}
		if err := validateNetworkInstances(key, mapping.GetInstances()); err != nil {
			return err
		}
		if bound[unique] == nil {
			bound[unique] = make(map[string]struct{})
		}
		bound[unique][endpoint.GetName()] = struct{}{}
	}
	for _, dependency := range dependencies {
		kind := resources.DependencyKind(dependency.GetKind())
		// Runtime edges always consume a reachable endpoint. Legacy edges may
		// instead name endpointless work, and external capabilities may be
		// configuration-only; require their explicitly consumed endpoints.
		needsMapping := kind == resources.DependencyKindRuntime ||
			((kind == resources.DependencyKindLegacy || kind == resources.DependencyKindExternal) && len(dependency.GetEndpoints()) > 0)
		if !needsMapping {
			continue
		}
		unique := dependency.GetModule() + "/" + dependency.GetName()
		if len(bound[unique]) == 0 {
			return fmt.Errorf("%w: binding does not resolve dependency %s", ErrInvalid, unique)
		}
		for _, endpoint := range dependency.GetEndpoints() {
			if _, exists := bound[unique][endpoint]; !exists {
				return fmt.Errorf("%w: binding does not resolve endpoint %s/%s", ErrInvalid, unique, endpoint)
			}
		}
	}
	return nil
}

func validateNetworkInstances(endpoint string, instances []*basev0.NetworkInstance) error {
	if len(instances) == 0 {
		return fmt.Errorf("%w: binding endpoint %s requires a network instance", ErrInvalid, endpoint)
	}
	type instanceKey struct{ access, address string }
	seen := make(map[instanceKey]struct{}, len(instances))
	for _, instance := range instances {
		if strings.TrimSpace(instance.GetAddress()) == "" {
			return fmt.Errorf("%w: binding endpoint %s requires a nonempty instance address", ErrInvalid, endpoint)
		}
		key := instanceKey{instance.GetAccess().GetKind(), instance.GetAddress()}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: binding endpoint %s repeats instance %s for access %q", ErrInvalid, endpoint, key.address, key.access)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func canonicalizePackage(pkg *basev0.RunnablePackage) {
	slices.SortFunc(pkg.Execution.Facilities, func(a, b *basev0.RunnableFacility) int {
		return int(a.GetKind()) - int(b.GetKind())
	})
	slices.Sort(pkg.WorkspaceConfigurationDependencies)
	if pkg.Build != nil {
		slices.SortFunc(pkg.Build.Inputs, func(a, b *basev0.RunnableInputDigest) int {
			return strings.Compare(a.GetPath(), b.GetPath())
		})
	}
	slices.SortFunc(pkg.ServiceOperations, func(a, b *basev0.RunnableServiceOperation) int {
		return strings.Compare(serviceOperationKey(a), serviceOperationKey(b))
	})
	slices.SortFunc(pkg.Functions, func(a, b *basev0.RunnableFunction) int {
		return strings.Compare(functionKey(a), functionKey(b))
	})
	slices.SortFunc(pkg.Artifacts, func(a, b *basev0.RunnableArtifact) int {
		if a.GetKind() != b.GetKind() {
			return int(a.GetKind()) - int(b.GetKind())
		}
		return strings.Compare(a.GetPlatform(), b.GetPlatform())
	})
	slices.SortFunc(pkg.ServiceDependencies, func(a, b *basev0.RunnableDependency) int {
		if a.GetModule() != b.GetModule() {
			return strings.Compare(a.GetModule(), b.GetModule())
		}
		return strings.Compare(a.GetName(), b.GetName())
	})
	for _, dependency := range pkg.ServiceDependencies {
		slices.Sort(dependency.Endpoints)
	}
}

func canonicalizeBinding(binding *basev0.RunnableBinding) {
	slices.Sort(binding.CredentialReferences)
	slices.Sort(binding.ConfigurationReferences)
	slices.SortFunc(binding.DependencyNetworkMappings, func(a, b *basev0.NetworkMapping) int {
		return strings.Compare(networkMappingKey(a), networkMappingKey(b))
	})
	for _, mapping := range binding.DependencyNetworkMappings {
		sortNetworkInstances(mapping)
	}
	sortNetworkInstances(binding.GetTarget().GetService().GetEndpoint())
}

func sortNetworkInstances(mapping *basev0.NetworkMapping) {
	if mapping == nil {
		return
	}
	slices.SortFunc(mapping.Instances, func(a, b *basev0.NetworkInstance) int {
		if a.GetAccess().GetKind() != b.GetAccess().GetKind() {
			return strings.Compare(a.GetAccess().GetKind(), b.GetAccess().GetKind())
		}
		return strings.Compare(a.GetAddress(), b.GetAddress())
	})
}

func networkMappingKey(mapping *basev0.NetworkMapping) string {
	endpoint := mapping.GetEndpoint()
	return strings.Join([]string{endpoint.GetModule(), endpoint.GetService(), endpoint.GetName(), endpoint.GetApi()}, "/")
}
