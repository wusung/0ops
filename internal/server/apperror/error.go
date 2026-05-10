package apperror

import (
	"encoding/json"
	"net/http"
)

// Class groups errors into HTTP classes.
type Class string

const (
	ClassBadRequest  Class = "bad_request"
	ClassUnauthorized Class = "unauthorized"
	ClassForbidden    Class = "forbidden"
	ClassNotFound     Class = "not_found"
	ClassInternal     Class = "internal"
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
	case ClassInternal:
		return http.StatusInternalServerError
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
