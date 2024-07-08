package configurations

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/wool"
)

type ConfigurationInformationWrapper struct {
	*basev0.ConfigurationInformation
}

type ConfigurationInformationLocalReader struct {
	workspace      *resources.Workspace
	dns            []*basev0.DNS
	configurations []*basev0.Configuration
}

func (local *ConfigurationInformationLocalReader) Identity() string {
	return "local"
}

func (local *ConfigurationInformationLocalReader) DNS() []*basev0.DNS {
	return local.dns
}

func (local *ConfigurationInformationLocalReader) Configurations() []*basev0.Configuration {
	return local.configurations
}

func NewConfigurationLocalReader(_ context.Context, workspace *resources.Workspace) (*ConfigurationInformationLocalReader, error) {
	return &ConfigurationInformationLocalReader{workspace: workspace}, nil
}

func (local *ConfigurationInformationLocalReader) Load(ctx context.Context, env *resources.Environment) error {
	w := wool.Get(ctx).In("ConfigurationInformationLocalReader.Load")

	// Create a provider folder for local development
	configurationDir := path.Join(local.workspace.Dir(), "configurations", env.Name)
	_, err := shared.CheckDirectoryOrCreate(ctx, configurationDir)
	if err != nil {
		return w.Wrapf(err, "cannot create configuration directory")
	}

	workspaceInfos, err := LoadConfigurationInformationsFromFiles(ctx, configurationDir)
	if err != nil {
		return w.Wrapf(err, "cannot load configurations")
	}

	var confs []*basev0.Configuration

	// For workspace configurations, we add one configuration per info
	for _, info := range workspaceInfos {
		confs = append(confs, &basev0.Configuration{
			Origin: resources.ConfigurationWorkspace,
			Infos:  []*basev0.ConfigurationInformation{info},
		})
	}
	// Load services configurations
	services, err := local.workspace.LoadServices(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot load services")
	}

	w.Debug("loaded services", wool.Field("svcs", resources.MakeManyServicesSummary(services)))

	serviceConfs := make(map[string]*basev0.Configuration)
	for _, svc := range services {
		serviceConfDir := path.Join(svc.Dir(), "configurations", env.Name)
		exists, err := shared.DirectoryExists(ctx, serviceConfDir)
		if err != nil {
			return w.Wrapf(err, "cannot check service configuration directory")
		}
		if exists {
			serviceInfos, err := LoadConfigurationInformationsFromFiles(ctx, serviceConfDir)
			if err != nil {
				return w.Wrapf(err, "cannot load service configurations")
			}

			if len(serviceInfos) > 0 {
				if _, ok := serviceConfs[svc.Unique()]; !ok {
					serviceConfs[svc.Unique()] = &basev0.Configuration{
						Origin: svc.Unique(),
					}
				}
				for _, info := range serviceInfos {
					serviceConfs[svc.Unique()].Infos = append(serviceConfs[svc.Unique()].Infos, info)
				}
			}
		}
		// Load DNS
		serviceDNSDir := path.Join(svc.Dir(), "dns", env.Name)
		dnsFile := path.Join(serviceDNSDir, "dns.codefly.yaml")
		exists, err = shared.FileExists(ctx, dnsFile)
		if err != nil {
			return w.Wrapf(err, "cannot check dns file")
		}
		if exists {
			dns, err := loadDNS(ctx, dnsFile)
			if err != nil {
				return w.Wrapf(err, "cannot load dns")
			}
			for _, d := range dns {
				d.Service = svc.Name
				d.Module = svc.Module
				w.Debug("found DNS", wool.Field("dns", resources.MakeDNSSummary(d)))
				local.dns = append(local.dns, d)
			}
		}
	}
	for _, conf := range serviceConfs {
		confs = append(confs, conf)
	}
	local.configurations = confs
	return nil
}

func loadDNS(_ context.Context, file string) ([]*basev0.DNS, error) {
	var dns []*basev0.DNS
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(f, &dns)
	if err != nil {
		return nil, err

	}
	return dns, nil

}

type ConfigurationSource struct {
	ServiceWithModule *resources.ServiceWithModule
	Name              string
}

// FromService satisfies this format
// - Name
// - unique:Name
func FromService(service *resources.Service, dep string) (*ConfigurationSource, error) {
	if !strings.Contains(dep, ":") {
		return &ConfigurationSource{Name: dep}, nil
	}
	tokens := strings.Split(dep, ":")
	if len(tokens) != 2 {
		return nil, fmt.Errorf("invalid configuration dependency format: %s", dep)
	}
	name := tokens[1]
	parsed, err := resources.ParseServiceWithOptionalModule(tokens[0])
	if err != nil {
		return nil, err
	}
	if parsed.Module == "" {
		parsed.Module = service.Module
	}
	return &ConfigurationSource{ServiceWithModule: parsed, Name: name}, nil
}

// LoadConfigurationInformationsFromFiles returns all configurations infos
// Naming is path based with respect to dir
func LoadConfigurationInformationsFromFiles(ctx context.Context, dir string) ([]*basev0.ConfigurationInformation, error) {
	w := wool.Get(ctx).In("provider.LoadConfigurationsFromEnvFiles")

	w.Trace("loading configurations from directory", wool.DirField(dir))
	exists, err := shared.DirectoryExists(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot check directory")
	}
	if !exists {
		return nil, w.NewError("configuration directory doesn't exist: %s", dir)
	}

	var infos []*basev0.ConfigurationInformation
	// Walk directories recursively

	err = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return w.Wrapf(err, "cannot walk p")
		}
		if p == dir {
			return nil
		}
		if !info.IsDir() {
			base := path.Base(p)
			if strings.Contains(base, ".codefly.") {
				// Specific codefly files are ignored
				return nil
			}

			if strings.HasSuffix(p, ".env") {
				confInfo, err := loadFromEnvFile(ctx, dir, p)
				if err != nil {
					return w.Wrapf(err, "cannot load provider from env file: %s", p)
				}
				w.Trace("loaded configuration", wool.Field("configuration", confInfo.Name))
				infos = append(infos, confInfo)
				return nil
			}
			if strings.HasSuffix(p, ".yaml") {
				confInfo, err := loadFromYamlFile(ctx, dir, p)
				if err != nil {
					return w.Wrapf(err, "cannot load provider from yaml file: %s", p)
				}
				w.Trace("loaded configuration", wool.Field("configuration", confInfo.Name))
				infos = append(infos, confInfo)
				return nil
			}
		}
		return nil
	})

	if err != nil {
		return nil, w.Wrapf(err, "cannot walk providers directory")
	}
	consolidated := ConsolidateInfo(infos)
	w.Trace("loaded infos", wool.SliceCountField(consolidated))
	return consolidated, nil
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

func loadFromYamlFile(ctx context.Context, dir string, p string) (*basev0.ConfigurationInformation, error) {
	w := wool.Get(ctx).In("provider.loadFromYamlFile")

	base, err := filepath.Rel(dir, p)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get relative path")
	}
	var ok bool
	base, ok = strings.CutSuffix(base, ".yaml")
	if !ok {
		return nil, w.NewError("invalid yaml file Name: %s", base)
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

	var data map[string]interface{}
	err = yaml.Unmarshal(f, &data)
	if err != nil {
		return nil, w.Wrapf(err, "Error unmarshalling YAML: %s data: %s", p, string(f))
	}

	flattened := make(map[string]string)
	flattenMap("", data, flattened)

	for key, value := range flattened {
		info.ConfigurationValues = append(info.ConfigurationValues, &basev0.ConfigurationValue{
			Key:    key,
			Value:  value,
			Secret: isSecret,
		})
	}
	return info, nil
}

func flattenMap(prefix string, m map[string]interface{}, result map[string]string) {
	for k, v := range m {
		newKey := k
		if prefix != "" {
			newKey = prefix + "." + k
		}

		switch vv := v.(type) {
		case map[string]interface{}:
			flattenMap(newKey, vv, result)
		default:
			result[newKey] = fmt.Sprintf("%v", v)
		}
	}
}

// ExtractFromPath gets modules/app/services/ServiceWithModule and we want to extract app/ServiceWithModule
func ExtractFromPath(p string) string {
	tokens := strings.Split(p, "/")
	if len(tokens) != 4 {
		return ""
	}
	return fmt.Sprintf("%s/%s", tokens[1], tokens[3])
}

// ConsolidateInfo consolidate informations values per name
func ConsolidateInfo(infos []*basev0.ConfigurationInformation) []*basev0.ConfigurationInformation {
	consolidated := make(map[string]*basev0.ConfigurationInformation)
	for _, info := range infos {
		if _, ok := consolidated[info.Name]; !ok {
			consolidated[info.Name] = &basev0.ConfigurationInformation{
				Name: info.Name,
			}
		}
		consolidated[info.Name].ConfigurationValues = append(consolidated[info.Name].ConfigurationValues, info.ConfigurationValues...)
	}
	var result []*basev0.ConfigurationInformation
	for _, info := range consolidated {
		result = append(result, info)
	}
	return result
}
