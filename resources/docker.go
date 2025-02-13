package resources

import (
	"errors"
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

func ParseDockerImage(s string) (*DockerImage, error) {
	tokens := strings.Split(s, ":")
	if len(tokens) == 1 {
		return &DockerImage{
			Name: tokens[0],
			Tag:  "latest",
		}, nil
	}
	if len(tokens) == 2 {
		return &DockerImage{
			Name: tokens[0],
			Tag:  tokens[1],
		}, nil
	}
	return nil, errors.New("invalid docker image")
}

// Add constructor for DockerImage
func NewDockerImage(name string) *DockerImage {
	return &DockerImage{
		Name: name,
	}
}
