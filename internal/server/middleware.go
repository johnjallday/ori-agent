// Package server provides the HTTP server for the Ori Agent application.
// This file contains HTTP middleware implementations.
package server

import "net/http"

// CORSMiddleware wraps an HTTP handler to add CORS headers based on allowed origins configuration.
// It checks the request origin against the allowed origins list and only sets CORS headers
// if the origin is explicitly allowed.
func (s *Server) CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get allowed origins from configuration
		allowedOrigins := s.configManager.GetAllowedOrigins()
		origin := r.Header.Get("Origin")

		// Check if origin is allowed
		allowed := false
		for _, allowedOrigin := range allowedOrigins {
			if origin == allowedOrigin {
				allowed = true
				break
			}
		}

		// Only set CORS headers if origin is allowed
		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
