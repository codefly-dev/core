package network

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
)

type remoteDNSManager struct {
	dns *basev0.DNS
}

func (manager remoteDNSManager) GetDNS(context.Context, *resources.ServiceIdentity, string) (*basev0.DNS, error) {
	return manager.dns, nil
}

func TestRemoteManagerUsesDeclaredEnvironmentNamespace(t *testing.T) {
	manager := &RemoteManager{}
	environment := &resources.Environment{Name: "production", Namespace: "platform"}
	service := &resources.ServiceIdentity{Module: "users", Name: "accounts"}

	for _, layout := range []resources.LayoutKind{resources.LayoutKindModules, resources.LayoutKindFlat} {
		workspace := &resources.Workspace{Name: "mind", Layout: layout}
		namespace, err := manager.GetNamespace(context.Background(), environment, workspace, service)
		if err != nil {
			t.Fatal(err)
		}
		if namespace != "platform" {
			t.Fatalf("layout %s namespace = %q, want platform", layout, namespace)
		}
	}
}

func TestRemoteManagerSynthesizesLegacyNamespaceWhenUndeclared(t *testing.T) {
	manager := &RemoteManager{}
	environment := &resources.Environment{Name: "local"}
	service := &resources.ServiceIdentity{Module: "users", Name: "accounts"}

	tests := []struct {
		layout resources.LayoutKind
		want   string
	}{
		{layout: resources.LayoutKindModules, want: "mind-users-local"},
		{layout: resources.LayoutKindFlat, want: "mind-local"},
	}
	for _, test := range tests {
		workspace := &resources.Workspace{Name: "mind", Layout: test.layout}
		namespace, err := manager.GetNamespace(context.Background(), environment, workspace, service)
		if err != nil {
			t.Fatal(err)
		}
		if namespace != test.want {
			t.Fatalf("layout %s namespace = %q, want %q", test.layout, namespace, test.want)
		}
	}
}

func TestRemoteManagerUsesDeclaredInternalDNSForContainerAccess(t *testing.T) {
	manager, err := NewRemoteManager(context.Background(), remoteDNSManager{
		dns: &basev0.DNS{
			Host:    "store.platform.svc.cluster.local",
			Port:    5432,
			Secured: false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	environment := &resources.Environment{Name: "aws", Namespace: "platform"}
	workspace := &resources.Workspace{Name: "mind", Layout: resources.LayoutKindModules}
	service := &resources.ServiceIdentity{Module: "users", Name: "store"}
	endpoint := &basev0.Endpoint{
		Module:  "users",
		Service: "store",
		Name:    "tcp",
		Api:     standards.TCP,
	}

	mappings, err := manager.GenerateNetworkMappings(context.Background(), environment, workspace, service, []*basev0.Endpoint{endpoint})
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 1 || len(mappings[0].Instances) != 2 {
		t.Fatalf("mappings = %#v, want one mapping with public and container instances", mappings)
	}
	for _, access := range []*basev0.NetworkAccess{
		resources.NewPublicNetworkAccess(),
		resources.NewContainerNetworkAccess(),
	} {
		instance := resources.FilterNetworkInstance(context.Background(), mappings[0].Instances, access)
		if instance == nil {
			t.Fatalf("missing %q access instance", access.GetKind())
		}
		if instance.GetHostname() != "store.platform.svc.cluster.local" || instance.GetPort() != 5432 {
			t.Fatalf("%s instance = %s:%d, want store.platform.svc.cluster.local:5432", access.GetKind(), instance.GetHostname(), instance.GetPort())
		}
	}
}

func TestRemoteManagerSynthesizesInternalDNSWhenUndeclared(t *testing.T) {
	manager, err := NewRemoteManager(context.Background(), remoteDNSManager{})
	if err != nil {
		t.Fatal(err)
	}
	environment := &resources.Environment{Name: "aws", Namespace: "platform"}
	workspace := &resources.Workspace{Name: "mind", Layout: resources.LayoutKindModules}
	service := &resources.ServiceIdentity{Module: "users", Name: "accounts"}
	endpoint := &basev0.Endpoint{
		Module:  "users",
		Service: "accounts",
		Name:    "grpc",
		Api:     standards.GRPC,
	}

	mappings, err := manager.GenerateNetworkMappings(context.Background(), environment, workspace, service, []*basev0.Endpoint{endpoint})
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 1 || len(mappings[0].Instances) != 1 {
		t.Fatalf("mappings = %#v, want one mapping with one instance", mappings)
	}
	instance := mappings[0].Instances[0]
	if instance.GetAccess().GetKind() != resources.NetworkAccessContainer {
		t.Fatalf("access = %q, want %q", instance.GetAccess().GetKind(), resources.NetworkAccessContainer)
	}
	if instance.GetHostname() != "accounts.platform.svc.cluster.local" || instance.GetPort() != uint32(standards.Port(standards.GRPC)) {
		t.Fatalf("instance = %s:%d, want accounts.platform.svc.cluster.local:%d", instance.GetHostname(), instance.GetPort(), standards.Port(standards.GRPC))
	}
}
