package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filecuratorguidance"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// resolvedInboxHire runs an inbox hire through the service, from the Desktop
// review to its verified outcome, with the first batch the host reads back.
func resolvedInboxHire(t *testing.T, batch FolderFirstBatch) (*folderDigestFixture, string) {
	t.Helper()
	f, offerID, _ := resolvedInboxHireWatching(t, batch, false)
	return f, offerID
}

// resolvedInboxHireWatching is resolvedInboxHire with the watching checkbox
// ticked or not; it also returns the digest of the plan the user confirmed.
func resolvedInboxHireWatching(t *testing.T, batch FolderFirstBatch, watching bool) (*folderDigestFixture, string, *fakeFolderSetup) {
	t.Helper()
	f, service, source, setup, _ := inboxServiceFixture(t)
	setup.monitoring = &FolderMonitoringOffer{Time: "09:00"}
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
	if _, err := f.service.StartSetup(ctx, "local", view.ID, FolderSetupInput{RequestID: "hire-1", PlanDigest: read.Plan.Digest, Monitoring: watching}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the inbox hire to resolve", func() bool {
		doc, err := f.store.Read(ctx, "local")
		offer := doc.Offer(view.ID)
		return err == nil && offer != nil && offer.Status == FolderOfferResolved
	})
	return f, view.ID, setup
}

// The watching checkbox is the user's tick, outside the plan digest: unticked
// the run is asked for no monitoring, ticked it is, and its fifth line shows.
func TestFolderInbox_TheWatchingTickTravelsInTheRequestNotThePlan(t *testing.T) {
	batch := FolderFirstBatch{Outcome: FolderFirstBatchEmpty, Route: "/workspaces/desktop?panel=file-janitor"}
	for _, watching := range []bool{false, true} {
		f, offerID, setup := resolvedInboxHireWatching(t, batch, watching)
		offer, _ := storedFirstBatch(t, f, offerID)
		ran := setup.ran[0]
		if ran.Monitoring != watching || offer.Setup.Monitoring != watching {
			t.Fatalf("watching %v: run asked %v, stored %v", watching, ran.Monitoring, offer.Setup.Monitoring)
		}
		hasLine := false
		for _, line := range offer.Setup.Lines {
			hasLine = hasLine || line.Name == "Watching turned on"
		}
		if hasLine != watching || len(ran.Plan.Lines) != len(offer.Setup.Lines) {
			t.Fatalf("watching %v: fifth line %v, run lines %d stored %d", watching, hasLine, len(ran.Plan.Lines), len(offer.Setup.Lines))
		}
	}
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

	needs, handled := firstBatchReportItems(context.Background(), offer, ws, now, nil)
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
		needs, _ := firstBatchReportItems(context.Background(), changed, ws, now, nil)
		if len(needs) != 1 || needs[0].Title != want.title || strings.Join(needs[0].Actions, ",") != want.actions || needs[0].State != want.state {
			t.Fatalf("%s item = %+v", outcome, needs)
		}
	}

	// A report older than a folder receipt is shown no more; no first-look
	// item stands in for it either.
	if needs, handled := firstBatchReportItems(context.Background(), offer, ws, now.Add(8*24*time.Hour), nil); !handled || len(needs) != 0 {
		t.Fatalf("an old report = %+v handled %v", needs, handled)
	}
}

// fakeFirstBatchProtection is the stored protection read, recording who asked.
type fakeFirstBatchProtection struct {
	fact       filecuratorguidance.ProtectionFact
	err        error
	user, read string
}

func (f *fakeFirstBatchProtection) ReadProtection(_ context.Context, user, id string) (filecuratorguidance.ProtectionFact, error) {
	f.user, f.read = user, id
	return f.fact, f.err
}

// An unmet protection fact leads the Ready report with a backup suggestion and
// Check protection; covered, unavailable, another workspace's fact or a failed
// read leave the report as it was, and the other outcomes never change.
func TestFirstBatchReport_LeadsWithBackupWhenProtectionIsUnmet(t *testing.T) {
	ready := FolderFirstBatch{Outcome: FolderFirstBatchReady, BatchID: "batch-1", Eligible: 23, NeedsReview: 4,
		Route: "/workspaces/desktop?panel=file-janitor&batch_id=batch-1"}
	f, offerID := resolvedInboxHire(t, ready)
	offer, _ := storedFirstBatch(t, f, offerID)
	ws := &workspace.Workspace{ID: "ws-desktop", Name: "Desktop", FolderSlug: "desktop"}
	now := offer.Outcome.FirstLook.At.Add(time.Minute)
	ctx := context.Background()
	title := "File Curator prepared a first batch for Desktop: 23 files proposed for filing, 4 need your review."

	for state, evidence := range map[string]string{
		"not_covered": "This folder is not in the backup evidence Ori observes. Other backups may exist.",
		"unknown":     "Protection is unknown. Missing evidence does not mean there is no backup.",
		"configured":  "A backup source is configured, but Ori has not observed a completed backup.",
	} {
		reader := &fakeFirstBatchProtection{fact: filecuratorguidance.ProtectionFact{WorkspaceID: "ws-desktop", State: state}}
		needs, handled := firstBatchReportItems(ctx, offer, ws, now, reader)
		if !handled || len(needs) != 1 {
			t.Fatalf("%s: items = %+v handled %v", state, needs, handled)
		}
		item := needs[0]
		if item.Suggestion == nil || item.Suggestion.Text != "Back up Desktop before the first filing batch" || item.Suggestion.Evidence != evidence ||
			strings.Join(item.Actions, ",") != "check_protection,review_batch,not_now" || item.Title != title {
			t.Fatalf("%s: item = %+v suggestion %+v", state, item, item.Suggestion)
		}
		// The owner reads it; an ownerless workspace is the local user's.
		if reader.user != "local" || reader.read != "ws-desktop" {
			t.Fatalf("%s: read as %q for %q", state, reader.user, reader.read)
		}
	}

	owned := &workspace.Workspace{ID: "ws-desktop", Name: "Desktop", FolderSlug: "desktop", OwnerUserID: "someone"}
	reader := &fakeFirstBatchProtection{fact: filecuratorguidance.ProtectionFact{WorkspaceID: "ws-desktop", State: "unknown"}}
	if needs, _ := firstBatchReportItems(ctx, offer, owned, now, reader); len(needs) != 1 || needs[0].Suggestion == nil || reader.user != "someone" {
		t.Fatalf("owned workspace: %+v read as %q", needs, reader.user)
	}

	for name, reader := range map[string]*fakeFirstBatchProtection{
		"covered":         {fact: filecuratorguidance.ProtectionFact{WorkspaceID: "ws-desktop", State: "covered"}},
		"unavailable":     {fact: filecuratorguidance.ProtectionFact{WorkspaceID: "ws-desktop", State: "unavailable"}},
		"other workspace": {fact: filecuratorguidance.ProtectionFact{WorkspaceID: "ws-other", State: "not_covered"}},
		"read error":      {fact: filecuratorguidance.ProtectionFact{WorkspaceID: "ws-desktop", State: "not_covered"}, err: errors.New("store down")},
	} {
		needs, _ := firstBatchReportItems(ctx, offer, ws, now, reader)
		if len(needs) != 1 || needs[0].Suggestion != nil || strings.Join(needs[0].Actions, ",") != "review_batch,not_now" || needs[0].Title != title {
			t.Fatalf("%s: item = %+v", name, needs)
		}
	}

	// Empty and failed scans never lead with backup, whatever the fact.
	unmet := &fakeFirstBatchProtection{fact: filecuratorguidance.ProtectionFact{WorkspaceID: "ws-desktop", State: "not_covered"}}
	for outcome, actions := range map[string]string{FolderFirstBatchEmpty: "open_janitor,not_now", FolderFirstBatchFailed: "retry"} {
		changed := offer
		look := *offer.Outcome.FirstLook
		outcomeCopy := *offer.Outcome
		look.Batch = &FolderFirstBatch{Outcome: outcome, Route: "/workspaces/desktop?panel=file-janitor"}
		outcomeCopy.FirstLook = &look
		changed.Outcome = &outcomeCopy
		needs, _ := firstBatchReportItems(ctx, changed, ws, now, unmet)
		if len(needs) != 1 || needs[0].Suggestion != nil || strings.Join(needs[0].Actions, ",") != actions {
			t.Fatalf("%s item = %+v", outcome, needs)
		}
	}
}

// The card's evidence line is the review console's sentence for the same
// state, word for word: the Go table equals the JS protectionSentence.
func TestFirstBatchReport_ProtectionSentencesMatchTheReviewConsole(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "web", "static", "js", "modules", "file-janitor-protection.js"))
	if err != nil {
		t.Fatal(err)
	}
	js := map[string]string{}
	for _, match := range regexp.MustCompile(`case '([a-z_]+)':\s*return '([^']+)';`).FindAllStringSubmatch(string(source), -1) {
		js[match[1]] = match[2]
	}
	if len(firstBatchProtectionSentences) != 3 {
		t.Fatalf("unmet states = %v", firstBatchProtectionSentences)
	}
	for state, sentence := range firstBatchProtectionSentences {
		if js[state] != sentence {
			t.Fatalf("%s: Go %q, JS %q", state, sentence, js[state])
		}
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
	if needs, handled := firstBatchReportItems(ctx, offer, &workspace.Workspace{ID: "ws-desktop", Name: "Desktop"}, offer.Outcome.FirstLook.At.Add(time.Minute), nil); !handled || len(needs) != 0 {
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
