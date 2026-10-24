package personalassistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// resolvedInboxHire runs an inbox hire through the service, from the Desktop
// review to its verified outcome, with the first batch the host reads back.
func resolvedInboxHire(t *testing.T, batch FolderFirstBatch) (*folderDigestFixture, string) {
	t.Helper()
	f, service, source, setup, _ := inboxServiceFixture(t)
	ctx := context.Background()
	observed, err := service.Observe(ctx, source, "chip", "desktop")
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.Review(ctx, source, "conversation", observed.ID, folderRootCandidateID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := f.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	stored := doc.Offer(view.ID)
	setup.intent = FolderSetupIntent{Placement: "standalone", Recipe: folderdigest.BuiltinInboxFolder, RecipeVersion: 1, Inbox: &FolderInboxIntent{
		WorkspaceName: "Desktop", SubjectKey: stored.Subject.Key, SubjectIdentity: stored.ConversationReview.CandidateIdentity,
		BlueprintID: "file-janitor", BlueprintVersion: 5, BlueprintDigest: "blueprint-1", PlanRevision: "plan-1",
		Agent: FolderInboxAgent{RoleID: "file-curator", Name: "File Curator", Action: FolderSoftwareCreate, ConfigDigest: "config-1"},
	}}
	f.service.deps.InboxProject = func(context.Context, string, FolderOffer, string, string) (FolderCreateResult, error) {
		copied := batch
		return FolderCreateResult{WorkspaceID: "ws-desktop", Route: "/workspaces/desktop", Created: true, FirstBatch: &copied}, nil
	}
	service.BindSaved(source, observed.ID, "conversation")
	target := source
	target.ConversationID, target.DraftID = "conversation", ""
	read, err := service.ReadReview(ctx, target, view.ID)
	if err != nil || read.Plan == nil {
		t.Fatalf("review plan = %+v %v", read, err)
	}
	if _, err := f.service.StartSetup(ctx, "local", view.ID, FolderSetupInput{RequestID: "hire-1", PlanDigest: read.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the inbox hire to resolve", func() bool {
		doc, err := f.store.Read(ctx, "local")
		offer := doc.Offer(view.ID)
		return err == nil && offer != nil && offer.Status == FolderOfferResolved
	})
	return f, view.ID
}

func storedFirstBatch(t *testing.T, f *folderDigestFixture, offerID string) (FolderOffer, *FolderFirstBatch) {
	t.Helper()
	doc, err := f.store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	offer := doc.Offer(offerID)
	if offer == nil || offer.Outcome == nil || offer.Outcome.FirstLook == nil {
		t.Fatalf("no first look on %s", offerID)
	}
	return *offer, offer.Outcome.FirstLook.Batch
}

func TestFirstBatchReport_CountsActionsAndNoPath(t *testing.T) {
	ready := FolderFirstBatch{Outcome: FolderFirstBatchReady, BatchID: "batch-1", Eligible: 23, NeedsReview: 4, Ineligible: 1,
		Route: "/workspaces/desktop?panel=file-janitor&batch_id=batch-1"}
	f, offerID := resolvedInboxHire(t, ready)
	offer, _ := storedFirstBatch(t, f, offerID)
	ws := &workspace.Workspace{ID: "ws-desktop", Name: "Desktop", FolderSlug: "desktop"}
	now := offer.Outcome.FirstLook.At.Add(time.Minute)

	needs, handled := firstBatchReportItems(offer, ws, now)
	if !handled || len(needs) != 1 {
		t.Fatalf("items = %+v handled %v", needs, handled)
	}
	item := needs[0]
	if item.Kind != TodayKindFirstBatchReport || item.State != "finished" || item.Attribution != "File Curator" || item.Workspace != "Desktop" ||
		item.Title != "File Curator prepared a first batch for Desktop: 23 files proposed for filing, 4 need your review." ||
		strings.Join(item.Actions, ",") != "review_batch,not_now" || item.ReportRoute != ready.Route ||
		item.Ref.EntityType != "folder_offer" || item.Ref.EntityID != offerID {
		t.Fatalf("ready item = %+v", item)
	}
	if strings.Contains(item.Title+item.Detail, f.home) {
		t.Fatalf("the report names a path: %+v", item)
	}

	// Singular and plural, nothing to file, and a scan that did not complete.
	if got := firstBatchSentence("File Curator", "Desktop", 1, 1); got != "File Curator prepared a first batch for Desktop: 1 file proposed for filing, 1 needs your review." {
		t.Fatalf("singular = %q", got)
	}
	if got := firstBatchSentence("File Curator", "Desktop", 2, 0); got != "File Curator prepared a first batch for Desktop: 2 files proposed for filing, 0 need your review." {
		t.Fatalf("plural = %q", got)
	}
	for outcome, want := range map[string]struct{ title, actions, state string }{
		FolderFirstBatchEmpty:  {"Nothing to file in Desktop right now.", "open_janitor,not_now", "finished"},
		FolderFirstBatchFailed: {"The first scan of Desktop did not complete.", "retry", "failed"},
	} {
		changed := offer
		look := *offer.Outcome.FirstLook
		outcomeCopy := *offer.Outcome
		look.Batch = &FolderFirstBatch{Outcome: outcome, Route: "/workspaces/desktop?panel=file-janitor"}
		outcomeCopy.FirstLook = &look
		changed.Outcome = &outcomeCopy
		needs, _ := firstBatchReportItems(changed, ws, now)
		if len(needs) != 1 || needs[0].Title != want.title || strings.Join(needs[0].Actions, ",") != want.actions || needs[0].State != want.state {
			t.Fatalf("%s item = %+v", outcome, needs)
		}
	}

	// A report older than a folder receipt is shown no more; no first-look
	// item stands in for it either.
	if needs, handled := firstBatchReportItems(offer, ws, now.Add(8*24*time.Hour)); !handled || len(needs) != 0 {
		t.Fatalf("an old report = %+v handled %v", needs, handled)
	}
}

func TestFirstBatchReport_NotNowTakesItAwayAndKeepsTheBatch(t *testing.T) {
	f, offerID := resolvedInboxHire(t, FolderFirstBatch{Outcome: FolderFirstBatchReady, BatchID: "batch-1", Eligible: 3, Route: "/workspaces/desktop?panel=file-janitor&batch_id=batch-1"})
	ctx := context.Background()
	if err := f.service.DismissFirstBatch(ctx, "local", offerID); err != nil {
		t.Fatal(err)
	}
	offer, batch := storedFirstBatch(t, f, offerID)
	if batch == nil || batch.DismissedAt == nil || batch.BatchID != "batch-1" {
		t.Fatalf("batch after Not now = %+v", batch)
	}
	if needs, handled := firstBatchReportItems(offer, &workspace.Workspace{ID: "ws-desktop", Name: "Desktop"}, offer.Outcome.FirstLook.At.Add(time.Minute)); !handled || len(needs) != 0 {
		t.Fatalf("a dismissed report = %+v", needs)
	}
	// A repeated Not now is a replay; another user's or an unknown offer is not found.
	if err := f.service.DismissFirstBatch(ctx, "local", offerID); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := f.service.DismissFirstBatch(ctx, "local", "offer-unknown"); !errors.Is(err, ErrFirstLookNotFound) {
		t.Fatalf("unknown offer: %v", err)
	}
}

func TestFirstBatchReport_RetryRunsTheScanAgainAndReplacesTheReport(t *testing.T) {
	f, offerID := resolvedInboxHire(t, FolderFirstBatch{Outcome: FolderFirstBatchFailed, Route: "/workspaces/desktop?panel=file-janitor"})
	ctx := context.Background()
	var attempts []int
	f.service.deps.InboxRetry = func(_ context.Context, _ string, offer FolderOffer, attempt int) (FolderFirstBatch, error) {
		attempts = append(attempts, attempt)
		if attempt == 1 {
			return FolderFirstBatch{Outcome: FolderFirstBatchFailed, Route: "/workspaces/desktop?panel=file-janitor"}, nil
		}
		return FolderFirstBatch{Outcome: FolderFirstBatchReady, BatchID: "batch-2", Eligible: 5, NeedsReview: 1,
			Route: "/workspaces/desktop?panel=file-janitor&batch_id=batch-2"}, nil
	}
	first, err := f.service.RetryFirstBatch(ctx, "local", offerID)
	if err != nil || first.Outcome != FolderFirstBatchFailed || first.Attempts != 1 {
		t.Fatalf("first retry = %+v, %v", first, err)
	}
	second, err := f.service.RetryFirstBatch(ctx, "local", offerID)
	if err != nil || second.Outcome != FolderFirstBatchReady || second.Attempts != 2 {
		t.Fatalf("second retry = %+v, %v", second, err)
	}
	if len(attempts) != 2 || attempts[0] != 1 || attempts[1] != 2 {
		t.Fatalf("attempts = %v", attempts)
	}
	offer, batch := storedFirstBatch(t, f, offerID)
	if batch.BatchID != "batch-2" || batch.Eligible != 5 || offer.Status != FolderOfferResolved || offer.Outcome.WorkspaceID != "ws-desktop" {
		t.Fatalf("stored = %+v outcome %+v", batch, offer.Outcome)
	}
	// A completed scan is not retried.
	if _, err := f.service.RetryFirstBatch(ctx, "local", offerID); !errors.Is(err, ErrFirstBatchNotFailed) || len(attempts) != 2 {
		t.Fatalf("retry of a completed scan: %v attempts %v", err, attempts)
	}
}
