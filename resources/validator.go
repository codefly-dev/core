package resources

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/bufbuild/protovalidate-go"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"google.golang.org/protobuf/proto"
)

var validator protovalidate.Validator

func init() {
	var initErr error
	validator, initErr = protovalidate.New()
	if initErr != nil {
		panic(initErr)
	}
}

func Validate(req proto.Message) error {
	err := validator.Validate(req)
	if err != nil {
		msgType := reflect.TypeOf(req).Elem().Name()
		var vErr *protovalidate.ValidationError
		if errors.As(err, &vErr) {
			var errDetails []string
			var fieldsViolation []*errdetails.BadRequest_FieldViolation
			for _, violation := range vErr.Violations {
				// Directly use the fields from the Violation struct
				fieldPath := violation.Proto.Field.String()
				message_ := violation.Proto.Message
				if message_ == nil {
					continue
				}
				message := *message_
				if fieldPath == "" {
					continue
				}

				errDetails = append(errDetails, fmt.Sprintf("field '%s': %s", fieldPath, message))
				fieldsViolation = append(fieldsViolation, &errdetails.BadRequest_FieldViolation{
					Field:       fieldPath,
					Description: message,
				})
			}
			detailedErr := fmt.Errorf("invalid %s: %s", msgType, strings.Join(errDetails, "; "))

			st := status.New(codes.InvalidArgument, detailedErr.Error())
			st, _ = st.WithDetails(&errdetails.BadRequest{
				FieldViolations: fieldsViolation,
			})
			return st.Err()
		}

		// If it's not a ValidationError, return a generic error
		return status.Errorf(codes.InvalidArgument, "invalid %s: %v", msgType, err)
	}
	return nil
}
