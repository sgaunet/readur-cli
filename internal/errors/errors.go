package errors

import (
	"errors"
	"fmt"
)

// CLIError carries an exit-code category plus a user-facing message.
// The command layer returns a CLIError (or wraps one) and main.go runs
// it through Classify to select the process exit code.
type CLIError struct {
	Code    int    // one of the Code* constants in codes.go
	Message string // human-readable, free of stack traces
	Cause   error  // wrapped underlying error (optional)
}

// Error implements the error interface.
//
// If Message and Cause carry the same text (common when wrapping a
// retryablehttp error whose own string is already user-grade), the
// duplicate is suppressed.
func (e *CLIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" && e.Cause != nil {
		if e.Message == e.Cause.Error() {
			return e.Message
		}
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return Name(e.Code)
}

// Unwrap allows errors.Is / errors.As traversal to reach the cause.
func (e *CLIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// New constructs a CLIError with the given code, message, and optional
// cause. The cause may be nil.
func New(code int, msg string, cause error) *CLIError {
	return &CLIError{Code: code, Message: msg, Cause: cause}
}

// Wrap promotes an existing error to a CLIError with the provided code
// and message. If err is already a *CLIError, its Code is preserved
// unless overrideCode is true.
func Wrap(err error, code int, msg string) *CLIError {
	if err == nil {
		return nil
	}
	var cli *CLIError
	if errors.As(err, &cli) {
		return &CLIError{Code: cli.Code, Message: msg, Cause: err}
	}
	return &CLIError{Code: code, Message: msg, Cause: err}
}

// Classify walks the error chain and returns the best-matching exit
// code. Precedence order is documented in exit-codes.md §Precedence
// and enforced by errors_test.go.
//
// If err is nil, returns CodeOK. If no CLIError is found in the chain,
// returns CodeGeneric.
func Classify(err error) int {
	if err == nil {
		return CodeOK
	}
	var cli *CLIError
	if errors.As(err, &cli) {
		return cli.Code
	}
	return CodeGeneric
}
