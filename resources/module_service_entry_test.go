package resources

import (
	"context"
	"testing"
)

func TestModuleServiceEntryRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	module := &Module{
		Kind:         ModuleKind,
		Name:         "starter",
		ServiceEntry: "frontend",
		ServiceReferences: []*ServiceReference{
			{Name: "accounts"},
			{Name: "frontend"},
		},
	}
	module.WithDir(dir)
	if err := module.Save(ctx); err != nil {
		t.Fatalf("save module: %v", err)
	}

	loaded, err := LoadModuleFromDir(ctx, dir)
	if err != nil {
		t.Fatalf("load module: %v", err)
	}
	if loaded.ServiceEntry != "frontend" {
		t.Fatalf("service entry = %q, want frontend", loaded.ServiceEntry)
	}
	proto, err := loaded.Proto(ctx)
	if err != nil {
		t.Fatalf("module proto: %v", err)
	}
	if proto.GetServiceEntry() != "frontend" {
		t.Fatalf("proto service entry = %q, want frontend", proto.GetServiceEntry())
	}
}

func TestModuleServiceEntryMustReferenceDeclaredService(t *testing.T) {
	module := &Module{
		Kind:         ModuleKind,
		Name:         "starter",
		ServiceEntry: "missing",
		ServiceReferences: []*ServiceReference{
			{Name: "frontend"},
		},
	}
	module.WithDir(t.TempDir())
	if err := module.Save(context.Background()); err == nil {
		t.Fatal("module accepted a service entry that is not declared")
	}
}
