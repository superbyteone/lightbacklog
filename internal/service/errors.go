// Package service holds LightBacklog's business logic: validation, authorization and
// persistence rules. REST handlers and MCP tools are thin adapters over it.
package service

import (
	"fmt"
	"net/http"
)

// FieldError describes one invalid input field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error is a machine-readable domain error. Code is stable; Message is for humans.
type Error struct {
	Status  int          `json:"-"`
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Fields  []FieldError `json:"errors,omitempty"`
	// Current carries the server's current state on version conflicts so clients can merge.
	Current any `json:"current,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errNotFound(what string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: what + " not found"}
}

func errForbidden(msg string) *Error {
	return &Error{Status: http.StatusForbidden, Code: "forbidden", Message: msg}
}

func errScope() *Error {
	return &Error{Status: http.StatusForbidden, Code: "insufficient_scope", Message: "this API token is read-only"}
}

func errUnauthorized(msg string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: msg}
}

func errConflict(code, msg string) *Error {
	return &Error{Status: http.StatusConflict, Code: code, Message: msg}
}

func errValidation(fields ...FieldError) *Error {
	msg := "request validation failed"
	if len(fields) == 1 {
		msg = fields[0].Field + ": " + fields[0].Message
	}
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: msg, Fields: fields}
}

func invalid(field, format string, args ...any) *Error {
	return errValidation(FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
}
