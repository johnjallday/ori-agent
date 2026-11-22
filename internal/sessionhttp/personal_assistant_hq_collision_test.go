package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type hqCollisionFixture struct {
	*continuityInstallation
	states *personalassistant.SQLiteStore
	hq     *personalhq.Service
	briefs *dailybrief.Service
	hired  *personalassistant.State
}

func newHQCollisionFixture(t *testing.T) *hqCollisionFixture {
	t.Helper()
	install := newContinuityInstallation(t, "hq-recovery")
	library := t.TempDir()
	if err := projecttemplates.EnsureLibrary(library); err != nil {
		t.Fatal(err)
	}
	agents, err := agentstore.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	install.handler.SetTemplatesRootResolver(func() string { return library })
	install.handler.SetAgentStore(agents)
	install.handler.SetWorkspaceTaskStore(install.sync)
	fixture := &hqCollisionFixture{
		continuityInstallation: install,
		states:                 personalassistant.NewSQLiteStore(install.db),
		hq:                     personalhq.NewService(userprofile.NewSQLiteStore(install.db), install.store),
		briefs:                 dailybrief.NewService(dailybrief.NewSQLiteStore(install.db), nil),
	}
	hire := personalassistant.NewHireCoordinator(fixture.states, install.handler, nil, nil, nil)
	result, err := hire.Hire(t.Context(), "local", personalassistant.HireRequest{
		RequestID: "hire-once", DisplayName: "Atlas", Mandate: "Help me plan the week.",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.hired = result.State
	fixture.retainFolder(t, "my-hq")
	fixture.retainFolder(t, "my-hq-2")
	return fixture
}

func (f *hqCollisionFixture) coordinator() *personalassistant.HQSetupCoordinator {
	return f.coordinatorWithStore(f.states)
}

func (f *hqCollisionFixture) coordinatorWithStore(s personalassistant.Store) *personalassistant.HQSetupCoordinator {
	return personalassistant.NewHQSetupCoordinator(s, f.handler, f.hq, f.briefs,
		personalassistant.NewAgentStoreProfileReader(f.handler.agentStore))
}

func (f *hqCollisionFixture) buildRequest() personalassistant.HQSetupRequest {
	enabled := false
	return personalassistant.HQSetupRequest{
		RequestID: "build-my-hq", IfVersion: f.hired.StateVersion, HQName: "My HQ",
		ScheduleEnabled: &enabled, ScheduleTime: "07:45", ScheduleDays: []string{"tue", "thu"},
		Notes: &personalassistant.HQNotesChoice{Include: false},
	}
}

func (f *hqCollisionFixture) retainFolder(t *testing.T, slug string) {
	t.Helper()
	dir := filepath.Join(f.root, slug)
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte(slug+" retained contents"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *hqCollisionFixture) assertRetained(t *testing.T, slugs ...string) {
	t.Helper()
	for _, slug := range slugs {
		dir := filepath.Join(f.root, slug)
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 1 || entries[0].Name() != "keep.txt" {
			t.Fatalf("retained folder %s was changed: %v, %v", slug, entries, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "keep.txt"))
		if err != nil || string(data) != slug+" retained contents" {
			t.Fatalf("retained contents %s = %q, %v", slug, data, err)
		}
	}
}

func (f *hqCollisionFixture) assertPaused(t *testing.T) *personalassistant.State {
	t.Helper()
	state, err := f.states.GetState(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != personalassistant.StatusProvisioningHQ || state.RepairStep != personalassistant.RepairHQCreation ||
		state.AssistantID != f.hired.AssistantID || state.GlobalAgentProfileName != f.hired.GlobalAgentProfileName ||
		state.HQWorkspaceID != "" || state.HQEntryAgentInstanceID != "" ||
		state.LastHQRequestID != f.buildRequest().RequestID || state.HQPayloadJSON == "" {
		t.Fatalf("paused relationship = %+v", state)
	}
	return state
}

func TestPersonalAssistantHQDetachedFoldersBlockDurableSetup(t *testing.T) {
	f := newHQCollisionFixture(t)
	ctx := t.Context()
	profileBefore, ok := f.handler.agentStore.GetAgent(f.hired.GlobalAgentProfileName)
	if !ok {
		t.Fatal("hired profile is missing")
	}
	prompt := profileBefore.Settings.SystemPrompt
	provenance := append([]string(nil), profileBefore.Metadata.Tags...)
	profilesBefore := f.handler.agentStore.ListAgents()

	code, response := f.request(t, http.MethodPost, "/api/workspaces", map[string]any{"name": "My HQ"})
	conflict, _ := response["conflict"].(map[string]any)
	if code != http.StatusConflict || conflict["type"] != "folder_slug" ||
		conflict["requested_slug"] != "my-hq" || conflict["suggested_slug"] != "my-hq-3" {
		t.Fatalf("workspace create = %d %v", code, response)
	}
	_, err := f.coordinator().Setup(ctx, "local", f.buildRequest())
	var partial *personalassistant.PartialHQSetupError
	if !errors.As(err, &partial) || partial.Step != personalassistant.RepairHQCreation {
		t.Fatalf("first setup = %v", err)
	}
	before := f.assertPaused(t)

	// A new coordinator (as on restart) has no form data to repeat. The
	// conflict persists when it loads the confirmed request from SQLite.
	_, err = f.coordinator().Resume(ctx, "local")
	var folder *personalhq.FolderConflict
	if !errors.As(err, &partial) || partial.Step != personalassistant.RepairHQCreation ||
		!errors.As(err, &folder) || folder.RequestedSlug != "my-hq" || folder.SuggestedSlug != "my-hq-3" {
		t.Fatalf("resume lost the lower-level folder conflict: %v", err)
	}
	if after := f.assertPaused(t); before.HQPayloadJSON != after.HQPayloadJSON {
		t.Fatal("resume replaced the confirmed request")
	}
	rows, err := f.store.ListWorkspaces(ctx)
	if err != nil || len(rows) != 0 || len(f.files.CachedWorkspaces()) != 0 {
		t.Fatalf("conflict left a new workspace: %v, %v", rows, err)
	}
	if profiles := f.handler.agentStore.ListAgents(); !reflect.DeepEqual(profiles, profilesBefore) {
		t.Fatalf("conflict changed profiles: %v -> %v", profilesBefore, profiles)
	}
	profileAfter, ok := f.handler.agentStore.GetAgent(f.hired.GlobalAgentProfileName)
	if !ok || profileAfter.Settings.SystemPrompt != prompt || !reflect.DeepEqual(profileAfter.Metadata.Tags, provenance) {
		t.Fatal("the hired profile was changed")
	}
	f.assertRetained(t, "my-hq", "my-hq-2")
}

func folderChangeFromConflict(t *testing.T, err error, requested, suggested string) personalassistant.HQFolderChange {
	t.Helper()
	var folder *personalhq.FolderConflict
	var partial *personalassistant.PartialHQSetupError
	if !errors.As(err, &folder) || !errors.As(err, &partial) || folder.RequestedSlug != requested || folder.SuggestedSlug != suggested || folder.RootWitness == "" {
		t.Fatalf("want a review from %q to %q, got %v", requested, suggested, err)
	}
	return personalassistant.HQFolderChange{
		RequestID: partial.State.LastHQRequestID, IfVersion: partial.State.StateVersion,
		FolderSlug: suggested, Review: folder.RootWitness,
	}
}

func (f *hqCollisionFixture) assertReady(t *testing.T, result *personalassistant.HQSetupResult, slug string) {
	t.Helper()
	if result.State.Status != personalassistant.StatusActive || result.State.AssistantID != f.hired.AssistantID ||
		result.State.GlobalAgentProfileName != f.hired.GlobalAgentProfileName || result.State.HQEntryAgentInstanceID == "" ||
		result.State.HQPayloadJSON != "" {
		t.Fatalf("finished relationship = %+v", result.State)
	}
	rows, err := f.store.ListWorkspaces(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != result.State.HQWorkspaceID || rows[0].Name != "My HQ" || rows[0].FolderSlug != slug {
		t.Fatalf("want one canonical My HQ at %q: %+v, %v", slug, rows, err)
	}
	if folder, err := f.files.GetFolderPath(rows[0].ID); err != nil || filepath.Base(folder) != slug {
		t.Fatalf("folder = %q, %v", folder, err)
	}
	if len(rows[0].AgentInstances) != 1 {
		t.Fatalf("unexpected roster: %+v", rows[0].AgentInstances)
	}
	entry := rows[0].AgentInstances[0]
	if !entry.EntryPoint || entry.ID != result.State.HQEntryAgentInstanceID || entry.Name != f.hired.GlobalAgentProfileName {
		t.Fatalf("canonical entry = %+v", entry)
	}
	if status, err := f.hq.Status(t.Context(), "local"); err != nil || status == nil || !status.Valid || status.WorkspaceID != rows[0].ID {
		t.Fatalf("designation = %+v, %v", status, err)
	}
	if cfg := result.BriefConfig; cfg == nil || cfg.ScheduleEnabled || cfg.ScheduleTime != "07:45" || !slices.Equal(cfg.ScheduleDays, []string{"tue", "thu"}) {
		t.Fatalf("Daily Brief choices changed: %+v", cfg)
	}
}

func TestPersonalAssistantHQConfirmsOnlyTheCurrentSuggestedFolder(t *testing.T) {
	f := newHQCollisionFixture(t)
	ctx := t.Context()
	_, err := f.coordinator().Setup(ctx, "local", f.buildRequest())
	change := folderChangeFromConflict(t, err, "my-hq", "my-hq-3")
	for range 2 {
		state, observed, err := f.coordinator().ReviewFolder(ctx, "local")
		if err != nil || state == nil || state.StateVersion != change.IfVersion || observed == nil || observed.SuggestedSlug != change.FolderSlug {
			t.Fatalf("read-only review changed or lost the saved conflict: %+v, %+v, %v", state, observed, err)
		}
	}
	// A second user review competes; submit the prior one after a new Resume
	// and verify no stale consent can overwrite the current operation.
	_, err = f.coordinator().Resume(ctx, "local")
	current := folderChangeFromConflict(t, err, "my-hq", "my-hq-3")
	if _, err := f.coordinator().ChangeFolder(ctx, "local", change); !errors.Is(err, personalassistant.ErrConflict) {
		t.Fatalf("stale confirmation = %v", err)
	}
	competing := current
	competing.RequestID = "some-other-build"
	if _, err := f.coordinator().ChangeFolder(ctx, "local", competing); !errors.Is(err, personalassistant.ErrConflict) {
		t.Fatalf("competing confirmation = %v", err)
	}
	invalid := current
	invalid.FolderSlug = "../elsewhere"
	if _, err := f.coordinator().ChangeFolder(ctx, "local", invalid); !errors.Is(err, personalassistant.ErrValidation) {
		t.Fatalf("invalid slug = %v", err)
	}
	// The previously reviewed suggestion has become occupied. The request can
	// return a new suggestion but cannot accept it or create anything.
	f.retainFolder(t, "my-hq-3")
	_, err = f.coordinator().ChangeFolder(ctx, "local", current)
	alternate := folderChangeFromConflict(t, err, "my-hq", "my-hq-4")
	if paused := f.assertPaused(t); paused.StateVersion != current.IfVersion || strings.Contains(paused.HQPayloadJSON, "folder_change") {
		t.Fatal("a stale review modified the saved target")
	}
	result, err := f.coordinator().ChangeFolder(ctx, "local", alternate)
	if err != nil {
		t.Fatal(err)
	}
	f.assertReady(t, result, "my-hq-4")
	// Lost response or double-submit after restart is a read of the same
	// result. Another edit, even with the terminal version, is refused.
	replay, err := f.coordinator().ChangeFolder(ctx, "local", alternate)
	if err != nil || !replay.Resumed || replay.State.HQWorkspaceID != result.State.HQWorkspaceID {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	alternate.IfVersion = result.State.StateVersion
	if _, err := f.coordinator().ChangeFolder(ctx, "local", alternate); !errors.Is(err, personalassistant.ErrConflict) {
		t.Fatalf("edit after activation = %v", err)
	}
	f.assertRetained(t, "my-hq", "my-hq-2", "my-hq-3")
}

type hqStateWriteHook struct {
	personalassistant.Store
	update func(context.Context, *personalassistant.State, int64) (*personalassistant.State, error)
}

func (s hqStateWriteHook) UpdateState(ctx context.Context, next *personalassistant.State, version int64) (*personalassistant.State, error) {
	return s.update(ctx, next, version)
}

func TestPersonalAssistantHQAlternateSurvivesRestartAndAnotherCollision(t *testing.T) {
	f := newHQCollisionFixture(t)
	ctx := t.Context()
	_, err := f.coordinator().Setup(ctx, "local", f.buildRequest())
	change := folderChangeFromConflict(t, err, "my-hq", "my-hq-3")
	lost := errors.New("process stopped immediately after storing the folder confirmation")
	hook := hqStateWriteHook{Store: f.states, update: func(ctx context.Context, next *personalassistant.State, version int64) (*personalassistant.State, error) {
		saved, err := f.states.UpdateState(ctx, next, version)
		if err == nil && strings.Contains(saved.HQPayloadJSON, `"folder_slug":"my-hq-3"`) {
			// Race a new arrival with acceptance after the read-only check.
			f.retainFolder(t, "my-hq-3")
			return nil, lost
		}
		return saved, err
	}}
	if _, err := f.coordinatorWithStore(hook).ChangeFolder(ctx, "local", change); !errors.Is(err, lost) {
		t.Fatalf("failed to simulate an interrupted confirmation: %v", err)
	}
	// No browser inputs after restart. Resume attempts the accepted alternate,
	// not the default or any newly available suggestion.
	_, err = f.coordinator().Resume(ctx, "local")
	second := folderChangeFromConflict(t, err, "my-hq-3", "my-hq-3-2")
	f.assertPaused(t)
	if rows, err := f.store.ListWorkspaces(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("retry silently created another workspace: %+v, %v", rows, err)
	}
	result, err := f.coordinator().ChangeFolder(ctx, "local", second)
	if err != nil {
		t.Fatal(err)
	}
	f.assertReady(t, result, "my-hq-3-2")
	f.assertRetained(t, "my-hq", "my-hq-2", "my-hq-3")
}

func TestPersonalAssistantHQResumeUsesSavedNameAndRefusesToMoveAWorkspaceWithUnrecordedIDs(t *testing.T) {
	f := newHQCollisionFixture(t)
	ctx := t.Context()
	_, err := f.coordinator().Setup(ctx, "local", f.buildRequest())
	change := folderChangeFromConflict(t, err, "my-hq", "my-hq-3")
	if err := os.Rename(filepath.Join(f.root, "my-hq"), filepath.Join(t.TempDir(), "moved-aside")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.coordinator().ChangeFolder(ctx, "local", change); !errors.Is(err, personalassistant.ErrHQFolderAvailable) {
		t.Fatalf("a resolved collision must not change the folder: %v", err)
	}
	lost := errors.New("process stopped after workspace creation but before saving IDs")
	hook := hqStateWriteHook{Store: f.states, update: func(ctx context.Context, next *personalassistant.State, version int64) (*personalassistant.State, error) {
		if next.HQWorkspaceID != "" {
			return nil, lost
		}
		return f.states.UpdateState(ctx, next, version)
	}}
	if _, err := f.coordinatorWithStore(hook).Resume(ctx, "local"); !errors.Is(err, lost) {
		t.Fatalf("failed to simulate the lost IDs: %v", err)
	}
	rows, err := f.store.ListWorkspaces(ctx)
	if err != nil || len(rows) != 1 || rows[0].FolderSlug != "my-hq" {
		t.Fatalf("workspace was not created at the saved folder: %+v, %v", rows, err)
	}
	// The current version and an apparent collision are not enough: the
	// creator must rediscover the operation's workspace by provenance first.
	change.IfVersion = f.assertPaused(t).StateVersion
	if _, review, err := f.coordinator().ReviewFolder(ctx, "local"); err != nil || review != nil {
		t.Fatalf("read-only review offered a move after workspace creation: %+v, %v", review, err)
	}
	if _, err := f.coordinator().ChangeFolder(ctx, "local", change); !errors.Is(err, personalassistant.ErrConflict) {
		t.Fatalf("must refuse a new folder once a workspace exists: %v", err)
	}
	result, err := f.coordinator().Resume(ctx, "local")
	if err != nil || result.State.HQWorkspaceID != rows[0].ID {
		t.Fatalf("resume must reuse the original workspace: %+v, %v", result, err)
	}
	f.assertReady(t, result, "my-hq")
	f.assertRetained(t, "my-hq-2")
}
