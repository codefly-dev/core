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

// ParsePinnedImage parses a "name:tag" image reference for a plugin's
// runtimeImage override. Unlike NewDockerImage, it REJECTS untagged
// references ("foo") and explicit ":latest" tags — our mode-consistency
// policy requires every runtime image pin to be a specific version
// so builds are reproducible. Callers typically feed this a value
// from a plugin's Settings.DockerImage field.
//
// Accepts:  "codeflydev/python:0.0.1", "my.registry/foo:1.2.3"
// Rejects:  "foo" (no tag), "foo:latest" (floating), "foo:1:2" (malformed)
func ParsePinnedImage(s string) (*DockerImage, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("image override is empty")
	}
	tokens := strings.Split(s, ":")
	if len(tokens) != 2 {
		return nil, fmt.Errorf("image override must be name:tag (got %q)", s)
	}
	name, tag := tokens[0], tokens[1]
	if name == "" || tag == "" {
		return nil, fmt.Errorf("image override has empty name or tag (got %q)", s)
	}
	if tag == "latest" {
		return nil, fmt.Errorf("image override cannot use :latest — pin a specific version (got %q)", s)
	}
	return &DockerImage{Name: name, Tag: tag}, nil
}
