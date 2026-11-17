package folderagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// releases records the workspaces a store told "the welcome is free to post".
type releases struct {
	mu  sync.Mutex
	ids []string
}

func (r *releases) add(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, id)
}

func (r *releases) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ids)
}

// While the Commander is asking, looking, reporting a failure or setting up, the
// lead's welcome waits, so a second, unrelated question does not arrive beside
// the Commander's own. Set up and kept release it. A state this code does not
// know is not a reason to speak, and a setup that stopped at a step still waits
// on the owner (Retry).
func TestWelcomeHoldOf_States(t *testing.T) {
	cases := []struct {
		name string
		rec  *Record
		want WelcomeHold
	}{
		{"no record", nil, WelcomeNotFed},
		{"looking", &Record{State: StateLooking}, WelcomeHeld},
		{"reported (the question is asked)", &Record{State: StateReported}, WelcomeHeld},
		{"failed", &Record{State: StateFailed}, WelcomeHeld},
		{"setting up", &Record{State: StateSettingUp}, WelcomeHeld},
		{"a state this code does not know", &Record{State: State("mystery")}, WelcomeHeld},
		{"set up, setup done", &Record{State: StateSetUp, Setup: &Setup{Status: personalassistant.FolderSetupDone}}, WelcomeAfterSetUp},
		{"set up with no setup receipt", &Record{State: StateSetUp}, WelcomeAfterSetUp},
		{"set up, but the setup stopped at a step", &Record{State: StateSetUp, Setup: &Setup{Status: personalassistant.FolderSetupStopped}}, WelcomeHeld},
		{"kept as it is", &Record{State: StateKept}, WelcomeAfterKept},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WelcomeHoldOf(tc.rec); got != tc.want {
				t.Fatalf("hold = %d, want %d", got, tc.want)
			}
		})
	}
}

// The welcome worker and the browser can reach the workspace the moment it
// exists, and the folder record is saved a little later. In that window the
// workspace must already read as fed (the race measured on the live demo: the
// welcome was posted 45 ms before the record). The probes stand inside the
// window, at the two places the host does work between the workspace and the
// record, with no sleeps.
func TestStart_HoldsTheWelcomeBeforeTheRecordExists(t *testing.T) {
	h := newHarness(t, demo(t))
	var seen []WelcomeHold
	probe := func(where string) func(string) {
		return func(id string) {
			if _, err := h.svc.deps.Store.Read(id); !errors.Is(err, ErrNoRecord) {
				t.Errorf("%s: the record must not exist yet, read = %v", where, err)
			}
			seen = append(seen, h.svc.FolderWelcome(id))
		}
	}
	h.ws.afterCreate = probe("after the workspace is created")
	h.tickets.onOpen = probe("after the first ticket opens")

	view := h.start(t, "")

	if len(seen) != 2 {
		t.Fatalf("probes ran %d times, want 2", len(seen))
	}
	for i, hold := range seen {
		if hold != WelcomeHeld {
			t.Errorf("probe %d: hold = %d, want held (a welcome posted here is a second question)", i, hold)
		}
	}
	// Once the record exists it holds on its own.
	if got := h.svc.FolderWelcome(view.WorkspaceID); got != WelcomeHeld {
		t.Fatalf("after Start: hold = %d, want held (the report waits on the owner)", got)
	}
}

// A Start in progress holds the welcome of a workspace that has no record, any
// workspace: the host cannot tell which new workspace is the fed one until the
// record exists, and the window is a fraction of a second. Nothing else is
// held once Start returns.
func TestStart_TheHoldIsProcessWideForTheWindowAndGoneAfterIt(t *testing.T) {
	h := newHarness(t, demo(t))
	var during WelcomeHold = -1
	h.ws.afterCreate = func(string) { during = h.svc.FolderWelcome("some-other-new-workspace") }
	h.start(t, "")
	if during != WelcomeHeld {
		t.Fatalf("another new workspace during a Start: hold = %d, want held", during)
	}
	if got := h.svc.FolderWelcome("some-other-new-workspace"); got != WelcomeNotFed {
		t.Fatalf("after Start: hold = %d, want not fed (a workspace that was not fed is unchanged)", got)
	}
}

// If the record cannot be written there is no Commander question to protect, so
// the hold is released: the welcome posts under today's rules, and whoever waits
// is told to look again.
func TestStart_WhenTheRecordCannotBeWrittenTheWelcomeIsReleased(t *testing.T) {
	h := newHarness(t, demo(t))
	var told releases
	h.svc.deps.Store = NewStore(func(string) (string, error) { return "", errors.New("the disk is gone") })
	h.svc.deps.Store.OnWelcomeRelease(told.add)
	var during WelcomeHold = -1
	h.ws.afterCreate = func(id string) { during = h.svc.FolderWelcome(id) }

	chosen, err := h.svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true})
	if err != nil || chosen == nil {
		t.Fatalf("choose: %v %v", chosen, err)
	}
	if _, err := h.svc.Start(context.Background(), "u1", chosen.Token, "", Hire{}); err == nil {
		t.Fatal("Start must fail when the record cannot be written")
	}
	if during != WelcomeHeld {
		t.Fatalf("inside Start: hold = %d, want held", during)
	}
	if got := h.svc.FolderWelcome("ws-1"); got != WelcomeNotFed {
		t.Fatalf("after the failed Start: hold = %d, want not fed (released)", got)
	}
	if told.count() != 1 {
		t.Fatalf("whoever waits was told %d times, want 1", told.count())
	}
}

// A workspace that has no record and no Start in progress was not made by
// feeding a folder; a record that cannot be read is not proof that it was not.
func TestFolderWelcome_NoRecordIsNotFedAndAnUnreadableRecordHolds(t *testing.T) {
	h := newHarness(t, demo(t))
	if got := h.svc.FolderWelcome("ws-never-fed"); got != WelcomeNotFed {
		t.Fatalf("no record: hold = %d, want not fed", got)
	}
	start := h.start(t, "")
	path := filepath.Join(h.ws.sidecars, start.WorkspaceID, sidecarDirName, recordFile)
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := h.svc.FolderWelcome(start.WorkspaceID); got != WelcomeHeld {
		t.Fatalf("unreadable record: hold = %d, want held", got)
	}
}

// The store tells its listener only when a write frees the welcome (held to not
// held), so a progress write four times a second during a walk costs nothing,
// and a setup that stopped, or an interrupted one, frees nothing.
func TestStore_ReleaseHookFiresOnlyWhenHeldBecomesReleased(t *testing.T) {
	h := newHarness(t, demo(t))
	var told releases
	h.svc.deps.Store.OnWelcomeRelease(told.add)
	start := h.start(t, "")
	id := start.WorkspaceID
	if told.count() != 0 {
		t.Fatalf("Start released the hold %d times", told.count())
	}
	set := func(fn func(*Record)) {
		t.Helper()
		if _, err := h.svc.deps.Store.Update(id, func(r *Record) error { fn(r); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	step := func(name string, change func(*Record), want int) {
		t.Helper()
		before := told.count()
		set(change)
		if got := told.count() - before; got != want {
			t.Fatalf("%s: the hook fired %d times, want %d", name, got, want)
		}
	}
	step("a progress write while held", func(r *Record) { r.Progress.Entries++ }, 0)
	step("looking again", func(r *Record) { r.State = StateLooking }, 0)
	step("reported", func(r *Record) { r.State = StateReported }, 0)
	step("setting up", func(r *Record) { r.State = StateSettingUp }, 0)
	step("a setup that stopped at a step", func(r *Record) {
		r.State, r.Setup = StateSetUp, &Setup{Status: personalassistant.FolderSetupStopped}
	}, 0)
	step("Retry carries on", func(r *Record) { r.State = StateSettingUp }, 0)
	step("the setup finished", func(r *Record) {
		r.State, r.Setup = StateSetUp, &Setup{Status: personalassistant.FolderSetupDone}
	}, 1)
	step("a write while already released", func(r *Record) { r.Progress.Entries++ }, 0)
	step("looked at again after set up (held again)", func(r *Record) { r.State = StateLooking }, 0)
	step("reported again", func(r *Record) { r.State = StateReported }, 0)
	step("kept as it is", func(r *Record) { r.State = StateKept }, 1)
	if got := h.svc.FolderWelcome(id); got != WelcomeAfterKept {
		t.Fatalf("hold after keeping = %d", got)
	}
}

// The real transitions that free the welcome: Keep, and a Set up that finishes.
func TestService_KeepAndSetUpReleaseTheWelcome(t *testing.T) {
	kept := newHarness(t, demo(t))
	var told releases
	kept.svc.deps.Store.OnWelcomeRelease(told.add)
	start := kept.start(t, "")
	if _, err := kept.svc.Keep(context.Background(), "u1", start.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if told.count() != 1 || kept.svc.FolderWelcome(start.WorkspaceID) != WelcomeAfterKept {
		t.Fatalf("Keep: told %d times, hold = %d", told.count(), kept.svc.FolderWelcome(start.WorkspaceID))
	}

	done := newHarness(t, demo(t))
	var toldDone releases
	done.svc.deps.Store.OnWelcomeRelease(toldDone.add)
	s := done.start(t, "")
	if _, err := done.svc.AnswerPremise(context.Background(), "u1", s.WorkspaceID, PremiseConfirmed); err != nil {
		t.Fatal(err)
	}
	report := done.view(t, s.WorkspaceID)
	if _, err := done.svc.SetUp(context.Background(), "u1", s.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); err != nil {
		t.Fatal(err)
	}
	done.svc.Wait()
	if toldDone.count() != 1 || done.svc.FolderWelcome(s.WorkspaceID) != WelcomeAfterSetUp {
		t.Fatalf("Set up: told %d times, hold = %d", toldDone.count(), done.svc.FolderWelcome(s.WorkspaceID))
	}
}

// A setup a restart interrupted is a stopped setup, which waits on the owner's
// Retry: sweeping it frees nothing.
func TestService_SweepingAnInterruptedSetUpKeepsTheWelcomeHeld(t *testing.T) {
	h := newHarness(t, demo(t))
	var told releases
	h.svc.deps.Store.OnWelcomeRelease(told.add)
	start := h.start(t, "")
	if _, err := h.svc.deps.Store.Update(start.WorkspaceID, func(r *Record) error {
		r.State, r.Setup = StateSettingUp, &Setup{Status: personalassistant.FolderSetupRunning}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if closed := h.svc.SweepInterrupted(); closed != 1 {
		t.Fatalf("swept %d, want 1", closed)
	}
	if told.count() != 0 || h.svc.FolderWelcome(start.WorkspaceID) != WelcomeHeld {
		t.Fatalf("told %d times, hold = %d, want 0 and held", told.count(), h.svc.FolderWelcome(start.WorkspaceID))
	}
}
