package workspaceplan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// revisable materializes a plan at version 1 and returns everything needed to
// revise it.
func revisable(t *testing.T, ctx context.Context) (
	*Service, *Materializer, *Reconciler, *fakeTaskWriter, *Plan) {
	t.Helper()

	service := reviewService(t)
	writer := newFakeTaskWriter()
	reconciler := NewReconciler(service, writer, &fakeMutator{writer: writer})
	materializer := NewMaterializer(service, writer, WithReconciler(reconciler))

	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	_, approval := approveNow(t, ctx, service, plan, "v1")
	if _, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID}); err != nil {
		t.Fatalf("materialize v1: %v", err)
	}
	service.progress = NewTaskProgressSource(writer)

	refreshed, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	return service, materializer, reconciler, writer, refreshed
}

// reviseTo starts a revision with the given intent, writes new content, and
// snapshots it as the next version.
func reviseTo(
	t *testing.T, ctx context.Context, service *Service, plan *Plan,
	intent RevisionIntent, content PlanContent,
) *Version {
	t.Helper()
	revised, err := service.EditApproved(ctx, "ws-1", plan.ID, intent, "jj")
	if err != nil {
		t.Fatalf("edit approved: %v", err)
	}
	if _, err := service.Store().UpdatePlanDraft(ctx, "ws-1", plan.ID, revised.DraftRevision, DraftUpdate{
		Title:     revised.Title,
		Objective: revised.Objective,
		Content:   content,
		Intent:    intent,
		UpdatedAt: service.Now(),
	}); err != nil {
		t.Fatalf("write revised draft: %v", err)
	}
	version, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj", Intent: intent})
	if err != nil {
		t.Fatalf("request review of revision: %v", err)
	}
	return version
}

// withExtraItem returns the base content plus one new item in the same group.
func withExtraItem() PlanContent {
	content := reviewableContent()
	content.Groups[0].Items = append(content.Groups[0].Items, TaskItem{
		ID: "itm-3", Description: "Archive the old tables", Assignee: "builder",
	})
	return content
}

// --- Additive revisions add and disturb nothing (FR-76) --------------------

func TestAdditivePreviewRetainsEveryPriorTask(t *testing.T) {
	ctx := context.Background()
	service, _, reconciler, _, plan := revisable(t, ctx)
	reviseTo(t, ctx, service, plan, RevisionAdditive, withExtraItem())

	preview, err := reconciler.Preview(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if preview.Summary[DispositionCreated] != 1 {
		t.Errorf("created = %d, want 1", preview.Summary[DispositionCreated])
	}
	if preview.Summary[DispositionRetained] != 2 {
		t.Errorf("retained = %d, want the two prior items", preview.Summary[DispositionRetained])
	}
	for _, disposition := range []ReconcileDisposition{DispositionCancel, DispositionReplace} {
		if preview.Summary[disposition] != 0 {
			t.Errorf("%s = %d; an additive revision cancels nothing",
				disposition, preview.Summary[disposition])
		}
	}
	// Nothing is cancelled, so there is nothing extra to confirm.
	if preview.RequiresConfirmation {
		t.Error("an additive revision asked for a separate confirmation")
	}
}

func TestAdditiveMaterializationCreatesOnlyTheNewTask(t *testing.T) {
	ctx := context.Background()
	service, materializer, _, writer, plan := revisable(t, ctx)
	before := len(writer.tasks())

	version := reviseTo(t, ctx, service, plan, RevisionAdditive, withExtraItem())
	approval := approveExact(t, ctx, service, plan, version)

	result, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID})
	if err != nil {
		t.Fatalf("materialize revision: %v", err)
	}

	// One new item task. The group and both prior items are carried, so they
	// are not recreated.
	if len(result.TaskIDs) != 1 {
		t.Errorf("created %d task(s), want 1: %v", len(result.TaskIDs), result.TaskIDs)
	}
	if got := len(writer.tasks()); got != before+1 {
		t.Errorf("workspace has %d tasks, want %d", got, before+1)
	}
	for _, task := range writer.tasks() {
		if task.Status == workspace.TaskStatusCancelled {
			t.Errorf("an additive revision cancelled %q", task.Description)
		}
	}
}

// The new item is parented to the SAME group task as the prior items, rather
// than to a duplicate group created for the new version.
func TestAdditiveRevisionReusesTheExistingGroupTask(t *testing.T) {
	ctx := context.Background()
	service, materializer, _, writer, plan := revisable(t, ctx)

	version := reviseTo(t, ctx, service, plan, RevisionAdditive, withExtraItem())
	approval := approveExact(t, ctx, service, plan, version)
	if _, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID}); err != nil {
		t.Fatalf("materialize revision: %v", err)
	}

	parents := map[string]bool{}
	var groups int
	for _, task := range writer.tasks() {
		if task.ParentTaskID == "" {
			groups++
			continue
		}
		parents[task.ParentTaskID] = true
	}
	if groups != 1 {
		t.Errorf("workspace has %d group tasks, want 1", groups)
	}
	if len(parents) != 1 {
		t.Errorf("items are parented to %d different groups, want 1", len(parents))
	}
}

// --- Corrective revisions need a confirmation (FR-77) ----------------------

// withRemovedItem drops the second item, so a corrective revision has
// something to cancel.
func withRemovedItem() PlanContent {
	content := reviewableContent()
	content.Groups[0].Items = content.Groups[0].Items[:1]
	return content
}

func TestCorrectivePreviewCancelsOnlyUnstartedWork(t *testing.T) {
	ctx := context.Background()
	service, _, reconciler, writer, plan := revisable(t, ctx)

	// The first item completed; the second never started.
	setTaskStatus(t, writer, taskIDByDescription(t, writer, "Snapshot staging"),
		workspace.TaskStatusCompleted)

	reviseTo(t, ctx, service, plan, RevisionCorrective, withRemovedItem())
	preview, err := reconciler.Preview(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if !preview.RequiresConfirmation {
		t.Error("a corrective revision did not ask for a separate confirmation")
	}
	if preview.Summary[DispositionCancel] != 1 {
		t.Errorf("cancel = %d, want the one unstarted dropped item",
			preview.Summary[DispositionCancel])
	}
	// The completed item is still in the revision and unchanged, so it is
	// retained rather than touched.
	if preview.Summary[DispositionRetained] != 1 {
		t.Errorf("retained = %d, want the completed item",
			preview.Summary[DispositionRetained])
	}
}

// A completed Task that the revision drops is immutable: it is left alone
// entirely, never cancelled.
func TestCorrectiveRevisionNeverCancelsCompletedWork(t *testing.T) {
	ctx := context.Background()
	service, _, reconciler, writer, plan := revisable(t, ctx)

	completed := taskIDByDescription(t, writer, "Verify checksums")
	setTaskStatus(t, writer, completed, workspace.TaskStatusCompleted)

	reviseTo(t, ctx, service, plan, RevisionCorrective, withRemovedItem())
	preview, err := reconciler.Preview(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if preview.Summary[DispositionImmutable] != 1 {
		t.Fatalf("immutable = %d, want the completed dropped item: %+v",
			preview.Summary[DispositionImmutable], preview.Entries)
	}
	for _, entry := range preview.Entries {
		if entry.TaskID == completed && entry.Disposition == DispositionCancel {
			t.Error("a completed task was scheduled for cancellation")
		}
	}
}

func TestMaterializingACorrectiveRevisionNeedsAConfirmation(t *testing.T) {
	ctx := context.Background()
	service, materializer, _, _, plan := revisable(t, ctx)

	version := reviseTo(t, ctx, service, plan, RevisionCorrective, withRemovedItem())
	approval := approveExact(t, ctx, service, plan, version)

	_, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID})
	if !errors.Is(err, ErrReconciliationNotFound) {
		t.Fatalf("error = %v, want a missing reconciliation confirmation", err)
	}
}

func TestConfirmedCorrectiveRevisionCancelsAndRetires(t *testing.T) {
	ctx := context.Background()
	service, materializer, reconciler, writer, plan := revisable(t, ctx)
	dropped := taskIDByDescription(t, writer, "Verify checksums")

	version := reviseTo(t, ctx, service, plan, RevisionCorrective, withRemovedItem())
	preview, err := reconciler.Preview(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := reconciler.Confirm(ctx, "ws-1", plan.ID,
		ConfirmInput{Token: preview.Token, Actor: "jj"}); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	approval := approveExact(t, ctx, service, plan, version)
	if _, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID}); err != nil {
		t.Fatalf("materialize revision: %v", err)
	}

	// The dropped Task is cancelled, not deleted.
	var found bool
	for _, task := range writer.tasks() {
		if task.ID != dropped {
			continue
		}
		found = true
		if task.Status != workspace.TaskStatusCancelled {
			t.Errorf("dropped task status = %q, want cancelled", task.Status)
		}
	}
	if !found {
		t.Error("the dropped task was deleted; reconciliation must never delete work")
	}

	// Its link is retired rather than removed, so the history still shows the
	// work this plan once committed to.
	refreshed, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	var retired bool
	for _, link := range refreshed.TaskLinks {
		if link.TaskID == dropped {
			retired = link.RetiredAt != nil
		}
	}
	if !retired {
		t.Error("the dropped task's link was not retired")
	}
}

// Dropping an item from a group must not produce a second group Task.
//
// This is a regression test for a bug the live demo caught: the group was
// treated as "fresh" because one of its items was not carried, so a duplicate
// container appeared with no children — the retained items were still parented
// to the original.
func TestDroppingAnItemDoesNotDuplicateItsGroup(t *testing.T) {
	ctx := context.Background()
	service, materializer, reconciler, writer, plan := revisable(t, ctx)

	version := reviseTo(t, ctx, service, plan, RevisionCorrective, withRemovedItem())
	preview, err := reconciler.Preview(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := reconciler.Confirm(ctx, "ws-1", plan.ID,
		ConfirmInput{Token: preview.Token, Actor: "jj"}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	approval := approveExact(t, ctx, service, plan, version)
	if _, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID}); err != nil {
		t.Fatalf("materialize revision: %v", err)
	}

	var groups []workspace.Task
	for _, task := range writer.tasks() {
		if task.ParentTaskID == "" {
			groups = append(groups, task)
		}
	}
	if len(groups) != 1 {
		descriptions := make([]string, 0, len(groups))
		for _, group := range groups {
			descriptions = append(descriptions, group.Description)
		}
		t.Fatalf("workspace has %d group tasks, want 1: %v", len(groups), descriptions)
	}

	// And the surviving item is still parented to that one group.
	for _, task := range writer.tasks() {
		if task.Description == "Snapshot staging" && task.ParentTaskID != groups[0].ID {
			t.Errorf("the retained item is parented to %q, not to the surviving group",
				task.ParentTaskID)
		}
	}
}

// --- Follow-up work beside immutable work (FR-78) --------------------------

// withChangedItem keeps both items but revises the second one's instruction.
func withChangedItem() PlanContent {
	content := reviewableContent()
	content.Groups[0].Items[1].Description = "Verify checksums against the audit log"
	return content
}

// A completed Task whose item the revision CHANGED gets follow-up work: the
// completed Task is untouched, and a new Task appears beside it for the revised
// instruction. Erasing the completed work would erase what actually happened.
func TestACompletedItemGetsFollowUpWorkRatherThanRewriting(t *testing.T) {
	ctx := context.Background()
	service, materializer, reconciler, writer, plan := revisable(t, ctx)

	completed := taskIDByDescription(t, writer, "Verify checksums")
	setTaskStatus(t, writer, completed, workspace.TaskStatusCompleted)

	version := reviseTo(t, ctx, service, plan, RevisionCorrective, withChangedItem())
	preview, err := reconciler.Preview(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Summary[DispositionFollowUp] != 1 {
		t.Fatalf("follow_up = %d, want 1: %+v", preview.Summary[DispositionFollowUp], preview.Entries)
	}

	if _, err := reconciler.Confirm(ctx, "ws-1", plan.ID,
		ConfirmInput{Token: preview.Token, Actor: "jj"}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	approval := approveExact(t, ctx, service, plan, version)
	if _, err := materializer.Materialize(ctx, "ws-1", plan.ID,
		MaterializeInput{ApprovalID: approval.ID}); err != nil {
		t.Fatalf("materialize revision: %v", err)
	}

	// The completed Task is exactly as it was.
	var original workspace.Task
	var followUp bool
	for _, task := range writer.tasks() {
		if task.ID == completed {
			original = task
		}
		if task.Description == "Verify checksums against the audit log" {
			followUp = true
		}
	}
	if original.ID == "" {
		t.Fatal("the completed task was deleted")
	}
	if original.Status != workspace.TaskStatusCompleted {
		t.Errorf("completed task status = %q, want it untouched", original.Status)
	}
	if original.Description != "Verify checksums" {
		t.Errorf("the completed task's description was rewritten to %q", original.Description)
	}
	if !followUp {
		t.Error("no follow-up task was created for the revised instruction")
	}
}

// --- Stale previews are refused (FR-77) ------------------------------------

// A Task that started between the preview and the confirmation changes the
// token, and the confirmation is refused rather than cancelling running work.
func TestAStartedTaskInvalidatesAPreview(t *testing.T) {
	ctx := context.Background()
	service, _, reconciler, writer, plan := revisable(t, ctx)

	reviseTo(t, ctx, service, plan, RevisionCorrective, withRemovedItem())
	preview, err := reconciler.Preview(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	// The work the preview said it would cancel starts.
	setTaskStatus(t, writer, taskIDByDescription(t, writer, "Verify checksums"),
		workspace.TaskStatusInProgress)

	_, err = reconciler.Confirm(ctx, "ws-1", plan.ID,
		ConfirmInput{Token: preview.Token, Actor: "jj"})
	if !errors.Is(err, ErrStalePreview) {
		t.Fatalf("error = %v, want a stale preview", err)
	}
}

// A confirmation authorizes one reconciliation. A second materialization
// attempt cannot spend it again.
func TestAConfirmationIsSingleUse(t *testing.T) {
	ctx := context.Background()
	service, _, reconciler, _, plan := revisable(t, ctx)

	reviseTo(t, ctx, service, plan, RevisionCorrective, withRemovedItem())
	preview, err := reconciler.Preview(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	confirmation, err := reconciler.Confirm(ctx, "ws-1", plan.ID,
		ConfirmInput{Token: preview.Token, Actor: "jj"})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}

	if err := reconciler.Apply(ctx, "ws-1", plan.ID, preview, confirmation); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	err = reconciler.Apply(ctx, "ws-1", plan.ID, preview, confirmation)
	if !errors.Is(err, ErrReconciliationConsumed) {
		t.Fatalf("second apply error = %v, want already consumed", err)
	}
}

// Confirming the same preview twice records one decision, so a double click
// cannot authorize two rounds of cancellation.
func TestConfirmingTwiceRecordsOneDecision(t *testing.T) {
	ctx := context.Background()
	service, _, reconciler, _, plan := revisable(t, ctx)

	reviseTo(t, ctx, service, plan, RevisionCorrective, withRemovedItem())
	preview, err := reconciler.Preview(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	first, err := reconciler.Confirm(ctx, "ws-1", plan.ID, ConfirmInput{Token: preview.Token})
	if err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	second, err := reconciler.Confirm(ctx, "ws-1", plan.ID, ConfirmInput{Token: preview.Token})
	if err != nil {
		t.Fatalf("second confirm: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("two confirmations recorded: %s vs %s", first.ID, second.ID)
	}
}

// A confirmation naming a preview the server never produced is refused.
func TestAnInventedTokenIsRefused(t *testing.T) {
	ctx := context.Background()
	service, _, reconciler, _, plan := revisable(t, ctx)
	reviseTo(t, ctx, service, plan, RevisionCorrective, withRemovedItem())

	_, err := reconciler.Confirm(ctx, "ws-1", plan.ID, ConfirmInput{Token: "not-a-real-token"})
	if !errors.Is(err, ErrStalePreview) {
		t.Fatalf("error = %v, want a stale preview", err)
	}
}

// --- Preconditions ---------------------------------------------------------

// A plan whose revision has not been sent for review has nothing to preview:
// there is no second version to compare against.
func TestPreviewNeedsAVersionUnderReview(t *testing.T) {
	ctx := context.Background()
	service, _, reconciler, _, plan := revisable(t, ctx)
	if _, err := service.EditApproved(ctx, "ws-1", plan.ID, RevisionCorrective, "jj"); err != nil {
		t.Fatalf("edit approved: %v", err)
	}

	_, err := reconciler.Preview(ctx, "ws-1", plan.ID)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("error = %v, want an invalid transition", err)
	}
	if !strings.Contains(err.Error(), "request review") {
		t.Errorf("error does not say what to do next: %v", err)
	}
}

// approveExact approves one specific version.
func approveExact(
	t *testing.T, ctx context.Context, service *Service, plan *Plan, version *Version,
) *Approval {
	t.Helper()
	approval, err := service.Approve(ctx, "ws-1", plan.ID, ApprovalRequest{
		Version:        version.Number,
		ContentHash:    version.ContentHash,
		Effect:         EffectFor(version.Content.Execution.Mode),
		UserName:       "jj",
		IdempotencyKey: fmt.Sprintf("approve-v%d", version.Number),
	})
	if err != nil {
		t.Fatalf("approve version %d: %v", version.Number, err)
	}
	return approval
}
