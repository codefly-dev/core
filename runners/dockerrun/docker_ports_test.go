package dockerrun

import (
	"context"
	"testing"
)

func TestDockerPortBindingsDefaultToLoopback(t *testing.T) {
	env := &DockerEnvironment{}
	env.WithPortMapping(context.Background(), 15432, 5432)

	bindings := env.portBindings()
	if len(bindings) != 1 {
		t.Fatalf("port bindings = %+v", bindings)
	}
	for _, byPort := range bindings {
		if len(byPort) != 1 || byPort[0].HostIP.String() != "127.0.0.1" {
			t.Fatalf("default port binding = %+v, want loopback", byPort)
		}
	}
}

func TestDockerPublicPortsRequireExplicitOptIn(t *testing.T) {
	env := &DockerEnvironment{}
	env.WithPortMapping(context.Background(), 15432, 5432)
	env.WithPublicPorts()

	bindings := env.portBindings()
	if len(bindings) != 1 {
		t.Fatalf("port bindings = %+v", bindings)
	}
	for _, byPort := range bindings {
		if len(byPort) != 1 || byPort[0].HostIP.String() != "0.0.0.0" {
			t.Fatalf("public port binding = %+v, want all interfaces", byPort)
		}
	}
}
