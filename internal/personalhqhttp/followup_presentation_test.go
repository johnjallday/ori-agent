package personalhqhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type fakeNoteSources struct {
	source    secondbrain.Source
	connected bool
	err       error
}

func (f fakeNoteSources) ConnectedSource(context.Context, string) (secondbrain.Source, bool, error) {
	return f.source, f.connected, f.err
}

const presentationWorkspaceID = "hq-id"

func newPresentationHandler(t *testing.T, notes NoteSourceReader) (*Handler, *followup.Service) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := followup.NewService(followup.NewSQLiteStore(db))
	handler := NewHandler(nil, nil, nil, userprofile.LocalUserProvider{})
	handler.SetFollowUps(service)
	if notes != nil {
		handler.SetNoteSources(func() NoteSourceReader { return notes })
	}
	return handler, service
}

func capturePresentationFollowUp(t *testing.T, service *followup.Service, in followup.CaptureInput) *followup.FollowUp {
	t.Helper()
	in.UserID = userprofile.LocalUserID
	in.WorkspaceID = presentationWorkspaceID
	if in.Category == "" {
		in.Category = followup.CategoryIOwe
	}
	if in.Direction == "" {
		in.Direction = followup.DirectionOutbound
	}
	if in.Provenance == "" {
		in.Provenance = followup.ProvenanceExplicit
	}
	item, err := service.Capture(context.Background(), in)
	if err != nil {
		t.Fatalf("capture %q: %v", in.Title, err)
	}
	return item
}

type presentedFollowUp struct {
	ID           string               `json:"id"`
	Detail       string               `json:"detail"`
	Presentation followUpPresentation `json:"presentation"`
}

func presentedFollowUps(t *testing.T, handler *Handler) map[string]presentedFollowUp {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+presentationWorkspaceID+"/followups", nil)
	req.SetPathValue("workspaceID", presentationWorkspaceID)
	rec := httptest.NewRecorder()
	handler.WorkspaceFollowUps(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		FollowUps []presentedFollowUp `json:"followups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	byID := make(map[string]presentedFollowUp, len(payload.FollowUps))
	for _, item := range payload.FollowUps {
		byID[item.ID] = item
	}
	return byID
}

func TestWorkspaceFollowUps_PresentsEachSourceKind(t *testing.T) {
	link := "obsidian://open?vault=Studio&file=Projects%2FAlbum%20Release.md"
	handler, service := newPresentationHandler(t, fakeNoteSources{
		source: secondbrain.Source{Name: "Studio", Kind: "obsidian"}, connected: true,
	})
	withLink := capturePresentationFollowUp(t, service, followup.CaptureInput{
		Title: "Send the mix notes", Detail: "Projects/Album Release.md\nOpen in Obsidian: " + link,
		Source: followup.SourceRef{Type: secondbrain.FollowUpSourceType, ID: "item-1"},
	})
	markdownFolder := capturePresentationFollowUp(t, service, followup.CaptureInput{
		Title: "Book the studio", Detail: "Inbox.markdown",
		Source: followup.SourceRef{Type: secondbrain.FollowUpSourceType, ID: "item-2"},
	})
	otherScheme := capturePresentationFollowUp(t, service, followup.CaptureInput{
		Title: "Call the venue", Detail: "Venues.md\nOpen in Obsidian: javascript:alert(1)",
		Source: followup.SourceRef{Type: secondbrain.FollowUpSourceType, ID: "item-3"},
	})
	email := capturePresentationFollowUp(t, service, followup.CaptureInput{
		Title: "Reply about the contract", Counterparty: "Alex Rivera",
		Category: followup.CategoryWaitingOn, Direction: followup.DirectionInbound,
		Source: followup.SourceRef{Type: "email_thread", ID: "thread-1"},
	})
	journal := capturePresentationFollowUp(t, service, followup.CaptureInput{
		Title:  "Stretch every morning",
		Source: followup.SourceRef{Type: "journal", ID: "2026-11-01"},
	})
	manual := capturePresentationFollowUp(t, service, followup.CaptureInput{
		Title: "Water the plants", Detail: "Back porch", Provenance: followup.ProvenanceManual,
		Source: followup.SourceRef{Type: "manual"},
	})

	got := presentedFollowUps(t, handler)
	want := map[string]followUpPresentation{
		withLink.ID:       {SourceLabel: "Studio", SourceName: "Album Release", Link: link},
		markdownFolder.ID: {SourceLabel: "Studio", SourceName: "Inbox"},
		otherScheme.ID:    {SourceLabel: "Studio", SourceName: "Venues"},
		email.ID:          {SourceLabel: "Email Ops", SourceName: "Alex Rivera"},
		journal.ID:        {SourceLabel: "Journal"},
		manual.ID:         {},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d follow-ups, want %d: %+v", len(got), len(want), got)
	}
	for id, presentation := range want {
		if got[id].Presentation != presentation {
			t.Errorf("follow-up %s presentation = %+v, want %+v", id, got[id].Presentation, presentation)
		}
	}
	// The stored detail is returned unchanged; the presentation is read-time only.
	if got[withLink.ID].Detail != withLink.Detail {
		t.Fatalf("detail changed: got %q want %q", got[withLink.ID].Detail, withLink.Detail)
	}
	stored, err := service.Get(context.Background(), userprofile.LocalUserID, withLink.ID)
	if err != nil || stored.Detail != withLink.Detail {
		t.Fatalf("stored detail = %+v err=%v, want %q", stored, err, withLink.Detail)
	}
}

func TestWorkspaceFollowUps_NoteLabelFallsBackWhenTheFolderCannotBeRead(t *testing.T) {
	cases := map[string]NoteSourceReader{
		"no reader wired":   nil,
		"read fails":        fakeNoteSources{err: errors.New("disk unavailable")},
		"nothing connected": fakeNoteSources{connected: false},
		"unnamed folder":    fakeNoteSources{connected: true},
	}
	for name, reader := range cases {
		t.Run(name, func(t *testing.T) {
			handler, service := newPresentationHandler(t, reader)
			item := capturePresentationFollowUp(t, service, followup.CaptureInput{
				Title: "Send the mix notes", Detail: "Projects/Album Release.md",
				Source: followup.SourceRef{Type: secondbrain.FollowUpSourceType, ID: "item-1"},
			})
			got := presentedFollowUps(t, handler)[item.ID].Presentation
			if want := (followUpPresentation{SourceLabel: "Notes", SourceName: "Album Release"}); got != want {
				t.Fatalf("presentation = %+v, want %+v", got, want)
			}
		})
	}
}

func TestNoteDisplayName(t *testing.T) {
	cases := map[string]string{
		"Projects/Album Release.md": "Album Release",
		"Inbox.MARKDOWN":            "Inbox",
		`Daily\2026-11-02.md`:       "2026-11-02",
		"README":                    "README",
		".md":                       ".md",
		"":                          "",
	}
	for in, want := range cases {
		if got := noteDisplayName(in); got != want {
			t.Errorf("noteDisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}
