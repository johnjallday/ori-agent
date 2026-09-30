package projectlibrary

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type activationPlugins []plugin.InstalledPlugin

func (p activationPlugins) List() ([]plugin.InstalledPlugin, error) { return p, nil }

func activationFixture(t *testing.T) (*ActivationInspector, Scope, *Roots, *workspace.FileStore, musicTree, activationPlugins) {
	t.Helper()
	r, scope, file, picker := rootTestService(t)
	library := NewStore(file).WithProviderEvidence(func(_ Scope, _ *workspace.Workspace) bool { return true })
	r.library = library
	tree := newMusicTree(t)
	picker.path, _ = filepath.EvalSymlinks(tree.root)
	pick, err := r.Pick(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, pick, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := r.Commit(scope, review.Token, "activation-fixture-grant")
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		id, folder, format, availability string
		alternates                       []string
	}{
		{"single", tree.single, "reaper", "available", []string{"Song.rpp"}},
		{"alternates", tree.alternate, "reaper", "ambiguous", []string{"Take A.rpp", "Take B.RPP"}},
		{"unsupported", tree.ableton, "ableton", "available", []string{"Arrangement.als"}},
	} {
		info, statErr := os.Lstat(input.folder)
		if statErr != nil {
			t.Fatal(statErr)
		}
		identity, idErr := DirectoryIdentity(info)
		if idErr != nil {
			t.Fatal(idErr)
		}
		doc, err = library.Read(scope)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = library.mutate(scope, doc.Revision, operation{key: input.id, action: "fixture_scan", digest: input.id}, func(current *Document) (string, error) {
			finished := library.now().UTC()
			current.Scans = append(current.Scans, Scan{ID: input.id, RootID: root.ID, RootRevision: root.Revision,
				RootDigest: strings.Repeat("c", 64), ResultDigest: strings.Repeat("d", 64),
				Status: "complete", StartedAt: finished, FinishedAt: &finished})
			current.Entries = append(current.Entries, Entry{ID: input.id, Revision: 1,
				Observations: []Observation{{RootID: root.ID, RelativeFolder: filepath.Base(input.folder),
					FileIdentity: identity, Format: input.format, Availability: input.availability,
					Alternates: input.alternates, ScanID: input.id, ScannedAt: finished}},
			})
			return input.id, nil
		})
		if err != nil {
			t.Fatalf("fixture observation %s: %v", input.id, err)
		}
	}
	home := projecttemplates.AssistantProgramHome{ID: scope.ProgramID, SchemaVersion: 1, Version: 1,
		Roles: []projecttemplates.AssistantProgramHomeRole{{ID: "portfolio_manager", Skills: []string{"music-project-management"}}},
		AllowedProjectAttachments: []projecttemplates.AssistantProgramAllowedProjectAttachment{{
			ProviderPluginID: "reaper-plugin", BlueprintID: "reaper-song", ProjectTeamID: "reaper-song-team",
			ProjectTeamSchemaVersion: 1, MinProjectTeamVersion: 1, MaxProjectTeamVersion: 1,
		}}}
	owner := workspace.AssistantProgramHomeOwner{
		PluginID: scope.ProviderID, PluginVersion: "0.1.1", ProgramID: scope.ProgramID,
		HomeSchemaVersion: 1, HomeVersion: 1, PluginGeneration: 1,
		ComponentFingerprint: strings.Repeat("a", 64), DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(home),
	}
	if err := file.Update(scope.HomeID, func(ws *workspace.Workspace) error {
		state := ws.GetAssistantProgramState()
		state.HomeProvider = &owner
		ws.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	project := &projecttemplates.AssistantProjectDeclaration{
		SchemaVersion: 1, Version: 1, ID: "reaper-song-team",
		Home: projecttemplates.AssistantProjectHomeReference{ProviderPluginID: scope.ProviderID,
			ProgramID: scope.ProgramID, HomeSchemaVersion: 1, MinHomeVersion: 1, MaxHomeVersion: 1},
		Roles: []projecttemplates.AssistantProjectRole{{ID: "reaper-assistant", Label: "REAPER Assistant"}},
	}
	installed := activationPlugins{
		{Name: scope.ProviderID, Version: "0.1.1", Enabled: true, ContentGeneration: 1,
			ComponentFingerprint: strings.Repeat("a", 64), Skills: []string{"music-project-management"},
			WorkspaceSurfaces: &plugin.SurfaceContribution{Protocol: plugin.ProtocolRange{Min: 1, Max: 1},
				RequiresHostFeatures:  []string{plugin.HostFeatureIndependentProgramHomesV1},
				AssistantProgramHomes: []projecttemplates.AssistantProgramHome{home}}},
		{Name: "reaper-plugin", Version: "0.9.0", Enabled: true, ContentGeneration: 1,
			ComponentFingerprint: strings.Repeat("b", 64), WorkspaceSurfaces: &plugin.SurfaceContribution{
				Protocol: plugin.ProtocolRange{Min: 1, Max: 1}, RequiresHostFeatures: []string{plugin.HostFeatureIndependentProgramHomesV1}},
			ResolvedBlueprints: []plugin.ResolvedBlueprint{{ID: "reaper-song", QualifiedID: "plugin:reaper-plugin:reaper-song", Version: 10,
				Template: projecttemplates.Template{ID: "plugin:reaper-plugin:reaper-song",
					PluginOwner: &workspace.PluginTemplateOwner{PluginID: "reaper-plugin", PluginVersion: "0.9.0", BlueprintID: "reaper-song", BlueprintVersion: 10},
					GroupRequirement: &projecttemplates.GroupRequirement{SchemaVersion: projecttemplates.SplitGroupRequirementSchemaVersion,
						Policy: projecttemplates.GroupPolicyRequired, AssistantProjectID: "reaper-song-team"},
					AssistantProject: project,
					ProjectConnection: &projecttemplates.ProjectConnectionDeclaration{SchemaVersion: 1,
						SupportedModes: []projecttemplates.ProjectConnectionMode{projecttemplates.ProjectConnectionExistingProject},
						AttachExisting: &projecttemplates.AttachExistingDeclaration{EntryExtensions: []string{".rpp"}}}},
			}}},
	}
	return NewActivationInspector(library, r, installed, file), scope, r, file, tree, installed
}

func TestActivationEligibility_UsesCurrentBlueprintAndNeverCreatesOrReadsProjectContents(t *testing.T) {
	a, scope, _, file, tree, _ := activationFixture(t)
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	result, err := a.Eligibility(t.Context(), scope, "single")
	if err != nil || result.State != "review_available" || result.BlueprintID != "plugin:reaper-plugin:reaper-song" ||
		len(result.ProjectRoleLabels) != 1 || result.ProjectRoleLabels[0] != "REAPER Assistant" ||
		len(result.ProjectFiles) != 1 || result.ProjectFiles[0] != "Song.rpp" {
		t.Fatalf("eligible: %+v %v", result, err)
	}
	alternates, err := a.Eligibility(t.Context(), scope, "alternates")
	if err != nil || alternates.State != "file_choice_required" || len(alternates.ProjectFiles) != 2 {
		t.Fatalf("ambiguous: %+v %v", alternates, err)
	}
	unsupported, err := a.Eligibility(t.Context(), scope, "unsupported")
	if err != nil || unsupported.State != "unsupported_format" || len(unsupported.ProjectFiles) != 0 {
		t.Fatalf("unsupported DAW: %+v %v", unsupported, err)
	}
	ids, err := file.List()
	if err != nil || len(ids) != 1 || ids[0] != scope.HomeID || fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("eligibility wrote outside Home: ids=%v err=%v", ids, err)
	}
}

func TestActivationEligibility_RevokedRootKeepsHistoryWithoutActivation(t *testing.T) {
	a, scope, roots, file, _, installed := activationFixture(t)
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := roots.ReviewRevoke(scope, doc.Roots[0].ID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := roots.CommitRevoke(scope, doc.Roots[0].ID, review.Token, "activation-revoke"); err != nil {
		t.Fatal(err)
	}
	result, err := a.Eligibility(t.Context(), scope, "single")
	if err != nil || result.State != "revoked_source" || len(result.ProjectFiles) != 0 {
		t.Fatalf("revoked root still offered activation: %+v %v", result, err)
	}
	if doc, err := a.library.Read(scope); err != nil || len(doc.Entries) != 3 {
		t.Fatalf("revocation erased history: entries=%d err=%v", len(doc.Entries), err)
	}
	missing := NewActivationInspector(a.library, roots, installed[:1], file)
	if result, err := missing.Eligibility(t.Context(), scope, "single"); err != nil || result.State != "revoked_source" {
		t.Fatalf("a missing project integration hid revocation: %+v %v", result, err)
	}
}

func TestActivationEligibility_RefusesUnverifiedProvidersOwnersAndRoots(t *testing.T) {
	a, scope, roots, file, tree, installed := activationFixture(t)
	if _, err := a.Eligibility(t.Context(), Scope{OwnerUserID: "foreign", HomeID: scope.HomeID, ProviderID: scope.ProviderID, ProgramID: scope.ProgramID}, "single"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("foreign Home authority: %v", err)
	}
	missing := NewActivationInspector(a.library, roots, installed[:1], file)
	if result, err := missing.Eligibility(t.Context(), scope, "single"); err != nil ||
		result.State != "project_provider_unavailable" || !strings.Contains(result.Reason, "format: reaper") ||
		result.ObservedFormat != "reaper" {
		t.Fatalf("missing project provider: %+v %v", result, err)
	}
	if result, err := missing.Eligibility(t.Context(), scope, "unsupported"); err != nil ||
		result.State != "project_provider_unavailable" || !strings.Contains(result.Reason, "format: ableton") ||
		result.ObservedFormat != "ableton" {
		t.Fatalf("cataloged DAW was hidden by missing provider: %+v %v", result, err)
	}
	// Only the provider-missing state names a format for the host to consider;
	// every other state, including revoked or connected, carries none.
	if result, err := a.Eligibility(t.Context(), scope, "single"); err != nil || result.ObservedFormat != "" {
		t.Fatalf("a state with a compatible provider leaked an observed format: %+v %v", result, err)
	}
	changed := append(activationPlugins(nil), installed...)
	changed[1].Version = "9.9.9"
	incompatible := NewActivationInspector(a.library, roots, changed, file)
	if result, err := incompatible.Eligibility(t.Context(), scope, "single"); err != nil || result.State != "project_provider_unavailable" {
		t.Fatalf("stale blueprint owner: %+v %v", result, err)
	}
	owned := &workspace.Workspace{ID: "another-project", Name: "Same song", OwnerUserID: "foreign"}
	if err := projectconnection.RecordAttachedProject(owned, "Taken", tree.single, "Song.rpp", "foreign-reference"); err != nil {
		t.Fatal(err)
	}
	if err := file.Save(owned); err != nil {
		t.Fatal(err)
	}
	if result, err := a.Eligibility(t.Context(), scope, "single"); err != nil || result.State != "folder_owned" || result.WorkspaceID != "" {
		t.Fatalf("foreign folder owner leaked/adopted: %+v %v", result, err)
	}
	if err := file.Delete(owned.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(tree.single, "Song.rpp"), filepath.Join(tree.single, "Song.rpp-old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tree.single, "Song.rpp-old"), filepath.Join(tree.single, "Song.rpp")); err != nil {
		t.Fatal(err)
	}
	if result, err := a.Eligibility(t.Context(), scope, "single"); err != nil || result.State != "unavailable" {
		t.Fatalf("symlink project file inherited authority: %+v %v", result, err)
	}
	if err := os.Rename(tree.single, tree.single+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(tree.single, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree.single, "Song.rpp"), []byte("different song"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := a.Eligibility(context.Background(), scope, "single"); err != nil || result.State != "unavailable" {
		t.Fatalf("same-name replacement inherited authority: %+v %v", result, err)
	}
	if err := file.Update(scope.HomeID, func(ws *workspace.Workspace) error {
		state := ws.GetAssistantProgramState()
		state.HomeProvider.ComponentFingerprint = strings.Repeat("e", 64)
		ws.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if result, err := a.Eligibility(t.Context(), scope, "single"); err != nil || result.State != "home_provider_unavailable" {
		t.Fatalf("changed Home package inherited authority: %+v %v", result, err)
	}
}
