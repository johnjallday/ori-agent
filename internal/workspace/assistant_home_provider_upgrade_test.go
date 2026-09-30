package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	upgradeOldPrompt = "Coordinate the music portfolio."
	upgradeNewPrompt = "Coordinate the music portfolio and its approved library."
)

func homeProviderUpgradeFixture() HomeProviderUpgrade {
	from, _ := splitProviderOwners()
	to := from
	to.PluginVersion, to.DeclarationDigest = "2.0.1", strings.Repeat("7", 64)
	to.PluginGeneration, to.ComponentFingerprint = 12, strings.Repeat("8", 64)
	return HomeProviderUpgrade{
		OperationID: "home-upgrade-1", From: from, To: to,
		RolePrompts: map[string]HomeRolePromptChange{"producer": {Old: upgradeOldPrompt, New: upgradeNewPrompt}},
		UpgradedAt:  time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
	}
}

// linkedIndependentHome creates an independent Home on the upgrade's old
// evidence and links one child through the ordinary EnsureProjectStation path.
func linkedIndependentHome(t *testing.T, upgrade HomeProviderUpgrade) (*SyncStore, *InMemoryStore, *FileStore, *AssistantProgramStore, string, string) {
	t.Helper()
	primary := NewInMemoryStore()
	folders, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	store := NewSyncStore(primary, folders)
	programs := NewAssistantProgramStore(store)
	key := AssistantProgramKey{OwnerUserID: "owner", PluginID: "music", ProgramID: "music_home"}
	home, _, err := programs.EnsureNamedIndependentStation(key, splitHomeDeclaration(), "Music Home", upgrade.From)
	if err != nil {
		t.Fatal(err)
	}
	_, projectOwner := splitProviderOwners()
	project := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
	project.ID, project.OwnerUserID, project.ParentID = "song-one", "owner", home.ID
	project.SetTemplateProvenance(splitProjectProvenance(home.ID, project.ID, upgrade.From, projectOwner))
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	if _, _, err := programs.EnsureProjectStation(project.ID); err != nil {
		t.Fatal(err)
	}
	return store, primary, folders, programs, home.ID, project.ID
}

func rebindThroughStore(t *testing.T, store Store, upgrade HomeProviderUpgrade, homeID, projectID string) (homeChanged, projectChanged bool) {
	t.Helper()
	if err := store.Update(homeID, func(current *Workspace) error {
		var err error
		homeChanged, err = upgrade.RebindHome(current)
		return err
	}); err != nil {
		t.Fatalf("rebind Home: %v", err)
	}
	home, err := store.Get(homeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(projectID, func(current *Workspace) error {
		var err error
		projectChanged, err = upgrade.RebindProject(current, homeID, home.GetAssistantProgramState().Declaration)
		return err
	}); err != nil {
		t.Fatalf("rebind project: %v", err)
	}
	return homeChanged, projectChanged
}

func TestHomeProviderUpgradeMovesHomeAndChildOntoTheNewEvidence(t *testing.T) {
	upgrade := homeProviderUpgradeFixture()
	store, primary, folders, programs, homeID, projectID := linkedIndependentHome(t, upgrade)
	homeBefore, _ := store.Get(homeID)
	stateBefore := homeBefore.GetAssistantProgramState()
	projectBefore, _ := store.Get(projectID)
	linkBefore, snapshotBefore := projectBefore.GetAssistantProjectLink(), projectBefore.GetTemplateProvenance().GroupRequirement
	// The SQLite primary has no provenance column; the write must restore the
	// folder-only provenance rather than persist a provenance-less child.
	if err := primary.Update(projectID, func(current *Workspace) error {
		current.TemplateProvenance = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	homeChanged, projectChanged := rebindThroughStore(t, store, upgrade, homeID, projectID)
	if !homeChanged || !projectChanged {
		t.Fatalf("first rebind changed Home=%t project=%t", homeChanged, projectChanged)
	}

	home, _ := store.Get(homeID)
	state := home.GetAssistantProgramState()
	if state.HomeProvider == nil || *state.HomeProvider != upgrade.To || state.Declaration.Roles[0].SystemPrompt != upgradeNewPrompt ||
		state.StateRevision != stateBefore.StateRevision+1 || state.GroupTemplate != nil {
		t.Fatalf("Home after rebind = %#v", state)
	}
	if len(state.ProviderUpgrades) != 1 || state.ProviderUpgrades[0] != (AssistantHomeProviderUpgradeReceipt{
		OperationID: upgrade.OperationID, FromVersion: "2.0.0", ToVersion: "2.0.1",
		FromFingerprint: upgrade.From.ComponentFingerprint, ToFingerprint: upgrade.To.ComponentFingerprint, UpgradedAt: upgrade.UpgradedAt,
	}) {
		t.Fatalf("upgrade receipt = %#v", state.ProviderUpgrades)
	}
	stateBefore.HomeProvider, stateBefore.Declaration, stateBefore.StateRevision, stateBefore.ProviderUpgrades =
		state.HomeProvider, state.Declaration, state.StateRevision, state.ProviderUpgrades
	if !sameJSON(t, stateBefore, state) {
		t.Fatalf("rebind changed more of the Home than its pin and declaration:\nbefore %#v\nafter  %#v", stateBefore, state)
	}

	for name, source := range map[string]Store{"primary": primary, "folder": folders} {
		project, err := source.Get(projectID)
		if err != nil {
			t.Fatal(err)
		}
		link, provenance := project.GetAssistantProjectLink(), project.GetTemplateProvenance()
		if provenance == nil || provenance.GroupRequirement == nil || provenance.AssistantProgram == nil {
			t.Fatalf("%s child lost its portable provenance: %#v", name, provenance)
		}
		if link.HomeProvider == nil || *link.HomeProvider != upgrade.To || link.StateRevision != linkBefore.StateRevision ||
			*link.ProjectProvider != *linkBefore.ProjectProvider {
			t.Fatalf("%s link = %#v", name, link)
		}
		snapshot := provenance.GroupRequirement
		if snapshot.HomeProvider == nil || *snapshot.HomeProvider != upgrade.To || *snapshot.ProjectProvider != *snapshotBefore.ProjectProvider ||
			snapshot.DefinitionDigest != snapshotBefore.DefinitionDigest || snapshot.ReviewDigest != snapshotBefore.ReviewDigest ||
			snapshot.OperationDigest != snapshotBefore.OperationDigest || !snapshot.AppliedAt.Equal(snapshotBefore.AppliedAt) {
			t.Fatalf("%s snapshot creation evidence changed or pin not moved: %#v", name, snapshot)
		}
		if !sameJSON(t, provenance.AssistantProgram, state.Declaration) || len(provenance.AssistantProjectRoles) != 1 {
			t.Fatalf("%s child declaration = %#v", name, provenance.AssistantProgram)
		}
	}
	if !AssistantProjectLinkMirrorsAgree(store, mustGet(t, store, projectID)) {
		t.Fatal("rebind left the link mirrors disagreeing")
	}

	// Every live comparator accepts the moved pair.
	if status := EvaluateGroupRequirementLifecycle(mustGet(t, store, projectID), store.Get); status == nil || status.State != GroupRequirementStatusReadyGrouped {
		t.Fatalf("lifecycle after rebind = %#v", status)
	}
	if station, _, err := programs.EnsureProjectStation(projectID); err != nil || station.ID != homeID {
		t.Fatalf("relink after rebind = %#v, %v", station, err)
	}
	_, projectOwner := splitProviderOwners()
	for name, pin := range map[string]AssistantProgramHomeOwner{"new": upgrade.To, "old": upgrade.From} {
		later := NewWorkspace(CreateWorkspaceParams{Name: "Later " + name})
		later.ID, later.OwnerUserID, later.ParentID = "later-"+name, "owner", homeID
		later.SetTemplateProvenance(splitProjectProvenance(homeID, later.ID, pin, projectOwner))
		if err := store.Save(later); err != nil {
			t.Fatal(err)
		}
		_, _, err := programs.EnsureProjectStation(later.ID)
		if name == "new" && err != nil {
			t.Fatalf("project created on the new release cannot join the moved Home: %v", err)
		}
		if name == "old" && !errors.Is(err, ErrAssistantProgramVersionConflict) {
			t.Fatalf("project created on the old release joined the moved Home: %v", err)
		}
	}

	// A rerun after a crash finds everything moved and changes nothing.
	revision := mustGet(t, store, homeID).GetAssistantProgramState().StateRevision
	if homeChanged, projectChanged := rebindThroughStore(t, store, upgrade, homeID, projectID); homeChanged || projectChanged {
		t.Fatalf("rerun changed Home=%t project=%t", homeChanged, projectChanged)
	}
	if again := mustGet(t, store, homeID).GetAssistantProgramState(); again.StateRevision != revision || len(again.ProviderUpgrades) != 1 {
		t.Fatalf("rerun rewrote the Home: %#v", again)
	}
}

func TestHomeProviderUpgradeRefusesForeignAndPartlyMovedHomes(t *testing.T) {
	upgrade := homeProviderUpgradeFixture()
	foreign := upgrade.To
	foreign.PluginVersion, foreign.ComponentFingerprint = "2.0.2", strings.Repeat("9", 64)
	for name, tc := range map[string]struct {
		pin    *AssistantProgramHomeOwner
		prompt string
	}{
		"legacy Home without a pin":      {pin: nil, prompt: upgradeOldPrompt},
		"pinned to a third release":      {pin: &foreign, prompt: upgradeOldPrompt},
		"old pin with the new prompt":    {pin: &upgrade.From, prompt: upgradeNewPrompt},
		"new pin with the old prompt":    {pin: &upgrade.To, prompt: upgradeOldPrompt},
		"old pin with an unknown prompt": {pin: &upgrade.From, prompt: "Something else."},
	} {
		t.Run(name, func(t *testing.T) {
			declaration := splitHomeDeclaration()
			declaration.Roles[0].SystemPrompt = tc.prompt
			home := NewWorkspace(CreateWorkspaceParams{Name: "Music Home"})
			home.SetAssistantProgramState(&AssistantProgramState{SchemaVersion: AssistantProgramStateSchemaVersion, StateRevision: 3,
				Key: AssistantProgramKey{OwnerUserID: "owner", PluginID: "music", ProgramID: "music_home"}, Declaration: declaration, HomeProvider: tc.pin})
			before := home.GetAssistantProgramState()
			if changed, err := upgrade.RebindHome(home); changed || !errors.Is(err, ErrHomeProviderPinUnexpected) {
				t.Fatalf("rebind = %t, %v", changed, err)
			}
			if !sameJSON(t, before, home.GetAssistantProgramState()) {
				t.Fatal("refused rebind changed the Home")
			}
		})
	}

	receipts := make([]AssistantHomeProviderUpgradeReceipt, maxHomeProviderUpgradeReceipts)
	for index := range receipts {
		receipts[index].OperationID = "earlier-" + string(rune('a'+index))
	}
	home := NewWorkspace(CreateWorkspaceParams{Name: "Music Home"})
	home.SetAssistantProgramState(&AssistantProgramState{SchemaVersion: AssistantProgramStateSchemaVersion, StateRevision: 3,
		Key: AssistantProgramKey{OwnerUserID: "owner", PluginID: "music", ProgramID: "music_home"}, Declaration: splitHomeDeclaration(),
		HomeProvider: &upgrade.From, ProviderUpgrades: receipts})
	if changed, err := upgrade.RebindHome(home); !changed || err != nil {
		t.Fatalf("rebind = %t, %v", changed, err)
	}
	if kept := home.GetAssistantProgramState().ProviderUpgrades; len(kept) != maxHomeProviderUpgradeReceipts ||
		kept[0].OperationID != "earlier-b" || kept[len(kept)-1].OperationID != upgrade.OperationID {
		t.Fatalf("receipts not bounded to the newest %d: %#v", maxHomeProviderUpgradeReceipts, kept)
	}
}

func TestHomeProviderUpgradeRefusesChildrenItCannotAccountFor(t *testing.T) {
	upgrade := homeProviderUpgradeFixture()
	foreign := upgrade.To
	foreign.PluginVersion, foreign.ComponentFingerprint = "2.0.2", strings.Repeat("9", 64)
	_, projectOwner := splitProviderOwners()
	child := func(linkPin, snapshotPin *AssistantProgramHomeOwner, prompt string) *Workspace {
		project := NewWorkspace(CreateWorkspaceParams{Name: "Song"})
		project.ID = "song-one"
		provenance := splitProjectProvenance("home-1", project.ID, upgrade.From, projectOwner)
		provenance.GroupRequirement.HomeProvider = snapshotPin
		provenance.AssistantProgram.Roles[0].SystemPrompt = prompt
		project.SetTemplateProvenance(provenance)
		project.SetAssistantProjectLink(&AssistantProjectLink{ID: AssistantProjectLinkID("home-1", project.ID), SchemaVersion: AssistantProjectLinkSchemaVersion,
			StationWorkspaceID: "home-1", Key: AssistantProgramKey{OwnerUserID: "owner", PluginID: "music", ProgramID: "music_home"}, StateRevision: 4,
			HomeProvider: linkPin, ProjectProvider: &projectOwner})
		return project
	}
	homeDeclaration := splitHomeDeclaration()
	for name, tc := range map[string]struct {
		project *Workspace
		homeID  string
	}{
		"linked to another Home":         {project: child(&upgrade.From, &upgrade.From, upgradeOldPrompt), homeID: "home-2"},
		"link pinned to a third release": {project: child(&foreign, &upgrade.From, upgradeOldPrompt), homeID: "home-1"},
		"snapshot on a third release":    {project: child(&upgrade.To, &foreign, upgradeOldPrompt), homeID: "home-1"},
		"declaration copy unknown":       {project: child(&upgrade.From, &upgrade.From, "Something else."), homeID: "home-1"},
		"snapshot names another Home": {project: func() *Workspace {
			project := child(&upgrade.From, &upgrade.From, upgradeOldPrompt)
			provenance := project.GetTemplateProvenance()
			provenance.GroupRequirement.HomeWorkspaceID = "home-2"
			project.SetTemplateProvenance(provenance)
			return project
		}(), homeID: "home-1"},
	} {
		t.Run(name, func(t *testing.T) {
			linkBefore, provenanceBefore := tc.project.GetAssistantProjectLink(), tc.project.GetTemplateProvenance()
			if changed, err := upgrade.RebindProject(tc.project, tc.homeID, homeDeclaration); changed || !errors.Is(err, ErrHomeProviderPinUnexpected) {
				t.Fatalf("rebind = %t, %v", changed, err)
			}
			if !sameJSON(t, linkBefore, tc.project.GetAssistantProjectLink()) || !sameJSON(t, provenanceBefore, tc.project.GetTemplateProvenance()) {
				t.Fatal("refused rebind changed the child")
			}
		})
	}

	// A half-moved child (link moved, snapshot not) completes; an ordinary
	// child without a Group Requirement snapshot moves its link alone.
	halfMoved := child(&upgrade.To, &upgrade.From, upgradeNewPrompt)
	if changed, err := upgrade.RebindProject(halfMoved, "home-1", homeDeclaration); !changed || err != nil {
		t.Fatalf("half-moved child = %t, %v", changed, err)
	}
	if pin := halfMoved.GetTemplateProvenance().GroupRequirement.HomeProvider; pin == nil || *pin != upgrade.To {
		t.Fatalf("half-moved snapshot pin = %#v", pin)
	}
	plain := child(&upgrade.From, nil, upgradeOldPrompt)
	provenance := plain.GetTemplateProvenance()
	provenance.GroupRequirement = nil
	plain.SetTemplateProvenance(provenance)
	if changed, err := upgrade.RebindProject(plain, "home-1", homeDeclaration); !changed || err != nil ||
		*plain.GetAssistantProjectLink().HomeProvider != upgrade.To || plain.GetTemplateProvenance().AssistantProgram.Roles[0].SystemPrompt != upgradeNewPrompt {
		t.Fatalf("ordinary child = %t, %v, %#v", changed, err, plain.GetAssistantProjectLink())
	}
}

func TestHomeProviderUpgradeRejectsMalformedDescriptions(t *testing.T) {
	for name, mutate := range map[string]func(*HomeProviderUpgrade){
		"no operation":         func(u *HomeProviderUpgrade) { u.OperationID = " " },
		"same evidence":        func(u *HomeProviderUpgrade) { u.To = u.From },
		"another plugin":       func(u *HomeProviderUpgrade) { u.To.PluginID = "other" },
		"another program":      func(u *HomeProviderUpgrade) { u.To.ProgramID = "other_home" },
		"another Home version": func(u *HomeProviderUpgrade) { u.To.HomeVersion = 3 },
		"invalid target":       func(u *HomeProviderUpgrade) { u.To.ComponentFingerprint = "short" },
		"unchanged prompt": func(u *HomeProviderUpgrade) {
			u.RolePrompts["producer"] = HomeRolePromptChange{Old: "Same.", New: "Same."}
		},
		"no time": func(u *HomeProviderUpgrade) { u.UpgradedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			upgrade := homeProviderUpgradeFixture()
			mutate(&upgrade)
			home := NewWorkspace(CreateWorkspaceParams{Name: "Music Home"})
			home.SetAssistantProgramState(&AssistantProgramState{SchemaVersion: AssistantProgramStateSchemaVersion, StateRevision: 1,
				Declaration: splitHomeDeclaration(), HomeProvider: &upgrade.From})
			if _, err := upgrade.RebindHome(home); !errors.Is(err, ErrInvalidHomeProviderUpgrade) {
				t.Fatalf("RebindHome = %v", err)
			}
			if _, err := upgrade.RebindProject(NewWorkspace(CreateWorkspaceParams{Name: "Song"}), "home-1", splitHomeDeclaration()); !errors.Is(err, ErrInvalidHomeProviderUpgrade) {
				t.Fatalf("RebindProject = %v", err)
			}
		})
	}
	upgrade := homeProviderUpgradeFixture()
	upgrade.RolePrompts["missing_role"] = HomeRolePromptChange{Old: "a", New: "b"}
	if _, _, ok := upgrade.Declarations(splitHomeDeclaration()); ok {
		t.Fatal("a prompt change for a role the Home does not declare was accepted")
	}
}

func TestCloneAssistantProgramStateDetachesUpgradeReceipts(t *testing.T) {
	state := &AssistantProgramState{ProviderUpgrades: []AssistantHomeProviderUpgradeReceipt{{OperationID: "one"}}}
	clone := CloneAssistantProgramState(state)
	clone.ProviderUpgrades[0].OperationID = "changed"
	if state.ProviderUpgrades[0].OperationID != "one" {
		t.Fatal("clone shares its upgrade receipts with the source")
	}
}

func mustGet(t *testing.T, store Store, id string) *Workspace {
	t.Helper()
	ws, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func sameJSON(t *testing.T, left, right any) bool {
	t.Helper()
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		t.Fatalf("marshal: %v %v", leftErr, rightErr)
	}
	return bytes.Equal(leftJSON, rightJSON)
}
