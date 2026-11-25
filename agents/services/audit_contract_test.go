package services

import (
	"testing"

	builderv1 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v1"
	"github.com/stretchr/testify/require"
)

func TestAuditResponseEnforcesFailOnVulnWithoutDroppingEvidence(t *testing.T) {
	wrapper := &BuilderWrapper{}
	finding := &builderv1.AuditFinding{Severity: builderv1.AuditFinding_HIGH, Id: "CVE-TEST"}
	response, err := wrapper.AuditResponse(&builderv1.AuditRequest{FailOnVuln: true}, []*builderv1.AuditFinding{finding}, nil, "scanner", "TEST")
	require.NoError(t, err)
	require.Equal(t, builderv1.AuditStatus_ERROR, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "HIGH")
	require.Equal(t, []*builderv1.AuditFinding{finding}, response.GetFindings())
}

func TestAuditResponseReportsFindingsWhenPolicyDisabled(t *testing.T) {
	wrapper := &BuilderWrapper{}
	response, err := wrapper.AuditResponse(nil, []*builderv1.AuditFinding{{Severity: builderv1.AuditFinding_CRITICAL}}, nil, "scanner", "TEST")
	require.NoError(t, err)
	require.Equal(t, builderv1.AuditStatus_FINDINGS, response.GetState().GetState())
}
