package resources

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

const ApplicationConfigurationName = "application.codefly.yaml"

// ApplicationAgent is the agent kind for applications
const ApplicationAgent = AgentKind("codefly:application")

func init() {
	RegisterAgent(ApplicationAgent, basev0.Agent_APPLICATION)
}

// ApplicationDependency represents a dependency on another application
type ApplicationDependency struct {
	Name   string `yaml:"name"`
	Module string `yaml:"module,omitempty"`
}

// Artifact represents a build output from an application
type Artifact struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`               // "binary", "bundle", "package"
	Platform string `yaml:"platform,omitempty"` // "darwin-arm64", "linux-amd64", etc.
	Path     string `yaml:"path,omitempty"`
}

// Application represents an end-user facing program (CLI, desktop app, etc.)
type Application struct {
	Kind        string `yaml:"kind"`
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Version     string `yaml:"version"`

	PathOverride *string `yaml:"path,omitempty"`

	Agent *Agent `yaml:"agent"`

	// Dependencies
	ServiceDependencies                []*ServiceDependency     `yaml:"service-dependencies,omitempty"`
	ApplicationDependencies            []*ApplicationDependency `yaml:"application-dependencies,omitempty"`
	LibraryDependencies                []*LibraryDependency     `yaml:"library-dependencies,omitempty"`
	WorkspaceConfigurationDependencies []string                 `yaml:"workspace-configuration-dependencies,omitempty"`

	// Build outputs
	Artifacts []*Artifact `yaml:"artifacts,omitempty"`

	// Application-specific settings
	Spec map[string]any `yaml:"spec,omitempty"`

	// Internal
	dir    string
	module string
}

// NewApplication creates a new Application
func NewApplication(ctx context.Context, name string) (*Application, error) {
	w := wool.Get(ctx).In("NewApplication", wool.NameField(name))

	app := &Application{
		Kind:    "application",
		Name:    name,
		Version: "0.0.1",
	}

	// Validate name
	if name == "" {
		return nil, w.NewError("application name cannot be empty")
	}

	return app, nil
}

// Dir returns the application directory
func (app *Application) Dir() string {
	return app.dir
}

// SetDir sets the application directory
func (app *Application) SetDir(dir string) {
	app.dir = dir
}

// Module returns the module name
func (app *Application) Module() string {
	return app.module
}

// SetModule sets the module name
func (app *Application) SetModule(module string) {
	app.module = module
}

// Unique returns the unique identifier for the application
func (app *Application) Unique() string {
	return fmt.Sprintf("%s/%s", app.module, app.Name)
}

// Identity returns the application identity
func (app *Application) Identity() (*ApplicationIdentity, error) {
	if app.module == "" {
		return nil, fmt.Errorf("module not set")
	}
	return &ApplicationIdentity{
		Name:    app.Name,
		Module:  app.module,
		Version: app.Version,
	}, nil
}

// ApplicationIdentity represents the identity of an application
type ApplicationIdentity struct {
	Name    string
	Module  string
	Version string
}

// Unique returns the unique identifier
func (id *ApplicationIdentity) Unique() string {
	return fmt.Sprintf("%s/%s", id.Module, id.Name)
}

// LoadApplicationFromDir loads an application from a directory
func LoadApplicationFromDir(ctx context.Context, dir string) (*Application, error) {
	w := wool.Get(ctx).In("LoadApplicationFromDir", wool.DirField(dir))

	configPath := path.Join(dir, ApplicationConfigurationName)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, w.Wrapf(err, "application configuration not found")
	}

	app, err := LoadFromPath[Application](ctx, configPath)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load application")
	}

	app.dir = dir

	return app, nil
}

// SaveToDir saves the application configuration to a directory
func (app *Application) SaveToDir(ctx context.Context, dir string) error {
	app.dir = dir
	return SaveToDir(ctx, app, dir)
}

// Save saves the application configuration to its directory
func (app *Application) Save(ctx context.Context) error {
	return app.SaveToDir(ctx, app.dir)
}

// Local returns a path relative to the application directory
func (app *Application) Local(p ...string) string {
	return filepath.Join(append([]string{app.dir}, p...)...)
}

// Proto converts to protobuf representation
func (app *Application) Proto(_ context.Context) (*basev0.Application, error) {
	proto := &basev0.Application{
		Name:        app.Name,
		Description: app.Description,
	}

	// Convert agent
	if app.Agent != nil {
		proto.Agent = &basev0.Agent{
			Kind:      basev0.Agent_APPLICATION,
			Name:      app.Agent.Name,
			Publisher: app.Agent.Publisher,
			Version:   app.Agent.Version,
		}
	}

	// Convert service dependencies
	for _, dep := range app.ServiceDependencies {
		proto.ServiceDependencies = append(proto.ServiceDependencies, &basev0.ServiceReference{
			Name:   dep.Name,
			Module: dep.Module,
		})
	}

	// Convert application dependencies
	for _, dep := range app.ApplicationDependencies {
		proto.ApplicationDependencies = append(proto.ApplicationDependencies, &basev0.ApplicationReference{
			Name:   dep.Name,
			Module: dep.Module,
		})
	}

	// Convert artifacts
	for _, art := range app.Artifacts {
		proto.Artifacts = append(proto.Artifacts, &basev0.Artifact{
			Name:     art.Name,
			Type:     art.Type,
			Platform: art.Platform,
			Path:     art.Path,
		})
	}

	return proto, nil
}

// ApplicationReference is used in module configuration
type ApplicationReference struct {
	Name string `yaml:"name"`
}

// FindApplicationsInDir finds all applications in a directory
func FindApplicationsInDir(ctx context.Context, dir string) ([]*Application, error) {
	w := wool.Get(ctx).In("FindApplicationsInDir", wool.DirField(dir))

	var applications []*Application

	// Check if directory exists
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return applications, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot read directory")
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		appDir := path.Join(dir, entry.Name())
		configPath := path.Join(appDir, ApplicationConfigurationName)

		if ok, _ := shared.FileExists(ctx, configPath); !ok {
			continue
		}

		app, err := LoadApplicationFromDir(ctx, appDir)
		if err != nil {
			w.Warn("cannot load application", wool.Field("dir", appDir), wool.ErrField(err))
			continue
		}

		applications = append(applications, app)
	}

	return applications, nil
}

// HasApplicationDependency checks if an application has a dependency on another application
func (app *Application) HasApplicationDependency(name string, module string) bool {
	for _, dep := range app.ApplicationDependencies {
		if dep.Name == name && (dep.Module == "" || dep.Module == module) {
			return true
		}
	}
	return false
}

// AddApplicationDependency adds a dependency on another application
func (app *Application) AddApplicationDependency(name string, module string) {
	if app.HasApplicationDependency(name, module) {
		return
	}
	app.ApplicationDependencies = append(app.ApplicationDependencies, &ApplicationDependency{
		Name:   name,
		Module: module,
	})
}

// HasServiceDependency checks if an application has a dependency on a service
func (app *Application) HasServiceDependency(name string, module string) bool {
	for _, dep := range app.ServiceDependencies {
		if dep.Name == name && (dep.Module == "" || dep.Module == module) {
			return true
		}
	}
	return false
}

// AddServiceDependency adds a dependency on a service
func (app *Application) AddServiceDependency(name string, module string) {
	if app.HasServiceDependency(name, module) {
		return
	}
	app.ServiceDependencies = append(app.ServiceDependencies, &ServiceDependency{
		Name:   name,
		Module: module,
	})
}

// Unique returns the unique identifier for an application dependency
func (dep *ApplicationDependency) Unique() string {
	if dep.Module == "" {
		return dep.Name
	}
	return fmt.Sprintf("%s/%s", dep.Module, dep.Name)
}

// String returns a string representation of the application dependency
func (dep *ApplicationDependency) String() string {
	return fmt.Sprintf("ApplicationDependency<%s>", dep.Unique())
}

// RemoveApplicationDependency removes a dependency on another application
func (app *Application) RemoveApplicationDependency(name string, module string) {
	var deps []*ApplicationDependency
	for _, dep := range app.ApplicationDependencies {
		if dep.Name == name && (dep.Module == "" || dep.Module == module) {
			continue
		}
		deps = append(deps, dep)
	}
	app.ApplicationDependencies = deps
}

// RemoveServiceDependency removes a dependency on a service
func (app *Application) RemoveServiceDependency(name string, module string) {
	var deps []*ServiceDependency
	for _, dep := range app.ServiceDependencies {
		if dep.Name == name && (dep.Module == "" || dep.Module == module) {
			continue
		}
		deps = append(deps, dep)
	}
	app.ServiceDependencies = deps
}

// GetApplicationDependencies returns all application dependencies
func (app *Application) GetApplicationDependencies() []*ApplicationDependency {
	return app.ApplicationDependencies
}

// GetServiceDependencies returns all service dependencies
func (app *Application) GetServiceDependencies() []*ServiceDependency {
	return app.ServiceDependencies
}

// ResolveApplicationDependency resolves an application dependency to an actual Application
func (app *Application) ResolveApplicationDependency(ctx context.Context, dep *ApplicationDependency, workspace *Workspace) (*Application, error) {
	w := wool.Get(ctx).In("ResolveApplicationDependency", wool.Field("dependency", dep.Unique()))

	// If module is specified, use it; otherwise use the same module as the current application
	moduleName := dep.Module
	if moduleName == "" {
		moduleName = app.module
	}

	// Load the module
	mod, err := workspace.LoadModuleFromName(ctx, moduleName)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load module for dependency")
	}

	// Load the application from the module
	depApp, err := mod.LoadApplicationFromName(ctx, dep.Name)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load application dependency")
	}

	return depApp, nil
}

// ResolveAllApplicationDependencies resolves all application dependencies
func (app *Application) ResolveAllApplicationDependencies(ctx context.Context, workspace *Workspace) ([]*Application, error) {
	w := wool.Get(ctx).In("ResolveAllApplicationDependencies")

	var resolved []*Application
	for _, dep := range app.ApplicationDependencies {
		depApp, err := app.ResolveApplicationDependency(ctx, dep, workspace)
		if err != nil {
			return nil, w.Wrapf(err, "cannot resolve dependency %s", dep.Unique())
		}
		resolved = append(resolved, depApp)
	}

	return resolved, nil
}
