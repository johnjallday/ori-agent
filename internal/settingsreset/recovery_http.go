package settingsreset

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

var recoveryPage = template.Must(template.New("reset-recovery").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Ori Reset Recovery</title>
<style>
:root{color-scheme:light dark;font-family:ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#101418;color:#eef3f7}body{margin:0;min-height:100vh;display:grid;place-items:center;background:radial-gradient(circle at 20% 10%,#263d46 0,transparent 35%),#101418}.card{width:min(42rem,calc(100% - 2rem));box-sizing:border-box;padding:2rem;border:1px solid #43545e;border-radius:1rem;background:#182127;box-shadow:0 1rem 4rem #0008}.eyebrow{color:#9dd8c8;font-weight:700;letter-spacing:.08em;text-transform:uppercase;font-size:.75rem}h1{font-size:clamp(1.8rem,6vw,3rem);line-height:1.05;margin:.6rem 0 1rem}p{line-height:1.6;color:#cad5db}.status{margin:1.5rem 0;padding:1rem;border-left:.3rem solid #e7b66b;background:#202c33}.status strong,.id{display:block}.id{font-family:ui-monospace,SFMono-Regular,monospace;overflow-wrap:anywhere;color:#9fb0ba;margin-top:.4rem}.blockers{padding-left:1.2rem}.blockers li{margin:.8rem 0;line-height:1.5}.blockers strong{display:block;color:#f1c98d}button{font:inherit;font-weight:700;padding:.75rem 1rem;border:0;border-radius:.6rem;background:#9dd8c8;color:#10201d;cursor:pointer}button:focus-visible{outline:.2rem solid #fff;outline-offset:.2rem}@media(prefers-reduced-motion:no-preference){.card{animation:arrive .35s ease-out}@keyframes arrive{from{opacity:0;transform:translateY(.5rem)}}}
</style>
</head>
<body><main class="card"><div class="eyebrow">Reset &amp; Recovery</div><h1>Ori is protecting an unfinished reset.</h1><p>The regular application stayed closed so background services cannot restore or change reviewed data.</p><div class="status" role="status" aria-live="polite"><strong>Current state: {{.State}}</strong><span class="id">Operation {{.ID}}</span></div>{{if .Blockers}}<ul class="blockers" aria-label="Recovery blockers">{{range .Blockers}}<li><strong>{{.Message}}</strong>{{.Recovery}}</li>{{end}}</ul>{{end}}<p>Resolve the reported blocker, then fully quit and relaunch Ori to retry only unresolved categories. Do not delete <code>.ori-reset</code> or start a second installation against this data folder.</p><form method="get" action="/"><button type="submit">Refresh status</button></form></main></body></html>`))

// CurrentRecoveryOperation reads the one bounded durable result without opening
// application stores. Absence is never presented as successful completion.
func CurrentRecoveryOperation(ctx context.Context, lease *resetstate.Lease) (Operation, error) {
	if lease == nil {
		return Operation{}, ErrLifecycleUnavailable
	}
	data, err := lease.Read(resetstate.OperationRecord)
	if err != nil || data == nil {
		return Operation{}, ErrJournalInvalid
	}
	journal, err := decodeJournal(data)
	if err != nil {
		return Operation{}, err
	}
	return NewCoordinator(lease, nil, nil).Status(ctx, journal.Operation.ID)
}

// NewRecoveryHandler exposes only read-only reset evidence. It has no route to
// application stores, credentials, arbitrary files, or reset application.
func NewRecoveryHandler(lease *resetstate.Lease) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/reset/recovery", func(w http.ResponseWriter, r *http.Request) {
		setRecoveryHeaders(w)
		operation, err := CurrentRecoveryOperation(r.Context(), lease)
		if err != nil {
			http.Error(w, "Reset recovery evidence is unavailable.", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Success   bool      `json:"success"`
			Operation Operation `json:"operation"`
		}{Success: operation.VerifiedComplete(), Operation: operation})
	})
	mux.HandleFunc("GET /api/reset/operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		setRecoveryHeaders(w)
		id := strings.TrimSpace(r.PathValue("id"))
		operation, err := NewCoordinator(lease, nil, nil).Status(r.Context(), id)
		if err != nil {
			http.Error(w, "Reset operation is unavailable.", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Success   bool      `json:"success"`
			Operation Operation `json:"operation"`
		}{Success: operation.VerifiedComplete(), Operation: operation})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		setRecoveryHeaders(w)
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		operation, err := CurrentRecoveryOperation(r.Context(), lease)
		if err != nil {
			http.Error(w, "Reset recovery evidence is unavailable.", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = recoveryPage.Execute(w, operation)
	})
	return mux
}

func setRecoveryHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

func RecoveryHTTPServer(lease *resetstate.Lease, address string) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           NewRecoveryHandler(lease),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
}
