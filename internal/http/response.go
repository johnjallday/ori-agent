package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"

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
	if err := json.NewEncoder(w).Encode(data); err != nil {
		if IsClientDisconnectError(err) {
			return nil
		}
		return err
	}
	return nil
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

// WriteJSON writes a JSON response and logs any encoding errors internally.
// This is a fire-and-forget version of RespondJSON for cases where
// the caller cannot meaningfully handle encoding errors (e.g., response already committed).
//
// This replaces the common anti-pattern of `_ = json.NewEncoder(w).Encode(data)`
// by ensuring errors are at least logged for debugging purposes.
//
// Usage:
//
//	http.WriteJSON(w, data)
func WriteJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		if IsClientDisconnectError(err) {
			return
		}
		logger.Error("Failed to encode JSON response", logger.Fields{"error": err})
	}
}

// =============================================================================
// Fire-and-forget success response functions
// =============================================================================
// These functions handle logging internally, eliminating boilerplate in handlers.
// Use these when you cannot meaningfully handle response write errors.
//
// Instead of:
//
//	if respErr := orihttp.RespondSuccess(w, data); respErr != nil {
//		logger.Error("Failed to write response", logger.Fields{"error": respErr})
//	}
//
// Use:
//
//	orihttp.Success(w, data)

// Success writes a 200 OK JSON response and logs any errors internally.
func Success(w http.ResponseWriter, data interface{}) {
	if err := RespondSuccess(w, data); err != nil {
		logger.Error("Failed to write success response", logger.Fields{"error": err})
	}
}

// Created writes a 201 Created JSON response and logs any errors internally.
func Created(w http.ResponseWriter, data interface{}) {
	if err := RespondCreated(w, data); err != nil {
		logger.Error("Failed to write created response", logger.Fields{"error": err})
	}
}

// WriteContent writes raw content with the specified content type and logs any errors internally.
// Use this for non-JSON responses like HTML, plain text, or binary data.
//
// Usage:
//
//	orihttp.WriteContent(w, "text/html; charset=utf-8", []byte(html))
func WriteContent(w http.ResponseWriter, contentType string, content []byte) {
	w.Header().Set("Content-Type", contentType)
	if _, err := w.Write(content); err != nil {
		if IsClientDisconnectError(err) {
			return
		}
		logger.Error("Failed to write response content", logger.Fields{"error": err})
	}
}

// WriteHTML writes HTML content and logs any errors internally.
func WriteHTML(w http.ResponseWriter, html string) {
	WriteContent(w, "text/html; charset=utf-8", []byte(html))
}

// WriteText writes plain text content and logs any errors internally.
func WriteText(w http.ResponseWriter, text string) {
	WriteContent(w, "text/plain; charset=utf-8", []byte(text))
}

// WriteBytes writes raw bytes to the response and logs any errors internally.
// Unlike WriteContent, this does NOT set Content-Type header, allowing the caller
// to set custom headers before writing. Use this when you need custom caching
// headers or other response customizations.
//
// Usage:
//
//	w.Header().Set("Content-Type", "image/svg+xml")
//	w.Header().Set("Cache-Control", "public, max-age=86400")
//	orihttp.WriteBytes(w, content)
func WriteBytes(w http.ResponseWriter, content []byte) {
	if _, err := w.Write(content); err != nil {
		if IsClientDisconnectError(err) {
			return
		}
		logger.Error("Failed to write response", logger.Fields{"error": err})
	}
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
		if IsClientDisconnectError(encodeErr) {
			return
		}
		logger.Error("Failed to encode error response", logger.Fields{"error": encodeErr})
	}
}

// IsClientDisconnectError returns true if err indicates the HTTP client disconnected
// before the server completed writing the response body.
func IsClientDisconnectError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, net.ErrClosed) ||
			errors.Is(opErr.Err, syscall.EPIPE) ||
			errors.Is(opErr.Err, syscall.ECONNRESET) {
			return true
		}
	}

	var sysErr *os.SyscallError
	if errors.As(err, &sysErr) {
		if errors.Is(sysErr.Err, syscall.EPIPE) || errors.Is(sysErr.Err, syscall.ECONNRESET) {
			return true
		}
	}

	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}

	markers := []string{
		"broken pipe",
		"connection reset by peer",
		"use of closed network connection",
		"client disconnected",
		"http2: stream closed",
		"stream error: stream id",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}

	return false
}
