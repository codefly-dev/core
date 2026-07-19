package resources

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"buf.build/go/protovalidate"
	"github.com/codefly-dev/core/failures"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
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
				fieldPath := protovalidate.FieldPathString(violation.Proto.GetField())
				message := violation.Proto.GetMessage()
				if fieldPath == "" || message == "" {
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
			failure := failures.New(basev0.FailureCode_FAILURE_CODE_INVALID_ARGUMENT, "validate", detailedErr.Error())
			for _, violation := range fieldsViolation {
				failure.FieldViolations = append(failure.FieldViolations, &basev0.FieldViolation{
					Field:       violation.GetField(),
					Description: violation.GetDescription(),
				})
			}
			st, _ = st.WithDetails(&errdetails.BadRequest{FieldViolations: fieldsViolation}, failure)
			return st.Err()
		}

		// If it's not a ValidationError, return a generic error
		return status.Errorf(codes.InvalidArgument, "invalid %s: %v", msgType, err)
	}
	return nil
}
