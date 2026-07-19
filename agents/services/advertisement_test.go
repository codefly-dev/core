package services

import (
	"testing"

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
