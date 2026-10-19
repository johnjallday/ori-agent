package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The server wires a workspace build's Set it up into Home's own setup: the
// folder digest reads a build review's folder only while a build session
// stands behind it, through the build handler's guard.
func TestWorkspaceBuildSetup_HomesSetupReadsABuildsFolderThroughItsBuild(t *testing.T) {
	f := newDraftServerFixture(t)
	ctx := context.Background()
	song := filepath.Join(os.Getenv("HOME"), "Music", "Ori Music Demo") // sandboxed by the fixture
	if err := os.MkdirAll(filepath.Join(song, "Media"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Ori Music Demo.rpp", "Media/take.wav"} {
		if err := os.WriteFile(filepath.Join(song, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	observations := f.builder.server.Storage.PersonalAssistantFolderObservations
	projection, err := f.builder.personalAssistantService.Get(ctx, "local")
	if err != nil || observations == nil || f.builder.workspaceBuildStore == nil {
		t.Fatalf("wiring: %v %v %v", projection, observations, err)
	}
	sessionID := "8f3c9a3e-1d2b-4c5d-9e6f-0a1b2c3d4e5f"
	draft := foldercontext.Target{UserID: "local", WorkspaceID: projection.HQWorkspaceID,
		AgentName: strings.ToLower(projection.GlobalAgentProfile), DraftID: personalassistant.BuildReviewPrefix + sessionID}
	observation, err := observations.Observe(ctx, draft, "chip", "music")
	if err != nil || observation == nil {
		t.Fatalf("observe: %+v %v", observation, err)
	}
	recognition, err := observations.Recognize(ctx, draft, observation.ID)
	if err != nil || !recognition.Recognized() {
		t.Fatalf("recognize: %+v %v", recognition, err)
	}
	offer, err := observations.ReviewBuild(ctx, draft, sessionID, observation.ID, recognition.CandidateID, "Summer Demos")
	if err != nil {
		t.Fatal(err)
	}

	// No build session stands behind the review: Home's setup cannot read it.
	if _, err := f.builder.personalAssistantFolderDigest.SubjectPath(ctx, "local", offer.ID); !errors.Is(err, personalassistant.ErrFolderPathLost) {
		t.Fatalf("an unowned build review was read: %v", err)
	}
	now := time.Now().UTC()
	if _, err := f.builder.workspaceBuildStore.Mutate(ctx, "local", func(doc *personalassistant.WorkspaceBuildDocument) error {
		doc.Add(personalassistant.WorkspaceBuildSession{
			ID: sessionID, Status: personalassistant.WorkspaceBuildOpen, Version: 1, CreatedAt: now, UpdatedAt: now,
			EntryPoint: "home_cockpit_create", Transcript: []personalassistant.BuildTranscriptEntry{},
			Folder: &personalassistant.BuildFolder{Observation: *observation, Recognition: recognition},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(song)
	if err != nil {
		t.Fatal(err)
	}
	if path, err := f.builder.personalAssistantFolderDigest.SubjectPath(ctx, "local", offer.ID); err != nil || path != canonical {
		t.Fatalf("the build's own review reads its folder: %q %v (want %q)", path, err, canonical)
	}
}

// musicHomeCarrierPlugin is the Music plugin as far as the catalog sees it:
// one program Home ("Music Production Home") and no project blueprint.
func musicHomeCarrierPlugin(t *testing.T) plugin.InstalledPlugin {
	t.Helper()
	home := projecttemplates.AssistantProgramHome{
		SchemaVersion: 1, Version: 1, ID: "music-producer-assistant",
		StationName: "Music Production Home", DefaultPrimaryName: "Portfolio Manager", HireTitle: "Staff this Home",
		Roles:      []projecttemplates.AssistantProgramHomeRole{{ID: "portfolio_manager", Label: "Portfolio Manager", Required: true, Primary: true, SystemPrompt: "Coordinate bounded work."}},
		Stages:     []workspace.AssistantProgramStageSpec{{ID: "foundation", Label: "Foundation", AcceptedCompletionThreshold: 0}},
		Reflection: workspace.AssistantReflectionConfig{MinimumProjects: 3, CadenceHours: 168, MaxProjects: 8, MaxEventsPerProject: 8, MaxCandidates: 8, MaxEvidence: 8, Rubric: "Use approved evidence."},
	}
	if err := projecttemplates.NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatal(err)
	}
	return plugin.InstalledPlugin{
		Name: "music-project-management", Version: "0.1.0", Generation: 1, Enabled: true,
		ComponentFingerprint: strings.Repeat("a", 64),
		WorkspaceSurfaces: &plugin.SurfaceContribution{
			Protocol:              plugin.ProtocolRange{Min: 1, Max: 1},
			RequiresHostFeatures:  []string{plugin.HostFeatureIndependentProgramHomesV1},
			AssistantProgramHomes: []projecttemplates.AssistantProgramHome{home},
		},
	}
}

// On 2026-10-19 an assisted build chose "Music Production Home" as the
// blueprint for a REAPER song folder. That template is the Home carrier the
// Group creator makes groups from, not a workspace blueprint: the build
// catalog must never contain one, while the Group creator keeps it.
func TestWorkspaceBuildCatalog_NeverOffersAHomeCarrier(t *testing.T) {
	root := t.TempDir()
	if err := projecttemplates.EnsureLibrary(root); err != nil {
		t.Fatal(err)
	}
	snapshot, err := buildBlueprintCatalogSnapshot(root, nil, staticInstalledPlugins{installed: []plugin.InstalledPlugin{musicHomeCarrierPlugin(t)}}, "local")
	if err != nil {
		t.Fatal(err)
	}
	carrier := "plugin-home:music-project-management:music-producer-assistant"
	if !snapshot.Active[carrier] {
		t.Fatalf("the fixture's Home carrier is in the shared snapshot and active: %v", snapshot.Active)
	}

	build := workspaceBuildCatalogEntries(snapshot)
	if len(build) == 0 {
		t.Fatal("the build catalog still offers the library's workspace blueprints")
	}
	for _, entry := range build {
		if entry.Template.ProgramHomeOwner != nil || entry.Template.ID == carrier {
			t.Fatalf("the build catalog offers the Home carrier %q", entry.Template.ID)
		}
		if !snapshot.Active[entry.Template.ID] {
			t.Fatalf("the build catalog offers the inactive %q", entry.Template.ID)
		}
	}

	groups := groupTemplateCatalogEntries(snapshot)
	found := false
	for _, entry := range groups {
		found = found || (entry.Template.ID == carrier && entry.Active && entry.Template.ProgramHomeOwner != nil)
	}
	if !found || len(groups) != len(snapshot.Entries) {
		t.Fatalf("the Group creator's catalog keeps every entry, the carrier included: found=%v %d/%d", found, len(groups), len(snapshot.Entries))
	}
}
