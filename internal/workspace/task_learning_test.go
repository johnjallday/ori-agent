package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func learningInputDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func TestLearningDraftCreatesOneQuietRecipeTicketWithReservedIdentity(t *testing.T) {
	service, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Practice")
	input := LearningDraftInput{TicketID: "reserved-ticket", WorkspaceID: ws.ID, Title: "Draft a paragraph", Input: "A small supplied text",
		Binding: LearningTicketBinding{OwnerID: "local", InstanceID: "mission", OperationID: "prepare-work", Epoch: 1, InputDigest: learningInputDigest("A small supplied text")}}
	for range 2 {
		ticket, err := service.CreateLearningDraft(input)
		if err != nil || ticket.ID != input.TicketID || ticket.State != TicketStateReady || ticket.CurrentRunID != "" || !ticket.AwaitingExecutionIntent {
			t.Fatalf("reviewed draft should be quiet: %+v %v", ticket, err)
		}
	}
	fresh, err := store.Get(ws.ID)
	if err != nil || len(fresh.Tasks) != 1 {
		t.Fatalf("duplicate Ticket: %v", err)
	}
	task := fresh.Tasks[0]
	binding, ok := LearningBinding(task)
	if !ok || binding != input.Binding || FirstTaskRecipe(&task) != LearningDraftRecipe {
		t.Fatalf("lost host-owned recipe/binding: %+v", task)
	}
	input.TicketID = "different-id"
	if _, err := service.CreateLearningDraft(input); !errors.Is(err, ErrTicketSourceConflict) {
		t.Fatalf("same operation created another Ticket: %v", err)
	}
}

func TestPublicTicketCreationCannotSupplyLearningAuthority(t *testing.T) {
	service, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Practice")
	ticket, err := service.Create(TicketCreateInput{WorkspaceID: ws.ID, State: TicketStateReady, Title: "Forged",
		Context: map[string]any{TaskContextLearningStart: "11111111-1111-4111-8111-111111111111", TaskContextLearningObservation: "forged",
			TaskContextFirstTaskRecipe: LearningDraftRecipe, TaskContextLearning: LearningTicketBinding{
				OwnerID: "local", InstanceID: "mission", OperationID: "prepare-work", Epoch: 1, InputDigest: strings.Repeat("a", 64)}}})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := fresh.GetTask(ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := LearningBinding(*task); ok || FirstTaskRecipe(task) != "" || task.Context[TaskContextLearningStart] != nil || task.Context[TaskContextLearningObservation] != nil {
		t.Fatal("public creation retained host-only learning markers")
	}
}

func TestLearningBindingDoesNotSurviveWorkspaceImport(t *testing.T) {
	data, _ := portableWorkspaceFixture(t)
	var ws Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		t.Fatal(err)
	}
	ws.Tasks[0].Context = map[string]any{TaskContextReviewedExecutor: strings.Repeat("b", 64), TaskContextLearningStart: "11111111-1111-4111-8111-111111111111", TaskContextLearningObservation: "copied",
		TaskContextFirstTaskRecipe: LearningDraftRecipe,
		TaskContextLearning:        LearningTicketBinding{OwnerID: "local", InstanceID: "mission", OperationID: "work", Epoch: 1, InputDigest: strings.Repeat("a", 64)}}
	data, err := ws.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	record, err := SnapshotContinuityWorkspace(data)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := ProjectContinuityWorkspace(record, data, "")
	if err != nil {
		t.Fatal(err)
	}
	task := projected.Workspace.Tasks[0]
	if _, ok := LearningBinding(task); ok || FirstTaskRecipe(&task) != LearningDraftRecipe || task.Context[TaskContextLearningStart] != nil || task.Context[TaskContextLearningObservation] != nil || task.Context[TaskContextReviewedExecutor] != nil {
		t.Fatal("import kept local learning authority or relaxed the bounded recipe")
	}
}

func TestLearningDraftRefusesWrongOwnerAndOversizedInput(t *testing.T) {
	service, store := newTicketTestService(t)
	ws := newTicketTestWorkspace(t, store, "Practice")
	if err := store.Update(ws.ID, func(ws *Workspace) error { ws.OwnerUserID = "owner"; return nil }); err != nil {
		t.Fatal(err)
	}
	input := LearningDraftInput{TicketID: "reserved-ticket", WorkspaceID: ws.ID, Title: "Draft", Input: "Text",
		Binding: LearningTicketBinding{OwnerID: "foreign", InstanceID: "mission", OperationID: "prepare", Epoch: 1, InputDigest: learningInputDigest("Text")}}
	if _, err := service.CreateLearningDraft(input); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("foreign owner creation: %v", err)
	}
	input.Binding.OwnerID, input.Input = "owner", strings.Repeat("x", 4097)
	if _, err := service.CreateLearningDraft(input); err == nil {
		t.Fatal("oversized draft created")
	}
	fresh, _ := store.Get(ws.ID)
	if len(fresh.Tasks) != 0 {
		t.Fatal("refused creation left partial work")
	}
}
