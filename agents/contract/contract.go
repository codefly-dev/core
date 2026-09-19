// Package contract checks the CLI-agent wire contract without comparing builds.
package contract

import (
	_ "embed"
	"fmt"
	"slices"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const ContainerRecoveryScope = "container-recovery-scope/v1"

//go:embed contract.json
var manifest []byte

var current = func() *agentv0.AgentContract {
	var value agentv0.AgentContract
	if err := protojson.Unmarshal(manifest, &value); err != nil {
		panic(err)
	}
	return &value
}()

func Current() *agentv0.AgentContract {
	return proto.Clone(current).(*agentv0.AgentContract)
}

func Check(advertised *agentv0.AgentContract, required ...string) error {
	if advertised.GetProtocolVersion() == 0 {
		return fmt.Errorf("agent does not declare a CLI-agent protocol version; host requires version %d", current.ProtocolVersion)
	}
	if advertised.ProtocolVersion != current.ProtocolVersion {
		return fmt.Errorf("agent declares CLI-agent protocol version %d; host requires version %d", advertised.ProtocolVersion, current.ProtocolVersion)
	}
	for _, capability := range required {
		if !slices.Contains(advertised.Capabilities, capability) {
			return fmt.Errorf("agent does not implement required capability %q (CLI-agent protocol version %d)", capability, current.ProtocolVersion)
		}
	}
	return nil
}
