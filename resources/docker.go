package resources

import (
	"fmt"
	"strings"
)

type DockerImage struct {
	Repository string
	Name       string
	Tag        string
}

func (image *DockerImage) FullName() string {
	base := image.Name
	if image.Tag != "" {
		base = fmt.Sprintf("%s:%s", image.Name, image.Tag)
	}
	if image.Repository == "" {
		return base
	}
	return fmt.Sprintf("%s/%s", image.Repository, base)
}

func NewDockerImage(s string) *DockerImage {
	tokens := strings.Split(s, ":")
	if len(tokens) == 1 {
		return &DockerImage{
			Name: tokens[0],
			Tag:  "latest",
		}
	}
	if len(tokens) == 2 {
		return &DockerImage{
			Name: tokens[0],
			Tag:  tokens[1],
		}
	}
	return nil
}
