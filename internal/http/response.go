package http

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// ErrorResponse represents a standardized error response
type ErrorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
}

// RespondJSON writes a JSON response with the given status code and data.
// It sets the Content-Type header to application/json and returns any encoding errors.
//
// This function centralizes JSON response handling across all HTTP handlers,
// ensuring consistent behavior and proper error handling. Unlike the common
// pattern of discarding encoding errors with `_ = json.NewEncoder(w).Encode(data)`,
// this function returns the error so callers can log or handle it appropriately.
//
// Usage:
//
//	if err := http.RespondJSON(w, http.StatusOK, data); err != nil {
//		logger.Error("Failed to encode response", logger.Fields{"response": err})
//	}
func RespondJSON(w http.ResponseWriter, statusCode int, data interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	return json.NewEncoder(w).Encode(data)
}

// RespondError writes a JSON error response with the given status code and message.
// It uses a simple error object format: {"error": "message"}.
//
// This provides a consistent error response format across all API endpoints.
//
// Usage:
//
//	if err := http.RespondError(w, http.StatusNotFound, "Agent not found"); err != nil {
//		logger.Error("Failed to write error response", logger.Fields{"response": err})
//	}
func RespondError(w http.ResponseWriter, statusCode int, message string) error {
	return RespondJSON(w, statusCode, map[string]string{
		"error": message,
	})
}

// RespondSuccess is a convenience wrapper for RespondJSON that always returns HTTP 200 OK.
//
// Usage:
//
//	if err := http.RespondSuccess(w, data); err != nil {
//		logger.Error("Failed to encode success response", logger.Fields{"response": err})
//	}
func RespondSuccess(w http.ResponseWriter, data interface{}) error {
	return RespondJSON(w, http.StatusOK, data)
}

// RespondCreated is a convenience wrapper for RespondJSON that returns HTTP 201 Created.
// Typically used when a new resource has been successfully created.
//
// Usage:
//
//	if err := http.RespondCreated(w, newAgent); err != nil {
//		logger.Error("Failed to encode created response", logger.Fields{"response": err})
//	}
func RespondCreated(w http.ResponseWriter, data interface{}) error {
	return RespondJSON(w, http.StatusCreated, data)
}

// RespondNoContent writes a 204 No Content response.
// Used when an operation succeeds but there's no content to return.
//
// Usage:
//
//	http.RespondNoContent(w)
func RespondNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// RespondErrorWithErr sends a standardized JSON error response.
// It automatically sets Content-Type and status code.
// If err is not nil, it will be appended to the message.
//
// Usage:
//
//	RespondErrorWithErr(w, http.StatusBadRequest, "Invalid input", err)
func RespondErrorWithErr(w http.ResponseWriter, status int, message string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	errMsg := message
	if err != nil {
		errMsg = fmt.Sprintf("%s: %v", message, err)
	}

	response := ErrorResponse{
		Success: false,
		Error:   errMsg,
	}

	if encodeErr := json.NewEncoder(w).Encode(response); encodeErr != nil {
		logger.Error("Failed to encode error response", logger.Fields{"error": encodeErr})
	}
}
