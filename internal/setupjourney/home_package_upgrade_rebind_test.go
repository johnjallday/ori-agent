package setupjourney

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A reviewed Home package upgrade (independent-program-homes.md §6.1) must
// leave the Home and its old split child exactly as inspectable against the
// NEW installed release as they were against the old one.
func TestHomePackageUpgradeRebindKeepsChildInspectableAgainstNewRelease(t *testing.T) {
	store, _, folder, installed, owner, homeID, projectID := oldSplitChildFixture(t)
	if _, err := InspectMissingSplitProjectRoles(store, installed, owner, homeID, projectID); err != nil {
		t.Fatalf("fixture is not inspectable before the upgrade: %v", err)
	}

	oldHome := installed[0].WorkspaceSurfaces.AssistantProgramHomes[0]
	newHome := oldHome
	newHome.Roles = append([]projecttemplates.AssistantProgramHomeRole(nil), oldHome.Roles...)
	newHome.Roles[0].SystemPrompt = "Home only, with the approved project library."
	upgraded := append([]plugin.InstalledPlugin(nil), installed...)
	surfaces := *installed[0].WorkspaceSurfaces
	surfaces.AssistantProgramHomes = []projecttemplates.AssistantProgramHome{newHome}
	upgraded[0].WorkspaceSurfaces = &surfaces
	upgraded[0].Version, upgraded[0].ContentGeneration, upgraded[0].ComponentFingerprint = "0.1.2", 5, strings.Repeat("f", 64)

	// Replaced but not rebound: the old pins match nothing installed.
	if _, err := InspectMissingSplitProjectRoles(store, upgraded, owner, homeID, projectID); err == nil {
		t.Fatal("old pins were accepted against the replaced release")
	}

	changes, err := projecttemplates.GuidanceOnlyHomeChange(oldHome, newHome)
	if err != nil || len(changes) != 1 {
		t.Fatalf("guidance-only change = %#v, %v", changes, err)
	}
	home, _ := store.Get(homeID)
	upgrade := workspace.HomeProviderUpgrade{
		OperationID: "home-upgrade-1", From: *home.GetAssistantProgramState().HomeProvider,
		To: workspace.AssistantProgramHomeOwner{
			PluginID: upgraded[0].Name, PluginVersion: upgraded[0].Version, ProgramID: newHome.ID,
			HomeSchemaVersion: newHome.SchemaVersion, HomeVersion: newHome.Version,
			DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(newHome),
			PluginGeneration:  upgraded[0].EvidenceGeneration(), ComponentFingerprint: upgraded[0].ComponentFingerprint,
		},
		RolePrompts: map[string]workspace.HomeRolePromptChange{changes[0].RoleID: {Old: changes[0].Old, New: changes[0].New}},
		UpgradedAt:  time.Now().UTC(),
	}
	if err := store.Update(homeID, func(current *workspace.Workspace) error {
		_, err := upgrade.RebindHome(current)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	home, _ = store.Get(homeID)
	state := home.GetAssistantProgramState()
	if err := store.Update(projectID, func(current *workspace.Workspace) error {
		_, err := upgrade.RebindProject(current, homeID, state.Declaration)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Swapping only the changed prompts yields exactly the new package's Home.
	want, _ := json.Marshal(newHome.AssistantProgram())
	got, _ := json.Marshal(state.Declaration)
	if !bytes.Equal(want, got) {
		t.Fatalf("rebound declaration differs from the new release:\nwant %s\ngot  %s", want, got)
	}
	if !plugin.IndependentHomeProviderEvidenceAvailable(upgraded, state.HomeProvider) ||
		plugin.IndependentHomeProviderEvidenceAvailable(installed, state.HomeProvider) {
		t.Fatal("rebound Home pin does not match exactly the new installed release")
	}
	child, _ := store.Get(projectID)
	if !plugin.IndependentProviderEvidenceAvailable(upgraded, state.HomeProvider, child.GetAssistantProjectLink().ProjectProvider) {
		t.Fatal("rebound child fails the cross-provider evidence check")
	}
	inspection, err := InspectMissingSplitProjectRoles(store, upgraded, owner, homeID, projectID)
	if err != nil || len(inspection.Roles) != 1 || inspection.Roles[0].ID != "reaper-assistant" || inspection.HomeStateRevision != state.StateRevision {
		t.Fatalf("role repair inspection after upgrade = %#v, %v", inspection, err)
	}
	portable, err := folder.Get(projectID)
	if err != nil {
		t.Fatal(err)
	}
	portableJSON, _ := json.Marshal(portable.GetTemplateProvenance().AssistantProgram)
	if !bytes.Equal(portableJSON, got) {
		t.Fatal("the folder copy of the child still holds the old Home declaration")
	}
}
