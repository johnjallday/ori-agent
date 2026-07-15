package progressionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/types"
	ws "github.com/johnjallday/ori-agent/internal/workspace"
)

// memStore is an in-memory progression.StateStore for tests.
type memStore struct{ state types.ProgressionState }

func (m *memStore) GetProgression() types.ProgressionState { return m.state }
func (m *memStore) SetProgression(p types.ProgressionState) error {
	m.state = p
	return nil
}

func newHandler() (*Handler, *progression.Engine) {
	engine := progression.New(&memStore{})
	return NewHandler(engine), engine
}

func decodeStatus(t *testing.T, body []byte) progression.Status {
	t.Helper()
	var st progression.Status
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("decode status: %v (body=%s)", err, body)
	}
	return st
}

func TestGetStatus(t *testing.T) {
	h, _ := newHandler()
	rec := httptest.NewRecorder()
	h.GetStatus(rec, httptest.NewRequest(http.MethodGet, "/api/progression", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rec.Code)
	}
	st := decodeStatus(t, rec.Body.Bytes())
	if st.TotalTiers != progression.TotalTiers {
		t.Fatalf("total_tiers = %d, want %d", st.TotalTiers, progression.TotalTiers)
	}
	if st.CurrentTier != 1 {
		t.Fatalf("fresh install current_tier = %d, want 1", st.CurrentTier)
	}
	if len(st.Tiers) == 0 {
		t.Fatal("expected tiers in status")
	}
}

func TestGetStatus_RejectsNonGet(t *testing.T) {
	h, _ := newHandler()
	rec := httptest.NewRecorder()
	h.GetStatus(rec, httptest.NewRequest(http.MethodPost, "/api/progression", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status code = %d, want 405", rec.Code)
	}
}

func TestDismiss(t *testing.T) {
	h, _ := newHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/progression/dismiss", strings.NewReader(`{"dismissed":true}`))
	h.Dismiss(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rec.Code)
	}
	if !decodeStatus(t, rec.Body.Bytes()).Dismissed {
		t.Fatal("expected dismissed=true in response")
	}
}

func TestReset(t *testing.T) {
	h, engine := newHandler()
	engine.HandleEvent(ws.Event{Type: ws.EventMessageSent})
	if engine.Status().CompletedCount == 0 {
		t.Fatal("precondition: expected a completion before reset")
	}

	rec := httptest.NewRecorder()
	h.Reset(rec, httptest.NewRequest(http.MethodPost, "/api/progression/reset", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rec.Code)
	}
	if got := decodeStatus(t, rec.Body.Bytes()).CompletedCount; got != 0 {
		t.Fatalf("completed_count after reset = %d, want 0", got)
	}
}

func TestSkip(t *testing.T) {
	h, _ := newHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/progression/skip", strings.NewReader(`{"quest_id":"t2-build-hq"}`))
	h.Skip(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	st := decodeStatus(t, rec.Body.Bytes())
	var found *progression.QuestView
	for _, tv := range st.Tiers {
		for i := range tv.Quests {
			if tv.Quests[i].ID == "t2-build-hq" {
				found = &tv.Quests[i]
			}
		}
	}
	if found == nil || found.Status != progression.StatusSkipped {
		t.Fatalf("expected t2-build-hq to be skipped, got %+v", found)
	}
}

func TestSkip_RejectsNonOptionalQuestWith400(t *testing.T) {
	h, _ := newHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/progression/skip", strings.NewReader(`{"quest_id":"t1-first-message"}`))
	h.Skip(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSkip_UnknownQuestReturns404(t *testing.T) {
	h, _ := newHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/progression/skip", strings.NewReader(`{"quest_id":"does-not-exist"}`))
	h.Skip(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status code = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSkip_MissingQuestIDReturns400(t *testing.T) {
	h, _ := newHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/progression/skip", strings.NewReader(`{}`))
	h.Skip(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSkip_RejectsNonPost(t *testing.T) {
	h, _ := newHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/progression/skip", nil)
	h.Skip(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status code = %d, want 405", rec.Code)
	}
}

// TestSkipIsDistinctFromDismiss covers the requirement that the per-quest
// skip endpoint stays separate from the whole-widget dismiss endpoint:
// skipping one quest must not dismiss the widget, and dismissing the widget
// must not resolve any quest.
func TestSkipIsDistinctFromDismiss(t *testing.T) {
	h, _ := newHandler()

	skipRec := httptest.NewRecorder()
	h.Skip(skipRec, httptest.NewRequest(http.MethodPost, "/api/progression/skip", strings.NewReader(`{"quest_id":"t2-build-hq"}`)))
	if decodeStatus(t, skipRec.Body.Bytes()).Dismissed {
		t.Fatal("skipping a quest must not dismiss the whole widget")
	}

	dismissRec := httptest.NewRecorder()
	h.Dismiss(dismissRec, httptest.NewRequest(http.MethodPost, "/api/progression/dismiss", strings.NewReader(`{"dismissed":true}`)))
	dismissedStatus := decodeStatus(t, dismissRec.Body.Bytes())
	if dismissedStatus.ResolvedCount != 1 {
		t.Fatalf("dismiss must not change resolution state; expected the earlier skip to remain the only resolved quest, got resolved_count=%d", dismissedStatus.ResolvedCount)
	}
}

func TestNilEngine_ServiceUnavailable(t *testing.T) {
	h := NewHandler(nil)
	rec := httptest.NewRecorder()
	h.GetStatus(rec, httptest.NewRequest(http.MethodGet, "/api/progression", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want 503", rec.Code)
	}
}

func TestNilEngine_SkipServiceUnavailable(t *testing.T) {
	h := NewHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/progression/skip", strings.NewReader(`{"quest_id":"t2-build-hq"}`))
	h.Skip(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want 503", rec.Code)
	}
}
