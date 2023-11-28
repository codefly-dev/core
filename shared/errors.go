package shared

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/go-multierror"

	"github.com/codefly-dev/golor"
)

func HandleError(err error) {
	if err != nil {
		panic(err)
	}
}

func ParseError(s string) error {
	if msg, ok := strings.CutPrefix(s, "OutError: "); ok {
		return NewOutputError(msg)
	}
	panic("not implemented")
}

/*
Useful errors
*/

// UserError: something went quite wrong from the user "side"

type UserError struct {
	Value      string
	Suggestion string
}

func (u *UserError) WithSuggestion(s string) *UserError {
	u.Suggestion = s
	return u
}

func (u *UserError) Error() string {
	return golor.Sprintf(`{{.Value}}
{{.Suggestion}}`, u)
}

func NewUserError(format string, args ...any) *UserError {
	return &UserError{Value: fmt.Sprintf(format, args...)}
}

func IsUserError(err error) bool {
	var userError *UserError
	ok := errors.As(err, &userError)
	return ok
}

func UserErrorMessage(err error) string {
	var userError *UserError
	ok := errors.As(err, &userError)
	if !ok {
		Exit("should have a user error: got %T", err)
	}
	return strings.TrimSpace(userError.Error())
}

// UserWarning: something went somewhat wrong from the user "side"

type UserWarning struct {
	value string
}

func (u *UserWarning) Error() string {
	return u.value
}

func NewUserWarning(format string, args ...any) error {
	return &UserWarning{value: fmt.Sprintf(format, args...)}
}

func IsUserWarning(err error) bool {
	var userWarning *UserWarning
	ok := errors.As(err, &userWarning)
	return ok
}

func UserWarnMessage(err error) string {
	var userWarning *UserWarning
	ok := errors.As(err, &userWarning)
	if ok {
		return userWarning.Error()
	}

	if debug {
		fmt.Printf("should have a user warning: got %T\n", err)
	}
	return ""
}

// OutputError: encapsulates the output of a command

type OutputError struct {
	value string
}

func (u *OutputError) Error() string {
	return u.value
}

func NewOutputError(format string, args ...any) error {
	return &OutputError{value: fmt.Sprintf(format, args...)}
}

func IsOutputError(err error) (error, bool) {
	if err == nil {
		return nil, false
	}
	var outputError *OutputError
	ok := errors.As(err, &outputError)
	return outputError, ok
}

func MultiErrors(errs ...error) error {
	var result error
	out := multierror.Append(result, errs...)
	return out.ErrorOrNil()
}
