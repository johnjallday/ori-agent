package server

import (
	"net/http"
	"os"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func enterResetRuntime(lease *resetstate.Lease, gate *resetstate.WorkGate) (func(), error) {
	if lease == nil {
		return gate.Enter() // Alternate host: never advertised as reset-ready.
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return lease.EnterRuntime(cwd, os.Getenv("ORI_DATA_DIR"))
}

// resetStreamPatterns are the exact read-only SSE routes whose handlers stay
// tracked but do not block a reset fence. Keep these aligned with registrations
// in server/routes.go, workspace/routes.go, fileshttp/routes.go, and
// mapactivityhttp/handlers.go; method-less production registrations are still
// deliberately classified as GET-only here.
var resetStreamPatterns = []string{
	"GET /api/orchestration/workflow/stream",      // registerOrchestrationRoutes
	"GET /api/orchestration/progress/stream",      // registerOrchestrationRoutes
	"GET /api/orchestration/notifications/stream", // registerOrchestrationRoutes
	"GET /api/workspaces/{workspaceID}/events",    // workspace.RegisterRoutes
	"GET /api/sessions/{id}/files/events",         // fileshttp.RegisterRoutes
	"GET /api/workspace-map/activity/stream",      // mapactivityhttp.Handler.Register
}

func newResetStreamMux() *http.ServeMux {
	mux := http.NewServeMux()
	for _, pattern := range resetStreamPatterns {
		mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
	}
	return mux
}

func writeResetPending(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":"reset_pending","message":"Ordinary work is paused. Recover the reset operation and fully relaunch Ori when instructed."}`))
}

// resetAdmissionMiddleware counts ordinary HTTP operations through their last
// handler write. It leaves the reset control mux outside admission so executing
// a reset doesn't count itself as active work and recovery remains readable.
// Classified read-only event streams use revocable stream permits: they do not
// block a fence, but remain tracked until fence cancellation unwinds the handler.
// Detached handler work must register its own permit before returning.
//
// This is deliberately NOT a lifecycle capability. Background timers, event
// callbacks, shell writers and external processes require independent coverage.
// A dedicated recovery page must be provided before enabling production reset;
// while fenced, even ordinary GET/page handlers are refused, not assumed inert.
func (s *Server) resetAdmissionMiddleware(next http.Handler) http.Handler {
	control := http.NewServeMux()
	if s.Handlers != nil && s.Handlers.Reset != nil {
		registerResetRoutes(control, s)
	}
	streams := newResetStreamMux()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := control.Handler(r); pattern != "" {
			// Dispatch only to the small control mux, never a path-prefix bypass
			// into the ordinary mux (which could grow new mutating reset routes).
			control.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodGet {
			if _, pattern := streams.Handler(r); pattern != "" {
				streamCtx, release, err := s.resetWork.EnterStream(r.Context())
				if err != nil {
					writeResetPending(w)
					return
				}
				defer release()
				next.ServeHTTP(w, r.WithContext(streamCtx))
				return
			}
		}
		release, err := s.resetWork.Enter()
		if err != nil {
			writeResetPending(w)
			return
		}
		defer release()
		next.ServeHTTP(w, r)
	})
}
