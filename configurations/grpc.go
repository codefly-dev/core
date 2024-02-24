package configurations

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hashicorp/go-multierror"

	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

type GRPCRoute struct {
	Name        string `yaml:"name"`
	Package     string `yaml:"package"`
	ServiceName string `yaml:"service-name"`
	Application string `yaml:"-"`
	Service     string `yaml:"-"`
}

type ExtendedGRPCRoute[T any] struct {
	GRPCRoute `yaml:",inline"`

	Extension T `yaml:"extension"`
}

func (g *GRPCRoute) Save(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("RestRoute::Save")
	file, err := FilePathForGRPC(ctx, dir, g.ServiceUnique(), g.Name)
	if err != nil {
		return w.Wrapf(err, "cannot get file path for route to save")
	}
	w.Trace("saving", wool.FileField(file))
	f, err := os.Create(file)
	if err != nil {
		return w.Wrapf(err, "cannot create file for route")
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			w.Error("cannot close file", wool.ErrField(err))
		}
	}(f)
	out, err := yaml.Marshal(g)
	if err != nil {
		return w.Wrapf(err, "cannot marshal route")
	}
	_, err = f.Write(out)
	if err != nil {
		return w.With(wool.FileField(file)).Wrapf(err, "cannot write route")
	}
	return nil
}

func (g *GRPCRoute) ServiceUnique() string {
	return ServiceUnique(g.Application, g.Service)
}

func (g *GRPCRoute) Delete(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("RestRoute::Delete")
	file, err := FilePathForGRPC(ctx, dir, g.ServiceUnique(), g.Name)
	if err != nil {
		return w.Wrapf(err, "cannot get file path for route to delete")
	}
	err = os.Remove(file)
	if err != nil {
		return w.Wrapf(err, "cannot delete route file")
	}
	return nil
}

// A Route in gRPC is defined as:
// /package.Service/Method
func (g *GRPCRoute) Route() string {
	return fmt.Sprintf("/%s.%s/%s", g.Package, g.ServiceName, g.Name)
}

func FilePathForGRPC(ctx context.Context, dir string, unique string, name string) (string, error) {
	dir = path.Join(dir, unique)
	_, err := shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return "", err
	}
	file := path.Join(dir, fmt.Sprintf("%s%s", name, GRPCRouteFileSuffix))
	return file, nil
}

func (g *ExtendedGRPCRoute[T]) Save(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("ExtendedRestRoute::Save")
	file, err := FilePathForGRPC(ctx, dir, g.ServiceUnique(), g.Name)
	if err != nil {
		return w.Wrapf(err, "cannot get file path for route to save")
	}
	w.Debug("saving", wool.FileField(file), wool.Field("content", g))
	f, err := os.Create(file)
	if err != nil {
		return w.Wrapf(err, "cannot create file for route")
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			w.Error("cannot close file", wool.ErrField(err))
		}
	}(f)
	out, err := yaml.Marshal(g)
	if err != nil {
		return w.Wrapf(err, "cannot marshal route")
	}
	_, err = f.Write(out)
	if err != nil {
		return w.With(wool.FileField(file)).Wrapf(err, "cannot write route")
	}
	return nil
}

// GRPCRouteLoader will return all GRPC route groups in a directory
type GRPCRouteLoader struct {
	dir    string
	routes []*GRPCRoute
}

func NewGRPCRouteLoader(ctx context.Context, dir string) (*GRPCRouteLoader, error) {
	w := wool.Get(ctx).In("NewGRPCRouteLoader")
	exists, err := shared.CheckDirectory(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot check directory")
	}
	if !exists {
		return nil, w.NewError("directory <%s> does not exist", dir)
	}
	return &GRPCRouteLoader{dir: dir}, nil
}

const GRPCRouteFileSuffix = ".grpc.codefly.yaml"

func (loader *GRPCRouteLoader) Load(ctx context.Context) error {
	w := wool.Get(ctx).In("GRPCRouteLoader::Load")
	var routes []*GRPCRoute
	err := filepath.Walk(loader.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return w.Wrapf(err, "error walking path <%s>", path)
		}
		if info.IsDir() {
			return nil
		}
		unique, err := filepath.Rel(loader.dir, filepath.Dir(path))
		if err != nil {
			return err
		}
		ref, err := ParseServiceUnique(unique)
		if err != nil {
			return nil
		}
		base := filepath.Base(path)
		var routePath string
		var ok bool
		if routePath, ok = strings.CutSuffix(base, GRPCRouteFileSuffix); !ok {
			return nil
		}
		route, err := LoadGRPCRoute(ctx, path)
		if err != nil {
			return err
		}
		if route.Name != routePath {
			return w.NewError("route name <%s> does not match file name <%s>", route.Name, routePath)
		}
		route.Application = ref.Application
		route.Service = ref.Name
		routes = append(routes, route)
		return nil
	})
	if err != nil {
		return err
	}
	loader.routes = routes
	return nil
}

func (loader *GRPCRouteLoader) Save(ctx context.Context) error {
	var result error
	for _, route := range loader.routes {
		err := route.Save(ctx, loader.dir)
		if err != nil {
			result = multierror.Append(result, err)
		}
	}
	return result
}

func (loader *GRPCRouteLoader) All() []*GRPCRoute {
	return loader.routes
}

func LoadGRPCRoute(ctx context.Context, p string) (*GRPCRoute, error) {
	var err error
	p, err = shared.SolvePath(p)
	if err != nil {
		return nil, err
	}
	r, err := LoadFromPath[GRPCRoute](ctx, p)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func LoadExtendedGRPCRoute[T any](ctx context.Context, p string) (*ExtendedGRPCRoute[T], error) {
	var err error
	p, err = shared.SolvePath(p)
	if err != nil {
		return nil, err
	}
	r, err := LoadFromPath[ExtendedGRPCRoute[T]](ctx, p)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ExtendedGRPCRouteLoader will return all GRPC route groups in a directory
type ExtendedGRPCRouteLoader[T any] struct {
	dir    string
	routes []*ExtendedGRPCRoute[T]
}

func NewExtendedGRPCRouteLoader[T any](ctx context.Context, dir string) (*ExtendedGRPCRouteLoader[T], error) {
	w := wool.Get(ctx).In("NewGRPCRouteLoader")
	exists, err := shared.CheckDirectory(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot check directory")
	}
	if !exists {
		return nil, w.NewError("directory <%s> does not exist", dir)
	}
	return &ExtendedGRPCRouteLoader[T]{dir: dir}, nil
}

func (loader *ExtendedGRPCRouteLoader[T]) Load(ctx context.Context) error {
	w := wool.Get(ctx).In("GRPCRouteLoader::Load")
	var routes []*ExtendedGRPCRoute[T]
	err := filepath.Walk(loader.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return w.Wrapf(err, "error walking path <%s>", path)
		}
		if info.IsDir() {
			return nil
		}
		unique, err := filepath.Rel(loader.dir, filepath.Dir(path))
		if err != nil {
			return err
		}
		ref, err := ParseServiceUnique(unique)
		if err != nil {
			return nil
		}
		base := filepath.Base(path)
		var routePath string
		var ok bool
		if routePath, ok = strings.CutSuffix(base, GRPCRouteFileSuffix); !ok {
			return nil
		}
		route, err := LoadExtendedGRPCRoute[T](ctx, path)
		if err != nil {
			return err
		}
		if route.Name != routePath {
			return w.NewError("route name <%s> does not match file name <%s>", route.Name, routePath)
		}
		route.Application = ref.Application
		route.Service = ref.Name
		routes = append(routes, route)
		return nil
	})
	if err != nil {
		return err
	}
	loader.routes = routes
	return nil
}

func (loader *ExtendedGRPCRouteLoader[T]) Save(ctx context.Context) error {
	var result error
	for _, route := range loader.routes {
		err := route.Save(ctx, loader.dir)
		if err != nil {
			result = multierror.Append(result, err)
		}
	}
	return result
}

func (loader *ExtendedGRPCRouteLoader[T]) All() []*ExtendedGRPCRoute[T] {
	return loader.routes
}

func (loader *ExtendedGRPCRouteLoader[T]) Add(route *ExtendedGRPCRoute[T]) {
	loader.routes = append(loader.routes, route)
}

func UnwrapGRPCRoute[T any](route *ExtendedGRPCRoute[T]) *GRPCRoute {
	return &route.GRPCRoute
}
