// Package apperror provides error handling utilities.
package apperror

import (
	"encoding/json"
	"net/http"
)

// Class groups errors into HTTP classes.
type Class string

const (
	// ClassBadRequest indicates a bad request error.
	ClassBadRequest Class = "bad_request"
	// ClassUnauthorized indicates an unauthorized error.
	ClassUnauthorized Class = "unauthorized"
	// ClassForbidden indicates a forbidden error.
	ClassForbidden Class = "forbidden"
	// ClassNotFound indicates a not found error.
	ClassNotFound Class = "not_found"
	// ClassConflict indicates a conflict error.
	ClassConflict Class = "conflict"
	// ClassTooManyRequests indicates a rate-limited error.
	ClassTooManyRequests Class = "too_many_requests"
	// ClassInternal indicates an internal error.
	ClassInternal Class = "internal"
	// ClassUnavailable indicates an upstream/dependency outage.
	ClassUnavailable Class = "unavailable"
)

// Error is the server error envelope source.
type Error struct {
	Class   Class
	Code    string
	Message string
	Details map[string]any
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

// New creates a typed application error.
func New(class Class, code, message string, details map[string]any) *Error {
	return &Error{Class: class, Code: code, Message: message, Details: details}
}

// Status maps a class to HTTP status.
func Status(class Class) int {
	switch class {
	case ClassBadRequest:
		return http.StatusBadRequest
	case ClassUnauthorized:
		return http.StatusUnauthorized
	case ClassForbidden:
		return http.StatusForbidden
	case ClassNotFound:
		return http.StatusNotFound
	case ClassConflict:
		return http.StatusConflict
	case ClassTooManyRequests:
		return http.StatusTooManyRequests
	case ClassInternal:
		return http.StatusInternalServerError
	case ClassUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// Write writes a JSON error response.
func Write(w http.ResponseWriter, code string, class Class, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(Status(class))
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	})
}
