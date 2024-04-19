package test

import basev0 "github.com/codefly-dev/core/generated/go/base/v0"

func agentTest() *basev0.Agent {
	return &basev0.Agent{
		Name:      "test-agent",
		Publisher: "codefly",
	}
}
