package services

import (
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
)

func TestAuditResponseEnforcesFailOnVulnWithoutDroppingEvidence(t *testing.T) {
	wrapper := &BuilderWrapper{}
	finding := &builderv0.AuditFinding{Severity: builderv0.AuditFinding_HIGH, Id: "CVE-TEST"}
	response, err := wrapper.AuditResponse(&builderv0.AuditRequest{FailOnVuln: true}, []*builderv0.AuditFinding{finding}, nil, "scanner", "TEST")
	require.NoError(t, err)
	require.Equal(t, builderv0.AuditStatus_ERROR, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "HIGH")
	require.Equal(t, []*builderv0.AuditFinding{finding}, response.GetFindings())
}

func TestAuditResponseReportsFindingsWhenPolicyDisabled(t *testing.T) {
	wrapper := &BuilderWrapper{}
	response, err := wrapper.AuditResponse(nil, []*builderv0.AuditFinding{{Severity: builderv0.AuditFinding_CRITICAL}}, nil, "scanner", "TEST")
	require.NoError(t, err)
	require.Equal(t, builderv0.AuditStatus_FINDINGS, response.GetState().GetState())
}
