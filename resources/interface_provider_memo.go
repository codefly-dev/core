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
// value, the layout, the directory and the local overlay), and, for each
// module, the file its declaration resolves to and the content of that file,
// which is all Module.InterfaceProviders reads. AddModuleReference,
// DeleteModule, composing other workspaces, a caller editing the exported
// Modules slice, a module saved by this process or any other, a reference
// that resolves to another checkout — each changes one of those, and the next
// call recomputes. Nothing has to remember to clear it.
type providerMemo struct {
	composition [sha256.Size]byte
	// declarations maps each composed module, by its workspace-local name, to
	// the declaration it resolved to, read before the modules were loaded: a
	// file written while they load leaves an entry the next call refuses,
	// never one that hides it.
	declarations map[string]moduleDeclaration
	providers    []*InterfaceProvider
}

// moduleDeclaration locates one module's declaration file and digests its
// content.
type moduleDeclaration struct {
	file   string
	digest [sha256.Size]byte
}

// providerMemoMu guards every workspace's memo field. Entries are immutable,
// so it is held only to read or replace the pointer, never across a load.
var providerMemoMu sync.Mutex

// memoizedProviders returns the providers remembered for composition and
// declarations, or false when there are none or either has changed since.
func (workspace *Workspace) memoizedProviders(composition [sha256.Size]byte, declarations map[string]moduleDeclaration) ([]*InterfaceProvider, bool) {
	providerMemoMu.Lock()
	memo := workspace.providerMemo
	providerMemoMu.Unlock()
	if memo == nil || memo.composition != composition || !maps.Equal(memo.declarations, declarations) {
		return nil, false
	}
	return copyProviders(memo.providers), true
}

func (workspace *Workspace) rememberProviders(memo *providerMemo) {
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

// moduleDeclarations locates and digests the declaration file of each module
// the workspace composes, as LoadModuleFromReference locates it. The flat
// layout's module is the workspace itself and declares no interface. False
// when a module does not resolve to a readable declaration: the load reports
// that, and nothing is remembered.
func (workspace *Workspace) moduleDeclarations(ctx context.Context) (map[string]moduleDeclaration, bool) {
	declarations := make(map[string]moduleDeclaration, len(workspace.Modules))
	for _, ref := range workspace.Modules {
		if workspace.Layout == LayoutKindFlat && ref.Name == workspace.Name {
			continue
		}
		resolution, err := workspace.ResolveModule(ctx, ref)
		if err != nil || resolution.Kind == ResolutionPinned || resolution.Dir == "" {
			return nil, false
		}
		file, err := Path[Module](ctx, resolution.Dir)
		if err != nil {
			return nil, false
		}
		digest, err := fileDigest(file)
		if err != nil {
			return nil, false
		}
		declarations[ref.Name] = moduleDeclaration{file: file, digest: digest}
	}
	return declarations, true
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
