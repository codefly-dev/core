package builders

import (
	"context"

	builderv0 "github.com/codefly-dev/core/generated/go/services/builder/v0"
)

type DockerContext struct {
	Repository string
}

type BuildContext = builderv0.BuildContext

func NewDockerBuilderContext(_ context.Context, dockerContext DockerContext) (*BuildContext, error) {
	return &builderv0.BuildContext{
		Kind: &builderv0.BuildContext_DockerBuildContext{
			DockerBuildContext: &builderv0.DockerBuildContext{
				DockerRepository: dockerContext.Repository,
			},
		},
	}, nil
}
