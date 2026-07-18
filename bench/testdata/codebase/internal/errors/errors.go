package errors

import "fmt"

// ErrCode represents a category of pipeline error.
type ErrCode int

const (
	ErrConfig     ErrCode = iota // configuration errors
	ErrProcessing                // data processing errors
	ErrValidation                // validation errors
	ErrOutput                    // output write errors
	ErrAuth                      // authentication errors
)

// PipelineError is the canonical error type used across the pipeline.
// In practice, some packages return plain errors instead.
type PipelineError struct {
	Code    ErrCode
	Message string
	Cause   error
}

func (e *PipelineError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%d] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

func (e *PipelineError) Unwrap() error {
	return e.Cause
}

// NewConfigError returns a config-category error.
func NewConfigError(msg string, cause error) *PipelineError {
	return &PipelineError{Code: ErrConfig, Message: msg, Cause: cause}
}

// NewProcessingError returns a processing-category error.
func NewProcessingError(msg string, cause error) *PipelineError {
	return &PipelineError{Code: ErrProcessing, Message: msg, Cause: cause}
}

// NewValidationError returns a validation-category error.
func NewValidationError(msg string) *PipelineError {
	return &PipelineError{Code: ErrValidation, Message: msg}
}

// NewOutputError returns an output-category error.
func NewOutputError(msg string, cause error) *PipelineError {
	return &PipelineError{Code: ErrOutput, Message: msg, Cause: cause}
}

// NewAuthError returns an auth-category error.
func NewAuthError(msg string) *PipelineError {
	return &PipelineError{Code: ErrAuth, Message: msg}
}

// IsCode checks whether an error is a PipelineError with the given code.
// Returns false for plain errors, which is a source of inconsistency since
// some packages wrap errors as PipelineError and others do not.
func IsCode(err error, code ErrCode) bool {
	if pe, ok := err.(*PipelineError); ok {
		return pe.Code == code
	}
	return false
}
