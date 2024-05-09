package resources

import (
	"context"
	"fmt"
	"path"

	"github.com/codefly-dev/core/wool"
)

type LayoutKind = string

const (
	LayoutKindFlat    LayoutKind = "flat"
	LayoutKindModules LayoutKind = "modules"
)

type Layout interface {

	// Modules inside a Workspace

	ModulesRoot() string
	ModulePath(name string) string

	// Services inside a Module

	ServicesRoot(module string) string
	ServicePath(module string, name string) string
}

func NewLayout(ctx context.Context, root string, kind string, conf []byte) (Layout, error) {
	switch kind {
	case LayoutKindFlat:
		return NewFlatLayout(ctx, root, conf)
	case LayoutKindModules:
		return NewModulesLayout(ctx, root, conf)
	default:
		return nil, fmt.Errorf("unknown layout kind %s", kind)
	}
}

// FlatLayout is a layout where the module "root" is the top folders
// Services are in the services folder at the root

type FlatLayout struct {
	root string
}

func (f FlatLayout) ModulesRoot() string {
	return f.root
}

func (f FlatLayout) ModulePath(string) string {
	return f.root
}

func (f FlatLayout) ServicesRoot(string) string {
	return path.Join(f.root, "services")
}

func (f FlatLayout) ServicePath(_ string, name string) string {
	return path.Join(f.root, "services", name)
}

func NewFlatLayout(ctx context.Context, root string, conf []byte) (*FlatLayout, error) {
	w := wool.Get(ctx).In("resources.NewFlatLayout")
	w.Debug("create", wool.Field("root", root))
	layout := &FlatLayout{root: root}
	err := LoadSpec(ctx, conf, layout)
	if err != nil {
		return nil, w.Wrapf(err, "failed to load spec")
	}
	return layout, nil
}

// ModulesLayout is a layout with modules

type ModulesLayout struct {
	root string
}

func (f ModulesLayout) ModulesRoot() string {
	return f.root
}

func (f ModulesLayout) ModulePath(module string) string {
	return path.Join(f.root, "modules", module)
}

func (f ModulesLayout) ServicesRoot(module string) string {
	return path.Join(f.root, "modules", module, "services")
}

func (f ModulesLayout) ServicePath(module string, name string) string {
	return path.Join(f.root, "modules", module, "services", name)
}

func NewModulesLayout(ctx context.Context, root string, conf []byte) (*ModulesLayout, error) {
	w := wool.Get(ctx).In("resources.NewModulesLayout")
	w.Debug("create", wool.Field("root", root))
	layout := &ModulesLayout{root: root}
	err := LoadSpec(ctx, conf, layout)
	if err != nil {
		return nil, w.Wrapf(err, "failed to load spec")
	}
	return layout, nil
}
