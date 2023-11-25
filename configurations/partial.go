package configurations

type Partial struct {
	Name    string `yaml:"name"`
	Project string `yaml:"project"`
	// Applications in the partial of the project
	Applications []string `yaml:"applications"`
}
