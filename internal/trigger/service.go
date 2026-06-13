package trigger

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Ingest outcome codes returned to the webhook HTTP layer so it can map them
// to status codes without importing net/http semantics into the core.
type IngestResult int

const (
	IngestAccepted     IngestResult = iota // queued; 202
	IngestNotFound                         // unknown or disabled token; 404
	IngestUnauthorized                     // missing/wrong secret; 401
	IngestRateLimited                      // over the per-trigger cap; 429
	IngestWrongType                        // unsupported content type; 415 (decided by caller)
)

// ErrServiceNotReady is returned by operations attempted before Start.
var ErrServiceNotReady = errors.New("trigger service not started")

// Service is the single dependency the HTTP layer talks to. It owns the
// store, coalescer, watch manager, dispatcher, and rate limiter, and keeps
// them consistent across CRUD operations (e.g. enabling a file-watch trigger
// registers its watch; deleting one tears it down and forgets its rate
// bucket).
type Service struct {
	store       *Store
	coalescer   *Coalescer
	dispatcher  *Dispatcher
	watch       *WatchManager
	rateLimiter *rateLimiter
}

// ServiceConfig groups Service dependencies.
type ServiceConfig struct {
	WorkspaceStore    workspace.Store
	Source            WorkspaceSource
	Mission           MissionRunner
	Opportunities     workspace.OpportunityStore
	WebhookRatePerMin int
}

// NewService constructs a trigger Service. Call Start to load persisted
// triggers and begin watching/draining.
func NewService(cfg ServiceConfig) (*Service, error) {
	store := NewStore(cfg.Source)
	dispatcher := NewDispatcher(store, cfg.WorkspaceStore, cfg.Mission, cfg.Opportunities)
	coalescer := NewCoalescer(store, dispatcher.Dispatch)
	watch, err := NewWatchManager(store, coalescer, cfg.Opportunities)
	if err != nil {
		return nil, err
	}
	return &Service{
		store:       store,
		coalescer:   coalescer,
		dispatcher:  dispatcher,
		watch:       watch,
		rateLimiter: newRateLimiter(cfg.WebhookRatePerMin),
	}, nil
}

// Start loads triggers from disk, begins file watching, and re-dispatches any
// fires that were persisted before a restart. Order matters: load → watch →
// restore, so restored fires see a fully-wired dispatcher (PRD #18, #21).
func (s *Service) Start() error {
	if err := s.store.LoadAll(); err != nil {
		return err
	}
	s.watch.Start()
	s.coalescer.RestorePending()
	return nil
}

// Close tears down watching and stops accepting new events. In-flight
// dispatches finish on their own; queued pending fires stay persisted.
func (s *Service) Close() {
	s.watch.Close()
	s.coalescer.Close()
}

// List returns a workspace's triggers.
func (s *Service) List(workspaceID string) []Trigger { return s.store.List(workspaceID) }

// Get returns one trigger.
func (s *Service) Get(workspaceID, triggerID string) (Trigger, error) {
	return s.store.Get(workspaceID, triggerID)
}

// Create persists a new trigger and, for enabled file-watch triggers, starts
// its watch.
func (s *Service) Create(t Trigger) (Trigger, error) {
	created, err := s.store.Create(t)
	if err != nil {
		return Trigger{}, err
	}
	if created.Type == TypeFileWatch && created.Enabled {
		if werr := s.watch.Add(created); werr != nil {
			// Add already disabled the trigger and recorded the failure;
			// return the reloaded state so the caller sees enabled=false.
			reloaded, _ := s.store.Get(created.WorkspaceID, created.ID)
			return reloaded, werr
		}
	}
	return created, nil
}

// Update applies fn and reconciles the file watch to match the new state.
func (s *Service) Update(workspaceID, triggerID string, fn func(*Trigger) error) (Trigger, error) {
	updated, err := s.store.Update(workspaceID, triggerID, fn)
	if err != nil {
		return Trigger{}, err
	}
	// Reconcile watch: simplest correct approach is stop-then-(maybe)-start.
	s.watch.Remove(triggerID)
	if updated.Type == TypeFileWatch && updated.Enabled {
		if werr := s.watch.Add(updated); werr != nil {
			reloaded, _ := s.store.Get(workspaceID, triggerID)
			return reloaded, werr
		}
	}
	return updated, nil
}

// SetEnabled toggles a trigger, validating the watch path on enable.
func (s *Service) SetEnabled(workspaceID, triggerID string, enabled bool) (Trigger, error) {
	return s.Update(workspaceID, triggerID, func(t *Trigger) error {
		if enabled && t.Type == TypeFileWatch {
			if err := t.CheckWatchPath(); err != nil {
				return err
			}
		}
		t.Enabled = enabled
		return nil
	})
}

// RegenerateToken issues a fresh webhook token (old URL stops working
// immediately) and returns the updated trigger.
func (s *Service) RegenerateToken(workspaceID, triggerID string) (Trigger, error) {
	return s.Update(workspaceID, triggerID, func(t *Trigger) error {
		if t.Type != TypeWebhook {
			return errors.New("only webhook triggers have tokens")
		}
		tok, err := GenerateToken()
		if err != nil {
			return err
		}
		if t.Webhook == nil {
			t.Webhook = &WebhookConfig{}
		}
		t.Webhook.Token = tok
		return nil
	})
}

// Delete removes a trigger, tears down its watch, and forgets its rate
// bucket.
func (s *Service) Delete(workspaceID, triggerID string) error {
	s.watch.Remove(triggerID)
	s.rateLimiter.forget(triggerID)
	return s.store.Delete(workspaceID, triggerID)
}

// IngestWebhook resolves a token, enforces the secret and rate limit, then
// queues the event. It returns the fire ID (for the 202 body) and a result
// code. Content-type validation is left to the HTTP layer, which knows the
// raw header; here we only require the resolved trigger to be a webhook.
func (s *Service) IngestWebhook(token, secret string, ev Event) (string, IngestResult) {
	t, ok := s.store.GetByToken(token)
	// Unknown and disabled both look identical to the caller (PRD #9).
	if !ok || !t.Enabled || t.Type != TypeWebhook {
		return "", IngestNotFound
	}
	if t.Webhook != nil && t.Webhook.Secret != "" {
		if !SecureCompare(t.Webhook.Secret, secret) {
			return "", IngestUnauthorized
		}
	}
	if !s.rateLimiter.allow(t.ID) {
		return "", IngestRateLimited
	}
	fireID := s.coalescer.Observe(t, ev)
	return fireID, IngestAccepted
}

// TestFire runs a trigger's action immediately with a synthetic event, so the
// user can verify wiring without waiting for a real event (PRD #28). It
// dispatches synchronously and returns the resulting fire record.
func (s *Service) TestFire(workspaceID, triggerID string) (FireRecord, error) {
	t, err := s.store.Get(workspaceID, triggerID)
	if err != nil {
		return FireRecord{}, err
	}
	fire := PendingFire{
		FireID:    newFireID(),
		Events:    []Event{{Kind: "test", Timestamp: time.Now()}},
		CreatedAt: time.Now(),
	}
	s.dispatcher.Dispatch(t, fire)
	// Return the just-recorded fire (last in history).
	reloaded, err := s.store.Get(workspaceID, triggerID)
	if err != nil {
		return FireRecord{}, err
	}
	if n := len(reloaded.FireHistory); n > 0 {
		return reloaded.FireHistory[n-1], nil
	}
	return FireRecord{FireID: fire.FireID}, nil
}

// WebhookEventFromRequest builds a webhook Event from an HTTP request body,
// capping the payload at MaxPayloadBytes (PRD #7, #12). The caller is
// responsible for content-type gating before calling this.
func WebhookEventFromRequest(contentType, remoteAddr, body string) Event {
	truncated := false
	if len(body) > MaxPayloadBytes {
		body = body[:MaxPayloadBytes]
		truncated = true
	}
	return Event{
		Kind:        "webhook",
		Timestamp:   time.Now(),
		ContentType: contentType,
		Body:        body,
		RemoteAddr:  remoteAddr,
		Truncated:   truncated,
	}
}

// AcceptedContentType reports whether a webhook content type is supported
// (PRD #12). JSON, plain text, and form encodings are allowed; everything
// else (notably binary) is rejected with 415.
func AcceptedContentType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if ct == "" {
		// Be lenient: many curl/script callers omit it. Treat as plain text.
		return true
	}
	// Strip any "; charset=..." parameter.
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "application/json",
		"text/plain",
		"application/x-www-form-urlencoded",
		"multipart/form-data":
		return true
	}
	return false
}

// StatusForIngest maps an IngestResult to an HTTP status code. Lives here so
// the mapping is testable without spinning an HTTP server.
func StatusForIngest(r IngestResult) int {
	switch r {
	case IngestAccepted:
		return http.StatusAccepted
	case IngestUnauthorized:
		return http.StatusUnauthorized
	case IngestRateLimited:
		return http.StatusTooManyRequests
	case IngestWrongType:
		return http.StatusUnsupportedMediaType
	default:
		return http.StatusNotFound
	}
}
