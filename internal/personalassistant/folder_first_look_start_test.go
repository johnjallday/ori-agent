package personalassistant

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// fakeFirstLook stands in for the host's start of a hired agent's first look.
type fakeFirstLook struct {
	mu     sync.Mutex
	calls  []string
	result FolderFirstLookStart
	err    error
}

func (f *fakeFirstLook) StartFirstLook(_ context.Context, _ string, workspaceID string) (FolderFirstLookStart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, workspaceID)
	return f.result, f.err
}

func (f *fakeFirstLook) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func hireAndStartFixture(t *testing.T, look *fakeFirstLook) (*oneCardFixture, *fakeFirstTaskSeeder) {
	t.Helper()
	f := folderHireFixture(t)
	seeder := &fakeFirstTaskSeeder{}
	f.service.deps.FirstTask = seeder
	f.service.deps.FirstLook = look
	return f, seeder
}

// Setup seeds the optional task but never borrows execution consent from hire.
func TestFolderHire_SetupSeedsTheOptionalLookWithoutStartingIt(t *testing.T) {
	look := &fakeFirstLook{result: FolderFirstLookStart{Started: true, TaskID: "look-1"}}
	f, seeder := hireAndStartFixture(t, look)
	ctx := context.Background()
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID,
		FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest, Brief: "Tighten the mix."}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the workspace to be ready", func() bool { return f.stored(t).Status == FolderOfferResolved })
	outcome := f.stored(t).Outcome
	if look.callCount() != 0 || outcome.FirstLook != nil {
		t.Fatalf("setup must not start or claim a summary: calls=%v, record=%+v", look.calls, outcome.FirstLook)
	}
	if seeder.callCount() != 1 || seeder.calls[0].Brief != "Tighten the mix." {
		t.Fatalf("the look was seeded with %+v", seeder.calls)
	}
	// A replayed settle starts nothing more.
	f.service.startHiredLook(ctx, "local", f.offer.ID)
	if look.callCount() != 0 {
		t.Fatalf("a legacy replay started the optional look: %v", look.calls)
	}
}

// Optional execution readiness is not a failed or blocked setup.
func TestFolderHire_OptionalExecutorReadinessDoesNotStartOrUndoSetup(t *testing.T) {
	for name, tc := range map[string]struct {
		look   *fakeFirstLook
		reason string
	}{
		"no model": {&fakeFirstLook{result: FolderFirstLookStart{Reason: FolderFirstTaskReasonNoModel, TaskID: "look-1"}}, FolderFirstTaskReasonNoModel},
		"error":    {&fakeFirstLook{err: errors.New("executor down")}, FolderFirstTaskReasonStartFailed},
	} {
		f, _ := hireAndStartFixture(t, tc.look)
		if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID,
			FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the workspace to be ready", func() bool { return f.stored(t).Status == FolderOfferResolved })
		if got := f.stored(t).Outcome.FirstLook; got != nil || tc.look.callCount() != 0 {
			t.Errorf("%s: optional task must remain unstarted: record=%+v", name, got)
		}
		if f.stored(t).Status != FolderOfferResolved {
			t.Errorf("%s: a look that did not start must not undo the hire", name)
		}
	}
}

// A setup that hired no one leaves its look for the user to start.
func TestFolderSetup_ASetupWithoutAHireStartsNoLook(t *testing.T) {
	look := &fakeFirstLook{result: FolderFirstLookStart{Started: true}}
	f := newOneCardFixture(t)
	f.service.deps.FirstLook = look
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID,
		FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the offer to resolve", func() bool { return f.stored(t).Status == FolderOfferResolved })
	if look.callCount() != 0 || f.stored(t).Outcome.FirstLook != nil {
		t.Fatalf("a setup without a hire started the look: %v", look.calls)
	}
}

// The generic first look ends with one "Next:" line, and a hire's brief rides
// in its details as the user's quoted intent, before that closing line.
func TestFolderFirstTaskFor_AddsTheBriefAndEndsWithNext(t *testing.T) {
	for _, shape := range []folderdigest.Shape{folderdigest.ShapeCode, folderdigest.ShapeAudio, folderdigest.ShapeManuscript, folderdigest.ShapeCorpus, folderdigest.ShapeNotes} {
		_, plain := FolderFirstTask(shape)
		if !strings.HasSuffix(plain, `End your reply with one final line that begins with "Next:" followed by one small next task, as a single plain sentence.`) ||
			strings.Contains(plain, "What the user asked for") {
			t.Errorf("%s: details = %q", shape, plain)
		}
		description, details := FolderFirstTaskFor(shape, "  Find the\nloudest track. ")
		if base, _ := FolderFirstTask(shape); description != base {
			t.Errorf("%s: the brief changed the description", shape)
		}
		asked := strings.Index(details, "What the user asked for, in their own words")
		if asked < 0 || !strings.Contains(details, "“Find the loudest track.”") || asked > strings.Index(details, `"Next:"`) {
			t.Errorf("%s: details = %q", shape, details)
		}
	}
	_, long := FolderFirstTaskFor(folderdigest.ShapeCode, strings.Repeat("a", WorkspaceGoalMaxRunes+50))
	if strings.Contains(long, strings.Repeat("a", WorkspaceGoalMaxRunes+1)) {
		t.Fatal("the brief in the details is not bounded")
	}
}
