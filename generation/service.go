package generation

const ServiceGenerationConfigurationName = "service.generation.codefly.yaml"

type Base struct {
	Domain string `yaml:"domain"`
	Name   string `yaml:"name"`
}

type Service struct {
	Base    Base     `yaml:"base"`
	Ignores []string `yaml:"ignores"`
}
