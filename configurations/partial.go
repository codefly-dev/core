package configurations

type Partial struct {
	Name string `yaml:"name"`

	// Applications in the partial of the project
	Applications []string `yaml:"applications"`
}
