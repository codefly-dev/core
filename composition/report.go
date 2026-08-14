package composition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type ValidationStatus string

const (
	ValidationPassed ValidationStatus = "passed"
	ValidationFailed ValidationStatus = "failed"
)

type ValidationResult struct {
	Name   string           `json:"name"`
	Kind   string           `json:"kind"`
	Status ValidationStatus `json:"status"`
	Detail string           `json:"detail,omitempty"`
}

type ContractChange struct {
	Contract      string `json:"contract"`
	Before        string `json:"before,omitempty"`
	After         string `json:"after"`
	Compatibility string `json:"compatibility"`
}

type Delta struct {
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

type SemanticReport struct {
	Schema         string                  `json:"schema"`
	Module         string                  `json:"module"`
	Package        string                  `json:"package"`
	BeforeVersion  string                  `json:"beforeVersion,omitempty"`
	AfterVersion   string                  `json:"afterVersion"`
	Contracts      []ContractChange        `json:"contracts"`
	Services       Delta                   `json:"services"`
	Endpoints      Delta                   `json:"endpoints"`
	Dependencies   Delta                   `json:"dependencies"`
	Deltas         map[CollisionKind]Delta `json:"deltas"`
	Validations    []ValidationResult      `json:"validations"`
	BlockedReasons []string                `json:"blockedReasons,omitempty"`
	LockDiff       string                  `json:"lockDiff"`
}

func (report *SemanticReport) JSON() ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

func (report *SemanticReport) String() string {
	var output bytes.Buffer
	fmt.Fprintf(&output, "%s %s -> %s\n\n", report.Package, displayVersion(report.BeforeVersion), report.AfterVersion)
	output.WriteString("Compatibility\n")
	for _, change := range report.Contracts {
		fmt.Fprintf(&output, "  %-24s %-12s %s\n", change.Contract, displayVersion(change.Before)+" -> "+change.After, change.Compatibility)
	}
	output.WriteString("\nProduct projection\n")
	fmt.Fprintf(&output, "  %-24s +%d / -%d\n", "services", len(report.Services.Added), len(report.Services.Removed))
	fmt.Fprintf(&output, "  %-24s +%d / -%d\n", "endpoints", len(report.Endpoints.Added), len(report.Endpoints.Removed))
	for _, kind := range collisionKinds {
		delta := report.Deltas[kind]
		fmt.Fprintf(&output, "  %-24s +%d / -%d\n", kind, len(delta.Added), len(delta.Removed))
	}
	fmt.Fprintf(&output, "  %-24s +%d / -%d\n", "dependencies", len(report.Dependencies.Added), len(report.Dependencies.Removed))
	output.WriteString("\nValidation\n")
	for _, validation := range report.Validations {
		fmt.Fprintf(&output, "  %-24s %s\n", validation.Name, validation.Status)
	}
	if len(report.BlockedReasons) == 0 {
		output.WriteString("\nResult: ready\n")
	} else {
		output.WriteString("\nResult: blocked — " + strings.Join(report.BlockedReasons, "; ") + "\n")
	}
	return output.String()
}

func newSemanticReport(descriptor *Descriptor, before *Lock, after *Lock, beforeManifest, afterManifest *PackageManifest, oldClaims, newClaims []Claim, validations []ValidationResult) *SemanticReport {
	report := &SemanticReport{
		Schema: "codefly/module-update-report/v2", Module: descriptor.Name, Package: after.Package,
		AfterVersion: after.Version, Deltas: make(map[CollisionKind]Delta), Validations: validations,
	}
	if before != nil {
		report.BeforeVersion = before.Version
	}
	contractNames := make(map[string]struct{}, len(after.Contracts))
	for name := range after.Contracts {
		contractNames[name] = struct{}{}
	}
	if before != nil {
		for name := range before.Contracts {
			contractNames[name] = struct{}{}
		}
	}
	var orderedContracts []string
	for name := range contractNames {
		orderedContracts = append(orderedContracts, name)
	}
	sort.Strings(orderedContracts)
	for _, name := range orderedContracts {
		change := ContractChange{Contract: name, After: after.Contracts[name], Compatibility: "compatible"}
		if before != nil {
			change.Before = before.Contracts[name]
		}
		if change.After == "" || incompatibleMajor(change.Before, change.After) {
			change.Compatibility = "blocked"
			report.BlockedReasons = append(report.BlockedReasons, fmt.Sprintf("contract %s changed incompatibly", name))
		}
		report.Contracts = append(report.Contracts, change)
	}
	for _, kind := range collisionKinds {
		report.Deltas[kind] = claimsDelta(kind, oldClaims, newClaims)
	}
	report.Services = stringDelta(serviceNames(beforeManifest), serviceNames(afterManifest))
	report.Endpoints = stringDelta(endpointNames(beforeManifest), endpointNames(afterManifest))
	report.Dependencies = report.Deltas[CollisionPackage]
	return report
}

func serviceNames(manifest *PackageManifest) []string {
	if manifest == nil {
		return nil
	}
	services := make([]string, 0, len(manifest.Services))
	for _, service := range manifest.Services {
		services = append(services, service.Name)
	}
	return services
}

func endpointNames(manifest *PackageManifest) []string {
	if manifest == nil {
		return nil
	}
	var endpoints []string
	for _, service := range manifest.Services {
		for _, endpoint := range service.Endpoints {
			endpoints = append(endpoints, service.Name+"/"+endpoint)
		}
	}
	return endpoints
}

func stringDelta(before, after []string) Delta {
	beforeValues := make(map[string]struct{}, len(before))
	afterValues := make(map[string]struct{}, len(after))
	for _, value := range before {
		beforeValues[value] = struct{}{}
	}
	for _, value := range after {
		afterValues[value] = struct{}{}
	}
	var delta Delta
	for value := range afterValues {
		if _, exists := beforeValues[value]; !exists {
			delta.Added = append(delta.Added, value)
		}
	}
	for value := range beforeValues {
		if _, exists := afterValues[value]; !exists {
			delta.Removed = append(delta.Removed, value)
		}
	}
	sort.Strings(delta.Added)
	sort.Strings(delta.Removed)
	return delta
}

func claimsDelta(kind CollisionKind, before, after []Claim) Delta {
	beforeKeys := make(map[string]struct{})
	afterKeys := make(map[string]struct{})
	for _, claim := range before {
		if claim.Kind == kind {
			beforeKeys[claim.Key] = struct{}{}
		}
	}
	for _, claim := range after {
		if claim.Kind == kind {
			afterKeys[claim.Key] = struct{}{}
		}
	}
	var delta Delta
	for key := range afterKeys {
		if _, exists := beforeKeys[key]; !exists {
			delta.Added = append(delta.Added, key)
		}
	}
	for key := range beforeKeys {
		if _, exists := afterKeys[key]; !exists {
			delta.Removed = append(delta.Removed, key)
		}
	}
	sort.Strings(delta.Added)
	sort.Strings(delta.Removed)
	return delta
}

func incompatibleMajor(before, after string) bool {
	if before == "" || after == "" {
		return false
	}
	return strings.SplitN(before, ".", 2)[0] != strings.SplitN(after, ".", 2)[0]
}

func displayVersion(version string) string {
	if version == "" {
		return "none"
	}
	return version
}
