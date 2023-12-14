package actions

import (
	"context"

	"github.com/bufbuild/protovalidate-go"
	"github.com/codefly-dev/core/shared"
	"google.golang.org/protobuf/proto"
)

type Config struct {
	Kind string `json:"kind"`
}

// Validate action input
func Validate(ctx context.Context, input proto.Message) error {
	logger := shared.GetLogger(ctx).With("Validate")
	v, err := protovalidate.New()

	if err != nil {
		return logger.Wrap(err)
	}
	err = v.Validate(input)
	if err != nil {
		return logger.Wrap(err)
	}
	return nil
}
