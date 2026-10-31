package personalhqhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type fakeJournal struct {
	daily personalhq.DailyResult
	err   error
}

func (f fakeJournal) Propose(context.Context, string, string) (*personalhq.JournalProposal, error) {
	return &personalhq.JournalProposal{}, nil
}

func (f fakeJournal) SaveMirrored(_ context.Context, _, localDate, _ string) (personalhq.JournalSaved, error) {
	if f.err != nil {
		return personalhq.JournalSaved{}, f.err
	}
	return personalhq.JournalSaved{
		Note:      &session.WorkspaceNote{ID: "note-1", WorkspaceID: "hq-1", Name: "Journal — " + localDate},
		DailyNote: f.daily,
	}, nil
}

func saveJournal(t *testing.T, api JournalAPI) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(nil, nil, nil, userprofile.LocalUserProvider{})
	h.SetJournal(api)
	request := httptest.NewRequest(http.MethodPost, "/api/personal-hq/journal/save", strings.NewReader(`{"local_date":"2026-10-31","content":"Shipped it."}`))
	recorder := httptest.NewRecorder()
	h.SaveJournal(recorder, request)
	return recorder
}

func TestSaveJournal_CarriesDailyNoteOnlyWhenThereIsASecondBrainToCopyTo(t *testing.T) {
	for result, want := range map[personalhq.DailyResult]string{
		"":                        "",
		personalhq.DailyMirrored:  "mirrored",
		personalhq.DailyUserOwned: "user_owned",
		personalhq.DailyFailed:    "failed",
	} {
		recorder := saveJournal(t, fakeJournal{daily: result})
		if recorder.Code != http.StatusOK {
			t.Fatalf("%q: status %d: %s", result, recorder.Code, recorder.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		got, present := body["daily_note"].(string)
		if present != (want != "") || got != want {
			t.Errorf("%q: daily_note = %q (present %v), want %q", result, got, present, want)
		}
		if body["note_id"] != "note-1" || body["workspace_id"] != "hq-1" {
			t.Errorf("%q: body = %v", result, body)
		}
	}
}

func TestSaveJournal_ASaveErrorIsAConflictWithNoDailyNote(t *testing.T) {
	recorder := saveJournal(t, fakeJournal{err: errors.New("no valid Personal HQ is designated")})
	if recorder.Code != http.StatusConflict || strings.Contains(recorder.Body.String(), "daily_note") {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
}
