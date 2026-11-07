package workspace

import "testing"

func TestLearningObservationMarkerNeedsFreshReviewedStart(t *testing.T) {
	f, task := learningExecutionFixture(t)
	view, _ := f.handler.RecipeExecutorDisclosure(task)
	read := func() Task {
		t.Helper()
		ws, err := f.handler.workspaceStore.Get(task.WorkspaceID)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := ws.GetTask(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		return *saved
	}
	if LearningReviewedStart(read()) != "" {
		t.Fatal("unreviewed work has a start token")
	}
	if err := f.handler.ReviewLearningExecutor(t.Context(), task.WorkspaceID, task.ID, "Robin", view.Digest); err != nil {
		t.Fatal(err)
	}
	first := LearningReviewedStart(read())
	if first == "" {
		t.Fatal("winning owner review lacks a start identity")
	}
	service := NewTicketService(f.handler.workspaceStore)
	binding := LearningObservationBinding{OwnerID: "local", OperationID: "observation", StartToken: first}
	if err := service.BindLearningObservation(task.WorkspaceID, task.ID, binding); err != nil {
		t.Fatal(err)
	}
	if got, ok := LearningObservation(read()); !ok || got != binding {
		t.Fatal("exact correlation unavailable")
	}
	if err := f.handler.ReviewLearningExecutor(t.Context(), task.WorkspaceID, task.ID, "Robin", view.Digest); err != nil {
		t.Fatal(err)
	}
	second := LearningReviewedStart(read())
	if second == "" || second == first {
		t.Fatal("same-input fresh Start reused its prior identity")
	}
	if _, ok := LearningObservation(read()); ok {
		t.Fatal("fresh Start retained old observation")
	}
	if err := service.BindLearningObservation(task.WorkspaceID, task.ID, binding); err == nil {
		t.Fatal("old callback attached to fresh Start")
	}
	binding.StartToken = second
	if err := service.BindLearningObservation(task.WorkspaceID, task.ID, binding); err != nil {
		t.Fatal(err)
	}
	if _, err := f.handler.ExecuteTask(t.Context(), "Robin", task); err != nil {
		t.Fatal(err)
	}
	if LearningReviewedStart(read()) != "" || read().Context[TaskContextLearningObservation] != nil {
		t.Fatal("consumed approval retained reusable correlation")
	}
	if err := service.BindLearningObservation(task.WorkspaceID, task.ID, binding); err == nil {
		t.Fatal("late callback revived consumed approval")
	}
}
