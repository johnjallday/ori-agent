package projectstaffing

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// library-setup-staffing: the rules that keep one Home to one shared assistant
// when a staffing sequence is interrupted or raced.

func grantHome(t *testing.T, service *Service) {
	t.Helper()
	if _, err := service.Consents().Grant("home", workspace.ProjectStaffingConsentGrant{
		Source: workspace.ProjectStaffingConsentFolderOffer, PluginID: "ori-reaper", BlueprintID: "reaper-song",
		TeamDigest: digest, RoleIDs: []string{"reaper-assistant"},
	}); err != nil {
		t.Fatal(err)
	}
}

func roleOf(t *testing.T, service *Service) workspace.ProjectStaffingConsentRole {
	t.Helper()
	consent, err := service.Consents().Read("home")
	if err != nil || consent == nil {
		t.Fatalf("consent = %v %v", consent, err)
	}
	return consent.Roles[0]
}

// A reservation is timed; a created binding older than it (an earlier per-song
// agent that carries the role's label) is never recorded as the shared one.
func TestSettleNeverRecordsABindingOlderThanTheReservation(t *testing.T) {
	store, service, agents := fixture(t)
	grantHome(t, service)
	// song-a already has its own created agent named like the role.
	if err := store.Update("song-a", func(song *workspace.Workspace) error {
		song.AgentInstances = append(song.AgentInstances, workspace.AgentInstance{ID: "i-song-a", Name: "REAPER Assistant",
			RoleSource: workspace.RoleSourceCreated, CreatedAt: time.Now().Add(-time.Hour)})
		link := song.GetAssistantProjectLink()
		link.ProjectBindings.Bindings = append(link.ProjectBindings.Bindings, workspace.AssistantRoleBinding{RoleID: "reaper-assistant", AgentInstanceID: "i-song-a", AgentName: "REAPER Assistant"})
		song.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Its roster entry is gone, so a create may pick the label... but the
	// Home's own projects still hold the name, so it takes the next one.
	fills, err := service.Fill("song-b", []string{"reaper-assistant"}, nil)
	if err != nil || len(fills) != 1 || fills[0].Name != "REAPER Assistant 2" {
		t.Fatalf("fill = %+v %v", fills, err)
	}
	// Even a reservation of the old name is never proven by the old binding.
	if err := service.Consents().ReleaseReservation("home", "reaper-assistant", "REAPER Assistant 2"); err != nil {
		t.Fatal(err)
	}
	if err := service.Consents().ReserveAgent("home", "ori-reaper", "reaper-song", digest, "reaper-assistant", "REAPER Assistant"); err != nil {
		t.Fatal(err)
	}
	if err := service.Recover("home"); err != nil {
		t.Fatal(err)
	}
	if role := roleOf(t, service); role.AgentName != "" {
		t.Fatalf("an older per-song agent was adopted: %+v", role)
	}
	_ = agents
}

// Recover records a create that was bound in some project of the Home under
// the reservation, and nothing else.
func TestRecoverRecordsTheBoundCreateOfAnyProjectOfTheHome(t *testing.T) {
	store, service, agents := fixture(t)
	grantHome(t, service)
	fills, err := service.Fill("song-a", []string{"reaper-assistant"}, nil)
	if err != nil || len(fills) != 1 {
		t.Fatal(fills, err)
	}
	agents[fills[0].Name] = true
	bindInSong(t, store, "song-a", fills[0].Name, workspace.RoleSourceCreated)
	if role := roleOf(t, service); role.AgentName != "" || role.PendingName != "REAPER Assistant" || role.ReservedAt == nil {
		t.Fatalf("before recovery = %+v", role)
	}
	if err := service.Recover("home"); err != nil {
		t.Fatal(err)
	}
	if role := roleOf(t, service); role.AgentName != "REAPER Assistant" || role.PendingName != "" || role.ReservedAt != nil {
		t.Fatalf("after recovery = %+v", role)
	}
	fills, err = service.Fill("song-b", []string{"reaper-assistant"}, nil)
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeBind || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("next song = %+v %v", fills, err)
	}
}

// After a restart (a new service over the same stores, no lock or memory of
// the interrupted turn) the stored reservation and binding are enough to
// recover: nothing lived only in memory.
func TestRecoverAfterARestartNeedsNoMemoryOfTheInterruptedTurn(t *testing.T) {
	store, before, agents := fixture(t)
	grantHome(t, before)
	fills, err := before.Fill("song-a", []string{"reaper-assistant"}, nil)
	if err != nil || len(fills) != 1 {
		t.Fatal(fills, err)
	}
	agents[fills[0].Name] = true
	bindInSong(t, store, "song-a", fills[0].Name, workspace.RoleSourceCreated)
	after := New(store, agents) // the restarted process
	fills, err = after.Fill("song-b", []string{"reaper-assistant"}, nil)
	if !errors.Is(err, ErrReservationHeld) {
		// Without recovery the held name stops the decision: never a "… 2".
		t.Fatalf("before recovery = %+v %v", fills, err)
	}
	if err := after.Recover("home"); err != nil {
		t.Fatal(err)
	}
	fills, err = after.Fill("song-b", []string{"reaper-assistant"}, nil)
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeBind || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("after recovery = %+v %v", fills, err)
	}
}

// A reserved name that an agent holds without proof is never stepped around:
// no second create is decided until the owner resolves it.
func TestFillRefusesToStepAroundAHeldReservation(t *testing.T) {
	_, service, agents := fixture(t)
	grantHome(t, service)
	if _, err := service.Fill("song-a", []string{"reaper-assistant"}, nil); err != nil {
		t.Fatal(err)
	}
	agents["REAPER Assistant"] = true // created, never bound
	if _, err := service.Fill("song-b", []string{"reaper-assistant"}, nil); !errors.Is(err, ErrReservationHeld) {
		t.Fatalf("fill = %v, want ErrReservationHeld", err)
	}
	if _, err := service.Plan("song-b", []string{"reaper-assistant"}); !errors.Is(err, ErrReservationHeld) {
		t.Fatalf("plan = %v", err)
	}
	if holder := service.PendingHolder("home", []string{"reaper-assistant"}); holder != "REAPER Assistant" {
		t.Fatalf("holder = %q", holder)
	}
	if role := roleOf(t, service); role.PendingName != "REAPER Assistant" {
		t.Fatalf("the reservation moved: %+v", role)
	}
}

// Plan decides exactly as Fill does and writes nothing.
func TestPlanReservesNothing(t *testing.T) {
	_, service, _ := fixture(t)
	grantHome(t, service)
	fills, err := service.Plan("song-a", []string{"reaper-assistant"})
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeCreate || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("plan = %+v %v", fills, err)
	}
	if role := roleOf(t, service); role.PendingName != "" || role.ReservedAt != nil {
		t.Fatalf("a plan reserved: %+v", role)
	}
	if name := service.FreeName("home", "REAPER Assistant"); name != "REAPER Assistant" {
		t.Fatalf("free name = %q", name)
	}
}

// One Home's turns never overlap: a second WithHome waits for the first.
func TestWithHomeSerializesOneHome(t *testing.T) {
	_, service, _ := fixture(t)
	var mu sync.Mutex
	inside, overlapped := 0, false
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = service.WithHome("home", func(*Tx) error {
				mu.Lock()
				inside++
				if inside > 1 {
					overlapped = true
				}
				mu.Unlock()
				time.Sleep(time.Millisecond)
				mu.Lock()
				inside--
				mu.Unlock()
				return nil
			})
		}()
	}
	wg.Wait()
	if overlapped {
		t.Fatal("two turns of one Home overlapped")
	}
}
