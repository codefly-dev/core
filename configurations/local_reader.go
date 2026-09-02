package configurations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/wool"
)

// ErrConfigurationConflict marks configuration definitions that cannot be
// composed into one logical configuration. Callers should use errors.Is
// instead of matching the human-readable diagnostic.
var ErrConfigurationConflict = errors.New("configuration definitions conflict")

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

	configurationProfile, err := env.ConfigurationProfileName()
	if err != nil {
		return w.Wrapf(err, "cannot select configuration profile")
	}

	// Create a provider folder for local development
	configurationDir := path.Join(local.workspace.Dir(), "configurations", configurationProfile)
	_, err = shared.CheckDirectoryOrCreate(ctx, configurationDir)
	if err != nil {
		return w.Wrapf(err, "cannot create configuration directory")
	}

	workspaceInfos, err := LoadConfigurationInformationsFromFiles(ctx, configurationDir)
	if err != nil {
		return w.Wrapf(err, "cannot load configurations")
	}
	workspaceInfos, err = local.composeModuleWorkspaceConfigurations(ctx, workspaceInfos, configurationDir, configurationProfile)
	if err != nil {
		return w.Wrapf(err, "cannot compose module workspace configurations")
	}
	workspaceInfos, err = applyWorkspaceConfigurationOverrides(workspaceInfos, os.Getenv(resources.WorkspaceConfigurationOverridesEnvironment))
	if err != nil {
		return w.Wrapf(err, "cannot load invocation-scoped workspace configurations")
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

	serviceConfs := make(map[string]*basev0.Configuration)
	serviceOrigins := make([]string, 0, len(services))
	for _, svc := range services {
		identity, err := svc.Identity()
		if err != nil {
			return w.Wrapf(err, "cannot get service identity")
		}
		serviceOrigins = append(serviceOrigins, identity.Unique())
		serviceConfDir := path.Join(svc.Dir(), "configurations", configurationProfile)
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
				if _, ok := serviceConfs[identity.Unique()]; !ok {
					serviceConfs[identity.Unique()] = &basev0.Configuration{
						Origin: identity.Unique(),
					}
				}
				for _, info := range serviceInfos {
					serviceConfs[identity.Unique()].Infos = append(serviceConfs[identity.Unique()].Infos, info)
				}
			}
		}
		// Load DNS
		serviceDNSDir := path.Join(svc.Dir(), "dns", configurationProfile)
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
				d.Service = identity.Name
				d.Module = identity.Module
				w.Debug("found DNS", wool.Field("dns", resources.MakeDNSSummary(d)))
				local.dns = append(local.dns, d)
			}
		}
	}
	serviceConfs, err = applyServiceConfigurationOverrides(
		serviceConfs,
		serviceOrigins,
		os.Getenv(resources.ServiceConfigurationOverridesEnvironment),
	)
	if err != nil {
		return w.Wrapf(err, "cannot load invocation-scoped service configurations")
	}
	serviceNames := make([]string, 0, len(serviceConfs))
	for name := range serviceConfs {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)
	for _, name := range serviceNames {
		confs = append(confs, serviceConfs[name])
	}
	local.configurations = confs
	return nil
}

// composeModuleWorkspaceConfigurations brings each referenced module's own
// workspace configurations into the consuming workspace's configuration space.
// A module composed from another repo carries its configurations/<profile>/*
// with it; without this, the composed module's services cannot satisfy their
// workspace-configuration-dependencies from the consuming workspace alone —
// which is what makes a solution-as-root boot fail on the composed host's
// config-dependencies.
//
// The consuming workspace wins on a name conflict, so a solution can override a
// composed configuration by declaring one of the same name, but is never
// required to redeclare every configuration the host brings. A collision
// between two composed modules is genuine ambiguity with no principled winner —
// matching how the loader hard-errors on conflicting definitions within a
// single directory, it is a loud error rather than a silent, order-dependent
// pick; the solution resolves it by declaring its own configuration of that
// name. Secret material follows the same typed path as any other workspace
// configuration: reference values resolve lazily once a dependency selects them.
func (local *ConfigurationInformationLocalReader) composeModuleWorkspaceConfigurations(
	ctx context.Context,
	workspaceInfos []*basev0.ConfigurationInformation,
	workspaceConfigurationDir string,
	configurationProfile string,
) ([]*basev0.ConfigurationInformation, error) {
	w := wool.Get(ctx).In("ConfigurationInformationLocalReader.composeModuleWorkspaceConfigurations")

	modules, err := local.workspace.LoadModules(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load modules")
	}
	fromWorkspace := make(map[string]bool, len(workspaceInfos))
	for _, info := range workspaceInfos {
		fromWorkspace[info.Name] = true
	}
	providedBy := make(map[string]string)
	for _, mod := range modules {
		moduleConfigurationDir := path.Join(mod.Dir(), "configurations", configurationProfile)
		// The consuming workspace's own configurations are already loaded; a flat
		// root module's directory coincides with the workspace's, so skip it
		// rather than load the same directory twice.
		if resources.SameDir(moduleConfigurationDir, workspaceConfigurationDir) {
			continue
		}
		exists, err := shared.DirectoryExists(ctx, moduleConfigurationDir)
		if err != nil {
			return nil, w.Wrapf(err, "cannot check module configuration directory")
		}
		if !exists {
			continue
		}
		moduleInfos, err := LoadConfigurationInformationsFromFiles(ctx, moduleConfigurationDir)
		if err != nil {
			return nil, w.Wrapf(err, "cannot load configurations for composed module %s", mod.Name)
		}
		for _, info := range moduleInfos {
			if fromWorkspace[info.Name] {
				w.Debug("workspace configuration overrides composed module configuration",
					wool.Field("configuration", info.Name), wool.Field("module", mod.Name))
				continue
			}
			if other, ok := providedBy[info.Name]; ok {
				return nil, w.NewError(
					"workspace configuration %q is provided by composed modules %q and %q; declare it in the solution workspace to resolve the ambiguity: %w",
					info.Name, other, mod.Name, ErrConfigurationConflict)
			}
			providedBy[info.Name] = mod.Name
			workspaceInfos = append(workspaceInfos, info)
		}
	}
	return workspaceInfos, nil
}

// applyWorkspaceConfigurationOverrides merges the SDK's invocation-scoped
// values over persisted workspace configuration. The merge happens at the
// Codefly configuration boundary, before dependency validation and runtime
// injection, so services receive the same typed Configuration objects in
// persisted and ephemeral runs.
func applyWorkspaceConfigurationOverrides(
	infos []*basev0.ConfigurationInformation,
	encoded string,
) ([]*basev0.ConfigurationInformation, error) {
	overrides, err := resources.DecodeWorkspaceConfigurationOverrides(encoded)
	if err != nil {
		return nil, err
	}
	for _, override := range overrides {
		infos = applyConfigurationValueOverride(infos, override.Name, override.Key, override.Value, override.Secret)
	}
	return infos, nil
}

// applyServiceConfigurationOverrides merges SDK-owned invocation values over
// persisted service configuration after validating every target against the
// loaded workspace. This is deliberately earlier than Manager validation and
// agent startup, so ephemeral and persisted values follow one typed path.
func applyServiceConfigurationOverrides(
	confs map[string]*basev0.Configuration,
	serviceOrigins []string,
	encoded string,
) (map[string]*basev0.Configuration, error) {
	overrides, err := resources.DecodeServiceConfigurationOverrides(encoded)
	if err != nil {
		return nil, err
	}
	// With nothing to apply there is nothing to resolve: building (and
	// ambiguity-checking) the origin index when the feature is unused would
	// let an unrelated coordinate collision fail an otherwise valid load.
	if len(overrides) == 0 {
		return confs, nil
	}
	known := make(map[string]string, len(serviceOrigins))
	for _, origin := range serviceOrigins {
		coordinate := resources.UniqueToKey(origin)
		if existing, duplicate := known[coordinate]; duplicate && existing != origin {
			return nil, fmt.Errorf("service configuration override target is ambiguous between %s and %s", existing, origin)
		}
		known[coordinate] = origin
	}
	for _, override := range overrides {
		origin, ok := known[resources.UniqueToKey(override.Service)]
		if !ok {
			return nil, fmt.Errorf("service configuration override targets unknown service %s", override.Service)
		}
		conf := confs[origin]
		if conf == nil {
			conf = &basev0.Configuration{Origin: origin}
			confs[origin] = conf
		}
		conf.Infos = applyConfigurationValueOverride(conf.Infos, override.Name, override.Key, override.Value, override.Secret)
	}
	return confs, nil
}

func applyConfigurationValueOverride(
	infos []*basev0.ConfigurationInformation,
	name, key, value string,
	secret bool,
) []*basev0.ConfigurationInformation {
	var info *basev0.ConfigurationInformation
	for _, candidate := range infos {
		if resources.Match(candidate.Name, name) {
			info = candidate
			break
		}
	}
	if info == nil {
		info = &basev0.ConfigurationInformation{Name: name}
		infos = append(infos, info)
	}
	var configurationValue *basev0.ConfigurationValue
	for _, candidate := range info.ConfigurationValues {
		if resources.Match(candidate.Key, key) {
			configurationValue = candidate
			break
		}
	}
	if configurationValue == nil {
		configurationValue = &basev0.ConfigurationValue{Key: key}
		info.ConfigurationValues = append(info.ConfigurationValues, configurationValue)
	}
	configurationValue.Value = value
	configurationValue.Secret = secret
	return infos
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

type configurationFile struct {
	path          string
	relative      string
	name          string
	kind          string
	secret        bool
	referenceOnly bool
}

// FromService satisfies this format
// - Name
// - unique:Name
func FromService(service *resources.ServiceIdentity, dep string) (*ConfigurationSource, error) {
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

	var files []*configurationFile
	err = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return w.Wrapf(err, "cannot walk p")
		}
		if p == dir || info.IsDir() {
			return nil
		}
		if strings.Contains(filepath.Base(p), ".codefly.") {
			return nil
		}
		file, ok, err := classifyConfigurationFile(dir, p)
		if err != nil {
			return err
		}
		if ok {
			files = append(files, file)
		}
		return nil
	})

	if err != nil {
		return nil, w.Wrapf(err, "cannot walk providers directory")
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].relative < files[j].relative
	})
	if err := validateConfigurationDefinitions(files); err != nil {
		return nil, w.Wrapf(err, "cannot load secret configurations")
	}

	infos := make([]*basev0.ConfigurationInformation, 0, len(files))
	for _, file := range files {
		var confInfo *basev0.ConfigurationInformation
		switch file.kind {
		case "env":
			confInfo, err = loadFromEnvFile(ctx, file)
		case "yaml":
			confInfo, err = loadFromYamlFile(ctx, file)
		}
		if err != nil {
			return nil, w.Wrapf(err, "cannot load configuration from %s", file.relative)
		}
		w.Trace("loaded configuration", wool.Field("configuration", confInfo.Name))
		infos = append(infos, confInfo)
	}
	consolidated, err := ConsolidateInfo(ctx, infos)
	if err != nil {
		return nil, w.Wrapf(err, "cannot consolidate infos")
	}
	w.Trace("loaded infos", wool.SliceCountField(consolidated))
	return consolidated, nil
}

func classifyConfigurationFile(dir, p string) (*configurationFile, bool, error) {
	relative, err := filepath.Rel(dir, p)
	if err != nil {
		return nil, false, fmt.Errorf("cannot get relative configuration path: %w", err)
	}
	relative = filepath.ToSlash(relative)
	file := &configurationFile{path: p, relative: relative}
	switch {
	case strings.HasSuffix(relative, ".secret.ref.env"):
		file.name = strings.TrimSuffix(relative, ".secret.ref.env")
		file.kind = "env"
		file.secret = true
		file.referenceOnly = true
	case strings.HasSuffix(relative, ".secret.ref.yaml"):
		file.name = strings.TrimSuffix(relative, ".secret.ref.yaml")
		file.kind = "yaml"
		file.secret = true
		file.referenceOnly = true
	case strings.HasSuffix(relative, ".secret.env"):
		file.name = strings.TrimSuffix(relative, ".secret.env")
		file.kind = "env"
		file.secret = true
	case strings.HasSuffix(relative, ".secret.yaml"):
		file.name = strings.TrimSuffix(relative, ".secret.yaml")
		file.kind = "yaml"
		file.secret = true
	case strings.HasSuffix(relative, ".env"):
		file.name = strings.TrimSuffix(relative, ".env")
		file.kind = "env"
	case strings.HasSuffix(relative, ".yaml"):
		file.name = strings.TrimSuffix(relative, ".yaml")
		file.kind = "yaml"
	default:
		return nil, false, nil
	}
	return file, true, nil
}

func validateConfigurationDefinitions(files []*configurationFile) error {
	type configurationDefinitions struct {
		legacy    string
		reference string
		first     string
		data      string
	}
	seen := make(map[string]configurationDefinitions)
	for _, file := range files {
		definition := seen[file.name]
		if file.secret {
			if file.referenceOnly {
				if definition.reference != "" {
					return fmt.Errorf("configuration %q has duplicate reference-only secret definitions in %q and %q: %w", file.name, definition.reference, file.relative, ErrConfigurationConflict)
				}
				if definition.legacy != "" {
					return fmt.Errorf("configuration %q is defined by plaintext-capable secret file %q and reference-only manifest %q: %w", file.name, definition.legacy, file.relative, ErrConfigurationConflict)
				}
				definition.reference = file.relative
			} else {
				if definition.reference != "" {
					return fmt.Errorf("configuration %q is defined by reference-only manifest %q and plaintext-capable secret file %q: %w", file.name, definition.reference, file.relative, ErrConfigurationConflict)
				}
				definition.legacy = file.relative
			}
		}
		if file.kind == "yaml" && definition.first != "" {
			return fmt.Errorf("configuration %q has incompatible data definitions in %q and %q: %w", file.name, definition.first, file.relative, ErrConfigurationConflict)
		}
		if definition.data != "" {
			return fmt.Errorf("configuration %q has incompatible data definitions in %q and %q: %w", file.name, definition.data, file.relative, ErrConfigurationConflict)
		}
		if definition.first == "" {
			definition.first = file.relative
		}
		if file.kind == "yaml" {
			definition.data = file.relative
		}
		seen[file.name] = definition
	}
	return nil
}

func loadFromEnvFile(ctx context.Context, file *configurationFile) (*basev0.ConfigurationInformation, error) {
	w := wool.Get(ctx).In("provider.loadFromEnvFile")

	f, err := os.ReadFile(file.path)
	if err != nil {
		return nil, w.Wrapf(err, "cannot read env file")
	}
	info := &basev0.ConfigurationInformation{
		Name: file.name,
	}
	lines := strings.Split(string(f), "\n")

	for index, line := range lines {
		if file.referenceOnly {
			line = strings.TrimSuffix(line, "\r")
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
		}
		tokens := strings.SplitN(line, "=", 2)
		if len(tokens) < 2 {
			if file.referenceOnly {
				return nil, w.NewError("invalid reference-only env entry on line %d", index+1)
			}
			continue
		}
		if file.referenceOnly {
			key := strings.TrimSpace(tokens[0])
			if key == "" {
				return nil, w.NewError("reference-only env entry on line %d has an empty key", index+1)
			}
			if err := validateReferenceOnlySecret(tokens[1]); err != nil {
				return nil, w.Wrapf(err, "reference-only secret %q on line %d", key, index+1)
			}
		}
		info.ConfigurationValues = append(info.ConfigurationValues, &basev0.ConfigurationValue{
			Key:    tokens[0],
			Value:  tokens[1],
			Secret: file.secret,
		})
	}
	return info, nil
}

func loadFromYamlFile(ctx context.Context, file *configurationFile) (*basev0.ConfigurationInformation, error) {
	info, err := ConfigurationInformationDataFromFile(ctx, file.name, file.path, file.secret)
	if err != nil {
		return nil, err
	}
	if file.referenceOnly {
		if err := validateReferenceOnlyYAML(info.Data.Content); err != nil {
			return nil, err
		}
	}
	return info, nil
}

func validateReferenceOnlyYAML(content []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if err == io.EOF {
			return fmt.Errorf("reference-only YAML manifest is empty")
		}
		return fmt.Errorf("cannot parse reference-only YAML manifest: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("reference-only YAML manifest must contain exactly one document")
		}
		return fmt.Errorf("cannot parse reference-only YAML manifest: %w", err)
	}
	return validateReferenceOnlyYAMLNode(&document, "$")
}

func validateReferenceOnlyYAMLNode(node *yaml.Node, keyPath string) error {
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) != 1 {
			return fmt.Errorf("reference-only YAML manifest is empty")
		}
		return validateReferenceOnlyYAMLNode(node.Content[0], keyPath)
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if err := validateReferenceOnlyYAMLNode(node.Content[i+1], appendSecretKeyPath(keyPath, key.Value)); err != nil {
				return err
			}
		}
		return nil
	case yaml.SequenceNode:
		for i, child := range node.Content {
			if err := validateReferenceOnlyYAMLNode(child, fmt.Sprintf("%s[%d]", keyPath, i)); err != nil {
				return err
			}
		}
		return nil
	case yaml.AliasNode:
		return validateReferenceOnlyYAMLNode(node.Alias, keyPath)
	case yaml.ScalarNode:
		if node.Tag != "!!str" {
			return fmt.Errorf("secret at %s on line %d is plaintext; use a supported provider reference", keyPath, node.Line)
		}
		if err := validateReferenceOnlySecret(node.Value); err != nil {
			return fmt.Errorf("secret at %s on line %d: %w", keyPath, node.Line, err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported YAML node at %s", keyPath)
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
func ConsolidateInfo(_ context.Context, infos []*basev0.ConfigurationInformation) ([]*basev0.ConfigurationInformation, error) {
	ordered := append([]*basev0.ConfigurationInformation(nil), infos...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Name < ordered[j].Name
	})
	consolidated := make(map[string]*basev0.ConfigurationInformation)
	for _, info := range ordered {
		if info.Data != nil {
			if _, found := consolidated[info.Name]; found {
				return nil, fmt.Errorf("duplicate configuration data: %s: %w", info.Name, ErrConfigurationConflict)
			}
			consolidated[info.Name] = info
			continue
		}
		if existing, ok := consolidated[info.Name]; ok && existing.Data != nil {
			return nil, fmt.Errorf("duplicate configuration data: %s: %w", info.Name, ErrConfigurationConflict)
		}
		if _, ok := consolidated[info.Name]; !ok {
			consolidated[info.Name] = &basev0.ConfigurationInformation{
				Name: info.Name,
			}
		}
		consolidated[info.Name].ConfigurationValues = append(consolidated[info.Name].ConfigurationValues, info.ConfigurationValues...)
	}
	names := make([]string, 0, len(consolidated))
	for name := range consolidated {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]*basev0.ConfigurationInformation, 0, len(names))
	for _, name := range names {
		result = append(result, consolidated[name])
	}
	return result, nil
}
