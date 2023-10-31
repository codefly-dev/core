package configurations

const ProviderConfigurationName = "provider.codefly.yaml"

type Provider struct {
	Kind   string  `yaml:"kind"`
	Name   string  `yaml:"name"`
	Plugin *Plugin `yaml:"plugin"`
}

func NewProvider(name string, plugin *Plugin) (*Provider, error) {
	// logger := shared.NewLogger("configurations.NewProvider")
	return &Provider{
		Kind:   "provider",
		Name:   name,
		Plugin: plugin,
	}, nil
}

func (p *Provider) Reference() (*ProviderReference, error) {
	return &ProviderReference{
		Name: p.Name,
	}, nil
}
