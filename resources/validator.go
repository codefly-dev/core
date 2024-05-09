package resources

import (
	"fmt"

	"github.com/bufbuild/protovalidate-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

var validator *protovalidate.Validator

func init() {
	v, err := protovalidate.New()
	if err != nil {
		panic(fmt.Errorf("failed to create validator: %w", err))
	}
	validator = v
}

func Validate(req proto.Message) error {
	err := validator.Validate(req)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return nil
}
