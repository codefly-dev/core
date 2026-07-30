package test

import basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"

func agentTest() *basev0.Agent {
	return &basev0.Agent{
		Kind:      basev0.Agent_SERVICE,
		Name:      "test-agent",
		Publisher: "codefly",
	}
}
