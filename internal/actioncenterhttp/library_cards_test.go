package actioncenterhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeLibraryCards struct {
	cards []LibraryCard
	err   error
	calls int
}

func (f *fakeLibraryCards) LibraryCards(context.Context) ([]LibraryCard, error) {
	f.calls++
	return f.cards, f.err
}

func listLibrary(t *testing.T, h *Handler) (int, []LibraryCard) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ListLibrary(rec, httptest.NewRequest(http.MethodGet, "/api/action-center/library", nil))
	var body struct {
		Items []LibraryCard `json:"items"`
		Total int           `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode library list: %v", err)
	}
	if body.Items == nil || body.Total != len(body.Items) {
		t.Fatalf("library list must always be an array with its total: %+v", body)
	}
	return rec.Code, body.Items
}

func TestListLibrary_ReturnsDerivedCardsAndDegradesToEmpty(t *testing.T) {
	h, _, _ := newTestHandler(t)
	if code, items := listLibrary(t, h); code != http.StatusOK || len(items) != 0 {
		t.Fatalf("unwired source: %d %+v", code, items)
	}
	source := &fakeLibraryCards{cards: []LibraryCard{{HomeID: "home", HomeName: "Music Home",
		Route: "/workspaces/music-home/assistant#projectLibraryProposals", Activatable: 5, New: 6, Projects: 6}}}
	h.SetLibraryCardSource(source)
	code, items := listLibrary(t, h)
	if code != http.StatusOK || len(items) != 1 || items[0].HomeID != "home" || items[0].Activatable != 5 {
		t.Fatalf("library cards: %d %+v", code, items)
	}
	source.err, source.cards = errors.New("store unavailable"), nil
	if code, items := listLibrary(t, h); code != http.StatusOK || len(items) != 0 {
		t.Fatalf("failing source must not break the Action Center: %d %+v", code, items)
	}
	if source.calls != 2 {
		t.Fatalf("each list reads the source once: %d", source.calls)
	}
}

// Library cards are derived per request and are not opportunities: the
// opportunity list and its triage actions never see them.
func TestListLibrary_CardsAreNotOpportunities(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.SetLibraryCardSource(&fakeLibraryCards{cards: []LibraryCard{{HomeID: "home", Activatable: 1,
		Route: "/workspaces/h/assistant#projectLibraryProposals"}}})
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/action-center/opportunities", nil))
	if body := decodeList(t, rec); body.Total != 0 {
		t.Fatalf("a library card leaked into opportunities: %+v", body)
	}
}
