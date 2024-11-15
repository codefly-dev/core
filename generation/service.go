package generation

const ServiceGenerationConfigurationName = "service.generation.codefly.yaml"

type Replacement struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

type Service struct {
	Replacements   []Replacement `yaml:"replacements"`
	Ignores        []string      `yaml:"ignores"`
	SkipTemplatize bool          `yaml:"skip-templatize"`
}
