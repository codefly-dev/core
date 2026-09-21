// Package contract checks the CLI-agent wire contract without comparing builds.
package contract

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"google.golang.org/protobuf/proto"
)

const ContainerRecoveryScope = "container-recovery-scope/v1"

const StartupProtocolVersion = 2

// ErrIncompatible means the peer's runtime advertisement cannot satisfy the
// requested protocol. It is distinct from an unreachable or failed process.
var ErrIncompatible = errors.New("incompatible agent contract")

//go:embed contract.json
var manifest []byte

type releaseManifest struct {
	ProtocolVersion        uint32   `json:"protocolVersion"`
	Capabilities           []string `json:"capabilities"`
	StartupProtocolVersion uint32   `json:"startupProtocolVersion"`
	OperationContracts     []string `json:"operationContracts"`
}

var release = func() releaseManifest {
	var value releaseManifest
	decoder := json.NewDecoder(bytes.NewReader(manifest))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		panic(err)
	}
	return value
}()

var current = &agentv0.AgentContract{ProtocolVersion: release.ProtocolVersion, Capabilities: release.Capabilities, StartupProtocolVersion: release.StartupProtocolVersion}

// SupportedOperationContracts reports host support, not executor adoption.
// Executors advertise only contracts they implement on their operation probes.
func SupportedOperationContracts() []string { return slices.Clone(release.OperationContracts) }

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
