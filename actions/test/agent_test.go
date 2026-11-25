package test

import basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"

func agentTest() *basev1.Agent {
	return &basev1.Agent{
		Kind:      basev1.Agent_SERVICE,
		Name:      "test-agent",
		Publisher: "codefly",
		Version:   "1.0.0",
	}
}
