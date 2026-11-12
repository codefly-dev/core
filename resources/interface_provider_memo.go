package resources

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"maps"
	"os"
	"sync"
)

// providerMemo is what InterfaceProviders last computed for a workspace and
// what it was computed from. Every load of a service with an interface
// dependency asks for the providers, and answering loads every module of the
// composition from disk, so one command that loads services from several
// places would otherwise reload the whole composition each time.
//
// An entry is served only while everything it was computed from is unchanged,
// and that is checked on every call rather than cleared by the paths that
// mutate: the composition that locates the modules (the module references by
// value and in order, the layout, the directory and the local overlay), the
// directory each reference resolves to, and every declaration file the load
// read — recorded by the load itself, so the memo does not have to know which
// files loading a module reads: the module declaration and each service
// declaration its interface names. AddModuleReference, DeleteModule,
// composing other workspaces, a caller editing or reordering the exported
// Modules slice, a module or service saved by this process or any other, a
// service directory removed, a reference that resolves to another checkout —
// each changes one of those, and the next call recomputes. Nothing has to
// remember to clear it.
type providerMemo struct {
	composition [sha256.Size]byte
	// locations maps each composed module, by its workspace-local name, to the
	// directory its reference resolved to when the modules were loaded.
	locations map[string]string
	// read is every declaration file the load parsed, by path, with the
	// digest of the bytes it parsed (Module.read): a file written while the
	// modules load leaves an entry the next call refuses, never one that
	// hides it.
	read      map[string][sha256.Size]byte
	providers []*InterfaceProvider
}

// providerMemoMu guards every workspace's memo field. Entries are immutable,
// so it is held only to read or replace the pointer, never across a load.
var providerMemoMu sync.Mutex

// memoizedProviders returns the providers remembered for composition and
// locations, or false when there are none, either has changed since, or a
// declaration file the load read no longer has the content it read.
func (workspace *Workspace) memoizedProviders(composition [sha256.Size]byte, locations map[string]string) ([]*InterfaceProvider, bool) {
	providerMemoMu.Lock()
	memo := workspace.providerMemo
	providerMemoMu.Unlock()
	if memo == nil || memo.composition != composition || !maps.Equal(memo.locations, locations) {
		return nil, false
	}
	for file, digest := range memo.read {
		if current, err := fileDigest(file); err != nil || current != digest {
			return nil, false
		}
	}
	return copyProviders(memo.providers), true
}

func (workspace *Workspace) rememberProviders(composition [sha256.Size]byte, locations map[string]string, modules []*Module, providers []*InterfaceProvider) {
	read := make(map[string][sha256.Size]byte)
	for _, mod := range modules {
		maps.Copy(read, mod.read)
	}
	memo := &providerMemo{composition: composition, locations: locations, read: read, providers: copyProviders(providers)}
	providerMemoMu.Lock()
	workspace.providerMemo = memo
	providerMemoMu.Unlock()
}

// providerComposition digests what decides where each module is loaded from
// and under which name. ResolveModule reads nothing else of the workspace.
func (workspace *Workspace) providerComposition() ([sha256.Size]byte, error) {
	type overlay struct {
		Dir     string
		Resolve map[string]*ModuleResolveDirective
	}
	var local *overlay
	if workspace.overlay != nil {
		local = &overlay{Dir: workspace.overlay.dir, Resolve: workspace.overlay.Resolve}
	}
	encoded, err := json.Marshal(struct {
		Name, Layout, Dir string
		Modules           []*ModuleReference
		Overlay           *overlay
	}{workspace.Name, workspace.Layout, workspace.Dir(), workspace.Modules, local})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

// moduleLocations resolves each module the workspace composes to the
// directory LoadModuleFromReference loads it from. The flat layout's module is
// the workspace itself and declares no interface. False when a module does not
// resolve to a local directory: the load reports that, and nothing is
// remembered.
func (workspace *Workspace) moduleLocations(ctx context.Context) (map[string]string, bool) {
	locations := make(map[string]string, len(workspace.Modules))
	for _, ref := range workspace.Modules {
		if workspace.Layout == LayoutKindFlat && ref.Name == workspace.Name {
			continue
		}
		resolution, err := workspace.ResolveModule(ctx, ref)
		if err != nil || resolution.Kind == ResolutionPinned || resolution.Dir == "" {
			return nil, false
		}
		locations[ref.Name] = resolution.Dir
	}
	return locations, true
}

func fileDigest(file string) ([sha256.Size]byte, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(content), nil
}

// copyProviders hands each caller its own providers, so nothing a caller does
// to one reaches the memo or another caller.
func copyProviders(providers []*InterfaceProvider) []*InterfaceProvider {
	if providers == nil {
		return nil
	}
	copied := make([]*InterfaceProvider, len(providers))
	for i, provider := range providers {
		clone := *provider
		if provider.Identity != nil {
			identity := *provider.Identity
			clone.Identity = &identity
		}
		copied[i] = &clone
	}
	return copied
}
