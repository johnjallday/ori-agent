package http

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
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
		BadRequest(w, "Request body is required")
		return false
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		BadRequest(w, "Failed to read request body")
		return false
	}

	if len(body) == 0 {
		BadRequest(w, "Request body is empty")
		return false
	}

	if err := json.Unmarshal(body, v); err != nil {
		BadRequest(w, "Invalid JSON: "+err.Error())
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
		MethodNotAllowed(w)
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
	MethodNotAllowed(w)
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
		BadRequest(w, key+" is required")
		return ""
	}
	return value
}

// MaxFormSize is the maximum size for multipart form data (10 MB)
const MaxFormSize = 10 << 20 // 10 MB

// ParseFormData parses multipart form data, falling back to regular form parsing.
// Returns true if parsing succeeded, false if an error response was sent.
//
// Usage:
//
//	if !http.ParseFormData(w, r) {
//		return // Error response already sent
//	}
func ParseFormData(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseMultipartForm(MaxFormSize); err != nil {
		if err := r.ParseForm(); err != nil {
			BadRequest(w, "Failed to parse form data: "+err.Error())
			return false
		}
	}
	return true
}

// ValidateUploadFilename sanitizes and validates an uploaded filename.
// Returns the sanitized filename and true if valid, or sends an error response and returns false.
//
// Security checks performed:
//   - Path traversal prevention (uses filepath.Base)
//   - Empty/invalid filename rejection
//   - Hidden file rejection (files starting with .)
//   - Character validation (only alphanumeric, hyphens, underscores, dots allowed)
//
// Usage:
//
//	cleanName, ok := http.ValidateUploadFilename(w, header.Filename)
//	if !ok {
//		return // Error response already sent
//	}
func ValidateUploadFilename(w http.ResponseWriter, filename string) (string, bool) {
	// Sanitize filename to prevent path traversal attacks
	cleanFilename := filepath.Base(filename)
	if cleanFilename == "" || cleanFilename == "." || cleanFilename == ".." {
		BadRequest(w, "Invalid filename")
		return "", false
	}

	// Reject hidden files (files starting with .)
	if strings.HasPrefix(cleanFilename, ".") {
		BadRequest(w, "Hidden files not allowed")
		return "", false
	}

	// Allow common filename characters: alphanumeric, hyphens, underscores, dots, spaces, parentheses
	// Replace any disallowed characters with underscores
	var sanitized strings.Builder
	for _, c := range cleanFilename {
		isLower := c >= 'a' && c <= 'z'
		isUpper := c >= 'A' && c <= 'Z'
		isDigit := c >= '0' && c <= '9'
		isAllowed := c == '-' || c == '_' || c == '.' || c == ' ' || c == '(' || c == ')'
		if isLower || isUpper || isDigit || isAllowed {
			sanitized.WriteRune(c)
		} else {
			sanitized.WriteRune('_')
		}
	}

	return sanitized.String(), true
}
