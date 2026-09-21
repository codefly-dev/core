package composition

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"
	"syscall"

	"github.com/codefly-dev/core/artifactexecution"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"google.golang.org/protobuf/proto"
)

type ResolvedArtifactOperation struct {
	Target    string                 `json:"target"`
	Service   string                 `json:"service"`
	Operation string                 `json:"operation"`
	Protocol  string                 `json:"protocol"`
	Executor  Acquisition            `json:"executor"`
	Inputs    map[string]Acquisition `json:"inputs"`
	Outputs   map[string]string      `json:"outputs"`
}

func (resolved *ResolvedComposition) resolveArtifactOperations() error {
	for _, component := range resolved.record.Components {
		manifest := resolved.metadata[component.Target].manifest
		for _, service := range manifest.Services {
			if !slices.Contains(component.Services, service.Name) {
				continue
			}
			for _, declaration := range service.ArtifactOperations {
				build := slices.ContainsFunc(resolved.record.Builds, func(b BuildRequirement) bool { return b.Target == component.Target && b.Service == service.Name })
				if declaration.Operation == "build" && !build || declaration.Operation == "render" && component.LocalContent != "" {
					continue
				}
				operation := ResolvedArtifactOperation{Target: component.Target, Service: service.Name, Operation: declaration.Operation, Protocol: declaration.Protocol, Inputs: make(map[string]Acquisition), Outputs: maps.Clone(declaration.Outputs)}
				var err error
				operation.Executor, err = resolved.operationArtifact(component.Target, declaration.Executor, false)
				if err != nil {
					return err
				}
				if operation.Executor.Artifact.Purpose != ArtifactBuildAgent && operation.Executor.Artifact.Purpose != ArtifactLifecycleAgent {
					return errors.New("operation executor must be a selected agent artifact")
				}
				for name, reference := range declaration.Inputs {
					input, err := resolved.operationArtifact(component.Target, reference, declaration.Operation == "build")
					if err != nil {
						return err
					}
					if declaration.Operation == "build" && slices.ContainsFunc(resolved.record.Builds, func(b BuildRequirement) bool {
						return b.Target == input.Target && b.ReplacesArtifact == input.Artifact.Name
					}) {
						return errors.New("build input requires an unresolved derived output")
					}
					operation.Inputs[name] = input
				}
				if declaration.Operation == "build" {
					count := 0
					for _, requirement := range resolved.record.Builds {
						if requirement.Target != component.Target || requirement.Service != service.Name {
							continue
						}
						count++
						if operation.Executor.Target != requirement.AgentTarget {
							return errors.New("build executor differs from selected service agent")
						}
						found := false
						for _, input := range operation.Inputs {
							if input.Target == requirement.Target && input.Artifact.Purpose == ArtifactSource && input.Artifact.Digest == requirement.SourceIdentity {
								found = true
							}
						}
						artifactIndex := slices.IndexFunc(manifest.ReleaseArtifacts, func(a ReleaseArtifact) bool { return a.Name == requirement.ReplacesArtifact })
						if !found || artifactIndex < 0 || operation.Outputs[requirement.ReplacesArtifact] == "" || operation.Outputs[requirement.ReplacesArtifact] != manifest.ReleaseArtifacts[artifactIndex].MediaType {
							return errors.New("build operation must bind the selected source and every replaced runtime output media type")
						}
					}
					if count != len(operation.Outputs) {
						return errors.New("build operation declares unselected outputs")
					}
				} else {
					for _, name := range service.RuntimeArtifacts {
						if !slices.ContainsFunc(slices.Collect(maps.Values(operation.Inputs)), func(a Acquisition) bool { return a.Target == component.Target && a.Artifact.Name == name }) {
							return errors.New("render operation omits a selected service runtime artifact")
						}
					}
				}
				resolved.record.Operations = append(resolved.record.Operations, operation)
			}
		}
	}
	slices.SortFunc(resolved.record.Operations, func(a, b ResolvedArtifactOperation) int {
		return strings.Compare(a.Target+"\x00"+a.Service+"\x00"+a.Operation, b.Target+"\x00"+b.Service+"\x00"+b.Operation)
	})
	return nil
}

func (resolved *ResolvedComposition) operationArtifact(owner string, reference ArtifactReference, allowSource bool) (Acquisition, error) {
	target := owner
	if reference.Target != "" {
		if target != "" {
			target += "/"
		}
		target += reference.Target
	}
	metadata := resolved.metadata[target]
	if metadata == nil {
		return Acquisition{}, fmt.Errorf("operation refers to nonparticipating target %s", target)
	}
	index := slices.IndexFunc(metadata.manifest.ReleaseArtifacts, func(a ReleaseArtifact) bool { return a.Name == reference.Name })
	if index < 0 {
		return Acquisition{}, fmt.Errorf("%s: operation artifact %s is not published", target, reference.Name)
	}
	artifact := metadata.manifest.ReleaseArtifacts[index]
	if err := artifactexecution.ValidateMediaType(artifact.MediaType); err != nil {
		return Acquisition{}, err
	}
	if artifact.Purpose == ArtifactSource && !allowSource {
		return Acquisition{}, errors.New("render cannot acquire implementation source")
	}
	if artifact.Purpose == ArtifactSource && !slices.ContainsFunc(resolved.record.Builds, func(b BuildRequirement) bool { return b.Target == target }) {
		return Acquisition{}, errors.New("implementation source requires an explicitly selected build target")
	}
	selection := ReleaseSelection{ID: metadata.manifest.ID, Version: metadata.manifest.Version, Digest: metadata.provenance.ArtifactDigest}
	if local, exists := resolved.local[target]; exists {
		if artifact.Purpose != ArtifactSource || !allowSource {
			return Acquisition{}, errors.New("local checkout cannot substitute a released execution artifact")
		}
		for _, component := range resolved.record.Components {
			if component.Target == target {
				artifact.Digest = component.LocalContent
			}
		}
		artifact.URI = (&url.URL{Scheme: "file", Path: local}).String()
	} else if !slices.ContainsFunc(resolved.record.Builds, func(b BuildRequirement) bool { return b.Target == target && b.ReplacesArtifact == artifact.Name }) {
		if err := resolved.acquire(target, artifact.Name, artifact.Purpose, selection); err != nil {
			return Acquisition{}, err
		}
	}
	return Acquisition{Target: target, Owner: selection, Artifact: artifact}, nil
}

type PreparedArtifactExecution struct {
	request   *basev0.ArtifactExecution
	operation ResolvedArtifactOperation
}

func (execution *PreparedArtifactExecution) Request() *basev0.ArtifactExecution {
	return proto.Clone(execution.request).(*basev0.ArtifactExecution)
}

// PrepareArtifactExecution resolves only declared input slots. A missing mapping
// is an error, never a request to infer a renderer from a URI or artifact name.
func (engine *Engine) PrepareArtifactExecution(ctx context.Context, resolved *ResolvedComposition, target, service, operation string, inputs DeploymentInputs) (*PreparedArtifactExecution, error) {
	runtime, err := engine.prepareExecutionInputs(ctx, resolved, operation, inputs)
	if err != nil {
		return nil, err
	}
	return resolved.prepareArtifactExecution(target, service, operation, runtime)
}

// PrepareArtifactExecutions authenticates runtime streams once for the selected
// operation across all participating services. Each result still binds one instance.
func (engine *Engine) PrepareArtifactExecutions(ctx context.Context, resolved *ResolvedComposition, operation string, inputs DeploymentInputs) ([]*PreparedArtifactExecution, error) {
	runtime, err := engine.prepareExecutionInputs(ctx, resolved, operation, inputs)
	if err != nil {
		return nil, err
	}
	var executions []*PreparedArtifactExecution
	for _, component := range resolved.record.Components {
		for _, service := range component.Services {
			if operation == "build" && !slices.ContainsFunc(resolved.record.Builds, func(b BuildRequirement) bool { return b.Target == component.Target && b.Service == service }) {
				continue
			}
			prepared, err := resolved.prepareArtifactExecution(component.Target, service, operation, runtime)
			if err != nil {
				return nil, err
			}
			executions = append(executions, prepared)
		}
	}
	return executions, nil
}

func (engine *Engine) prepareExecutionInputs(ctx context.Context, resolved *ResolvedComposition, operation string, inputs DeploymentInputs) (*DeploymentRecord, error) {
	if operation != "build" && operation != "render" {
		return nil, errors.New("unsupported artifact operation")
	}
	if resolved == nil || resolved.identity == "" {
		return nil, errors.New("resolved composition is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := resolved.CheckLocalInputs(); err != nil {
		return nil, err
	}
	for _, component := range resolved.record.Components {
		if _, err := verifyMetadata(resolved.metadata[component.Target].attestation, component.Selected, engine.Trust); err != nil {
			return nil, err
		}
	}
	var runtime *DeploymentRecord
	if operation == "render" {
		inputs.Executions = nil
		var err error
		runtime, err = engine.CheckDeploymentInputs(ctx, resolved, inputs)
		if err != nil {
			return nil, err
		}
	}
	return runtime, nil
}

func (resolved *ResolvedComposition) prepareArtifactExecution(target, service, operation string, runtime *DeploymentRecord) (*PreparedArtifactExecution, error) {
	index := slices.IndexFunc(resolved.record.Operations, func(o ResolvedArtifactOperation) bool {
		return o.Target == target && o.Service == service && o.Operation == operation
	})
	if index < 0 {
		return nil, fmt.Errorf("%s/services/%s: explicit %s artifact operation is missing", target, service, operation)
	}
	declared := resolved.record.Operations[index]
	request := &basev0.ArtifactExecution{ContractVersion: artifactexecution.Contract, SelectionIdentity: resolved.identity, Target: target, Service: service, Protocol: declared.Protocol, ExecutorDigest: declared.Executor.Artifact.Digest, ConfigurationIdentity: resolved.record.ConfigurationIdentity, BindingIdentity: structuredIdentity(map[string]string{})}
	if runtime != nil {
		request.BindingIdentity = runtime.BindingIdentity
	}
	for name, acquisition := range declared.Inputs {
		artifact := acquisition.Artifact
		if artifact.Purpose == ArtifactRuntime && runtime != nil {
			index := slices.IndexFunc(runtime.Artifacts, func(a RuntimeArtifactIdentity) bool { return a.Target == acquisition.Target && a.Name == artifact.Name })
			if index < 0 {
				return nil, errors.New("render input is not a verified runtime artifact")
			}
			actual := runtime.Artifacts[index]
			artifact.Digest = actual.Digest
			if actual.Derived != nil {
				if err := validateArtifactURI(actual.Derived.URI); err != nil {
					return nil, fmt.Errorf("derived render input: %w", err)
				}
				artifact.URI = actual.Derived.URI
			}
		}
		request.Inputs = append(request.Inputs, &basev0.ArtifactExecutionInput{Name: name, Target: acquisition.Target, Artifact: artifact.Name, Uri: artifact.URI, MediaType: artifact.MediaType, Digest: artifact.Digest})
	}
	for name, media := range declared.Outputs {
		request.Outputs = append(request.Outputs, &basev0.ArtifactExecutionOutput{Name: name, MediaType: media})
	}
	prepared, err := artifactexecution.Prepare(request)
	if err != nil {
		return nil, err
	}
	return &PreparedArtifactExecution{request: prepared, operation: declared}, nil
}

type ExecutionOutputInput struct {
	Name    string
	Content io.Reader
}

type ArtifactExecutionRecord struct {
	Target    string                            `json:"target"`
	Service   string                            `json:"service"`
	Operation string                            `json:"operation"`
	Identity  string                            `json:"identity"`
	Outputs   []*basev0.ArtifactExecutionOutput `json:"outputs"`
}

type VerifiedArtifactExecution struct{ record ArtifactExecutionRecord }

// VerifyArtifactExecutionDirectory reads receipt paths confined to the caller's
// staging directory. Approval records the hashes; applying them remains the host's job.
func VerifyArtifactExecutionDirectory(ctx context.Context, prepared *PreparedArtifactExecution, receipt *basev0.ArtifactExecutionReceipt, directory string) (*VerifiedArtifactExecution, error) {
	if prepared == nil {
		return nil, errors.New("prepared artifact execution is required")
	}
	if err := artifactexecution.CheckReceipt(prepared.request, receipt); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	declared := make(map[string]bool, len(receipt.Outputs))
	for _, output := range receipt.Outputs {
		declared[output.Path] = true
	}
	if err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || !declared[path] {
			return fmt.Errorf("undeclared or nonregular execution output %s", path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for _, output := range receipt.Outputs {
		info, err := root.Stat(output.Path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("execution output must be a regular file")
		}
		file, err := openExecutionOutput(root, output.Path)
		if err != nil {
			return nil, err
		}
		err = verifyExecutionContent(ctx, file, output.Digest)
		closeErr := file.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return verifiedExecution(prepared, receipt), nil
}

func openExecutionOutput(root *os.Root, path string) (*os.File, error) {
	// The path can become a FIFO after inspection; do not wait for its writer.
	file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("execution output must be a regular file")
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func VerifyArtifactExecution(ctx context.Context, prepared *PreparedArtifactExecution, receipt *basev0.ArtifactExecutionReceipt, outputs []ExecutionOutputInput) (*VerifiedArtifactExecution, error) {
	if prepared == nil {
		return nil, errors.New("prepared artifact execution is required")
	}
	if err := artifactexecution.CheckReceipt(prepared.request, receipt); err != nil {
		return nil, err
	}
	if len(outputs) != len(receipt.Outputs) {
		return nil, errors.New("actual execution output bytes are required")
	}
	seen := make(map[string]bool)
	for _, output := range outputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		index := slices.IndexFunc(receipt.Outputs, func(o *basev0.ArtifactExecutionOutput) bool { return o.Name == output.Name })
		if index < 0 || seen[output.Name] || output.Content == nil {
			return nil, errors.New("unselected or duplicate execution output")
		}
		seen[output.Name] = true
		if err := verifyExecutionContent(ctx, output.Content, receipt.Outputs[index].Digest); err != nil {
			return nil, err
		}
	}
	return verifiedExecution(prepared, receipt), nil
}

func verifyExecutionContent(ctx context.Context, content io.Reader, digest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, content); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if fmt.Sprintf("sha256:%x", hash.Sum(nil)) != digest {
		return ErrDigestMismatch
	}
	return nil
}

func verifiedExecution(prepared *PreparedArtifactExecution, receipt *basev0.ArtifactExecutionReceipt) *VerifiedArtifactExecution {
	cloned := proto.Clone(receipt).(*basev0.ArtifactExecutionReceipt)
	slices.SortFunc(cloned.Outputs, func(a, b *basev0.ArtifactExecutionOutput) int { return strings.Compare(a.Name, b.Name) })
	return &VerifiedArtifactExecution{record: ArtifactExecutionRecord{Target: prepared.operation.Target, Service: prepared.operation.Service, Operation: prepared.operation.Operation, Identity: receipt.Identity, Outputs: cloned.Outputs}}
}

func (resolved *ResolvedComposition) checkExecutionOutputs(record *DeploymentRecord, executions []*VerifiedArtifactExecution) error {
	expected := make(map[string]bool)
	consumed := make(map[string]bool)
	for _, component := range resolved.record.Components {
		for _, service := range component.Services {
			prepared, err := resolved.prepareArtifactExecution(component.Target, service, "render", record)
			if err != nil {
				return err
			}
			expected[prepared.request.Identity] = true
			for _, input := range prepared.request.Inputs {
				consumed[input.Target+"\x00"+input.Artifact] = true
			}
		}
	}
	if len(expected) == 0 {
		return errors.New("deployment requires selected service render operations")
	}
	for _, artifact := range record.Artifacts {
		if !consumed[artifact.Target+"\x00"+artifact.Name] {
			return errors.New("runtime artifact is not bound to a selected render operation")
		}
	}
	if len(executions) != len(expected) {
		return errors.New("verified render outputs are required for every selected service")
	}
	for _, execution := range executions {
		if execution == nil || execution.record.Operation != "render" || !expected[execution.record.Identity] {
			return errors.New("render outputs belong to different selections, bindings or runtime artifacts")
		}
		delete(expected, execution.record.Identity)
		cloned := execution.record
		cloned.Outputs = make([]*basev0.ArtifactExecutionOutput, len(execution.record.Outputs))
		for i, output := range execution.record.Outputs {
			cloned.Outputs[i] = proto.Clone(output).(*basev0.ArtifactExecutionOutput)
		}
		record.Executions = append(record.Executions, cloned)
	}
	slices.SortFunc(record.Executions, func(a, b ArtifactExecutionRecord) int { return strings.Compare(a.Identity, b.Identity) })
	record.ExecutionIdentity = structuredIdentity(record.Executions)
	return nil
}
