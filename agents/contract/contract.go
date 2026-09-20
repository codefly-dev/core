// Package contract checks the CLI-agent wire contract without comparing builds.
package contract

import (
	_ "embed"
	"errors"
	"fmt"
	"slices"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const ContainerRecoveryScope = "container-recovery-scope/v1"

const StartupProtocolVersion = 2

// ErrIncompatible means the peer's runtime advertisement cannot satisfy the
// requested protocol. It is distinct from an unreachable or failed process.
var ErrIncompatible = errors.New("incompatible agent contract")

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
		return fmt.Errorf("%w: agent does not declare a CLI-agent protocol version; host requires version %d", ErrIncompatible, current.ProtocolVersion)
	}
	if advertised.ProtocolVersion != current.ProtocolVersion {
		return fmt.Errorf("%w: agent declares CLI-agent protocol version %d; host requires version %d", ErrIncompatible, advertised.ProtocolVersion, current.ProtocolVersion)
	}
	if advertised.StartupProtocolVersion != StartupProtocolVersion {
		return fmt.Errorf("%w: agent declares startup protocol version %d; host requires version %d", ErrIncompatible, advertised.StartupProtocolVersion, StartupProtocolVersion)
	}
	for _, capability := range required {
		if !slices.Contains(advertised.Capabilities, capability) {
			return fmt.Errorf("%w: agent does not implement required capability %q (CLI-agent protocol version %d)", ErrIncompatible, capability, current.ProtocolVersion)
		}
	}
	return nil
}
