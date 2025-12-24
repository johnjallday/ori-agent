package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// ParseJSONBody reads and parses a JSON request body into the provided struct.
// It handles common error cases and returns appropriate HTTP errors.
//
// Usage:
//
//	var req CreateAgentRequest
//	if !http.ParseJSONBody(w, r, &req) {
//		return // Error response already sent
//	}
//	// Use req...
func ParseJSONBody(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if r.Body == nil {
		if err := RespondBadRequest(w, "Request body is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return false
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		if err := RespondBadRequest(w, "Failed to read request body"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return false
	}

	if len(body) == 0 {
		if err := RespondBadRequest(w, "Request body is empty"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return false
	}

	if err := json.Unmarshal(body, v); err != nil {
		if err := RespondBadRequest(w, "Invalid JSON: "+err.Error()); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return false
	}

	return true
}

// RequireMethod checks if the request method matches the expected method.
// If not, it sends a 405 Method Not Allowed response and returns false.
//
// Usage:
//
//	if !http.RequireMethod(w, r, http.MethodPost) {
//		return // Error response already sent
//	}
func RequireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		if err := RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return false
	}
	return true
}

// RequireMethods checks if the request method is one of the allowed methods.
// If not, it sends a 405 Method Not Allowed response and returns false.
//
// Usage:
//
//	if !http.RequireMethods(w, r, http.MethodGet, http.MethodPost) {
//		return // Error response already sent
//	}
func RequireMethods(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, m := range methods {
		if r.Method == m {
			return true
		}
	}
	if err := RespondMethodNotAllowed(w); err != nil {
		logger.Error("Failed to write response", logger.Fields{"error": err})
	}
	return false
}

// GetQueryParam returns a query parameter value, or the default if not present.
//
// Usage:
//
//	name := http.GetQueryParam(r, "name", "default-agent")
func GetQueryParam(r *http.Request, key, defaultValue string) string {
	value := r.URL.Query().Get(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// RequireQueryParam checks if a required query parameter is present.
// If not, it sends a 400 Bad Request response and returns empty string.
//
// Usage:
//
//	name := http.RequireQueryParam(w, r, "name")
//	if name == "" {
//		return // Error response already sent
//	}
func RequireQueryParam(w http.ResponseWriter, r *http.Request, key string) string {
	value := r.URL.Query().Get(key)
	if value == "" {
		if err := RespondBadRequest(w, key+" is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return ""
	}
	return value
}
