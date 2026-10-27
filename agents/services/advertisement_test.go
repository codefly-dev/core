package services

import (
	"slices"
	"testing"

	"github.com/codefly-dev/core/agents/contract"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

func TestAdvertisementPreservesValidationContract(t *testing.T) {
	validation := &agentv0.ValidationCapabilities{
		Lint: &agentv0.ValidationOperationCapability{
			Supported: true,
			Scopes:    []agentv0.ValidationScope{agentv0.ValidationScope_VALIDATION_SCOPE_WORKSPACE},
		},
		Test: &agentv0.TestValidationCapability{
			Supported: true,
			Scopes:    []agentv0.ValidationScope{agentv0.ValidationScope_VALIDATION_SCOPE_WORKSPACE},
			Suites: []*agentv0.TestSuiteCapability{{
				Name:           "unit",
				DependencyMode: agentv0.TestDependencyMode_TEST_DEPENDENCY_MODE_NONE,
				DefaultSuite:   true,
			}},
		},
	}

	info := (Advertisement{Validation: validation}).Build()
	if info.GetValidation() != validation {
		t.Fatal("Build did not preserve the advertised validation contract")
	}
}

func TestAdvertisementLeavesLegacyValidationAbsent(t *testing.T) {
	if validation := (Advertisement{}).Build().GetValidation(); validation != nil {
		t.Fatalf("legacy advertisement unexpectedly became authoritative: %v", validation)
	}
}

func TestAdvertisementSupportsExecutionExporterOnlyPlugin(t *testing.T) {
	info := (Advertisement{CapabilityOnly: true, ExecutionExporter: true}).Build()
	if len(info.GetCapabilities()) != 1 {
		t.Fatalf("capabilities = %v, want execution exporter only", info.GetCapabilities())
	}
	if got := info.GetCapabilities()[0].GetType(); got != agentv0.Capability_EXECUTION_EXPORTER {
		t.Fatalf("capability = %v, want EXECUTION_EXPORTER", got)
	}
}

func TestAdvertisementEffectiveInputs(t *testing.T) {
	if got := (Advertisement{}).Build().GetEffectiveInputsVersions(); len(got) != 0 {
		t.Fatal("legacy agent advertised input discovery")
	}
	info := (Advertisement{EffectiveInputsVersions: []uint32{1}}).Build()
	if got := info.GetEffectiveInputsVersions(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("discovery versions: %v", got)
	}
}

func TestAdvertisementOwnsItsBackendDeclarationOnlyWhenAsked(t *testing.T) {
	if info := (Advertisement{}).Build(); info.GetContract() != nil {
		t.Fatalf("a plugin that did not claim a complete declaration advertised a contract: %v", info.GetContract())
	}
	info := (Advertisement{BackendsComplete: true}).Build()
	if got := info.GetContract().GetCapabilities(); !slices.Contains(got, contract.RuntimeBackendDeclaration) {
		t.Fatalf("capabilities = %v, want %s", got, contract.RuntimeBackendDeclaration)
	}
	if got := info.GetContract().GetProtocolVersion(); got != contract.Current().GetProtocolVersion() {
		t.Fatalf("protocol version = %d, want the shared contract's", got)
	}
	if slices.Contains(contract.Current().GetCapabilities(), contract.RuntimeBackendDeclaration) {
		t.Fatal("the shared contract promised a complete backend declaration for every plugin built on this Core")
	}
}
