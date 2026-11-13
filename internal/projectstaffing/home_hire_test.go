package projectstaffing

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// assistant-reads-projects: the Home hires its shared assistant when the
// folder is set up, before any song exists.

var songTeam = Team{
	PluginID: "ori-reaper", BlueprintID: "reaper-song", Digest: digest,
	Roles: []workspace.AssistantProgramRoleSpec{{
		ID: "reaper-assistant", Label: "REAPER Assistant", Scope: workspace.AssistantRoleScopeProject, Required: true,
	}},
}

var folderGrant = Grant{Source: workspace.ProjectStaffingConsentFolderOffer, OfferID: "folder-agent-ws-1"}

// seedHomeOnly saves a Home with no song at all.
func seedHomeOnly(t *testing.T, store workspace.Store) {
	t.Helper()
	home := &workspace.Workspace{ID: "home", Name: "Home", Status: workspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	home.SetAssistantProgramState(&workspace.AssistantProgramState{SchemaVersion: workspace.AssistantProgramStateSchemaVersion})
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
}

// creator writes the agent into the roster with the marker it was given, the
// way the staffing adapter's create does, and counts its calls.
func creator(roster definitions, calls *int) SharedCreate {
	return func(_ context.Context, _ workspace.AssistantProgramRoleSpec, name string, marker *agent.AssistantSetupProvenance) (bool, error) {
		*calls++
		if _, taken := roster[name]; taken {
			return false, errors.New("an agent already has that name")
		}
		stamped := *marker
		roster[name] = &agent.Agent{AssistantSetup: &stamped}
		return true, nil
	}
}

// stoppingCreator writes the agent and then fails, as a process that stopped
// (or a grant that failed) after the profile was written would leave it.
func stoppingCreator(roster definitions, calls *int) SharedCreate {
	create := creator(roster, calls)
	return func(ctx context.Context, role workspace.AssistantProgramRoleSpec, name string, marker *agent.AssistantSetupProvenance) (bool, error) {
		if created, err := create(ctx, role, name, marker); err != nil {
			return created, err
		}
		return true, errors.New("stopped after the profile was written")
	}
}

func hire(t *testing.T, service *Service, modelReady bool, create SharedCreate) ([]Fill, error) {
	t.Helper()
	var fills []Fill
	err := service.WithHome("home", func(tx *Tx) error {
		var hireErr error
		fills, hireErr = tx.HireShared(context.Background(), songTeam, folderGrant, modelReady, create)
		return hireErr
	})
	return fills, err
}

func hiredFixture(t *testing.T) (workspace.Store, *Service, definitions) {
	t.Helper()
	store := workspace.NewInMemoryStore()
	seedHomeOnly(t, store)
	roster := definitions{}
	return store, New(store, roster), roster
}

func TestHireShared_TwoRunsInARowMakeOneAssistant(t *testing.T) {
	_, service, roster := hiredFixture(t)
	calls := 0
	fills, err := hire(t, service, true, creator(roster, &calls))
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeCreate || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("first hire = %+v %v", fills, err)
	}
	role := roleOf(t, service)
	if role.AgentName != "REAPER Assistant" || role.PendingName != "" || role.ReservationID != "" || role.ReservedAt != nil {
		t.Fatalf("recorded role = %+v", role)
	}
	consent, _ := service.Consents().Read("home")
	if consent.Source != workspace.ProjectStaffingConsentFolderOffer || consent.OfferID != "folder-agent-ws-1" || !consent.Active() {
		t.Fatalf("the hire did not record the Set up as the approval: %+v", consent)
	}
	// The marker on the agent is the proof a later sweep would use.
	marker := roster["REAPER Assistant"].AssistantSetup
	if marker == nil || marker.RunID != "home" || marker.OperationID != "shared-project-role:reaper-assistant" ||
		marker.ReviewDigest != digest || len(marker.ID) != 32 {
		t.Fatalf("marker = %+v", marker)
	}

	// Set up again, and a second folder into the same Home: nothing is made.
	for i := 0; i < 2; i++ {
		fills, err = hire(t, service, true, creator(roster, &calls))
		if err != nil || len(fills) != 1 || fills[0].Mode != ModeBind || fills[0].Name != "REAPER Assistant" {
			t.Fatalf("repeat hire %d = %+v %v", i, fills, err)
		}
	}
	if calls != 1 || len(roster) != 1 {
		t.Fatalf("creates = %d, agents = %v", calls, roster.ListAgents())
	}
}

// With no model nothing is written: not the approval, not a reservation.
func TestHireShared_WithoutAModelWritesNothing(t *testing.T) {
	_, service, roster := hiredFixture(t)
	calls := 0
	if _, err := hire(t, service, false, creator(roster, &calls)); !errors.Is(err, ErrNoModel) {
		t.Fatalf("no model: %v", err)
	}
	if consent, _ := service.Consents().Read("home"); consent != nil || calls != 0 {
		t.Fatalf("a hire with no model wrote something: %+v, creates %d", consent, calls)
	}
	// After a model is chosen, the same press hires.
	if fills, err := hire(t, service, true, creator(roster, &calls)); err != nil || len(fills) != 1 || fills[0].Mode != ModeCreate {
		t.Fatalf("after a model: %+v %v", fills, err)
	}
}

// The reopened real FileStore: a second process binds what the first hired,
// in the Home and then in a song.
func TestHireShared_ReopenedFileStoreBinds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workspaces")
	open := func() workspace.Store {
		t.Helper()
		store, err := workspace.NewFileStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	first := open()
	seedHomeOnly(t, first)
	roster := definitions{}
	calls := 0
	if fills, err := hire(t, New(first, roster), true, creator(roster, &calls)); err != nil || len(fills) != 1 || fills[0].Mode != ModeCreate {
		t.Fatalf("hire = %+v %v", fills, err)
	}

	second := open()
	after := New(second, roster)
	fills, err := hire(t, after, true, creator(roster, &calls))
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeBind || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("hire after a restart = %+v %v", fills, err)
	}
	// The first song opened afterwards binds that same agent.
	addSong(t, second, "song-a")
	songFills, err := after.Fill("song-a", []string{"reaper-assistant"}, nil)
	if err != nil || len(songFills) != 1 || songFills[0].Mode != ModeBind || songFills[0].Name != "REAPER Assistant" {
		t.Fatalf("first song = %+v %v", songFills, err)
	}
	if calls != 1 || len(roster) != 1 {
		t.Fatalf("creates = %d, agents = %v", calls, roster.ListAgents())
	}
}

// Ori stops between creating the agent and recording it. The next start ends
// with exactly one assistant, recorded on the Home (FR10).
func TestHireShared_StopBetweenCreateAndRecord_SweepRecordsOne(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workspaces")
	open := func() workspace.Store {
		t.Helper()
		store, err := workspace.NewFileStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	first := open()
	seedHomeOnly(t, first)
	roster := definitions{}
	calls := 0
	before := New(first, roster)
	if _, err := hire(t, before, true, stoppingCreator(roster, &calls)); err == nil {
		t.Fatal("the stopped create reported success")
	}
	pending := roleOf(t, before)
	if pending.AgentName != "" || pending.PendingName != "REAPER Assistant" || pending.ReservationID == "" {
		t.Fatalf("a stopped hire lost its reservation: %+v", pending)
	}

	// "Restart": new stores, a new Service, and the startup sweep.
	after := New(open(), roster)
	if err := after.RecoverShared(); err != nil {
		t.Fatal(err)
	}
	recorded := roleOf(t, after)
	if recorded.AgentName != "REAPER Assistant" || recorded.PendingName != "" || recorded.ReservationID != "" {
		t.Fatalf("after the sweep: %+v", recorded)
	}
	fills, err := hire(t, after, true, creator(roster, &calls))
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeBind || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("Set up again = %+v %v", fills, err)
	}
	if calls != 1 || len(roster) != 1 {
		t.Fatalf("creates = %d, agents = %v (a second assistant was made)", calls, roster.ListAgents())
	}
}

// Without the startup sweep, the next Set up finishes the stopped hire itself.
func TestHireShared_NextSetUpFinishesAStoppedHire(t *testing.T) {
	_, service, roster := hiredFixture(t)
	calls := 0
	if _, err := hire(t, service, true, stoppingCreator(roster, &calls)); err == nil {
		t.Fatal("the stopped create reported success")
	}
	fills, err := hire(t, service, true, creator(roster, &calls))
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeBind || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("Set up again = %+v %v", fills, err)
	}
	if calls != 1 || len(roster) != 1 {
		t.Fatalf("creates = %d, agents = %v", calls, roster.ListAgents())
	}
}

// An agent that holds the reserved name without this reservation's marker is
// somebody else's: never recorded, never stepped around.
func TestHireShared_StrangerHoldingNameNotAdopted(t *testing.T) {
	const operation = "shared-project-role:reaper-assistant"
	marked := func(id, home, op string) *agent.Agent {
		return &agent.Agent{AssistantSetup: &agent.AssistantSetupProvenance{ID: id, RunID: home, OperationID: op}}
	}
	// Each stranger is built from the reservation's own ID, so a case can get
	// everything right but one part of the proof.
	for name, stranger := range map[string]func(reservationID string) *agent.Agent{
		"no marker":           func(string) *agent.Agent { return &agent.Agent{} },
		"an empty marker":     func(string) *agent.Agent { return marked("", "", "") },
		"another reservation": func(string) *agent.Agent { return marked("0123456789abcdef0123456789abcdef", "home", operation) },
		"another Home's hire": func(id string) *agent.Agent { return marked(id, "other-home", operation) },
		"another role's hire": func(id string) *agent.Agent { return marked(id, "home", "shared-project-role:mixer") },
		"a setup run's agent": func(string) *agent.Agent { return marked("file-janitor", "run-1", "op-1") },
	} {
		t.Run(name, func(t *testing.T) {
			_, service, roster := hiredFixture(t)
			grantHome(t, service)
			// A process stopped after reserving the name and before creating.
			if err := service.Consents().ReserveAgent("home", "ori-reaper", "reaper-song", digest, "reaper-assistant", "REAPER Assistant"); err != nil {
				t.Fatal(err)
			}
			roster["REAPER Assistant"] = stranger(roleOf(t, service).ReservationID)

			calls := 0
			if _, err := hire(t, service, true, creator(roster, &calls)); !errors.Is(err, ErrReservationHeld) {
				t.Fatalf("hire over a held name: %v", err)
			}
			if err := service.RecoverShared(); err != nil {
				t.Fatal(err)
			}
			role := roleOf(t, service)
			if role.AgentName != "" || role.PendingName != "REAPER Assistant" || calls != 0 || len(roster) != 1 {
				t.Fatalf("a stranger was adopted or stepped around: %+v, creates %d, agents %v", role, calls, roster.ListAgents())
			}
		})
	}
}

// A name somebody else took between the reservation and the create: the
// reservation is given up and nothing is recorded.
func TestHireShared_NameTakenAtCreateReleasesTheReservation(t *testing.T) {
	_, service, roster := hiredFixture(t)
	calls := 0
	racing := func(ctx context.Context, role workspace.AssistantProgramRoleSpec, name string, marker *agent.AssistantSetupProvenance) (bool, error) {
		roster[name] = &agent.Agent{} // someone else's create lands first
		return creator(roster, &calls)(ctx, role, name, marker)
	}
	if _, err := hire(t, service, true, racing); err == nil {
		t.Fatal("a create that lost the name reported success")
	}
	role := roleOf(t, service)
	if role.AgentName != "" || role.PendingName != "" || role.ReservationID != "" {
		t.Fatalf("the lost reservation was kept or recorded: %+v", role)
	}
	// The next hire takes the next free name and never the stranger's.
	fills, err := hire(t, service, true, creator(roster, &calls))
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeCreate || fills[0].Name != "REAPER Assistant 2" {
		t.Fatalf("next hire = %+v %v", fills, err)
	}
	if roster["REAPER Assistant"].AssistantSetup != nil || roleOf(t, service).AgentName != "REAPER Assistant 2" {
		t.Fatalf("the stranger was touched: %+v", roleOf(t, service))
	}
}

// A create that fails and leaves nothing gives its reservation up, so the next
// press starts clean under the same name.
func TestHireShared_FailedCreateLeavesNothingBehind(t *testing.T) {
	_, service, roster := hiredFixture(t)
	failing := func(context.Context, workspace.AssistantProgramRoleSpec, string, *agent.AssistantSetupProvenance) (bool, error) {
		return false, errors.New("the profile could not be written")
	}
	if _, err := hire(t, service, true, failing); err == nil {
		t.Fatal("a failed create reported success")
	}
	if role := roleOf(t, service); role.AgentName != "" || role.PendingName != "" || role.ReservationID != "" {
		t.Fatalf("a failed create left a reservation: %+v", role)
	}
	calls := 0
	fills, err := hire(t, service, true, creator(roster, &calls))
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeCreate || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("retry = %+v %v", fills, err)
	}
}

// Set up never turns the Home's switch back on, and never renews an approval
// given for an older team (FR4).
func TestHireShared_RevokedOrStaleHiresNobody(t *testing.T) {
	t.Run("switched off", func(t *testing.T) {
		_, service, roster := hiredFixture(t)
		grantHome(t, service)
		if _, err := service.Consents().Revoke("home"); err != nil {
			t.Fatal(err)
		}
		calls := 0
		if _, err := hire(t, service, true, creator(roster, &calls)); !errors.Is(err, ErrNoConsent) {
			t.Fatalf("hire on a Home switched off: %v", err)
		}
		consent, _ := service.Consents().Read("home")
		if consent.RevokedAt == nil || calls != 0 || len(roster) != 0 {
			t.Fatalf("Set up switched it back on or hired: %+v, creates %d", consent, calls)
		}
	})
	t.Run("older team", func(t *testing.T) {
		_, service, roster := hiredFixture(t)
		older := "b" + digest[1:]
		if _, err := service.Consents().Grant("home", workspace.ProjectStaffingConsentGrant{
			Source: workspace.ProjectStaffingConsentHomeSwitch, PluginID: "ori-reaper", BlueprintID: "reaper-song",
			TeamDigest: older, RoleIDs: []string{"reaper-assistant"},
		}); err != nil {
			t.Fatal(err)
		}
		calls := 0
		if _, err := hire(t, service, true, creator(roster, &calls)); !errors.Is(err, ErrConsentStale) {
			t.Fatalf("hire under an older team's approval: %v", err)
		}
		consent, _ := service.Consents().Read("home")
		if consent.TeamDigest != older || calls != 0 || len(roster) != 0 {
			t.Fatalf("Set up renewed the approval or hired: %+v, creates %d", consent, calls)
		}
	})
	t.Run("another blueprint", func(t *testing.T) {
		_, service, roster := hiredFixture(t)
		if _, err := service.Consents().Grant("home", workspace.ProjectStaffingConsentGrant{
			Source: workspace.ProjectStaffingConsentHomeSwitch, PluginID: "ori-other", BlueprintID: "other-song",
			TeamDigest: digest, RoleIDs: []string{"other-assistant"},
		}); err != nil {
			t.Fatal(err)
		}
		calls := 0
		if _, err := hire(t, service, true, creator(roster, &calls)); !errors.Is(err, ErrNotShared) {
			t.Fatalf("hire on a Home that shares another blueprint: %v", err)
		}
		if consent, _ := service.Consents().Read("home"); consent.PluginID != "ori-other" || calls != 0 {
			t.Fatalf("the other blueprint's approval was replaced: %+v", consent)
		}
	})
}

// A Home whose offer was answered "Not now" is not overruled by Set up.
func TestHireShared_DismissedOfferHiresNobody(t *testing.T) {
	store, service, roster := hiredFixture(t)
	if err := store.Update("home", func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		now := time.Now().UTC()
		state.ProjectStaffingOfferDismissedAt = &now
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if plan, err := service.PlanShared("home", songTeam); err != nil || plan.State != SharedOff {
		t.Fatalf("plan = %+v %v", plan, err)
	}
	calls := 0
	if _, err := hire(t, service, true, creator(roster, &calls)); !errors.Is(err, ErrNoConsent) {
		t.Fatalf("hire after Not now: %v", err)
	}
	if consent, _ := service.Consents().Read("home"); consent != nil || calls != 0 {
		t.Fatalf("Set up overruled Not now: %+v, creates %d", consent, calls)
	}
}

// An assistant that is gone is forgotten and replaced, under its own name.
func TestHireShared_GoneAssistantForgottenThenReplaced(t *testing.T) {
	_, service, roster := hiredFixture(t)
	calls := 0
	if _, err := hire(t, service, true, creator(roster, &calls)); err != nil {
		t.Fatal(err)
	}
	firstID := roster["REAPER Assistant"].AssistantSetup.ID
	delete(roster, "REAPER Assistant")

	// With no model the gone agent stays recorded: nothing can replace it yet.
	if _, err := hire(t, service, false, creator(roster, &calls)); !errors.Is(err, ErrNoModel) {
		t.Fatalf("replace with no model: %v", err)
	}
	fills, err := hire(t, service, true, creator(roster, &calls))
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeCreate || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("replacement = %+v %v", fills, err)
	}
	if calls != 2 || len(roster) != 1 || roster["REAPER Assistant"].AssistantSetup.ID == firstID {
		t.Fatalf("creates = %d, agents = %v", calls, roster.ListAgents())
	}
	if role := roleOf(t, service); role.AgentName != "REAPER Assistant" || role.PendingName != "" {
		t.Fatalf("replacement not recorded: %+v", role)
	}
}

// The path to a second assistant the Home hire would otherwise open: a
// reviewed create reads a held name as "give this project its own agent".
// The stopped Home hire is finished first, so the project binds it.
func TestFillCreated_RecoversHomeHireBeforeDeciding(t *testing.T) {
	store := workspace.NewInMemoryStore()
	seedHome(t, store)
	if err := store.Update("home", func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.Declaration = &workspace.AssistantProgramDeclaration{
			SchemaVersion: 2, ID: "music", DefaultPrimaryName: "Portfolio Manager",
			Roles: []workspace.AssistantProgramRoleSpec{homeRole, libraryRole},
		}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	roster := definitions{}
	service := New(store, roster)
	calls := 0
	if _, err := hire(t, service, true, stoppingCreator(roster, &calls)); err == nil {
		t.Fatal("the stopped create reported success")
	}

	fills, err := service.FillCreated("song-a", nil, reviewedCreate, true)
	if err != nil {
		t.Fatal(err)
	}
	project := fillsByRole(fills)["reaper-assistant"]
	if project.Mode != ModeBind || project.Name != "REAPER Assistant" {
		t.Fatalf("the project's role = %+v (a second assistant would be created)", project)
	}
	if role := roleOf(t, service); role.AgentName != "REAPER Assistant" || role.PendingName != "" {
		t.Fatalf("the Home hire was not recorded: %+v", role)
	}
}

// Opening songs after setup binds that one assistant into each (FR17).
func TestFill_TwoSongsBindSameAssistant(t *testing.T) {
	store, service, roster := hiredFixture(t)
	calls := 0
	if _, err := hire(t, service, true, creator(roster, &calls)); err != nil {
		t.Fatal(err)
	}
	for _, song := range []string{"song-a", "song-b"} {
		addSong(t, store, song)
		fills, err := service.Fill(song, []string{"reaper-assistant"}, nil)
		if err != nil || len(fills) != 1 || fills[0].Mode != ModeBind || fills[0].Name != "REAPER Assistant" {
			t.Fatalf("%s = %+v %v", song, fills, err)
		}
	}
	if calls != 1 || len(roster) != 1 {
		t.Fatalf("creates = %d, agents = %v", calls, roster.ListAgents())
	}
}

// A song opened while a Home hire is still unrecorded finishes it first.
func TestFill_FinishesAStoppedHomeHire(t *testing.T) {
	store, service, roster := hiredFixture(t)
	calls := 0
	if _, err := hire(t, service, true, stoppingCreator(roster, &calls)); err == nil {
		t.Fatal("the stopped create reported success")
	}
	addSong(t, store, "song-a")
	fills, err := service.Fill("song-a", []string{"reaper-assistant"}, nil)
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeBind || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("song after a stopped hire = %+v %v", fills, err)
	}
}

// PlanShared is what the plan card shows before Set up: it decides as
// HireShared does and writes nothing.
func TestPlanShared_SaysWhatTheHireWouldDo(t *testing.T) {
	plan := func(t *testing.T, service *Service, homeID string) SharedPlan {
		t.Helper()
		got, err := service.PlanShared(homeID, songTeam)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	t.Run("before the Home exists", func(t *testing.T) {
		_, service, roster := hiredFixture(t)
		if got := plan(t, service, ""); got.State != SharedHire || got.Name != "REAPER Assistant" || got.Taken != "" {
			t.Fatalf("plan = %+v", got)
		}
		roster["REAPER Assistant"] = &agent.Agent{} // one of the owner's own
		if got := plan(t, service, ""); got.State != SharedHire || got.Name != "REAPER Assistant 2" || got.Taken != "REAPER Assistant" {
			t.Fatalf("plan with the name taken = %+v", got)
		}
	})
	t.Run("the card's name is the name the hire uses, and nothing was written", func(t *testing.T) {
		store, service, roster := hiredFixture(t)
		roster["REAPER Assistant"] = &agent.Agent{}
		shown := plan(t, service, "home")
		before, _ := store.Get("home")
		if before.GetAssistantProgramState().GetProjectStaffingConsent() != nil {
			t.Fatal("the plan wrote a consent")
		}
		calls := 0
		fills, err := hire(t, service, true, creator(roster, &calls))
		if err != nil || fills[0].Name != shown.Name || shown.Name != "REAPER Assistant 2" {
			t.Fatalf("shown %+v, hired %+v %v", shown, fills, err)
		}
		if got := plan(t, service, "home"); got.State != SharedHas || got.Name != "REAPER Assistant 2" {
			t.Fatalf("after the hire = %+v", got)
		}
	})
	t.Run("off, older team, another blueprint", func(t *testing.T) {
		_, service, _ := hiredFixture(t)
		grantHome(t, service)
		if _, err := service.Consents().Revoke("home"); err != nil {
			t.Fatal(err)
		}
		if got := plan(t, service, "home"); got.State != SharedOff {
			t.Fatalf("off = %+v", got)
		}
		_, service, _ = hiredFixture(t)
		if _, err := service.Consents().Grant("home", workspace.ProjectStaffingConsentGrant{Source: workspace.ProjectStaffingConsentHomeSwitch,
			PluginID: "ori-reaper", BlueprintID: "reaper-song", TeamDigest: "b" + digest[1:], RoleIDs: []string{"reaper-assistant"}}); err != nil {
			t.Fatal(err)
		}
		if got := plan(t, service, "home"); got.State != SharedStale {
			t.Fatalf("stale = %+v", got)
		}
		_, service, _ = hiredFixture(t)
		if _, err := service.Consents().Grant("home", workspace.ProjectStaffingConsentGrant{Source: workspace.ProjectStaffingConsentHomeSwitch,
			PluginID: "ori-other", BlueprintID: "other-song", TeamDigest: digest, RoleIDs: []string{"other"}}); err != nil {
			t.Fatal(err)
		}
		if got := plan(t, service, "home"); got.State != SharedNotOurs {
			t.Fatalf("not ours = %+v", got)
		}
	})
	t.Run("held by a stranger, or by the reservation's own stopped create", func(t *testing.T) {
		_, service, roster := hiredFixture(t)
		grantHome(t, service)
		if err := service.Consents().ReserveAgent("home", "ori-reaper", "reaper-song", digest, "reaper-assistant", "REAPER Assistant"); err != nil {
			t.Fatal(err)
		}
		roster["REAPER Assistant"] = &agent.Agent{}
		if got := plan(t, service, "home"); got.State != SharedHeld || got.Name != "REAPER Assistant" {
			t.Fatalf("held = %+v", got)
		}
		_, service, roster = hiredFixture(t)
		calls := 0
		if _, err := hire(t, service, true, stoppingCreator(roster, &calls)); err == nil {
			t.Fatal("the stopped create reported success")
		}
		if got := plan(t, service, "home"); got.State != SharedHas || got.Name != "REAPER Assistant" {
			t.Fatalf("a stopped hire of our own = %+v", got)
		}
	})
	t.Run("gone: a hire replaces it", func(t *testing.T) {
		_, service, roster := hiredFixture(t)
		calls := 0
		if _, err := hire(t, service, true, creator(roster, &calls)); err != nil {
			t.Fatal(err)
		}
		delete(roster, "REAPER Assistant")
		if got := plan(t, service, "home"); got.State != SharedHire || got.Name != "REAPER Assistant" {
			t.Fatalf("gone = %+v", got)
		}
	})
	t.Run("a team that does not have exactly one role names nobody", func(t *testing.T) {
		_, service, _ := hiredFixture(t)
		two := songTeam
		two.Roles = append(append([]workspace.AssistantProgramRoleSpec(nil), songTeam.Roles...),
			workspace.AssistantProgramRoleSpec{ID: "mixer", Label: "Mixer", Required: true})
		if _, err := service.PlanShared("", two); !errors.Is(err, ErrNotShared) {
			t.Fatalf("two roles: %v", err)
		}
	})
}

// HomeAssistant names the Home's assistant whatever the switch says: the
// switch decides whether it joins songs, not whether the Home has it (FR15).
func TestHomeAssistant(t *testing.T) {
	_, service, roster := hiredFixture(t)
	if _, err := service.HomeAssistant("home", "ori-reaper", "reaper-song"); !errors.Is(err, ErrNoConsent) {
		t.Fatalf("no approval: %v", err)
	}
	calls := 0
	if _, err := hire(t, service, true, creator(roster, &calls)); err != nil {
		t.Fatal(err)
	}
	if name, err := service.HomeAssistant("home", "ori-reaper", "reaper-song"); err != nil || name != "REAPER Assistant" {
		t.Fatalf("hired = %q %v", name, err)
	}
	if _, err := service.HomeAssistant("home", "ori-other", "other-song"); !errors.Is(err, ErrNoConsent) {
		t.Fatalf("another blueprint: %v", err)
	}
	if _, err := service.Consents().Revoke("home"); err != nil {
		t.Fatal(err)
	}
	if name, err := service.HomeAssistant("home", "ori-reaper", "reaper-song"); err != nil || name != "REAPER Assistant" {
		t.Fatalf("switched off = %q %v", name, err)
	}
	delete(roster, "REAPER Assistant")
	if _, err := service.HomeAssistant("home", "ori-reaper", "reaper-song"); !errors.Is(err, ErrAssistantMissing) {
		t.Fatalf("agent gone: %v", err)
	}
}
