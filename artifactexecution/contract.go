// Package artifactexecution validates selection-bound executor requests and receipts.
package artifactexecution

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"google.golang.org/protobuf/proto"
)

const (
	Contract       = "artifact-execution/v1"
	BuilderBuild   = "codefly.builder.build/v1"
	BuilderRender  = "codefly.builder.deploy/v1"
	SolutionRender = "codefly.solution.render/v1"
)

var digest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var slot = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func ValidateMediaType(value string) error {
	media, parameters, err := mime.ParseMediaType(value)
	if err != nil || len(parameters) != 0 || media != value || strings.Count(media, "/") != 1 || strings.Contains(media, "*") {
		return fmt.Errorf("explicit canonical media type required: %q", value)
	}
	return nil
}

func Prepare(request *basev0.ArtifactExecution) (*basev0.ArtifactExecution, error) {
	if request == nil {
		return nil, errors.New("artifact execution is required")
	}
	value := proto.Clone(request).(*basev0.ArtifactExecution)
	if len(value.ProtoReflect().GetUnknown()) != 0 || !validTarget(value.Target) {
		return nil, errors.New("unsupported execution fields or invalid instance target")
	}
	if value.ContractVersion != Contract || !digest.MatchString(value.SelectionIdentity) || !digest.MatchString(value.ExecutorDigest) || !digest.MatchString(value.BindingIdentity) || !slot.MatchString(value.Service) {
		return nil, errors.New("artifact execution requires supported contract, selection, executor, bindings and service")
	}
	if len(value.ConfigurationIdentity) != len("hmac-")+len("sha256:")+64 || !digest.MatchString(value.ConfigurationIdentity[len("hmac-"):]) || value.ConfigurationIdentity[:len("hmac-")] != "hmac-" {
		return nil, errors.New("protected configuration identity is required")
	}
	if !slices.Contains([]string{BuilderBuild, BuilderRender, SolutionRender}, value.Protocol) {
		return nil, fmt.Errorf("unsupported artifact execution protocol %q", value.Protocol)
	}
	if len(value.Inputs) == 0 || len(value.Outputs) == 0 {
		return nil, errors.New("artifact execution requires explicit inputs and outputs")
	}
	seen := make(map[string]bool)
	for _, input := range value.Inputs {
		if input == nil || !slot.MatchString(input.Name) || seen[input.Name] || !slot.MatchString(input.Artifact) || !validTarget(input.Target) || !digest.MatchString(input.Digest) || len(input.ProtoReflect().GetUnknown()) != 0 {
			return nil, errors.New("artifact execution has invalid or duplicate input bindings")
		}
		location, err := url.Parse(input.Uri)
		if err != nil || location.User != nil || location.RawQuery != "" || location.ForceQuery || location.Fragment != "" || !((location.Scheme == "https" || location.Scheme == "oci") && location.Host != "" || value.Protocol == BuilderBuild && location.Scheme == "file" && location.Host == "" && strings.HasPrefix(location.Path, "/")) {
			return nil, errors.New("invalid artifact input URI")
		}
		if err := ValidateMediaType(input.MediaType); err != nil {
			return nil, err
		}
		seen[input.Name] = true
	}
	seen = make(map[string]bool)
	for _, output := range value.Outputs {
		if output == nil || !slot.MatchString(output.Name) || seen[output.Name] || output.Digest != "" || output.Path != "" || len(output.ProtoReflect().GetUnknown()) != 0 {
			return nil, errors.New("artifact execution has invalid or duplicate output declarations")
		}
		if err := ValidateMediaType(output.MediaType); err != nil {
			return nil, err
		}
		seen[output.Name] = true
	}
	slices.SortFunc(value.Inputs, func(a, b *basev0.ArtifactExecutionInput) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(value.Outputs, func(a, b *basev0.ArtifactExecutionOutput) int { return strings.Compare(a.Name, b.Name) })
	supplied := value.Identity
	value.Identity = ""
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(value)
	if err != nil {
		return nil, err
	}
	value.Identity = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	if supplied != "" && supplied != value.Identity {
		return nil, errors.New("artifact execution identity does not match its inputs")
	}
	return value, nil
}

func validTarget(value string) bool {
	if value == "" {
		return true
	}
	for _, part := range strings.Split(value, "/") {
		if !slot.MatchString(part) || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func Check(request *basev0.ArtifactExecution, protocol, executorDigest string, advertised []string) error {
	if request == nil {
		return nil
	}
	if _, err := Prepare(request); err != nil {
		return err
	}
	if request.Identity == "" || request.Protocol != protocol || request.ExecutorDigest != executorDigest {
		return errors.New("artifact execution does not bind this protocol and executor")
	}
	if !slices.Contains(advertised, Contract) {
		return errors.New("executor does not advertise artifact-execution/v1")
	}
	return nil
}

func CheckReceipt(request *basev0.ArtifactExecution, receipt *basev0.ArtifactExecutionReceipt) error {
	if request == nil {
		return nil
	}
	if _, err := Prepare(request); err != nil {
		return err
	}
	if receipt.GetIdentity() != request.Identity || request.Identity == "" || len(receipt.GetOutputs()) != len(request.Outputs) {
		return errors.New("executor did not acknowledge the selected inputs and complete output set")
	}
	if len(receipt.ProtoReflect().GetUnknown()) != 0 {
		return errors.New("unsupported execution receipt fields")
	}
	seen := make(map[string]bool)
	paths := make(map[string]bool)
	for _, output := range receipt.Outputs {
		if output == nil || seen[output.Name] || !digest.MatchString(output.Digest) || len(output.ProtoReflect().GetUnknown()) != 0 {
			return errors.New("invalid execution output receipt")
		}
		if output.Path == "" || output.Path == "." || path.Clean(output.Path) != output.Path || path.IsAbs(output.Path) || strings.HasPrefix(output.Path, "../") || strings.ContainsAny(output.Path, "\\\x00:") || paths[output.Path] {
			return errors.New("execution outputs require unique relative file paths")
		}
		paths[output.Path] = true
		index := slices.IndexFunc(request.Outputs, func(expected *basev0.ArtifactExecutionOutput) bool {
			return expected.Name == output.Name && expected.MediaType == output.MediaType
		})
		if index < 0 {
			return errors.New("execution output was not declared")
		}
		seen[output.Name] = true
	}
	return nil
}
