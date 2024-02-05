package providers

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/wool"
)

type Provider struct {
	project      *configurations.Project
	projectInfos map[string]*basev0.ProviderInformation
	serviceInfos map[string][]*basev0.ProviderInformation
	sharedInfos  map[string][]*basev0.ProviderInformation
}

func New(ctx context.Context, project *configurations.Project) (*Provider, error) {
	w := wool.Get(ctx).In("Project.CreateLocalProvider")
	// Create a provider folder for local development
	providerDir := path.Join(project.Dir(), "providers", "local")
	_, err := shared.CheckDirectoryOrCreate(ctx, providerDir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create provider directory")
	}

	provider := &Provider{
		project:      project,
		projectInfos: make(map[string]*basev0.ProviderInformation),
		serviceInfos: make(map[string][]*basev0.ProviderInformation),
		sharedInfos:  make(map[string][]*basev0.ProviderInformation),
	}
	infos, err := LoadProviderFromEnvFiles(ctx, provider.project, &configurations.Environment{Name: "local"})
	if err != nil {
		return nil, err
	}
	for _, info := range infos {
		if info.Origin == configurations.ProjectProviderOrigin {
			if _, ok := provider.projectInfos[info.Name]; ok {
				return nil, fmt.Errorf("provider %s already exists", info.Name)

			}
			provider.projectInfos[info.Name] = info
			continue
		}
		provider.serviceInfos[info.Origin] = append(provider.serviceInfos[info.Origin], info)
	}
	return provider, nil
}

func (provider *Provider) AddProjectProviderInformation(ctx context.Context, name string, data map[string]string) error {
	w := wool.Get(ctx).In("provider.AddProjectProviderInformation")
	if _, ok := provider.projectInfos[name]; ok {
		return w.NewError("provider %s already exists", name)
	}
	provider.projectInfos[name] = &basev0.ProviderInformation{
		Name:   name,
		Origin: configurations.ProjectProviderOrigin,
		Data:   data,
	}
	// Save to file
	file := path.Join(provider.project.Dir(), "providers", "local", fmt.Sprintf("%s.%s", name, "env"))
	f, err := os.Create(file)
	if err != nil {
		return w.Wrapf(err, "cannot create file")
	}
	defer f.Close()
	for key, value := range data {
		_, err := f.WriteString(fmt.Sprintf("%s=%s\n", key, value))
		if err != nil {
			return w.Wrapf(err, "cannot write to file")
		}
	}
	return nil
}

type InfoSource struct {
	*configurations.ServiceWithApplication
	Name string
}

// FromService satisfies this format:
// - Name
// - unique:Name
func FromService(service *configurations.Service, dep string) (*InfoSource, error) {
	if !strings.Contains(dep, ":") {
		return &InfoSource{Name: dep}, nil
	}
	tokens := strings.Split(dep, ":")
	if len(tokens) != 2 {
		return nil, fmt.Errorf("invalid provider dependency format: %s", dep)
	}
	name := tokens[1]
	parsed, err := configurations.ParseService(tokens[0])
	if err != nil {
		return nil, err
	}
	if parsed.Application == "" {
		parsed.Application = service.Application
	}
	return &InfoSource{ServiceWithApplication: parsed, Name: name}, nil
}

func (provider *Provider) GetProviderInformations(ctx context.Context, service *configurations.Service) ([]*basev0.ProviderInformation, error) {
	w := wool.Get(ctx).In("provider.GetProviderInformation")
	var res []*basev0.ProviderInformation
	infos, err := provider.GetProjectProviderInformations(ctx, service)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get project provider information")
	}
	res = append(res, infos...)
	infos, err = provider.GetProviderDependenciesInformations(ctx, service)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get provider dependencies information")
	}
	res = append(res, infos...)
	return res, nil
}

func (provider *Provider) GetProjectProviderInformations(_ context.Context, service *configurations.Service) ([]*basev0.ProviderInformation, error) {
	var res []*basev0.ProviderInformation
	for _, dep := range service.ProviderDependencies {
		if info, ok := provider.projectInfos[dep]; ok {
			res = append(res, info)
		}
	}
	return res, nil
}

func (provider *Provider) GetProjectProviderInformation(ctx context.Context, name string) (*basev0.ProviderInformation, error) {
	w := wool.Get(ctx).In("provider.GetProjectProviderInformation")
	if info, ok := provider.projectInfos[name]; ok {
		return info, nil
	}
	return nil, w.NewError("cannot find provider: %s", name)
}

func (provider *Provider) GetProviderDependenciesInformations(ctx context.Context, service *configurations.Service) ([]*basev0.ProviderInformation, error) {
	w := wool.Get(ctx).In("provider.GetProviderInformation")
	var res []*basev0.ProviderInformation
	for _, dep := range service.ProviderDependencies {
		// We check if the source is a service or not
		source, err := FromService(service, dep)
		if err != nil {
			return nil, w.Wrap(err)
		}
		// We have a global provider dependency
		if source.ServiceWithApplication == nil {
			if info, ok := provider.projectInfos[dep]; ok {
				res = append(res, info)
			}
			continue
		}
		unique := source.ServiceWithApplication.Unique()
		if infos, ok := provider.serviceInfos[unique]; ok {
			for _, info := range infos {
				if info.Name == source.Name {
					res = append(res, info)
				}

			}
		}
	}
	if infos, ok := provider.serviceInfos[service.Unique()]; ok {
		res = append(res, infos...)
	}
	return res, nil
}

func (provider *Provider) GetSharedInformation(ctx context.Context, uniques ...string) ([]*basev0.ProviderInformation, error) {
	w := wool.Get(ctx).In("provider.GetSharedInformation")
	var res []*basev0.ProviderInformation
	for _, unique := range uniques {
		if infos, ok := provider.sharedInfos[unique]; ok {
			res = append(res, infos...)
		}
	}
	w.Debug("got shared information", wool.Field("got", configurations.MakeProviderInformationSummary(res)))
	return res, nil
}

func (provider *Provider) Share(ctx context.Context, infos []*basev0.ProviderInformation) error {
	w := wool.Get(ctx).In("provider.Share")
	w.Debug("sharing", wool.Field("info", configurations.MakeProviderInformationSummary(infos)))
	for _, info := range infos {
		provider.sharedInfos[info.Origin] = append(provider.sharedInfos[info.Origin], info)
	}
	return nil
}

func loadFromEnvFile(ctx context.Context, dir string, p string) (*basev0.ProviderInformation, error) {
	w := wool.Get(ctx).In("provider.loadFromEnvFile")

	// Extract the relative path
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get relative path")
	}
	rel = filepath.Dir(rel)
	origin := configurations.ProjectProviderOrigin
	if rel != "." {
		parsed, err := configurations.ParseServiceUnique(rel)
		if err != nil {
			return nil, w.Wrapf(err, "cannot parse service unique")
		}
		origin = parsed.Unique()
	}
	base := filepath.Base(p)
	var ok bool
	base, ok = strings.CutSuffix(base, ".env")
	if !ok {
		return nil, w.NewError("invalid env file name: %s", base)
	}

	f, err := os.ReadFile(p)
	if err != nil {
		return nil, w.Wrapf(err, "cannot read auth0.env")
	}
	info := &basev0.ProviderInformation{
		Name:   base,
		Origin: origin,
		Data:   make(map[string]string),
	}
	lines := strings.Split(string(f), "\n")

	for _, line := range lines {
		tokens := strings.Split(line, "=")
		if len(tokens) != 2 {
			continue
		}
		info.Data[tokens[0]] = tokens[1]
	}
	return info, nil
}

func LoadProviderFromEnvFiles(ctx context.Context, project *configurations.Project, env *configurations.Environment) ([]*basev0.ProviderInformation, error) {
	w := wool.Get(ctx).In("provider.loadFromProject")
	var infos []*basev0.ProviderInformation
	dir := filepath.Join(project.Dir(), "providers", env.Name)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, w.NewError("path doesn't exist: %s", dir)
	}
	// Walk
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		if info.Name() == ".env" {
			return nil
		}
		prov, nerr := loadFromEnvFile(ctx, dir, path)
		if nerr != nil {
			return w.Wrapf(nerr, "cannot load provider from env file")
		}
		infos = append(infos, prov)
		return nil
	})

	if err != nil {
		return nil, w.Wrapf(err, "cannot walk providers directory")
	}
	return infos, nil
}
