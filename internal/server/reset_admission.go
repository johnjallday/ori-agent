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

// resetAdmissionMiddleware counts ordinary HTTP operations through their last
// handler write. It leaves the reset control mux outside admission so executing
// a reset doesn't count itself as active work and recovery remains readable.
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := control.Handler(r); pattern != "" {
			// Dispatch only to the small control mux, never a path-prefix bypass
			// into the ordinary mux (which could grow new mutating reset routes).
			control.ServeHTTP(w, r)
			return
		}
		release, err := s.resetWork.Enter()
		if err != nil {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"reset_pending","message":"Ordinary work is paused. Recover the reset operation and fully relaunch Ori when instructed."}`))
			return
		}
		defer release()
		next.ServeHTTP(w, r)
	})
}
