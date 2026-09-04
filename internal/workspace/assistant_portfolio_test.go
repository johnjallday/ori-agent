package workspace

import (
	"errors"
	"testing"
	"time"
)

func portfolioFixture(t *testing.T) (*InMemoryStore, *Workspace, *Workspace, *Workspace) {
	t.Helper()
	store := NewInMemoryStore()
	first := assistantProject(t, store, "Portfolio First")
	second := assistantProject(t, store, "Portfolio Second")
	service := NewAssistantProgramStore(store)
	station, _, err := service.EnsureProjectStation(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.EnsureProjectStation(second.ID); err != nil {
		t.Fatal(err)
	}
	return store, station, first, second
}

func TestAssistantPortfolioService_ReviewedUpdateStoresOnlyHomeCoordinationState(t *testing.T) {
	store, station, first, second := portfolioFixture(t)
	service := NewAssistantPortfolioService(store)
	storedFirst, _ := store.Get(first.ID)
	linkID := storedFirst.GetAssistantProjectLink().ID
	update := AssistantPortfolioUpdate{
		Status: AssistantPortfolioStatusActive, Priority: 4,
		Milestones:  []AssistantPortfolioMilestone{{ID: "rough-mix", Label: "Rough mix", DueDate: "2026-04-10"}},
		SessionDate: "2026-04-02", ReleaseDate: "2026-05-01",
		Blockers: []string{"Vocal edit decision"}, Deliverables: []string{"Stereo mix"},
		ArchiveReviewState: AssistantArchiveReviewReady,
	}
	review, err := service.Review(station.ID, linkID, 0, update)
	if err != nil {
		t.Fatal(err)
	}
	if review.Project.ProjectWorkspaceID != first.ID || review.Project.ProjectName != first.Name || review.Project.Fields.Priority != 4 || len(review.Project.ArchiveGuidance) == 0 {
		t.Fatalf("portfolio review = %#v", review)
	}
	receipt, err := service.Commit(station.ID, review.Token, "portfolio-update-1", update)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ProjectWorkspaceID != first.ID || receipt.StateRevision != 1 || receipt.Replayed {
		t.Fatalf("portfolio receipt = %#v", receipt)
	}
	replayed, err := service.Commit(station.ID, review.Token, "portfolio-update-1", update)
	if err != nil || !replayed.Replayed || replayed.StateRevision != receipt.StateRevision {
		t.Fatalf("portfolio replay = %#v, %v", replayed, err)
	}
	listed, err := service.List(station.ID)
	if err != nil || len(listed) != 2 {
		t.Fatalf("portfolio list = %#v, %v", listed, err)
	}
	var firstProjection, secondProjection AssistantPortfolioProjectProjection
	for _, projection := range listed {
		if projection.ProjectWorkspaceID == first.ID {
			firstProjection = projection
		}
		if projection.ProjectWorkspaceID == second.ID {
			secondProjection = projection
		}
	}
	if firstProjection.Fields.Status != AssistantPortfolioStatusActive || firstProjection.Fields.Blockers[0] != "Vocal edit decision" {
		t.Fatalf("first portfolio fields = %#v", firstProjection.Fields)
	}
	if secondProjection.Fields.Status != AssistantPortfolioStatusPlanning || secondProjection.Fields.Priority != 0 {
		t.Fatalf("sibling portfolio state was changed = %#v", secondProjection.Fields)
	}
	storedStation, _ := store.Get(station.ID)
	if len(storedStation.Tasks) != 0 || len(storedStation.GetAgentInstances()) != 0 || len(storedStation.DirectoryReferences) != 0 || len(storedStation.GetMCPBindings()) != 0 {
		t.Fatalf("portfolio update caused unrelated Home state: %#v", storedStation)
	}
}

func TestAssistantPortfolioHandoff_CreatesOneIdempotentChildOwnedTicket(t *testing.T) {
	store, station, first, _ := portfolioFixture(t)
	service := NewAssistantPortfolioService(store)
	storedFirst, _ := store.Get(first.ID)
	linkID := storedFirst.GetAssistantProjectLink().ID
	input := AssistantPortfolioHandoffInput{
		Title: "Prepare release notes", Description: "List the confirmed mix deliverables.", State: TicketStateBacklog,
	}
	review, err := service.ReviewHandoff(station.ID, linkID, input)
	if err != nil {
		t.Fatal(err)
	}
	if review.Handoff.LinkID != linkID || review.Handoff.ProjectWorkspaceID != first.ID || review.Handoff.Assignment == "" || review.Handoff.AuthorityBoundary == "" {
		t.Fatalf("handoff review = %#v", review)
	}
	receipt, err := service.CommitHandoff(station.ID, review.Token, "handoff-1", input)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ProjectWorkspaceID != first.ID || receipt.TicketID == "" || receipt.Replayed {
		t.Fatalf("handoff receipt = %#v", receipt)
	}
	replayed, err := service.CommitHandoff(station.ID, review.Token, "handoff-1", input)
	if err != nil || !replayed.Replayed || replayed.TicketID != receipt.TicketID {
		t.Fatalf("handoff replay = %#v, %v", replayed, err)
	}
	childTickets, err := NewTicketService(store).List(TicketQuery{WorkspaceID: first.ID})
	if err != nil || len(childTickets) != 1 || childTickets[0].ID != receipt.TicketID || childTickets[0].Title != input.Title || childTickets[0].Assignee != "" {
		t.Fatalf("child Tickets = %#v, %v", childTickets, err)
	}
	homeTickets, err := NewTicketService(store).List(TicketQuery{WorkspaceID: station.ID})
	if err != nil || len(homeTickets) != 0 {
		t.Fatalf("Home unexpectedly owns handoff Ticket = %#v, %v", homeTickets, err)
	}
	storedStation, _ := store.Get(station.ID)
	storedChild, _ := store.Get(first.ID)
	if len(storedStation.GetAgentInstances()) != 0 || len(storedChild.GetAgentInstances()) != 0 || len(storedStation.GetMCPBindings()) != 0 || len(storedChild.GetMCPBindings()) != 0 {
		t.Fatal("handoff granted agent or tool authority")
	}
}

func TestAssistantPortfolioHandoff_RejectsChangedExactLinkBeforeCreatingTicket(t *testing.T) {
	store, station, first, _ := portfolioFixture(t)
	service := NewAssistantPortfolioService(store)
	storedFirst, _ := store.Get(first.ID)
	linkID := storedFirst.GetAssistantProjectLink().ID
	input := AssistantPortfolioHandoffInput{Title: "Do bounded work", State: TicketStateReady}
	review, err := service.ReviewHandoff(station.ID, linkID, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(first.ID, func(current *Workspace) error {
		link := current.GetAssistantProjectLink()
		link.StateRevision++
		current.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.CommitHandoff(station.ID, review.Token, "handoff-link-race", input)
	if !errors.Is(err, ErrAssistantPortfolioLinkNotFound) {
		t.Fatalf("changed-link commit error = %v", err)
	}
	tickets, err := NewTicketService(store).List(TicketQuery{WorkspaceID: first.ID})
	if err != nil || len(tickets) != 0 {
		t.Fatalf("link-race created child work = %#v, %v", tickets, err)
	}
}

func TestAssistantPortfolioReviewExpiresWithoutMutation(t *testing.T) {
	store, station, first, _ := portfolioFixture(t)
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	service := NewAssistantPortfolioService(store)
	service.SetClock(func() time.Time { return now })
	storedFirst, _ := store.Get(first.ID)
	input := AssistantPortfolioUpdate{Status: AssistantPortfolioStatusPlanning, ArchiveReviewState: AssistantArchiveReviewNotReady}
	review, err := service.Review(station.ID, storedFirst.GetAssistantProjectLink().ID, 0, input)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(assistantPortfolioReviewTTL + time.Second)
	if _, err := service.Commit(station.ID, review.Token, "expired", input); !errors.Is(err, ErrAssistantPortfolioReviewExpired) {
		t.Fatalf("expired commit error = %v", err)
	}
	stored, _ := store.Get(station.ID)
	if stored.GetAssistantProgramState().Portfolio.StateRevision != 0 || len(stored.GetAssistantProgramState().Portfolio.Projects) != 0 {
		t.Fatal("expired portfolio review mutated fields")
	}
}
