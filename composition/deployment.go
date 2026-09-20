package composition

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"time"
)

type RuntimeInput struct {
	Target  string
	Name    string
	Content io.Reader
}

type DerivedOutput struct {
	Schema            string `json:"schema"`
	SelectionIdentity string `json:"selectionIdentity"`
	Target            string `json:"target"`
	Artifact          string `json:"artifact"`
	SourceIdentity    string `json:"sourceIdentity"`
	Digest            string `json:"digest"`
	Signer            string `json:"signer"`
}

type SignedDerivedOutput struct {
	Statement []byte
	Signature []byte
}

type Qualification struct {
	Schema            string    `json:"schema"`
	SelectionIdentity string    `json:"selectionIdentity"`
	RuntimeIdentity   string    `json:"runtimeIdentity"`
	BindingIdentity   string    `json:"bindingIdentity"`
	Kind              string    `json:"kind"`
	Signer            string    `json:"signer"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type SignedQualification struct {
	Statement []byte
	Signature []byte
}

type DeploymentPolicy struct {
	RequiredQualifications []string
	QualificationSigners   map[string]map[string]ed25519.PublicKey
}

type DeploymentInputs struct {
	Runtime        []RuntimeInput
	Derived        []SignedDerivedOutput
	Bindings       map[string]string
	Qualifications []SignedQualification
}

type RuntimeArtifactIdentity struct {
	Target  string         `json:"target"`
	Name    string         `json:"name"`
	Digest  string         `json:"digest"`
	Derived *DerivedOutput `json:"derived,omitempty"`
}

type DeploymentRecord struct {
	SelectionIdentity string                    `json:"selectionIdentity"`
	RuntimeIdentity   string                    `json:"runtimeIdentity"`
	BindingIdentity   string                    `json:"bindingIdentity"`
	Artifacts         []RuntimeArtifactIdentity `json:"artifacts"`
	Bindings          map[string]string         `json:"bindings"`
	ApprovedAt        time.Time                 `json:"approvedAt,omitempty"`
	ValidUntil        time.Time                 `json:"validUntil,omitempty"`
	Qualifications    []SignedQualification     `json:"qualifications,omitempty"`
}

type ApprovedDeployment struct {
	record   DeploymentRecord
	identity string
}

func (approved *ApprovedDeployment) Identity() string { return approved.identity }

func (approved *ApprovedDeployment) Record() DeploymentRecord {
	data, _ := json.Marshal(approved.record)
	var record DeploymentRecord
	_ = json.Unmarshal(data, &record)
	return record
}

// CheckDeploymentInputs authenticates owner-released or authorized derived
// output bytes. It does not grant rollout approval or claim anything is running.
func (engine *Engine) CheckDeploymentInputs(ctx context.Context, resolved *ResolvedComposition, inputs DeploymentInputs) (*DeploymentRecord, error) {
	if resolved == nil || resolved.identity == "" {
		return nil, errors.New("resolved composition is required")
	}
	if len(resolved.local) != 0 {
		return nil, errors.New("local development substitutions cannot be deployed, regardless of Git state")
	}
	for _, component := range resolved.record.Components {
		if _, err := verifyMetadata(resolved.metadata[component.Target].attestation, component.Selected, engine.Trust); err != nil {
			return nil, fmt.Errorf("%s: recheck release authority: %w", component.Target, err)
		}
	}
	expected := make(map[string]RuntimeArtifactIdentity)
	key := func(target, name string) string { return target + "\x00" + name }
	for _, acquisition := range resolved.record.Acquisitions {
		if acquisition.Artifact.Purpose == ArtifactRuntime {
			expected[key(acquisition.Target, acquisition.Artifact.Name)] = RuntimeArtifactIdentity{Target: acquisition.Target, Name: acquisition.Artifact.Name, Digest: acquisition.Artifact.Digest}
		}
	}
	derived := make(map[string]DerivedOutput)
	for _, signed := range inputs.Derived {
		var statement DerivedOutput
		if err := decodeStrictJSON(signed.Statement, &statement); err != nil {
			return nil, err
		}
		index := slices.IndexFunc(resolved.record.Builds, func(build BuildRequirement) bool {
			return build.Target == statement.Target && build.ReplacesArtifact == statement.Artifact
		})
		if index < 0 {
			return nil, errors.New("derived output has no selected build requirement")
		}
		build := resolved.record.Builds[index]
		metadata := resolved.metadata[build.Target]
		if !metadata.manifest.AllowDerivedBuilds {
			return nil, errors.New("component owner does not allow derived builds")
		}
		publicKey := engine.Trust.BuildSigners[metadata.manifest.ID][statement.Signer]
		if len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(publicKey, signed.Statement, signed.Signature) {
			return nil, ErrSignature
		}
		if statement.Schema != "codefly/derived-output/v1" || statement.SelectionIdentity != resolved.identity || statement.SourceIdentity != build.SourceIdentity || !digestPattern.MatchString(statement.Digest) {
			return nil, errors.New("derived output does not attest the exact selected owner source and build inputs")
		}
		identity := key(statement.Target, statement.Artifact)
		if _, exists := derived[identity]; exists {
			return nil, errors.New("duplicate derived output")
		}
		derived[identity] = statement
		expected[identity] = RuntimeArtifactIdentity{Target: statement.Target, Name: statement.Artifact, Digest: statement.Digest, Derived: &statement}
	}
	for _, build := range resolved.record.Builds {
		if _, exists := derived[key(build.Target, build.ReplacesArtifact)]; !exists {
			return nil, fmt.Errorf("%s: authorized derived output for %s is missing", build.Target, build.ReplacesArtifact)
		}
	}
	record := &DeploymentRecord{SelectionIdentity: resolved.identity, Bindings: maps.Clone(inputs.Bindings)}
	seen := make(map[string]bool)
	for _, input := range inputs.Runtime {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		identity := key(input.Target, input.Name)
		artifact, exists := expected[identity]
		if !exists || seen[identity] {
			return nil, fmt.Errorf("unselected or duplicate runtime artifact %s/%s", input.Target, input.Name)
		}
		seen[identity] = true
		if input.Content == nil {
			return nil, errors.New("actual runtime content is required")
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, input.Content); err != nil {
			return nil, fmt.Errorf("read runtime artifact: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if fmt.Sprintf("sha256:%x", hash.Sum(nil)) != artifact.Digest {
			return nil, fmt.Errorf("%w: runtime artifact %s/%s is not the selected owner output", ErrDigestMismatch, input.Target, input.Name)
		}
		record.Artifacts = append(record.Artifacts, artifact)
	}
	if len(seen) != len(expected) {
		return nil, errors.New("required runtime artifacts are missing")
	}
	if len(record.Artifacts) == 0 {
		return nil, errors.New("deployment contains no runtime artifacts")
	}
	for name, identity := range inputs.Bindings {
		if err := validateIdentifier("deployment binding", name); err != nil {
			return nil, err
		}
		if !digestPattern.MatchString(identity) {
			return nil, fmt.Errorf("binding %s requires a non-secret target identity digest", name)
		}
	}
	if len(inputs.Bindings) == 0 {
		return nil, errors.New("deployment target bindings are required")
	}
	sort.Slice(record.Artifacts, func(i, j int) bool {
		left, right := record.Artifacts[i], record.Artifacts[j]
		if left.Target != right.Target {
			return left.Target < right.Target
		}
		return left.Name < right.Name
	})
	record.RuntimeIdentity = structuredIdentity(record.Artifacts)
	record.BindingIdentity = structuredIdentity(record.Bindings)
	return record, nil
}

func (engine *Engine) AdmitDeployment(ctx context.Context, resolved *ResolvedComposition, inputs DeploymentInputs, policy DeploymentPolicy, now time.Time) (*ApprovedDeployment, error) {
	record, err := engine.CheckDeploymentInputs(ctx, resolved, inputs)
	if err != nil {
		return nil, err
	}
	if now.IsZero() {
		return nil, errors.New("deployment admission time is required")
	}
	if len(policy.RequiredQualifications) == 0 {
		return nil, errors.New("deployment qualification policy is required")
	}
	if err := uniqueStrings("required qualification", policy.RequiredQualifications); err != nil {
		return nil, err
	}
	qualified := make(map[string]bool)
	for _, signed := range inputs.Qualifications {
		var statement Qualification
		if err := decodeStrictJSON(signed.Statement, &statement); err != nil {
			return nil, err
		}
		publicKey := policy.QualificationSigners[statement.Kind][statement.Signer]
		if len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(publicKey, signed.Statement, signed.Signature) {
			return nil, ErrSignature
		}
		if statement.Schema != "codefly/deployment-qualification/v1" || statement.SelectionIdentity != record.SelectionIdentity || statement.RuntimeIdentity != record.RuntimeIdentity || statement.BindingIdentity != record.BindingIdentity || !statement.ExpiresAt.After(now) {
			return nil, errors.New("deployment qualification is expired or belongs to different inputs or target bindings")
		}
		if qualified[statement.Kind] {
			return nil, errors.New("duplicate deployment qualification")
		}
		qualified[statement.Kind] = true
		if record.ValidUntil.IsZero() || statement.ExpiresAt.Before(record.ValidUntil) {
			record.ValidUntil = statement.ExpiresAt
		}
		record.Qualifications = append(record.Qualifications, SignedQualification{Statement: slices.Clone(signed.Statement), Signature: slices.Clone(signed.Signature)})
	}
	for _, kind := range policy.RequiredQualifications {
		if !qualified[kind] {
			return nil, fmt.Errorf("required %s qualification is missing", kind)
		}
	}
	record.ApprovedAt = now
	sort.Slice(record.Qualifications, func(i, j int) bool {
		return string(record.Qualifications[i].Statement) < string(record.Qualifications[j].Statement)
	})
	return &ApprovedDeployment{record: *record, identity: structuredIdentity(record)}, nil
}

func structuredIdentity(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}
