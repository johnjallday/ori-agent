package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Adversarial pass for the Email Ops host quest (#455 task 5.1, FR 49-52).
// Each test drives the real service, store, readiness evaluator, linker, and
// mailbox adapter through the shared harness; only the Gmail credential sink
// and the wizard confirmation are recorded fakes.

// questJourneyTables are every table the setup journey framework writes.
var questJourneyTables = []string{"setup_journey_run", "setup_journey_operation_receipt", "setup_journey_review_receipt"}

func questJourneySnapshot(t *testing.T, h *emailOpsQuestHarness) string {
	t.Helper()
	var snapshot strings.Builder
	for _, table := range questJourneyTables {
		snapshot.WriteString(table)
		snapshot.WriteString(":")
		snapshot.WriteString(dumpQuestTable(t, h.db, table))
		snapshot.WriteString("\n")
	}
	return snapshot.String()
}

func questMailBindings(t *testing.T, h *emailOpsQuestHarness, workspaceID string) int {
	t.Helper()
	saved, err := h.store.Get(workspaceID)
	if err != nil {
		t.Fatalf("get %s: %v", workspaceID, err)
	}
	count := 0
	for _, binding := range saved.MCPBindings {
		if binding.IsNativeEmail() {
			count++
		}
	}
	return count
}

// linkedEmailOpsQuest walks the harness to a ready quest and returns the
// ready projection.
func linkedEmailOpsQuest(t *testing.T, h *emailOpsQuestHarness) *setupjourney.JourneyProjection {
	t.Helper()
	start, err := h.service.Read(context.Background(), "local", "")
	if err != nil {
		t.Fatal(err)
	}
	reviewed := h.review(t, start, "ready-review")
	linked, err := h.link(reviewed.Journey, reviewed.Review, "ready-link")
	if err != nil || linked.Journey.Lifecycle != setupjourney.LifecycleReady || linked.Journey.FirstCompletedAt == nil {
		t.Fatalf("link to ready = %+v err = %v", linked, err)
	}
	return linked.Journey
}

// FR 19, FR 45, FR 50: catalog list, status, Read, and Overview never write a
// journey row, a binding, a sink call, or a wizard confirmation once the root
// exists, and status never creates one.
func TestEmailOpsQuestReadsNeverMutate(t *testing.T) {
	ctx := context.Background()
	h := newEmailOpsQuestHarness(t, connectedWithGmail(connections.HealthHealthy))

	if _, exists, err := h.service.Status(ctx, "local"); err != nil || exists {
		t.Fatalf("status before start exists=%v err=%v", exists, err)
	}
	if _, err := h.service.ListQuests(ctx); err != nil {
		t.Fatal(err)
	}
	if rows := dumpQuestTable(t, h.db, "setup_journey_run"); rows != "" {
		t.Fatalf("status and list created journey rows: %s", rows)
	}

	root, err := h.service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	before := questJourneySnapshot(t, h)
	for round := 0; round < 3; round++ {
		if _, err := h.service.ListQuests(ctx); err != nil {
			t.Fatal(err)
		}
		if projection, exists, err := h.service.Status(ctx, "local"); err != nil || !exists || projection.RunID != root.RunID {
			t.Fatalf("status = %+v exists=%v err=%v", projection, exists, err)
		}
		if again, err := h.service.Read(ctx, "local", root.RunID); err != nil || again.StateRevision != root.StateRevision {
			t.Fatalf("read = %+v err=%v", again, err)
		}
		// Overview belongs to the specialist relationship; a host quest user
		// has none, so it may refuse, but it must never write.
		_, _ = h.service.Overview(ctx, "local")
	}
	if after := questJourneySnapshot(t, h); after != before {
		t.Fatalf("reads changed journey state\nbefore: %s\nafter:  %s", before, after)
	}
	if h.sink.calls != 0 || len(h.wizard.steps) != 0 || questMailBindings(t, h, "ws-email") != 0 {
		t.Fatalf("reads reached a consequence: sink=%d wizard=%v bindings=%d",
			h.sink.calls, h.wizard.steps, questMailBindings(t, h, "ws-email"))
	}
}

// FR 49, FR 51: another user cannot open, dismiss, review, or commit against a
// run they do not own, and a review token only commits the run and user it was
// issued to.
func TestEmailOpsQuestRejectsCrossUserRunsAndTokens(t *testing.T) {
	ctx := context.Background()
	h := newEmailOpsQuestHarness(t, connectedWithGmail(connections.HealthHealthy))
	mine, err := h.service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	reviewed := h.review(t, mine, "mine-review")
	mineAfterReview := reviewed.Journey

	presentation := setupjourney.PresentationMutation{IfRevision: mineAfterReview.StateRevision, IdempotencyKey: "intruder-presentation"}
	if _, err := h.service.Open(ctx, "intruder", mineAfterReview.RunID, presentation); err == nil {
		t.Fatal("another user opened this run")
	}
	if _, err := h.service.Dismiss(ctx, "intruder", mineAfterReview.RunID, presentation); err == nil {
		t.Fatal("another user dismissed this run")
	}
	for _, action := range []setupjourney.ActionID{setupjourney.ActionReviewMailboxLink, setupjourney.ActionLinkMailbox} {
		_, err := h.service.Mutate(ctx, "intruder", mineAfterReview.RunID, action, setupjourney.ActionMutation{
			IfRevision: mineAfterReview.StateRevision, IdempotencyKey: "intruder-" + string(action),
			ReviewToken: reviewed.Review.Token, Input: json.RawMessage(`{}`),
		})
		if err == nil {
			t.Fatalf("another user ran %s on this run", action)
		}
	}

	// The intruder's own run cannot consume this user's review token either.
	theirs, err := h.service.Read(ctx, "intruder", "")
	if err != nil {
		t.Fatal(err)
	}
	if theirs.RunID == mineAfterReview.RunID {
		t.Fatal("two users share one root")
	}
	if _, err := h.service.Mutate(ctx, "intruder", theirs.RunID, setupjourney.ActionLinkMailbox, setupjourney.ActionMutation{
		IfRevision: theirs.StateRevision, IdempotencyKey: "intruder-borrowed-token",
		ReviewToken: reviewed.Review.Token, Input: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("a review token committed a different user's run")
	}

	if h.sink.calls != 0 || questMailBindings(t, h, "ws-email") != 0 {
		t.Fatalf("a cross-user attempt linked a mailbox: sink=%d", h.sink.calls)
	}
	current, err := h.service.Read(ctx, "local", mineAfterReview.RunID)
	if err != nil || current.StateRevision != mineAfterReview.StateRevision || current.Dismissed {
		t.Fatalf("cross-user attempts changed this run: %+v err=%v", current, err)
	}

	// The untouched review still commits for its owner.
	if linked, err := h.link(mineAfterReview, reviewed.Review, "mine-link"); err != nil || linked.Journey.Lifecycle != setupjourney.LifecycleReady {
		t.Fatalf("owner commit after refusals = %+v err=%v", linked, err)
	}
}

// FR 49, FR 51: request input cannot pick the workspace, account, vault, or
// quest source, and an expired or forged review token never commits.
func TestEmailOpsQuestRejectsForgedInputAndDeadReviewTokens(t *testing.T) {
	ctx := context.Background()
	h := newEmailOpsQuestHarness(t, connectedWithGmail(connections.HealthHealthy))
	start, err := h.service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	for i, input := range []string{
		`{"workspace_id":"ws-other"}`, `{"account_id":"acct-evil"}`, `{"vault_id":"v-evil"}`,
		`{"source":"plugin"}`, `{"owner_user_id":"intruder"}`, `[]`, `"ws-other"`, `{"workspace_id":null}`,
	} {
		// The review carries no token, so its refusal is about the input alone.
		if _, err := h.service.Mutate(ctx, "local", start.RunID, setupjourney.ActionReviewMailboxLink, setupjourney.ActionMutation{
			IfRevision: start.StateRevision, IdempotencyKey: "forged-review-" + string(rune('a'+i)), Input: json.RawMessage(input),
		}); !isQuestFailure(err, setupjourney.ReasonInputInvalid) {
			t.Fatalf("review with forged input %s error = %v, want input_invalid", input, err)
		}
		if _, err := h.service.Mutate(ctx, "local", start.RunID, setupjourney.ActionLinkMailbox, setupjourney.ActionMutation{
			IfRevision: start.StateRevision, IdempotencyKey: "forged-link-" + string(rune('a'+i)),
			ReviewToken: "not-a-real-token", Input: json.RawMessage(input),
		}); err == nil {
			t.Fatalf("link accepted forged input %s", input)
		}
	}

	// A made-up token and a token for this run that has expired both refuse.
	if _, err := h.service.Mutate(ctx, "local", start.RunID, setupjourney.ActionLinkMailbox, setupjourney.ActionMutation{
		IfRevision: start.StateRevision, IdempotencyKey: "invented-token", ReviewToken: "invented-token", Input: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("an invented review token committed")
	}
	reviewed := h.review(t, start, "expiring-review")
	past := time.Now().UTC()
	if _, err := h.db.ExecContext(ctx,
		"UPDATE setup_journey_review_receipt SET created_at = ?, expires_at = ? WHERE consumed_at IS NULL",
		past.Add(-2*time.Hour), past.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.link(reviewed.Journey, reviewed.Review, "expired-link"); !isQuestFailure(err, setupjourney.ReasonReviewStale) {
		t.Fatalf("expired review commit error = %v, want review_stale", err)
	}
	if h.sink.calls != 0 || questMailBindings(t, h, "ws-email") != 0 {
		t.Fatalf("forged input or a dead token linked a mailbox: sink=%d", h.sink.calls)
	}

	// A fresh review still works, so the refusals left no poisoned state.
	fresh, err := h.service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	again := h.review(t, fresh, "fresh-review")
	if linked, err := h.link(again.Journey, again.Review, "fresh-link"); err != nil || linked.Journey.Lifecycle != setupjourney.LifecycleReady {
		t.Fatalf("fresh review after refusals = %+v err=%v", linked, err)
	}
}

// FR 51: an idempotency key replayed with a different revision is a conflict,
// never a second link, and a stale revision with a new key is refused.
func TestEmailOpsQuestReplayWithADifferentRevisionConflicts(t *testing.T) {
	ctx := context.Background()
	h := newEmailOpsQuestHarness(t, connectedWithGmail(connections.HealthHealthy))
	start, err := h.service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	reviewed := h.review(t, start, "replay-review")
	linked, err := h.link(reviewed.Journey, reviewed.Review, "replay-link")
	if err != nil {
		t.Fatal(err)
	}

	shifted := *reviewed.Journey
	shifted.StateRevision = linked.Journey.StateRevision
	if _, err := h.link(&shifted, reviewed.Review, "replay-link"); err == nil {
		t.Fatal("the same key with a different revision was accepted")
	}
	stale := *reviewed.Journey
	if _, err := h.link(&stale, reviewed.Review, "replay-link-new-key"); err == nil {
		t.Fatal("a consumed review with an old revision committed again")
	}
	if h.sink.calls != 1 || questMailBindings(t, h, "ws-email") != 1 {
		t.Fatalf("replays linked again: sink=%d bindings=%d", h.sink.calls, questMailBindings(t, h, "ws-email"))
	}
}

// FR 47, FR 36: after completion, deleting the workspace or losing the grant
// regresses the quest honestly, keeps its first completion, and never relinks
// on read. Repair through the owners makes it ready again.
func TestEmailOpsQuestRegressesAfterCompletionWithoutRelinking(t *testing.T) {
	ctx := context.Background()

	t.Run("revoked grant", func(t *testing.T) {
		h := newEmailOpsQuestHarness(t, connectedWithGmail(connections.HealthHealthy))
		ready := linkedEmailOpsQuest(t, h)
		completedAt := *ready.FirstCompletedAt

		if err := h.conns.Save(connectedWithGmail(connections.HealthReconnectRequired)); err != nil {
			t.Fatal(err)
		}
		regressed, err := h.service.Read(ctx, "local", "")
		if err != nil {
			t.Fatal(err)
		}
		connect := h.step(t, regressed, "connect")
		if regressed.Lifecycle != setupjourney.LifecycleNeedsAttention || regressed.FirstCompletedAt == nil ||
			!regressed.FirstCompletedAt.Equal(completedAt) || connect.ReasonCode != setupjourney.ReasonAccountReconnectRequired {
			t.Fatalf("revoked grant projection = %+v connect = %+v", regressed, connect)
		}
		if status, exists, err := h.service.Status(ctx, "local"); err != nil || !exists || status.FirstCompletedAt == nil {
			t.Fatalf("status after revoke = %+v exists=%v err=%v", status, exists, err)
		}

		if err := h.conns.Save(connectedWithGmail(connections.HealthHealthy)); err != nil {
			t.Fatal(err)
		}
		repaired, err := h.service.Read(ctx, "local", "")
		if err != nil || repaired.Lifecycle != setupjourney.LifecycleReady {
			t.Fatalf("after reconnect = %+v err=%v", repaired, err)
		}
		if h.sink.calls != 1 || questMailBindings(t, h, "ws-email") != 1 {
			t.Fatalf("regression and repair relinked: sink=%d bindings=%d", h.sink.calls, questMailBindings(t, h, "ws-email"))
		}
	})

	t.Run("deleted workspace", func(t *testing.T) {
		h := newEmailOpsQuestHarness(t, connectedWithGmail(connections.HealthHealthy))
		ready := linkedEmailOpsQuest(t, h)
		completedAt := *ready.FirstCompletedAt

		if err := h.store.Delete("ws-email"); err != nil {
			t.Fatal(err)
		}
		regressed, err := h.service.Read(ctx, "local", "")
		if err != nil {
			t.Fatal(err)
		}
		team := h.step(t, regressed, "team")
		mailbox := h.step(t, regressed, "mailbox")
		if regressed.Lifecycle == setupjourney.LifecycleReady || regressed.FirstCompletedAt == nil ||
			!regressed.FirstCompletedAt.Equal(completedAt) || team.Status == setupjourney.StepComplete ||
			mailbox.Status == setupjourney.StepComplete {
			t.Fatalf("deleted workspace projection = %+v", regressed)
		}
		if _, err := h.service.Mutate(ctx, "local", regressed.RunID, setupjourney.ActionReviewMailboxLink, setupjourney.ActionMutation{
			IfRevision: regressed.StateRevision, IdempotencyKey: "review-without-workspace", Input: json.RawMessage(`{}`),
		}); err == nil {
			t.Fatal("a mailbox review was offered with no workspace")
		}

		// A new Email Ops workspace is followed, and linking it is a fresh,
		// reviewed consequence rather than a replay of the deleted one.
		replacement := &workspace.Workspace{
			ID: "ws-email-2", Name: "Email Ops", FolderSlug: "email-ops-2", Status: workspace.StatusActive,
			TemplateProvenance: &workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true},
		}
		if err := h.store.Save(replacement); err != nil {
			t.Fatal(err)
		}
		followed, err := h.service.Read(ctx, "local", "")
		if err != nil {
			t.Fatal(err)
		}
		if followed.Receipts.ProjectWorkspaceID != "ws-email-2" || h.step(t, followed, "team").Status != setupjourney.StepComplete ||
			h.step(t, followed, "mailbox").Status != setupjourney.StepActive {
			t.Fatalf("replacement workspace projection = %+v", followed)
		}
		reviewed := h.review(t, followed, "replacement-review")
		if linked, err := h.link(reviewed.Journey, reviewed.Review, "replacement-link"); err != nil || linked.Journey.Lifecycle != setupjourney.LifecycleReady {
			t.Fatalf("link replacement = %+v err=%v", linked, err)
		}
		if h.sink.calls != 2 || !strings.HasSuffix(h.sink.refs[1], "|ws-email-2") || questMailBindings(t, h, "ws-email-2") != 1 {
			t.Fatalf("replacement link sink=%d refs=%v", h.sink.calls, h.sink.refs)
		}
	})
}

// FR 25, FR 34: with two Email Ops workspaces the quest follows the newest, a
// review taken against the older one goes stale when a newer one appears, and
// the older workspace is never linked.
func TestEmailOpsQuestFollowsTheNewestOfTwoEmailOpsWorkspaces(t *testing.T) {
	ctx := context.Background()
	h := newEmailOpsQuestHarness(t, connectedWithGmail(connections.HealthHealthy))
	start, err := h.service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	reviewed := h.review(t, start, "older-review")

	older, err := h.store.Get("ws-email")
	if err != nil {
		t.Fatal(err)
	}
	newer := &workspace.Workspace{
		ID: "ws-email-new", Name: "Email Ops 2", FolderSlug: "email-ops-2", Status: workspace.StatusActive,
		TemplateProvenance: &workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true},
		CreatedAt:          older.CreatedAt.Add(time.Hour), UpdatedAt: older.UpdatedAt.Add(time.Hour),
	}
	if err := h.store.Save(newer); err != nil {
		t.Fatal(err)
	}
	// The newer workspace moves the run's workspace receipt, so the commit is
	// refused either as a moved revision or as a stale review; both tell the
	// user to review the refreshed state, and neither links anything.
	if _, err := h.link(reviewed.Journey, reviewed.Review, "older-link"); !isQuestFailure(err, setupjourney.ReasonReviewStale) &&
		!isQuestFailure(err, setupjourney.ReasonRevisionConflict) {
		t.Fatalf("commit after a newer workspace appeared error = %v, want review_stale or revision_conflict", err)
	}
	if h.sink.calls != 0 {
		t.Fatal("a review of the older workspace linked a mailbox")
	}

	current, err := h.service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if current.Receipts.ProjectWorkspaceID != "ws-email-new" {
		t.Fatalf("quest follows %q, want the newest workspace", current.Receipts.ProjectWorkspaceID)
	}
	again := h.review(t, current, "newest-review")
	if again.Review.AccountLink == nil || again.Review.AccountLink.WorkspaceLabel != "Email Ops 2" {
		t.Fatalf("review names %+v, want the newest workspace", again.Review.AccountLink)
	}
	if linked, err := h.link(again.Journey, again.Review, "newest-link"); err != nil || linked.Journey.Lifecycle != setupjourney.LifecycleReady {
		t.Fatalf("link newest = %+v err=%v", linked, err)
	}
	if questMailBindings(t, h, "ws-email") != 0 || questMailBindings(t, h, "ws-email-new") != 1 {
		t.Fatalf("bindings older=%d newest=%d", questMailBindings(t, h, "ws-email"), questMailBindings(t, h, "ws-email-new"))
	}
}
