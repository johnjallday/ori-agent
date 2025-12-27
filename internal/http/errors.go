package http

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// Common error codes for consistent error handling across the API
const (
	ErrCodeBadRequest          = "bad_request"
	ErrCodeUnauthorized        = "unauthorized"
	ErrCodeForbidden           = "forbidden"
	ErrCodeNotFound            = "not_found"
	ErrCodeConflict            = "conflict"
	ErrCodeValidation          = "validation_error"
	ErrCodeInternal            = "internal_error"
	ErrCodeServiceUnavailable  = "service_unavailable"
	ErrCodeMethodNotAllowed    = "method_not_allowed"
	ErrCodeNotImplemented      = "not_implemented"
	ErrCodeTooManyRequests     = "too_many_requests"
	ErrCodeUnprocessableEntity = "unprocessable_entity"
)

// APIError represents a structured error response for the API.
// It provides consistent error formatting across all endpoints with
// optional details and request tracking.
//
// Example response:
//
//	{
//	  "code": "not_found",
//	  "message": "Agent 'assistant' not found",
//	  "details": {"agent_name": "assistant"},
//	  "request_id": "req_123"
//	}
type APIError struct {
	Code      string      `json:"code"`                 // Machine-readable error code
	Message   string      `json:"message"`              // Human-readable error message
	Details   interface{} `json:"details,omitempty"`    // Additional context (optional)
	RequestID string      `json:"request_id,omitempty"` // Request tracking ID (optional)
}

// Error implements the error interface, allowing APIError to be used
// as a standard Go error.
func (e *APIError) Error() string {
	if e.Details != nil {
		return fmt.Sprintf("%s: %s (details: %v)", e.Code, e.Message, e.Details)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewAPIError creates a new APIError with the given code and message.
//
// Usage:
//
//	err := http.NewAPIError(http.ErrCodeNotFound, "Agent not found")
func NewAPIError(code, message string) *APIError {
	return &APIError{
		Code:    code,
		Message: message,
	}
}

// WithDetails adds additional context to an APIError.
//
// Usage:
//
//	err := http.NewAPIError(http.ErrCodeNotFound, "Agent not found").
//	    WithDetails(map[string]string{"agent_name": "assistant"})
func (e *APIError) WithDetails(details interface{}) *APIError {
	e.Details = details
	return e
}

// WithRequestID adds a request tracking ID to an APIError.
//
// Usage:
//
//	err := http.NewAPIError(http.ErrCodeInternal, "Database error").
//	    WithRequestID(requestID)
func (e *APIError) WithRequestID(requestID string) *APIError {
	e.RequestID = requestID
	return e
}

// RespondAPIError writes an APIError as a JSON response with the given status code.
// This provides a consistent way to return structured errors across all handlers.
//
// Usage:
//
//	err := http.NewAPIError(http.ErrCodeNotFound, "Agent not found")
//	if encodeErr := http.RespondAPIError(w, http.StatusNotFound, err); encodeErr != nil {
//		log.Printf("Failed to encode error response: %v", encodeErr)
//	}
func RespondAPIError(w http.ResponseWriter, statusCode int, err *APIError) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	return json.NewEncoder(w).Encode(err)
}

// Convenience functions for common HTTP error responses

// RespondBadRequest writes a 400 Bad Request error response.
func RespondBadRequest(w http.ResponseWriter, message string) error {
	return RespondAPIError(w, http.StatusBadRequest,
		NewAPIError(ErrCodeBadRequest, message))
}

// RespondUnauthorized writes a 401 Unauthorized error response.
func RespondUnauthorized(w http.ResponseWriter, message string) error {
	return RespondAPIError(w, http.StatusUnauthorized,
		NewAPIError(ErrCodeUnauthorized, message))
}

// RespondForbidden writes a 403 Forbidden error response.
func RespondForbidden(w http.ResponseWriter, message string) error {
	return RespondAPIError(w, http.StatusForbidden,
		NewAPIError(ErrCodeForbidden, message))
}

// RespondNotFound writes a 404 Not Found error response.
func RespondNotFound(w http.ResponseWriter, message string) error {
	return RespondAPIError(w, http.StatusNotFound,
		NewAPIError(ErrCodeNotFound, message))
}

// RespondConflict writes a 409 Conflict error response.
func RespondConflict(w http.ResponseWriter, message string) error {
	return RespondAPIError(w, http.StatusConflict,
		NewAPIError(ErrCodeConflict, message))
}

// RespondValidationError writes a 422 Unprocessable Entity error response
// for validation failures.
func RespondValidationError(w http.ResponseWriter, message string, details interface{}) error {
	return RespondAPIError(w, http.StatusUnprocessableEntity,
		NewAPIError(ErrCodeValidation, message).WithDetails(details))
}

// RespondInternalError writes a 500 Internal Server Error response.
func RespondInternalError(w http.ResponseWriter, message string) error {
	return RespondAPIError(w, http.StatusInternalServerError,
		NewAPIError(ErrCodeInternal, message))
}

// RespondServiceUnavailable writes a 503 Service Unavailable error response.
func RespondServiceUnavailable(w http.ResponseWriter, message string) error {
	return RespondAPIError(w, http.StatusServiceUnavailable,
		NewAPIError(ErrCodeServiceUnavailable, message))
}

// RespondMethodNotAllowed writes a 405 Method Not Allowed error response.
func RespondMethodNotAllowed(w http.ResponseWriter) error {
	return RespondAPIError(w, http.StatusMethodNotAllowed,
		NewAPIError(ErrCodeMethodNotAllowed, "Method not allowed"))
}

// RespondNotImplemented writes a 501 Not Implemented error response.
func RespondNotImplemented(w http.ResponseWriter, message string) error {
	return RespondAPIError(w, http.StatusNotImplemented,
		NewAPIError(ErrCodeNotImplemented, message))
}

// =============================================================================
// Fire-and-forget response functions
// =============================================================================
// These functions handle logging internally, eliminating boilerplate in handlers.
// Use these when you cannot meaningfully handle response write errors.
//
// Instead of:
//
//	orihttp.BadRequest(w, "message"); respErr != nil {
//		logger.Error("Failed to write response", logger.Fields{"error": respErr})
//	}
//	return
//
// Use:
//
//	orihttp.BadRequest(w, "message")
//	return

// BadRequest writes a 400 Bad Request error and logs any write errors internally.
func BadRequest(w http.ResponseWriter, message string) {
	if err := RespondBadRequest(w, message); err != nil {
		logger.Error("Failed to write response", logger.Fields{"error": err})
	}
}

// Unauthorized writes a 401 Unauthorized error and logs any write errors internally.
func Unauthorized(w http.ResponseWriter, message string) {
	if err := RespondUnauthorized(w, message); err != nil {
		logger.Error("Failed to write response", logger.Fields{"error": err})
	}
}

// Forbidden writes a 403 Forbidden error and logs any write errors internally.
func Forbidden(w http.ResponseWriter, message string) {
	if err := RespondForbidden(w, message); err != nil {
		logger.Error("Failed to write response", logger.Fields{"error": err})
	}
}

// NotFound writes a 404 Not Found error and logs any write errors internally.
func NotFound(w http.ResponseWriter, message string) {
	if err := RespondNotFound(w, message); err != nil {
		logger.Error("Failed to write response", logger.Fields{"error": err})
	}
}

// MethodNotAllowed writes a 405 Method Not Allowed error and logs any write errors internally.
func MethodNotAllowed(w http.ResponseWriter) {
	if err := RespondMethodNotAllowed(w); err != nil {
		logger.Error("Failed to write response", logger.Fields{"error": err})
	}
}

// Conflict writes a 409 Conflict error and logs any write errors internally.
func Conflict(w http.ResponseWriter, message string) {
	if err := RespondConflict(w, message); err != nil {
		logger.Error("Failed to write response", logger.Fields{"error": err})
	}
}

// ValidationError writes a 422 Unprocessable Entity error and logs any write errors internally.
func ValidationError(w http.ResponseWriter, message string, details interface{}) {
	if err := RespondValidationError(w, message, details); err != nil {
		logger.Error("Failed to write response", logger.Fields{"error": err})
	}
}

// InternalError writes a 500 Internal Server Error and logs any write errors internally.
func InternalError(w http.ResponseWriter, message string) {
	if err := RespondInternalError(w, message); err != nil {
		logger.Error("Failed to write response", logger.Fields{"error": err})
	}
}

// ServiceUnavailable writes a 503 Service Unavailable error and logs any write errors internally.
func ServiceUnavailable(w http.ResponseWriter, message string) {
	if err := RespondServiceUnavailable(w, message); err != nil {
		logger.Error("Failed to write response", logger.Fields{"error": err})
	}
}

// NotImplemented writes a 501 Not Implemented error and logs any write errors internally.
func NotImplemented(w http.ResponseWriter, message string) {
	if err := RespondNotImplemented(w, message); err != nil {
		logger.Error("Failed to write response", logger.Fields{"error": err})
	}
}
