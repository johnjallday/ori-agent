package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestFolderOptionalSetup_RefreshesAnUnstartedLegacyReview(t *testing.T) {
	f := folderHireFixture(t)
	look := &fakeFirstLook{result: FolderFirstLookStart{Started: true}}
	f.service.deps.FirstLook = look
	old := f.offer.Plan.Digest
	f.setup.intent.OptionalTaskPolicy = FolderOptionalSummaryPolicy
	fresh, err := f.service.StartSetup(context.Background(), "local", f.offer.ID, FolderSetupInput{RequestID: "stale", PlanDigest: old})
	if !errors.Is(err, ErrFolderPlanChanged) || fresh.Plan == nil || fresh.Plan.Digest == old || !fresh.Plan.OptionalTask || f.setup.runCount() != 0 {
		t.Fatalf("old review was not refreshed before effects: %+v %v", fresh, err)
	}
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID, FolderSetupInput{RequestID: "current", PlanDigest: fresh.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ready setup", func() bool { return f.stored(t).Status == FolderOfferResolved })
	if look.callCount() != 0 {
		t.Fatal("refreshed setup executed the optional task")
	}
}

func TestFolderOptionalSetup_ResumesLegacyStepsWithoutNewExecutionConsent(t *testing.T) {
	f := folderHireFixture(t)
	look := &fakeFirstLook{result: FolderFirstLookStart{Started: true}}
	f.service.deps.FirstLook = look
	ctx := context.Background()
	_, err := f.store.Mutate(ctx, "local", func(d *FolderDigestDocument) error {
		offer := d.Offer(f.offer.ID)
		lines := append([]FolderPlanLine(nil), f.offer.Plan.Lines...)
		lines[0].State = FolderLineDone
		offer.Status, offer.Choice, offer.Decision = FolderOfferAwaitingOutcome, FolderChoiceProject, FolderDecisionYes
		now := f.service.now()
		offer.DecidedAt = &now
		offer.Outcome = &FolderOutcome{Kind: FolderChoiceProject}
		offer.Setup = &FolderSetupRun{PlanDigest: f.offer.Plan.Digest, Status: FolderSetupStopped, StopReason: FolderStopInterrupted, Lines: lines, Hire: f.offer.Plan.Hire, Brief: "Keep the reviewed goal"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Current plans differ, but a resume is held to its old saved intent and
	// completed steps. The saved hire is not permission to start another task.
	f.setup.intent.OptionalTaskPolicy = FolderOptionalSummaryPolicy
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{RequestID: "resume", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "legacy setup to finish", func() bool { return f.stored(t).Status == FolderOfferResolved })
	stored := f.stored(t)
	if stored.Setup.Brief != "Keep the reviewed goal" || stored.Setup.Lines[0].State != FolderLineDone || stored.Setup.Intent.OptionalTaskPolicy != "" || look.callCount() != 0 || stored.Outcome.FirstLook != nil {
		t.Fatalf("resume lost completed work or inferred execution: %+v", stored)
	}
}

func TestFolderOptionalSetup_LegacyReplayKeepsExistingTaskAndReportRecords(t *testing.T) {
	f := folderHireFixture(t)
	look := &fakeFirstLook{result: FolderFirstLookStart{Started: true}}
	f.service.deps.FirstLook = look
	ctx := context.Background()
	for _, reason := range []string{"running", "finished", "needs_input", "start_failed"} {
		_, err := f.store.Mutate(ctx, "local", func(d *FolderDigestDocument) error {
			offer := d.Offer(f.offer.ID)
			offer.Status = FolderOfferResolved
			offer.Outcome = &FolderOutcome{Kind: FolderChoiceProject, WorkspaceID: "existing", FirstLook: &FolderFirstLookRecord{Started: true, TaskID: "same-task", Reason: reason, At: f.service.now()}}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		before, _ := json.Marshal(f.stored(t))
		f.service.settleSetup(ctx, "local", f.offer.ID)
		f.service.startHiredLook(ctx, "local", f.offer.ID)
		after, _ := json.Marshal(f.stored(t))
		if string(before) != string(after) || look.callCount() != 0 {
			t.Fatalf("replay changed %s state", reason)
		}
	}
}
