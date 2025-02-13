package actions

import (
	"context"

	"github.com/bufbuild/protovalidate-go"
	"google.golang.org/protobuf/proto"
)

type Config struct {
	Kind string `json:"kind"`
}

var validator protovalidate.Validator

func init() {
	var err error
	validator, err = protovalidate.New()
	if err != nil {
		panic(err)
	}
}

// Validate action input
func Validate(_ context.Context, input proto.Message) error {
	err := validator.Validate(input)
	if err != nil {
		return err
	}
	return nil
}
