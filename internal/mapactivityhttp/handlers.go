// Package mapactivityhttp serves the maps' live activity feed: a JSON snapshot
// of what is running and what is waiting to be opened, and one Server-Sent
// Events stream for every workspace at once (tasks/prd-task-run-show.md §4.1).
//
// Every route answers 404 when ORI_MAP_SHOW_ENABLED is off or the tracker is not
// wired, so the maps behave exactly as they did before the feature (FR64).
package mapactivityhttp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/featureflags"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/mapactivity"
)

// defaultKeepalive keeps idle proxies and the browser from treating a quiet
// stream as dead (FR2).
const defaultKeepalive = 15 * time.Second

// Handler serves the activity snapshot and stream.
type Handler struct {
	tracker   *mapactivity.Tracker
	keepalive time.Duration
}

// NewHandler builds the handler. A nil tracker is valid and answers 404.
func NewHandler(tracker *mapactivity.Tracker) *Handler {
	return &Handler{tracker: tracker, keepalive: defaultKeepalive}
}

// Wired reports whether the handler has a tracker to serve (a builder check).
func (h *Handler) Wired() bool {
	return h != nil && h.tracker != nil
}

// Register adds the routes to the mux. A nil handler registers nothing.
func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /api/workspace-map/activity", h.GetActivity)
	mux.HandleFunc("GET /api/workspace-map/activity/stream", h.StreamActivity)
}

func (h *Handler) available(w http.ResponseWriter) bool {
	if !featureflags.MapShowEnabled() || !h.Wired() {
		_ = orihttp.RespondNotFound(w, "the map activity feed is not enabled")
		return false
	}
	return true
}

// GetActivity returns the running activities and unopened parcels.
// GET /api/workspace-map/activity
func (h *Handler) GetActivity(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = orihttp.RespondSuccess(w, h.tracker.Snapshot())
}

// StreamActivity streams the feed as Server-Sent Events. The first event is
// always `snapshot`; after it come `activity` (and later `parcel`) events and a
// keepalive comment every 15 seconds. A reconnecting browser gets a fresh
// snapshot and never replays missed events (FR7).
// GET /api/workspace-map/activity/stream
func (h *Handler) StreamActivity(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}

	controller := http.NewResponseController(w)
	// The HTTP server's WriteTimeout is sized for ordinary requests. This one
	// is meant to stay open for as long as the map is, so lift the deadline;
	// if the writer cannot, the stream ends at the timeout and the browser
	// reconnects with a fresh snapshot, which is still correct.
	_ = controller.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	snapshot, messages, unsubscribe := h.tracker.SnapshotAndSubscribe()
	defer unsubscribe()

	if err := writeEvent(w, "snapshot", snapshot); err != nil {
		return
	}
	if err := controller.Flush(); err != nil {
		logger.Warn("Map activity stream cannot flush; closing it", logger.Fields{"error": err})
		return
	}

	keepalive := h.keepalive
	if keepalive <= 0 {
		keepalive = defaultKeepalive
	}
	ticker := time.NewTicker(keepalive)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case message, open := <-messages:
			if !open {
				return // the tracker stopped
			}
			if err := writeEvent(w, message.Name, message.Payload); err != nil {
				return
			}
		case <-ticker.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
		}
		if err := controller.Flush(); err != nil {
			return
		}
	}
}

func writeEvent(w io.Writer, name string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		logger.Error("Failed to marshal a map activity event", logger.Fields{"event": name, "error": err})
		return nil // skip this one; the stream itself is fine
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	return err
}
