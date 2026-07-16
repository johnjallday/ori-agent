package agenthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
)

// postBulk issues a POST /api/agents/bulk against the handler and decodes the
// response. It fails the test on a non-200 status (use rawBulk for error cases).
func postBulk(t *testing.T, h *Handler, body string) bulkResponse {
	t.Helper()
	rr := rawBulk(t, h, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp bulkResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	return resp
}

func rawBulk(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/agents/bulk", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.HandleBulk(rr, req)
	return rr
}

// resultFor returns the per-agent result for a name (case-insensitive).
func resultFor(resp bulkResponse, name string) (bulkResult, bool) {
	for _, r := range resp.Results {
		if strings.EqualFold(r.Name, name) {
			return r, true
		}
	}
	return bulkResult{}, false
}

func agentTags(t *testing.T, h *Handler, name string) []string {
	t.Helper()
	ag, ok := h.State.GetAgent(name)
	if !ok || ag == nil || ag.Metadata == nil {
		return nil
	}
	return ag.Metadata.Tags
}

func setTags(t *testing.T, h *Handler, name string, tags []string) {
	t.Helper()
	ag, ok := h.State.GetAgent(name)
	if !ok || ag == nil {
		t.Fatalf("agent %q not found", name)
	}
	if ag.Metadata == nil {
		ag.Metadata = &types.AgentMetadata{}
	}
	ag.Metadata.Tags = tags
	if err := h.State.SetAgent(name, ag); err != nil {
		t.Fatalf("SetAgent %q: %v", name, err)
	}
}

// --- Validation -------------------------------------------------------------

func TestBulkRejectsEmptyList(t *testing.T) {
	h := guardTestHandler(t, []string{"A"}, nil)
	rr := rawBulk(t, h, `{"agent_names":[],"operation":"delete"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty list, got %d", rr.Code)
	}
}

func TestBulkRejectsUnknownOperation(t *testing.T) {
	h := guardTestHandler(t, []string{"A"}, nil)
	rr := rawBulk(t, h, `{"agent_names":["A"],"operation":"nuke"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown operation, got %d", rr.Code)
	}
}

func TestBulkRejectsOversizedBatch(t *testing.T) {
	h := guardTestHandler(t, []string{"A"}, nil)
	names := make([]string, 0, maxBulkBatchSize+1)
	for i := 0; i <= maxBulkBatchSize; i++ {
		names = append(names, fmt.Sprintf("%q", fmt.Sprintf("agent-%d", i)))
	}
	body := `{"agent_names":[` + strings.Join(names, ",") + `],"operation":"delete"}`
	rr := rawBulk(t, h, body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized batch, got %d", rr.Code)
	}
}

func TestBulkSetFavoriteRequiresBoolean(t *testing.T) {
	h := guardTestHandler(t, []string{"A"}, nil)
	rr := rawBulk(t, h, `{"agent_names":["A"],"operation":"set_favorite"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when favorite omitted, got %d", rr.Code)
	}
}

func TestBulkTagRejectsEmptyTags(t *testing.T) {
	h := guardTestHandler(t, []string{"A"}, nil)
	rr := rawBulk(t, h, `{"agent_names":["A"],"operation":"add_tags","tags":["  ",""]}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty tags, got %d", rr.Code)
	}
}

// --- Delete -----------------------------------------------------------------

func TestBulkDeleteMixedPartialSuccess(t *testing.T) {
	h := guardTestHandler(t,
		[]string{"Loose1", "Loose2", "Attached"},
		map[string][]string{"ws-a": {"Attached"}},
	)
	body := `{"agent_names":["Loose1","Attached","Ori","Missing","Loose2"],"operation":"delete"}`
	resp := postBulk(t, h, body)

	if resp.Summary.Requested != 5 {
		t.Fatalf("requested=%d, want 5", resp.Summary.Requested)
	}
	if resp.Summary.Succeeded != 2 {
		t.Errorf("succeeded=%d, want 2", resp.Summary.Succeeded)
	}
	if resp.Summary.Skipped != 3 {
		t.Errorf("skipped=%d, want 3 (attached, protected, missing)", resp.Summary.Skipped)
	}

	if r, _ := resultFor(resp, "Loose1"); r.Status != bulkStatusSucceeded {
		t.Errorf("Loose1 status=%s, want succeeded", r.Status)
	}
	if r, _ := resultFor(resp, "Attached"); r.ReasonCode != reasonAttachedAgent {
		t.Errorf("Attached reason=%s, want %s", r.ReasonCode, reasonAttachedAgent)
	}
	if r, _ := resultFor(resp, "Ori"); r.ReasonCode != reasonProtectedAgent {
		t.Errorf("Ori reason=%s, want %s", r.ReasonCode, reasonProtectedAgent)
	}
	if r, _ := resultFor(resp, "Missing"); r.ReasonCode != reasonAgentNotFound {
		t.Errorf("Missing reason=%s, want %s", r.ReasonCode, reasonAgentNotFound)
	}

	// Deleted agents are gone; skipped agents survive.
	if _, ok := h.State.GetAgent("Loose1"); ok {
		t.Errorf("Loose1 should be deleted from store")
	}
	if _, ok := h.State.GetAgent("Attached"); !ok {
		t.Errorf("Attached should survive the bulk delete")
	}
}

func TestBulkDeletePurgesSessionsAndLogs(t *testing.T) {
	h := guardTestHandler(t, []string{"Gone"}, nil)
	purger := &fakeSessionPurger{}
	h.SetSessionPurger(purger)
	logger := newTestActivityLogger(t)
	h.ActivityLogger = logger

	resp := postBulk(t, h, `{"agent_names":["Gone"],"operation":"delete"}`)
	if resp.Summary.Succeeded != 1 {
		t.Fatalf("expected 1 success, got %+v", resp.Summary)
	}
	if purger.calls != 1 || purger.lastAgent != "Gone" {
		t.Errorf("expected session purge for Gone, got calls=%d agent=%q", purger.calls, purger.lastAgent)
	}
	logs, _, err := logger.GetActivityLog("Gone", 0, 0, types.ActivityEventDeleted, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("GetActivityLog: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("expected 1 deletion activity event, got %d", len(logs))
	}
}

func TestBulkDeleteDedupesNames(t *testing.T) {
	h := guardTestHandler(t, []string{"Dup"}, nil)
	resp := postBulk(t, h, `{"agent_names":["Dup","dup","  Dup  "],"operation":"delete"}`)
	if resp.Summary.Requested != 1 {
		t.Fatalf("expected 1 unique requested, got %d", resp.Summary.Requested)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
}

// --- Tags -------------------------------------------------------------------

func TestBulkAddTagsPreservesUnrelatedTags(t *testing.T) {
	h := guardTestHandler(t, []string{"A", "B"}, nil)
	setTags(t, h, "A", []string{"keep"})
	setTags(t, h, "B", []string{"content"})

	resp := postBulk(t, h, `{"agent_names":["A","B"],"operation":"add_tags","tags":["content","new"]}`)
	if resp.Summary.Succeeded != 2 {
		t.Fatalf("expected 2 successes, got %+v results=%+v", resp.Summary, resp.Results)
	}

	gotA := agentTags(t, h, "A")
	if !containsAll(gotA, "keep", "content", "new") || len(gotA) != 3 {
		t.Errorf("A tags=%v, want keep+content+new", gotA)
	}
	// B already had "content" — must not duplicate it.
	gotB := agentTags(t, h, "B")
	if countTag(gotB, "content") != 1 {
		t.Errorf("B should have exactly one 'content' tag, got %v", gotB)
	}
}

func TestBulkRemoveTagsPreservesOthers(t *testing.T) {
	h := guardTestHandler(t, []string{"A"}, nil)
	setTags(t, h, "A", []string{"content", "draft", "keep"})

	resp := postBulk(t, h, `{"agent_names":["A"],"operation":"remove_tags","tags":["content","draft"]}`)
	if resp.Summary.Succeeded != 1 {
		t.Fatalf("expected 1 success, got %+v", resp.Summary)
	}
	got := agentTags(t, h, "A")
	if len(got) != 1 || got[0] != "keep" {
		t.Errorf("A tags=%v, want [keep]", got)
	}
}

func TestBulkTagSkipsCLIAndMissing(t *testing.T) {
	h := guardTestHandler(t, []string{"A"}, nil)
	resp := postBulk(t, h, `{"agent_names":["A","Nope"],"operation":"add_tags","tags":["x"]}`)
	if r, _ := resultFor(resp, "Nope"); r.ReasonCode != reasonAgentNotFound {
		t.Errorf("Nope reason=%s, want %s", r.ReasonCode, reasonAgentNotFound)
	}
	if r, _ := resultFor(resp, "A"); r.Status != bulkStatusSucceeded {
		t.Errorf("A status=%s, want succeeded", r.Status)
	}
}

func TestBulkTagSharedRequiresConfirmation(t *testing.T) {
	h := guardTestHandler(t,
		[]string{"Shared"},
		map[string][]string{"ws-a": {"Shared"}, "ws-b": {"Shared"}},
	)
	// Without confirmation → skipped.
	resp := postBulk(t, h, `{"agent_names":["Shared"],"operation":"add_tags","tags":["x"]}`)
	if r, _ := resultFor(resp, "Shared"); r.ReasonCode != reasonSharedEditNeedsOK {
		t.Fatalf("expected shared-edit skip, got %+v", r)
	}
	if got := agentTags(t, h, "Shared"); containsAll(got, "x") {
		t.Errorf("tag should not be applied without confirmation, got %v", got)
	}
	// With confirmation → applied.
	resp = postBulk(t, h, `{"agent_names":["Shared"],"operation":"add_tags","tags":["x"],"confirm_shared_edit":true}`)
	if r, _ := resultFor(resp, "Shared"); r.Status != bulkStatusSucceeded {
		t.Fatalf("expected success with confirmation, got %+v", r)
	}
	if got := agentTags(t, h, "Shared"); !containsAll(got, "x") {
		t.Errorf("tag should be applied with confirmation, got %v", got)
	}
}

// --- Favorite ---------------------------------------------------------------

func TestBulkSetFavorite(t *testing.T) {
	h := guardTestHandler(t, []string{"A", "B"}, nil)
	resp := postBulk(t, h, `{"agent_names":["A","B"],"operation":"set_favorite","favorite":true}`)
	if resp.Summary.Succeeded != 2 {
		t.Fatalf("expected 2 successes, got %+v", resp.Summary)
	}
	for _, name := range []string{"A", "B"} {
		ag, _ := h.State.GetAgent(name)
		if ag.Metadata == nil || !ag.Metadata.Favorite {
			t.Errorf("%s should be favorited", name)
		}
	}

	// Unfavorite one back.
	resp = postBulk(t, h, `{"agent_names":["A"],"operation":"set_favorite","favorite":false}`)
	if resp.Summary.Succeeded != 1 {
		t.Fatalf("expected 1 success, got %+v", resp.Summary)
	}
	ag, _ := h.State.GetAgent("A")
	if ag.Metadata.Favorite {
		t.Errorf("A should be unfavorited")
	}
}

// --- helpers ----------------------------------------------------------------

type fakeSessionPurger struct {
	calls     int
	lastAgent string
}

func (f *fakeSessionPurger) DeleteSessionsByAgent(_ context.Context, agentName string) (int, error) {
	f.calls++
	f.lastAgent = agentName
	return 1, nil
}

func newTestActivityLogger(t *testing.T) *ActivityLogger {
	t.Helper()
	al, err := NewActivityLogger(t.TempDir())
	if err != nil {
		t.Fatalf("NewActivityLogger: %v", err)
	}
	return al
}

func containsAll(haystack []string, needles ...string) bool {
	for _, n := range needles {
		found := false
		for _, h := range haystack {
			if strings.EqualFold(h, n) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func countTag(tags []string, tag string) int {
	c := 0
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			c++
		}
	}
	return c
}
