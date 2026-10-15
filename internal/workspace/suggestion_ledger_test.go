package workspace

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func nowForTest() time.Time { return time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC) }

func createSuggestedTicket(t *testing.T, svc *BacklogService, workspaceID, title string) *Task {
	t.Helper()
	ticket, err := svc.Create(BacklogCreateInput{
		WorkspaceID: workspaceID,
		Description: title,
		Details:     MissionSuggestionDetails(MissionTaskSuggestion{Title: title, Why: "Because the goal needs it", Evidence: "notes/plan.md"}, "run-1", nowForTest()),
		SourceType:  TicketSourceGoalSuggestion,
		SourceID:    "run-1",
		Context: map[string]any{
			MissionSuggestionContextKey: MissionSuggestionContext(MissionTaskSuggestion{Why: "Because the goal needs it", Evidence: "notes/plan.md"}, "run-1"),
		},
	})
	if err != nil {
		t.Fatalf("create suggested ticket: %v", err)
	}
	return ticket
}

// FR20: Not useful removes the ticket and records its title and reason; any
// other delete of a suggested ticket records it without a reason; deleting an
// ordinary ticket records nothing.
func TestDismissSuggestionRecordsTheLedger(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Brand"})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	svc := NewBacklogService(store)
	notUseful := createSuggestedTicket(t, svc, ws.ID, "Audit every old post.")
	deleted := createSuggestedTicket(t, svc, ws.ID, "Plan next week's posts")
	ordinary, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "My own idea"})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.DismissSuggestion(ws.ID, notUseful.ID, "  We stopped\nposting there  "); err != nil {
		t.Fatalf("DismissSuggestion: %v", err)
	}
	if err := svc.Delete(ws.ID, deleted.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := svc.Delete(ws.ID, ordinary.ID); err != nil {
		t.Fatalf("Delete ordinary: %v", err)
	}

	fresh, _ := store.Get(ws.ID)
	if len(fresh.Tasks) != 0 {
		t.Fatalf("tickets left: %+v", fresh.Tasks)
	}
	ledger := fresh.DismissedSuggestions
	if len(ledger) != 2 {
		t.Fatalf("ledger = %+v, want the two suggestions only", ledger)
	}
	// Newest first: the plain delete happened last.
	if ledger[0].Title != "Plan next week's posts" || ledger[0].Reason != "" {
		t.Errorf("deleted entry = %+v", ledger[0])
	}
	if ledger[1].NormalizedTitle != "audit every old post" || ledger[1].Reason != "We stopped posting there" ||
		ledger[1].RunID != "run-1" || ledger[1].DismissedAt.IsZero() {
		t.Errorf("not useful entry = %+v", ledger[1])
	}
	keys := fresh.DismissedSuggestionKeys()
	if _, ok := keys["plan next week's posts"]; !ok || len(keys) != 2 {
		t.Errorf("keys = %v", keys)
	}
	if titles := fresh.DismissedSuggestionTitles(); len(titles) != 2 || titles[0] != "Plan next week's posts" {
		t.Errorf("titles = %v", titles)
	}
}

func TestDismissSuggestionRefusesOrdinaryTickets(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Brand"})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	svc := NewBacklogService(store)
	ordinary, err := svc.Create(BacklogCreateInput{WorkspaceID: ws.ID, Description: "My own idea"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DismissSuggestion(ws.ID, ordinary.ID, "no"); !errors.Is(err, ErrNotASuggestion) {
		t.Fatalf("err = %v, want ErrNotASuggestion", err)
	}
	if fresh, _ := store.Get(ws.ID); len(fresh.Tasks) != 1 {
		t.Fatal("a refused Not useful must keep the ticket")
	}
}

// A title dismissed again is refreshed in place, and the ledger is bounded.
func TestSuggestionLedgerRefreshesAndIsBounded(t *testing.T) {
	ws := &Workspace{}
	for i := 0; i < SuggestionLedgerLimit+5; i++ {
		ws.recordDismissedSuggestionLocked(Task{Description: fmt.Sprintf("Idea %d", i), SourceType: TicketSourceGoalSuggestion}, "", nowForTest())
	}
	if len(ws.DismissedSuggestions) != SuggestionLedgerLimit || ws.DismissedSuggestions[0].Title != fmt.Sprintf("Idea %d", SuggestionLedgerLimit+4) {
		t.Fatalf("ledger len=%d first=%q", len(ws.DismissedSuggestions), ws.DismissedSuggestions[0].Title)
	}
	ws.recordDismissedSuggestionLocked(Task{Description: "idea 100.", SourceType: TicketSourceGoalSuggestion}, "dup", nowForTest())
	count := 0
	for _, entry := range ws.DismissedSuggestions {
		if entry.NormalizedTitle == "idea 100" {
			count++
		}
	}
	if count != 1 || ws.DismissedSuggestions[0].Reason != "dup" {
		t.Fatalf("a re-dismissed title must be one refreshed entry; count=%d first=%+v", count, ws.DismissedSuggestions[0])
	}
	long := strings.Repeat("x", SuggestionReasonMaxLength+40)
	if got := normalizeSuggestionReason(long); len([]rune(got)) != SuggestionReasonMaxLength {
		t.Fatalf("reason length = %d", len([]rune(got)))
	}
}

// The ledger travels with the folder: it is part of workspace.json.
func TestSuggestionLedgerPersistsInWorkspaceJSON(t *testing.T) {
	store, ws := newBacklogSyncTestStore(t)
	svc := NewBacklogService(store)
	ticket := createSuggestedTicket(t, svc, ws.ID, "Draft a brand voice guide")
	if err := svc.DismissSuggestion(ws.ID, ticket.ID, "already have one"); err != nil {
		t.Fatal(err)
	}
	folder, err := store.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(folder + "/" + WorkspaceConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"dismissed_suggestions"`) || !strings.Contains(string(data), "already have one") {
		t.Fatalf("workspace.json lacks the ledger:\n%s", data)
	}
}

// FR23: a suggested ticket keeps its marker, reason and structured context
// through a BACKLOG.md render and an edited re-import.
func TestSuggestedTicketSurvivesBacklogMarkdownRoundTrip(t *testing.T) {
	store, ws := newBacklogSyncTestStore(t)
	sync := NewFileBacklogSynchronizer(store)
	svc := NewBacklogService(store)
	svc.SetSynchronizer(sync)
	if _, err := sync.EnsureBacklogMarkdownFile(ws.ID); err != nil {
		t.Fatal(err)
	}
	ticket := createSuggestedTicket(t, svc, ws.ID, "Draft a brand voice guide")

	path := backlogPathFor(t, store, ws)
	rendered, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "Draft a brand voice guide") || !strings.Contains(string(rendered), "ori:id="+ticket.ID) {
		t.Fatalf("rendered BACKLOG.md lacks the suggestion:\n%s", rendered)
	}
	edited := strings.Replace(string(rendered), "priority=medium", "priority=high", 1)
	if edited == string(rendered) {
		t.Fatalf("fixture could not edit the row:\n%s", rendered)
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sync.Import(ws.ID); err != nil {
		t.Fatalf("Import: %v", err)
	}

	item, err := svc.Get(ws.ID, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := item.Task
	if got.Priority != 1 {
		t.Fatalf("the file edit was not imported: priority %d", got.Priority)
	}
	if got.SourceType != TicketSourceGoalSuggestion || got.SourceID != "run-1" {
		t.Fatalf("suggestion marker lost: %q %q", got.SourceType, got.SourceID)
	}
	if !strings.Contains(got.Details, "**Why:** Because the goal needs it") {
		t.Fatalf("reason lost: %q", got.Details)
	}
	reason, _ := got.Context[MissionSuggestionContextKey].(map[string]any)
	if reason["why"] != "Because the goal needs it" {
		t.Fatalf("structured reason lost: %#v", got.Context)
	}
}
