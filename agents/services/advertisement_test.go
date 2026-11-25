package services

import (
	"slices"
	"testing"

	"github.com/codefly-dev/core/agents/contract"
	usagecontract "github.com/codefly-dev/core/contracts/usage"
	agentv1 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v1"
)

func TestAdvertisementPreservesValidationContract(t *testing.T) {
	validation := &agentv1.ValidationCapabilities{
		Lint: &agentv1.ValidationOperationCapability{
			Supported: true,
			Scopes:    []agentv1.ValidationScope{agentv1.ValidationScope_VALIDATION_SCOPE_WORKSPACE},
		},
		Test: &agentv1.TestValidationCapability{
			Supported: true,
			Scopes:    []agentv1.ValidationScope{agentv1.ValidationScope_VALIDATION_SCOPE_WORKSPACE},
			Suites: []*agentv1.TestSuiteCapability{{
				Name:           "unit",
				DependencyMode: agentv1.TestDependencyMode_TEST_DEPENDENCY_MODE_NONE,
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
	if got := info.GetCapabilities()[0].GetType(); got != agentv1.Capability_EXECUTION_EXPORTER {
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

// An agent's usage contract crosses the advertisement byte for byte, and what
// arrives is what the one reader accepts: the advertisement neither parses nor
// rewrites it.
func TestAdvertisementCarriesTheUsageContractVerbatim(t *testing.T) {
	fixtures, err := usagecontract.Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	var document string
	for _, fixture := range fixtures {
		if fixture.Outcome == usagecontract.OutcomeAccepted {
			document = string(fixture.Document)
			break
		}
	}
	if document == "" {
		t.Fatal("the usage contract kit ships no accepted document")
	}
	carried := (Advertisement{UsageContract: document}).Build().GetUsageContract()
	if carried != document {
		t.Fatalf("the usage contract changed in the advertisement:\n%s", carried)
	}
	if _, err := usagecontract.Parse([]byte(carried)); err != nil {
		t.Fatalf("the carried usage contract does not parse: %v", err)
	}
}

func TestAdvertisementStatesNoUsageRulesByDefault(t *testing.T) {
	if carried := (Advertisement{}).Build().GetUsageContract(); carried != "" {
		t.Fatalf("an agent that declares no usage contract advertises %q", carried)
	}
}
