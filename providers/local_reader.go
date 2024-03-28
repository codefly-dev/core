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

type ConfigurationInformationWrapper struct {
	*basev0.ConfigurationInformation
}

type ConfigurationInformationLocalReader struct {
	project *configurations.Project
}

func NewConfigurationLocalReader(_ context.Context, project *configurations.Project) (*ConfigurationInformationLocalReader, error) {
	return &ConfigurationInformationLocalReader{project: project}, nil
}

func (local *ConfigurationInformationLocalReader) Load(ctx context.Context, env *configurations.Environment) ([]*basev0.Configuration, error) {
	w := wool.Get(ctx).In("provider.Load")
	// Create a provider folder for local development
	configurationDir := path.Join(local.project.Dir(), "configurations", env.Name)
	_, err := shared.CheckDirectoryOrCreate(ctx, configurationDir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create configuration directory")
	}
	projectConfs, err := LoadConfigurationsFromEnvFiles(ctx, configurationDir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load configurations")
	}

	var out []*basev0.Configuration

	projectConfsMap := make(map[string]*basev0.Configuration)

	for _, conf := range projectConfs {
		fmt.Println(conf.Name)
		if _, ok := projectConfsMap[conf.Name]; !ok {
			projectConfsMap[conf.Name] = &basev0.Configuration{
				Origin: configurations.ConfigurationProjectOrigin,
			}
			continue
		}
		projectConfsMap[conf.Name].Configurations = append(projectConfsMap[conf.Name].Configurations, conf)
	}

	for _, conf := range projectConfsMap {
		w.Debug("adding project conf")
		out = append(out, conf)
	}
	// Load services configurations
	services, err := local.project.LoadServices(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load services")
	}

	serviceConfs := make(map[string]*basev0.Configuration)
	for _, svc := range services {
		serviceDir := path.Join(svc.Dir(), "configurations", env.Name)
		if !shared.DirectoryExists(serviceDir) {
			continue
		}
		projectConfs, err = LoadConfigurationsFromEnvFiles(ctx, serviceDir)
		if err != nil {
			return nil, w.Wrapf(err, "cannot load service configurations")
		}
		if len(projectConfs) == 0 {
			continue
		}
		if _, ok := serviceConfs[svc.Unique()]; !ok {
			serviceConfs[svc.Unique()] = &basev0.Configuration{
				Origin: svc.Unique(),
			}
		}
		for _, conf := range projectConfs {
			serviceConfs[svc.Unique()].Configurations = append(serviceConfs[svc.Unique()].Configurations, conf)
		}
	}
	for _, conf := range serviceConfs {
		w.Debug("adding service conf")
		out = append(out, conf)
	}
	return out, nil
}

type ConfigurationSource struct {
	ServiceWithApplication *configurations.ServiceWithApplication
	Name                   string
}

// FromService satisfies this format
// - Name
// - unique:Name
func FromService(service *configurations.Service, dep string) (*ConfigurationSource, error) {
	if !strings.Contains(dep, ":") {
		return &ConfigurationSource{Name: dep}, nil
	}
	tokens := strings.Split(dep, ":")
	if len(tokens) != 2 {
		return nil, fmt.Errorf("invalid configuration dependency format: %s", dep)
	}
	name := tokens[1]
	parsed, err := configurations.ParseService(tokens[0])
	if err != nil {
		return nil, err
	}
	if parsed.Application == "" {
		parsed.Application = service.Application
	}
	return &ConfigurationSource{ServiceWithApplication: parsed, Name: name}, nil
}

func LoadConfigurationsFromEnvFiles(ctx context.Context, dir string) ([]*basev0.ConfigurationInformation, error) {
	w := wool.Get(ctx).In("provider.LoadConfigurationsFromEnvFiles")
	w.Debug("loading configurations from directory", wool.DirField(dir))
	if !shared.DirectoryExists(dir) {
		return nil, w.NewError("configuration directory doesn't exist: %s", dir)
	}
	var confs []*basev0.ConfigurationInformation
	// Walk directories recursively
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return w.Wrapf(err, "cannot walk p")
		}
		if p == dir {
			return nil
		}
		if !info.IsDir() {
			if !strings.HasSuffix(p, ".env") {
				return nil
			}
			conf, err := loadFromEnvFile(ctx, dir, p)
			if err != nil {
				return w.Wrapf(err, "cannot load provider from env file")
			}
			w.Debug("loaded configuration", wool.Field("configuration", conf.Name))
			confs = append(confs, conf)
			return nil
		}
		return nil
	})

	if err != nil {
		return nil, w.Wrapf(err, "cannot walk providers directory")
	}
	w.Debug("loaded confs", wool.SliceCountField(confs))
	return confs, nil
}

func loadFromEnvFile(ctx context.Context, dir string, p string) (*basev0.ConfigurationInformation, error) {
	w := wool.Get(ctx).In("provider.loadFromEnvFile")

	base, err := filepath.Rel(dir, p)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get relative path")
	}
	var ok bool
	base, ok = strings.CutSuffix(base, ".env")
	if !ok {
		return nil, w.NewError("invalid env file Name: %s", base)
	}
	var isSecret bool
	base, isSecret = strings.CutSuffix(base, ".secret")

	f, err := os.ReadFile(p)
	if err != nil {
		return nil, w.Wrapf(err, "cannot read auth0.env")
	}
	info := &basev0.ConfigurationInformation{
		Name: base,
	}
	lines := strings.Split(string(f), "\n")

	for _, line := range lines {
		tokens := strings.SplitN(line, "=", 2)
		if len(tokens) < 2 {
			continue
		}
		info.ConfigurationValues = append(info.ConfigurationValues, &basev0.ConfigurationValue{
			Key:    tokens[0],
			Value:  tokens[1],
			Secret: isSecret,
		})
	}
	return info, nil
}

// ExtractFromPath gets applications/app/services/ServiceWithApplication and we want to extract app/ServiceWithApplication
func ExtractFromPath(p string) string {
	tokens := strings.Split(p, "/")
	if len(tokens) != 4 {
		return ""
	}
	return fmt.Sprintf("%s/%s", tokens[1], tokens[3])
}
