package settingsreset

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

// ResetStatusPath is the same bookmarkable read-only page on both hosts.
const ResetStatusPath = "/reset/status"

//go:embed reset_status.tmpl
var statusTemplate string

var recoveryPage = template.Must(template.New("reset-status").Funcs(template.FuncMap{
	"categoryLabel": func(id CategoryID) string {
		def, _ := definition(id)
		return def.Label
	},
}).Parse(statusTemplate))

type statusView struct {
	ID          string
	Title       string
	Message     string
	ShowRestart bool
	Operation   Operation
}

func viewResetStatus(id string, op Operation) statusView {
	view := statusView{ID: id, Operation: op}
	switch op.State {
	case StatePreparing:
		view.Title = "Preparing the reset"
		view.Message = "Ori accepted your request and is stopping background work. Keep this tab open. Wait for restart instructions before quitting Ori."
	case StateAwaitingRestart:
		view.Title = "Restart required"
		view.Message = "Ori saved the reviewed reset and paused normal work. The reset will apply when a new Ori process starts, before opening your data."
		view.ShowRestart = true
	case StateApplying, StateVerifying:
		view.Title = "Checking the reset on startup"
		view.Message = "Ori is applying the reviewed changes and checking the result. Keep this tab open."
	case StateBlocked, StatePartialFailure, StateInterrupted:
		view.Title = "Reset needs attention"
		view.Message = "Ori is keeping normal work paused. Follow the recovery guidance below before restarting. Do not submit another reset."
		view.ShowRestart = true
	case StateCompleted:
		view.Title = "Reset receipt found"
		view.Message = "The stored receipt is complete. Ori still needs to be ready before you return."
	default:
		view.Title = "Reset status unavailable"
		view.Message = "Ori cannot read the awaited reset receipt. The request may still be preparing, or its receipt may be missing or replaced. This does not confirm success or failure. Do not submit the reset again."
	}
	return view
}

// NewResetStatusHandler reads only the bounded reset receipt, never app stores.
// A supplied operation ID is authoritative for the page identity, including
// when its receipt is absent. The bare path is for fenced navigation without a
// receipt, not a shortcut to a previous successful operation.
func NewResetStatusHandler(lease *resetstate.Lease) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setRecoveryHeaders(w)
		if r.URL.EscapedPath() != ResetStatusPath {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		ids := query["operation"]
		if err != nil || (r.URL.RawQuery != "" && (len(query) != 1 || len(ids) != 1 || !validID(ids[0]))) {
			http.Error(w, "Invalid reset operation identity", http.StatusBadRequest)
			return
		}
		var id string
		var operation Operation
		if len(ids) == 0 {
			current, readErr := CurrentRecoveryOperation(r.Context(), lease)
			if readErr == nil && current.State != StateCompleted {
				http.Redirect(w, r, ResetStatusPath+"?operation="+url.QueryEscape(current.ID), http.StatusSeeOther)
				return
			}
		} else {
			id = ids[0]
			operation, err = NewCoordinator(lease, nil, nil).Status(r.Context(), id)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if operation.ID == "" {
			if errors.Is(err, ErrOperationNotFound) {
				w.WriteHeader(http.StatusNotFound)
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		}
		if r.Method == http.MethodGet {
			_ = recoveryPage.Execute(w, viewResetStatus(id, operation))
		}
	}
}

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
	mux.HandleFunc(ResetStatusPath, NewResetStatusHandler(lease))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		setRecoveryHeaders(w)
		http.Redirect(w, r, ResetStatusPath, http.StatusSeeOther)
	})
	return mux
}

func setRecoveryHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
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
