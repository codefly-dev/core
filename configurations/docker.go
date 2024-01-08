package configurations

import "fmt"

type DockerImage struct {
	Repository string
	Name       string
	Tag        string
}

func (image *DockerImage) FullName() string {
	base := fmt.Sprintf("%s:%s", image.Name, image.Tag)
	if image.Repository == "" {
		return base
	}
	return fmt.Sprintf("%s/%s", image.Repository, base)
}
