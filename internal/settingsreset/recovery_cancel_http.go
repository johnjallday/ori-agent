package settingsreset

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

// CancelTokenHeader carries the per-process proof the recovery page was served
// by THIS recovery process. It is minted at start, never stored, and rendered
// only into the recovery host's own status page and read-only operation JSON.
const CancelTokenHeader = "X-Ori-Reset-Token"

const maxCancelBody = 1 << 10

// CancelOffer is what the recovery page may show. It exists only when the
// server itself just decided the receipt is cancelable; the button's presence is
// a convenience, never permission — the POST decides again.
type CancelOffer struct {
	Available bool   `json:"available"`
	Revision  uint64 `json:"revision"`
	Token     string `json:"token"`
	Headline  string `json:"headline"`
	Detail    string `json:"detail"`
	Warning   string `json:"warning"`
}

// cancelOfferTTL bounds how often a status poll may re-hash the retained
// folders. The POST never uses this cache.
const cancelOfferTTL = 10 * time.Second

// cancelHost is the recovery-only action surface: one POST route, a per-process
// token, strict local-origin checks and an optional restart handoff.
type cancelHost struct {
	lease   *resetstate.Lease
	token   string
	handoff *RestartHandoff
	// coordinator builds the coordinator a request runs under. A seam for tests
	// that need a write fault; it always returns a lease-only coordinator.
	coordinator func() *Coordinator

	mu     sync.Mutex
	cached struct {
		id       string
		revision uint64
		at       time.Time
		verdict  CancelVerdict
	}
}

func newCancelHost(lease *resetstate.Lease, handoff *RestartHandoff) *cancelHost {
	return &cancelHost{
		lease: lease, token: rand.Text(), handoff: handoff,
		coordinator: func() *Coordinator { return NewCoordinator(lease, nil, nil) },
	}
}

// workspaceSentence says only what the host observed. A missing folder may be an
// unmounted volume, so nothing here says "deleted".
func workspaceSentence(condition WorkspaceCondition) string {
	switch condition {
	case WorkspaceMissing:
		return "Ori can't find a folder you chose to keep when you reviewed this reset. It may have been moved or removed, or its drive may not be connected."
	case WorkspaceEmpty:
		return "A folder you chose to keep when you reviewed this reset is now empty."
	case WorkspaceChanged:
		return "The contents of a folder you chose to keep changed after you reviewed this reset."
	default:
		return "A folder you chose to keep changed after you reviewed this reset."
	}
}

const (
	cancelDetail = "Ori stopped before changing anything. No part of this reset was applied, so your settings, agents and records are as they were. Canceling closes this reset and lets Ori open normally."
	// The warning is the whole truth about what Cancel is not.
	cancelWarning = "Canceling does not restore anything that was removed outside Ori, and it does not reset anything. Canceled is not completed. To start fresh later, review a new reset."
)

// offer returns the cancel offer for a blocked receipt, or nil. It evaluates
// only a receipt that already has the shape of a provably unapplied block, so no
// other receipt causes a single retained byte to be hashed.
func (h *cancelHost) offer(ctx context.Context, op Operation) *CancelOffer {
	if h == nil || op.State != StateBlocked || op.Revision != cancelRevision {
		return nil
	}
	verdict, ok := h.cachedVerdict(op.ID, op.Revision)
	if !ok {
		var err error
		verdict, err = EvaluateCancel(ctx, h.lease, RecoveryOptions{DataDir: h.lease.Path()})
		if err != nil {
			return nil
		}
		h.mu.Lock()
		h.cached.id, h.cached.revision, h.cached.at, h.cached.verdict = op.ID, op.Revision, time.Now(), verdict
		h.mu.Unlock()
	}
	if !verdict.Eligible || verdict.OperationID != op.ID || verdict.Revision != op.Revision {
		return nil
	}
	return &CancelOffer{
		Available: true, Revision: op.Revision, Token: h.token,
		Headline: workspaceSentence(verdict.Workspace), Detail: cancelDetail, Warning: cancelWarning,
	}
}

func (h *cancelHost) cachedVerdict(id string, revision uint64) (CancelVerdict, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cached.id == id && h.cached.revision == revision && time.Since(h.cached.at) < cancelOfferTTL {
		return h.cached.verdict, true
	}
	return CancelVerdict{}, false
}

type cancelResponse struct {
	Canceled  bool       `json:"canceled"`
	Code      string     `json:"code,omitempty"`
	Message   string     `json:"message,omitempty"`
	Operation *Operation `json:"operation,omitempty"`
}

func writeCancel(w http.ResponseWriter, status int, response cancelResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func refuseCancelHTTP(w http.ResponseWriter, status int, code, message string) {
	writeCancel(w, status, cancelResponse{Code: code, Message: message})
}

// handle is the one write the recovery host exposes. Every guard below must
// pass before the coordinator is even consulted, and the coordinator then
// re-decides under the installation lease.
func (h *cancelHost) handle(w http.ResponseWriter, r *http.Request) {
	setRecoveryHeaders(w)
	if !h.localRequest(r) {
		refuseCancelHTTP(w, http.StatusForbidden, "forbidden_origin", "This action is only available from the Ori recovery page on this computer.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Header.Get("X-Requested-With") != "XMLHttpRequest" || err != nil || mediaType != "application/json" {
		refuseCancelHTTP(w, http.StatusBadRequest, "invalid_request", "A JSON XMLHttpRequest is required.")
		return
	}
	supplied := r.Header.Get(CancelTokenHeader)
	if len(supplied) != len(h.token) || subtle.ConstantTimeCompare([]byte(supplied), []byte(h.token)) != 1 {
		refuseCancelHTTP(w, http.StatusForbidden, "forbidden_token", "This page is out of date. Reload the recovery page and try again.")
		return
	}
	id := r.PathValue("id")
	revision, err := decodeCancelBody(w, r)
	if !validID(id) || err != nil {
		refuseCancelHTTP(w, http.StatusBadRequest, "invalid_request", "Use the operation and revision shown on the recovery page.")
		return
	}
	// A disconnected browser must not abandon a write half-decided; the
	// coordinator's own bounded context applies instead.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	op, err := h.coordinator().Cancel(ctx, CancelRequest{OperationID: id, Revision: revision})
	if err != nil {
		h.respondCancelError(w, err)
		return
	}
	op.Restart.Managed = h.handoff.Available()
	writeCancel(w, http.StatusOK, cancelResponse{Canceled: true, Operation: &op})
	// Only after the durable receipt is acknowledged and the reply is written.
	h.handoff.AfterCancel(op)
}

func (h *cancelHost) respondCancelError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		refuseCancelHTTP(w, http.StatusBadRequest, "invalid_request", "Use the operation and revision shown on the recovery page.")
	case errors.Is(err, ErrCancelStale):
		refuseCancelHTTP(w, http.StatusConflict, "stale", "This reset changed since you opened this page. Reload the recovery page to see its current state.")
	case errors.Is(err, ErrCancelNotAllowed):
		refuseCancelHTTP(w, http.StatusConflict, "not_cancelable", "Ori can no longer show that this reset changed nothing, so it cannot be canceled here. Keep the installation as it is and follow the recovery guidance.")
	case errors.Is(err, ErrAdmissionUncertain):
		refuseCancelHTTP(w, http.StatusServiceUnavailable, "not_confirmed", "Ori could not confirm that the reset was canceled. Nothing is assumed. Fully quit and relaunch Ori, then check this page again.")
	default:
		refuseCancelHTTP(w, http.StatusServiceUnavailable, "unavailable", "Reset recovery evidence is unavailable. Preserve the installation and relaunch Ori.")
	}
}

// decodeCancelBody accepts exactly {"revision": N}: no other field, no
// duplicate, no trailing data, bounded size.
func decodeCancelBody(w http.ResponseWriter, r *http.Request) (uint64, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCancelBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body struct {
		Revision *uint64 `json:"revision"`
	}
	if err := dec.Decode(&body); err != nil || body.Revision == nil || *body.Revision == 0 {
		return 0, ErrInvalidRequest
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return 0, ErrInvalidRequest
	}
	return *body.Revision, nil
}

// localRequest requires the request to be same-origin from this very machine:
// a loopback peer, a loopback Host naming this listener's port (so a rebound
// DNS name cannot reach it), and a same-origin Origin. The token is checked
// separately; none of these substitutes for another.
func (h *cancelHost) localRequest(r *http.Request) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(peer).IsLoopback() {
		return false
	}
	hostName, hostPort, err := net.SplitHostPort(r.Host)
	if err != nil || hostPort == "" {
		return false
	}
	switch strings.ToLower(hostName) {
	case "localhost", "127.0.0.1", "::1":
	default:
		return false
	}
	if local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if _, localPort, err := net.SplitHostPort(local.String()); err == nil && localPort != hostPort {
			return false
		}
	}
	if r.Header.Get("Origin") != "http://"+r.Host {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	return true
}
