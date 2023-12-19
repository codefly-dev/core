package actions

import (
	"context"

	"github.com/bufbuild/protovalidate-go"
	"google.golang.org/protobuf/proto"
)

type Config struct {
	Kind string `json:"kind"`
}

// Validate action input
func Validate(_ context.Context, input proto.Message) error {
	v, err := protovalidate.New()

	if err != nil {
		return err
	}
	err = v.Validate(input)
	if err != nil {
		return err
	}
	return nil
}
