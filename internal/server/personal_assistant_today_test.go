package server

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/followup"
)

func TestAssistantToday_ConfirmUsesOnlyTheReviewAndNeverWritesTwice(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	ctx := t.Context()
	due := time.Now().UTC().Add(24 * time.Hour)
	item, err := f.builder.followUpService.Capture(ctx, followup.CaptureInput{
		UserID: "local", WorkspaceID: f.hqID, Category: followup.CategoryWaitingOn,
		Title: "Reply to Mina", Detail: "Keep this detail", DueAt: &due, Source: followup.SourceRef{Type: "manual"},
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err = f.builder.followUpService.LinkTask(ctx, "local", item.ID, followup.TaskRef{WorkspaceID: f.project.ID, TaskID: f.taskID})
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]string{"origin": "personal_assistant_panel", "page_path": "/"}
	ask := func(prompt, id string, selection any) map[string]any {
		t.Helper()
		status, body := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
			"prompt": prompt, "context": refs, "conversation": map[string]string{"id": id}, "today_selection": selection,
		})
		if status != http.StatusOK {
			t.Fatalf("Ask: %d %v", status, body)
		}
		return body
	}
	list := ask("What needs me today?", "", nil)
	id := list["conversation"].(map[string]any)["id"].(string)
	view := list["today"].(map[string]any)["view"]
	review := ask("Snooze Reply to Mina until Monday", id, map[string]any{"view": view})["snooze"].(map[string]any)
	if review["status"] != "review" || review["can_confirm"] != true {
		t.Fatalf("review: %v", review)
	}
	confirm := map[string]any{"action": "confirm", "context": refs, "conversation": map[string]string{"id": id}, "review": review["review"]}
	var outcomes [2]map[string]any
	var wg sync.WaitGroup
	for i := range outcomes {
		wg.Go(func() {
			_, outcomes[i] = f.call(t, http.MethodPost, "/api/home-assistant/today/action", confirm)
		})
	}
	wg.Wait()
	saved := 0
	for _, outcome := range outcomes {
		if outcome["status"] == "saved" && outcome["until"] == review["until"] {
			saved++
		}
	}
	if saved != 1 {
		t.Fatalf("same review outcomes: %v", outcomes)
	}
	actual, err := f.builder.followUpService.Get(ctx, "local", item.ID)
	if err != nil || actual.Status != followup.StatusSnoozed || !actual.DueAt.Equal(due) || actual.WorkspaceID != f.hqID ||
		actual.RelatedTask == nil || *actual.RelatedTask != *item.RelatedTask || actual.Detail != item.Detail || !actual.UpdatedAt.After(item.UpdatedAt) {
		t.Fatalf("result changed unrelated fields: %+v %v", actual, err)
	}
	if _, body := f.call(t, http.MethodGet, "/api/personal-assistant/today", nil); strings.Contains(mustJSON(t, body["today"].(map[string]any)["needs_you"]), item.ID) {
		t.Fatal("snoozed follow-up remained in active Needs you")
	}
	if _, err := f.builder.followUpService.Complete(ctx, "local", item.ID); err != nil {
		t.Fatal(err)
	}
	if _, late := f.call(t, http.MethodPost, "/api/home-assistant/today/action", confirm); late["status"] == "saved" {
		t.Fatalf("late replay overwrote later work: %v", late)
	}
	actual, err = f.builder.followUpService.Get(ctx, "local", item.ID)
	if err != nil || actual.Status != followup.StatusCompleted {
		t.Fatalf("completed item reopened: %+v %v", actual, err)
	}
	if len(f.provider.requests) != 0 {
		t.Fatal("review or confirmation called the model")
	}
}

func TestAssistantToday_SQLiteRejectsCompetingWritersAndHQChanges(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	ctx := t.Context()
	projection, err := f.builder.personalAssistantService.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := f.seedConversation(t, "First turn")
	service := f.builder.followUpService
	capture := func(title string) *followup.FollowUp {
		t.Helper()
		item, err := service.Capture(ctx, followup.CaptureInput{UserID: "local", WorkspaceID: f.hqID, Title: title, Category: followup.CategoryWaitingOn})
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	command := func(item *followup.FollowUp, delay time.Duration) followup.ReviewedSnooze {
		return followup.ReviewedSnooze{UserID: "local", HQWorkspaceID: f.hqID, AssistantID: projection.AssistantID, ConversationID: conversation,
			RelationshipVersion: projection.StateVersion, FollowUpID: item.ID, ExpectedUpdatedAt: item.UpdatedAt, Until: time.Now().UTC().Add(delay)}
	}
	item := capture("Competing dates")
	var results [2]error
	var wg sync.WaitGroup
	for i, delay := range []time.Duration{time.Hour, 2 * time.Hour} {
		wg.Go(func() { _, results[i] = service.SnoozeReviewed(ctx, command(item, delay)) })
	}
	wg.Wait()
	if (results[0] == nil) == (results[1] == nil) {
		t.Fatalf("exactly one of two dates must win: %v", results)
	}
	for _, tc := range []struct {
		name   string
		change func(string) (*followup.FollowUp, error)
	}{
		{"edit", func(id string) (*followup.FollowUp, error) { return service.Edit(ctx, "local", id, "changed", "", nil) }},
		{"complete", func(id string) (*followup.FollowUp, error) { return service.Complete(ctx, "local", id) }},
		{"dismiss", func(id string) (*followup.FollowUp, error) { return service.Dismiss(ctx, "local", id) }},
	} {
		item := capture(tc.name)
		review := command(item, time.Hour)
		if _, err := tc.change(item.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := service.SnoozeReviewed(ctx, review); err != followup.ErrConflict {
			t.Fatalf("stale %s should conflict: %v", tc.name, err)
		}
	}
	item = capture("HQ moves")
	review := command(item, time.Hour)
	if _, err := f.builder.sessionStore.DB().ExecContext(ctx, `UPDATE personal_assistant_state SET hq_workspace_id=? WHERE user_id='local'`, f.project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SnoozeReviewed(ctx, review); err != followup.ErrConflict {
		t.Fatalf("replaced HQ should conflict: %v", err)
	}
	if owner, err := service.Get(ctx, "local", item.ID); err != nil || owner.Status != followup.StatusActive || owner.WorkspaceID != f.hqID {
		t.Fatalf("old owner was mutated: %+v %v", owner, err)
	}
}

func TestAssistantToday_RealRouteAndAskUseHomeRecordsWithoutCallingModel(t *testing.T) {
	f := newWorkspaceReaderFixture(t)
	item, err := f.builder.followUpService.Capture(t.Context(), followup.CaptureInput{
		UserID: "local", WorkspaceID: f.hqID, Category: followup.CategoryWaitingOn,
		Title: "Reply to Mina", Detail: "Detail not for chat", Counterparty: "Counterparty not for chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, home := f.call(t, http.MethodGet, "/api/personal-assistant/today", nil)
	if status != http.StatusOK {
		t.Fatalf("Home Today: %d %v", status, home)
	}
	needs := home["today"].(map[string]any)["needs_you"].(map[string]any)
	var href any
	for _, raw := range needs["items"].([]any) {
		row := raw.(map[string]any)
		if row["id"] == item.ID {
			href = row["route"]
		}
	}
	if href == nil {
		t.Fatal("follow-up missing from Home")
	}
	const prompt = "What needs me today?"
	for _, page := range []string{"/", "/workspaces/" + f.hqSlug} {
		refs := map[string]string{"origin": "personal_assistant_panel", "page_path": page}
		status, route := f.call(t, http.MethodPost, "/api/home-assistant/route", map[string]any{"prompt": prompt, "context": refs})
		if status != http.StatusOK || route["intent"] != "assistant_conversation" || route["route_mode"] != "home_inline" {
			t.Fatalf("Route: %d %v", status, route)
		}
		status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
			"prompt": prompt, "intent": route["intent"], "context": refs, "conversation": map[string]string{},
		})
		conversation, _ := reply["conversation"].(map[string]any)
		answer, _ := reply["today"].(map[string]any)
		rows, _ := answer["rows"].([]any)
		if status != http.StatusOK || conversation["stored"] != true || len(rows) == 0 || len(f.provider.requests) != 0 {
			t.Fatalf("host answer: %d %v (calls: %d)", status, reply, len(f.provider.requests))
		}
		matched := false
		for _, raw := range rows {
			row := raw.(map[string]any)
			matched = matched || (row["title"] == item.Title && row["href"] == href && row["owner"] == "My HQ")
		}
		if !matched || strings.Contains(mustJSON(t, reply), "not for chat") {
			t.Fatalf("not the minimal Home record: %v", rows)
		}
		saved := reloadedConversation(t, f.draftServerFixture, conversation["id"].(string))
		if text := mustJSON(t, saved); !strings.Contains(text, item.Title) || strings.Contains(text, "today_selection") {
			t.Fatalf("history must be inert: %s", text)
		}
	}
	refs := map[string]string{"origin": "personal_assistant_panel", "page_path": "/", "subject_workspace_id": f.project.ID}
	status, reply := f.call(t, http.MethodPost, "/api/home-assistant/ask", map[string]any{
		"prompt": prompt, "context": refs, "conversation": map[string]string{},
	})
	if status != http.StatusOK || reply["today"] != nil || strings.Contains(mustJSON(t, reply), item.Title) {
		t.Fatalf("pinned project widened to personal scope: %d %v", status, reply)
	}
}
