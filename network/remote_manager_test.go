package network

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
)

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
