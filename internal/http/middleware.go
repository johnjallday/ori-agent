package http

import (
	"net/http"
	"runtime/debug"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// ErrorRecovery returns middleware that recovers from panics in HTTP handlers.
// When a panic occurs, it logs the error with stack trace and returns a
// 500 Internal Server Error response to the client.
//
// This prevents a single handler panic from crashing the entire server
// and provides consistent error responses even in catastrophic failures.
//
// Usage:
//
//	mux := http.NewServeMux()
//	// ... register handlers ...
//	handler := http.ErrorRecovery()(mux)
//	http.ListenAndServe(":8080", handler)
func ErrorRecovery() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					// Log the panic with stack trace
					logger.Error("PANIC in HTTP handler", logger.Fields{
						"method": r.Method,
						"path":   r.URL.Path,
						"error":  err,
						"stack":  string(debug.Stack()),
					})

					// Attempt to send error response
					// If headers were already written, this will have no effect
					if encodeErr := RespondInternalError(w, "Internal server error"); encodeErr != nil {
						logger.Error("Failed to write panic recovery response", logger.Fields{"response": encodeErr})
					}
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// RequestLogger returns middleware that logs HTTP requests with method, path,
// status code, and duration.
//
// This provides visibility into API usage and helps identify slow endpoints.
//
// Usage:
//
//	mux := http.NewServeMux()
//	// ... register handlers ...
//	handler := http.RequestLogger()(mux)
//	http.ListenAndServe(":8080", handler)
func RequestLogger() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap ResponseWriter to capture status code
			lrw := &loggingResponseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK, // default
			}

			next.ServeHTTP(lrw, r)

			duration := time.Since(start)

			// Log request with appropriate emoji based on status code
			emoji := getStatusEmoji(lrw.statusCode)
			logger.Debug("HTTP Request", logger.Fields{
				"emoji":    emoji,
				"method":   r.Method,
				"path":     r.URL.Path,
				"status":   lrw.statusCode,
				"duration": duration,
			})
		})
	}
}

// loggingResponseWriter wraps http.ResponseWriter to capture the status code.
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code and delegates to the wrapped ResponseWriter.
func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

// getStatusEmoji returns an appropriate emoji for the HTTP status code.
func getStatusEmoji(statusCode int) string {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return "✅"
	case statusCode >= 300 && statusCode < 400:
		return "↩️"
	case statusCode >= 400 && statusCode < 500:
		return "⚠️"
	case statusCode >= 500:
		return "❌"
	default:
		return "ℹ️"
	}
}

// Chain combines multiple middleware functions into a single middleware.
// Middleware are applied in the order they are provided (left to right).
//
// Usage:
//
//	handler := http.Chain(
//	    http.ErrorRecovery(),
//	    http.RequestLogger(),
//	)(mux)
func Chain(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(final http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}

// SecurityHeaders returns middleware that adds security headers to all responses.
// This provides defense-in-depth against common web vulnerabilities including:
// - MIME type sniffing attacks
// - Clickjacking
// - XSS attacks
// - Information leakage via referrer
//
// Usage:
//
//	handler := http.SecurityHeaders()(mux)
func SecurityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Prevent MIME type sniffing
			w.Header().Set("X-Content-Type-Options", "nosniff")

			// Prevent clickjacking by disallowing framing
			w.Header().Set("X-Frame-Options", "DENY")

			// Enable XSS filter in older browsers
			w.Header().Set("X-XSS-Protection", "1; mode=block")

			// Control referrer information sent with requests
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

			// Prevent browser from caching sensitive API responses
			if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
				w.Header().Set("Cache-Control", "no-store")
			}

			// Content Security Policy
			// Allows inline styles/scripts for the UI, restricts other resources
			w.Header().Set("Content-Security-Policy",
				"default-src 'self'; "+
					"script-src 'self' 'unsafe-inline' 'unsafe-eval'; "+
					"style-src 'self' 'unsafe-inline'; "+
					"img-src 'self' data: blob:; "+
					"font-src 'self'; "+
					"connect-src 'self' ws: wss:; "+
					"frame-ancestors 'none'")

			next.ServeHTTP(w, r)
		})
	}
}
