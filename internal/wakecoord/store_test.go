package wakecoord

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var now = time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)

func overnight(id string, at time.Time) Candidate {
	return Candidate{ID: id, Source: SourceOvernightRun, WakeAt: at, Detail: "Claude reset"}
}

func workspaceTask(id string, at time.Time) Candidate {
	return Candidate{ID: id, Source: SourceWorkspaceTask, WakeAt: at, Detail: "scheduled task"}
}

func TestCandidatesFromSeveralSourcesCoexist(t *testing.T) {
	store := New(t.TempDir())
	if err := store.Register(workspaceTask("task-1", now.Add(2*time.Hour)), now); err != nil {
		t.Fatalf("register task: %v", err)
	}
	if err := store.Register(overnight("run-1", now.Add(time.Hour)), now); err != nil {
		t.Fatalf("register run: %v", err)
	}

	candidates, err := store.Candidates(now)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want both sources", candidates)
	}
	earliest, ok, err := store.Earliest(now)
	if err != nil || !ok {
		t.Fatalf("Earliest: %v %v", ok, err)
	}
	if earliest.Source != SourceOvernightRun {
		t.Fatalf("earliest = %+v, want the sooner Overnight wake", earliest)
	}
}

// TestCancellingOneSourceLeavesTheOtherAlone is the property that stops an
// Overnight Run from silently breaking a scheduled workspace task.
func TestCancellingOneSourceLeavesTheOtherAlone(t *testing.T) {
	store := New(t.TempDir())
	// Deliberately identical identifiers across the two sources: scoping must
	// come from the source, not from the id happening to be unique.
	if err := store.Register(workspaceTask("shared-id", now.Add(2*time.Hour)), now); err != nil {
		t.Fatal(err)
	}
	if err := store.Register(overnight("shared-id", now.Add(time.Hour)), now); err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(SourceOvernightRun, "shared-id", now); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	candidates, err := store.Candidates(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Source != SourceWorkspaceTask {
		t.Fatalf("candidates = %+v, want only the workspace task left", candidates)
	}
	// The next wake recomputes to the surviving candidate.
	earliest, ok, err := store.Earliest(now)
	if err != nil || !ok || !earliest.WakeAt.Equal(now.Add(2*time.Hour).UTC()) {
		t.Fatalf("earliest = %+v, %v; want the later workspace wake preserved", earliest, ok)
	}
}

func TestRegisteringTheSameCandidateTwiceReplacesIt(t *testing.T) {
	store := New(t.TempDir())
	if err := store.Register(overnight("run-1", now.Add(time.Hour)), now); err != nil {
		t.Fatal(err)
	}
	if err := store.Register(overnight("run-1", now.Add(3*time.Hour)), now); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.Candidates(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || !candidates[0].WakeAt.Equal(now.Add(3*time.Hour).UTC()) {
		t.Fatalf("candidates = %+v, want one updated candidate", candidates)
	}
}

func TestPastAndExpiredCandidatesAreNeverOffered(t *testing.T) {
	store := New(t.TempDir())
	past := overnight("past", now.Add(-time.Hour))
	expired := overnight("expired", now.Add(time.Hour))
	expired.ExpiresAt = now.Add(-time.Minute)
	if err := store.Register(past, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.Register(expired, now); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.Candidates(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want none usable", candidates)
	}
}

// TestOnlyTheOwnerRecordsWhatWasProgrammed is the verification contract: a
// process that wants a wake cannot manufacture the evidence that it exists.
func TestOnlyTheOwnerRecordsWhatWasProgrammed(t *testing.T) {
	store := New(t.TempDir())
	if err := store.Register(overnight("run-1", now.Add(time.Hour)), now); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Programmed(); err != nil || found {
		t.Fatalf("programmed = %v, %v; want nothing until the owner records it", found, err)
	}

	if err := store.RecordProgrammed(Programmed{
		CandidateID: "run-1", Source: SourceOvernightRun, WakeAt: now.Add(58 * time.Minute),
	}, now); err != nil {
		t.Fatalf("RecordProgrammed: %v", err)
	}
	programmed, found, err := store.Programmed()
	if err != nil || !found {
		t.Fatalf("programmed = %v, %v", found, err)
	}
	if programmed.CandidateID != "run-1" || programmed.ProgrammedAt.IsZero() {
		t.Fatalf("programmed = %+v", programmed)
	}
}

func TestRejectsIdentitiesItWillNotStore(t *testing.T) {
	store := New(t.TempDir())
	for _, candidate := range []Candidate{
		{ID: "", Source: SourceOvernightRun, WakeAt: now.Add(time.Hour)},
		{ID: "../escape", Source: SourceOvernightRun, WakeAt: now.Add(time.Hour)},
		{ID: "run-1", Source: "", WakeAt: now.Add(time.Hour)},
		{ID: "run-1", Source: SourceOvernightRun},
	} {
		if err := store.Register(candidate, now); err == nil {
			t.Fatalf("candidate %+v was accepted", candidate)
		}
	}
}

func TestDetailIsBoundedAndStripped(t *testing.T) {
	store := New(t.TempDir())
	candidate := overnight("run-1", now.Add(time.Hour))
	candidate.Detail = "reset\x1b[31m at 03:00\n" + string(make([]rune, MaxDetailRunes+50))
	if err := store.Register(candidate, now); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.Candidates(now)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(candidates[0].Detail)) > MaxDetailRunes {
		t.Fatalf("detail was not bounded: %d runes", len([]rune(candidates[0].Detail)))
	}
	for _, r := range candidates[0].Detail {
		if r < 32 || r == 127 {
			t.Fatalf("a control character survived: %q", candidates[0].Detail)
		}
	}
}

func TestTheSharedFileStaysPrivate(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	if err := store.Register(overnight("run-1", now.Add(time.Hour)), now); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// TestACorruptDocumentDoesNotStopTheOwner keeps a damaged shared file from
// becoming a permanent refusal to program any wake at all.
func TestACorruptDocumentDoesNotStopTheOwner(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := New(dir)
	candidates, err := store.Candidates(now)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("candidates = %+v, %v; want an empty store", candidates, err)
	}
	if err := store.Register(overnight("run-1", now.Add(time.Hour)), now); err != nil {
		t.Fatalf("registering over a corrupt document failed: %v", err)
	}
}

func TestADocumentFromANewerOriIsRefusedRatherThanMisread(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName),
		[]byte(`{"version":99,"candidates":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir).Candidates(now); err == nil {
		t.Fatal("a document from a newer Ori was read anyway")
	}
}

func TestAnEmptyStoreIsNotAnError(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "not-created-yet"))
	candidates, err := store.Candidates(now)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("candidates = %+v, %v; want an empty store", candidates, err)
	}
	if _, found, err := store.Programmed(); err != nil || found {
		t.Fatalf("programmed = %v, %v", found, err)
	}
}
