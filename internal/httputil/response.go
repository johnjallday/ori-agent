package httputil

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

// RespondError sends a standardized JSON error response
// It automatically sets Content-Type and status code
// If err is not nil, it will be appended to the message
func RespondError(w http.ResponseWriter, status int, message string, err error) {
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

// RespondValidationError sends a 400 Bad Request error for validation failures
// Convenience wrapper around RespondError
func RespondValidationError(w http.ResponseWriter, field, message string) {
	fullMessage := fmt.Sprintf("%s %s", field, message)
	RespondError(w, http.StatusBadRequest, fullMessage, nil)
}

// RespondJSON sends a JSON response with the given status code
// Returns error if encoding fails, allowing caller to handle it
func RespondJSON(w http.ResponseWriter, status int, data interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(data)
}

// RespondSuccess sends a standardized success response with optional data
// Wraps the data in a success envelope: {"success": true, "data": ...}
func RespondSuccess(w http.ResponseWriter, data interface{}) error {
	return RespondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    data,
	})
}

// RespondCreated sends a 201 Created response with optional data
func RespondCreated(w http.ResponseWriter, data interface{}) error {
	return RespondJSON(w, http.StatusCreated, map[string]interface{}{
		"success": true,
		"data":    data,
	})
}

// RespondNoContent sends a 204 No Content response (no body)
func RespondNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}
