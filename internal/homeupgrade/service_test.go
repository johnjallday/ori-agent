package homeupgrade

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	testPlugin   = "music-project-management"
	testProgram  = "music-producer-assistant"
	oldPortfolio = "Coordinate only reviewed Home work."
	newPortfolio = "Coordinate reviewed Home work and the approved project library."
	oldSample    = "Use only reviewed sample-library operations."
	newSample    = "Use only reviewed sample-library operations and approved roots."
	editedSample = "My own sample librarian instructions."
)

func homeSurfaces(t *testing.T, version, portfolioPrompt, samplePrompt, portfolioLabel string) *plugin.SurfaceContribution {
	t.Helper()
	contribution, err := plugin.ParseSurfaceContribution([]byte(fmt.Sprintf(`{
  "schema_version": 1, "name": %q, "version": %q, "protocol": {"min": 1, "max": 1},
  "requires_host_features": ["independent_program_homes_v1"], "capabilities": [], "services": [], "blueprints": [],
  "assistant_program_homes": [{
    "schema_version": 1, "version": 1, "id": %q, "station_name": "Music Production Home",
    "default_primary_name": "Portfolio Manager", "hire_title": "Staff your music production Home",
    "roles": [
      {"id":"portfolio_manager","label":%q,"required":true,"primary":true,"role":"orchestrator","system_prompt":%q,"skills":["music-project-management"]},
      {"id":"sample_library_manager","label":"Sample Library Manager","required":false,"capability_id":"sample-library","role":"specialist","system_prompt":%q}
    ],
    "stages": [{"id":"foundation","label":"Foundation","accepted_completion_threshold":0}],
    "reflection": {"minimum_projects":3,"cadence_hours":168,"max_projects":16,"max_events_per_project":16,"max_candidates":8,"max_evidence":8,"rubric":"Propose bounded improvements."},
    "allowed_project_attachments": [{"provider_plugin_id":"reaper-plugin","blueprint_id":"reaper-song","project_team_id":"reaper-song-team","project_team_schema_version":1,"min_project_team_version":1,"max_project_team_version":1}]
  }]
}`, testPlugin, version, testProgram, portfolioLabel, portfolioPrompt, samplePrompt)))
	if err != nil {
		t.Fatal(err)
	}
	return contribution
}

// fakePlugins stands in for plugin.Manager. Replace runs the host guard the
// way Manager does: any replacement of the package the Homes are pinned to is
// refused unless the service allows exactly it.
type fakePlugins struct {
	mu         sync.Mutex
	installed  plugin.InstalledPlugin
	next       plugin.InstalledPlugin
	target     Target
	targetErr  error
	replaceErr error
	guard      func(plugin.InstalledPlugin, string, string) bool
	replaced   int
}

func (f *fakePlugins) List() ([]plugin.InstalledPlugin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []plugin.InstalledPlugin{f.installed}, nil
}

func (f *fakePlugins) Target(context.Context, plugin.InstalledPlugin) (Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.targetErr != nil {
		return Target{}, f.targetErr
	}
	if f.installed.Version == f.target.Inspection.Descriptor.Version {
		return Target{}, ErrCurrent
	}
	return f.target, nil
}

func (f *fakePlugins) Replace(_ context.Context, name string, target Target) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.replaceErr != nil {
		return f.replaceErr
	}
	if f.guard != nil && !f.guard(f.installed, target.Inspection.Descriptor.Version, target.Inspection.Fingerprint) {
		return errors.New("plugin replacement would strand existing Homes")
	}
	f.installed = f.next
	f.replaced++
	return nil
}

func (f *fakePlugins) set(installed plugin.InstalledPlugin) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installed = installed
}

type fakeProfiles struct {
	mu     sync.Mutex
	agents map[string]*agent.Agent
}

func (f *fakeProfiles) GetAgent(name string) (*agent.Agent, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	found, ok := f.agents[name]
	if !ok {
		return nil, false
	}
	copied := *found
	return &copied, true
}

func (f *fakeProfiles) UpdateAgent(name string, update func(*agent.Agent) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	found, ok := f.agents[name]
	if !ok {
		return fmt.Errorf("agent %q not found", name)
	}
	return update(found)
}

type harness struct {
	service  *Service
	store    *workspace.SyncStore
	db       *database.DB
	plugins  *fakePlugins
	profiles *fakeProfiles
	from     workspace.AssistantProgramHomeOwner
	homeID   string
	childID  string
	clock    time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	folders, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	store := workspace.NewSyncStore(workspace.NewInMemoryStore(), folders)

	oldSurfaces := homeSurfaces(t, "0.1.0", oldPortfolio, oldSample, "Music Portfolio Manager")
	nextSurfaces := homeSurfaces(t, "0.1.1", newPortfolio, newSample, "Music Portfolio Manager")
	installed := plugin.InstalledPlugin{Name: testPlugin, Version: "0.1.0", Enabled: true, ContentGeneration: 1,
		ComponentFingerprint: strings.Repeat("a", 64), WorkspaceSurfaces: oldSurfaces, Skills: []string{"music-project-management"}}
	next := installed
	next.Version, next.ContentGeneration, next.ComponentFingerprint, next.WorkspaceSurfaces = "0.1.1", 2, strings.Repeat("b", 64), nextSurfaces
	plugins := &fakePlugins{installed: installed, next: next, target: Target{Inspection: plugin.ReplacementTarget{
		Descriptor:  plugin.PluginDescriptor{Name: testPlugin, Version: "0.1.1", WorkspaceSurfaces: nextSurfaces, Skills: []plugin.SkillSpec{{Name: "music-project-management"}}},
		Fingerprint: next.ComponentFingerprint,
	}}}
	profiles := &fakeProfiles{agents: map[string]*agent.Agent{}}

	home := oldSurfaces.AssistantProgramHomes[0]
	from := workspace.AssistantProgramHomeOwner{PluginID: testPlugin, PluginVersion: "0.1.0", ProgramID: testProgram,
		HomeSchemaVersion: home.SchemaVersion, HomeVersion: home.Version, DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(home),
		PluginGeneration: 1, ComponentFingerprint: installed.ComponentFingerprint}
	h := &harness{store: store, db: db, plugins: plugins, profiles: profiles, from: from, clock: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	h.service = New(db, store, plugins, profiles)
	h.service.now = func() time.Time { return h.clock }
	plugins.guard = h.service.AllowsReplacement
	h.homeID = h.addHome(t, "local", from)
	h.childID = h.addChild(t, h.homeID, "song-one", from)
	return h
}

func (h *harness) addHome(t *testing.T, owner string, pin workspace.AssistantProgramHomeOwner) string {
	t.Helper()
	declaration := homeSurfaces(t, "0.1.0", oldPortfolio, oldSample, "Music Portfolio Manager").AssistantProgramHomes[0].AssistantProgram()
	key := workspace.AssistantProgramKey{OwnerUserID: owner, PluginID: testPlugin, ProgramID: testProgram}
	home, _, err := workspace.NewAssistantProgramStore(h.store).EnsureNamedIndependentStation(key, declaration, "Music Production Home", pin)
	if err != nil {
		t.Fatal(err)
	}
	portfolio, sample := "Portfolio Manager "+owner, "Sample Librarian "+owner
	if err := h.store.Update(home.ID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		state.HomeBindings = workspace.AssistantRoleBindingSet{StateRevision: 1, Bindings: []workspace.AssistantRoleBinding{
			{RoleID: "portfolio_manager", AgentInstanceID: "portfolio-" + owner, AgentName: portfolio},
			{RoleID: "sample_library_manager", AgentInstanceID: "sample-" + owner, AgentName: sample},
		}}
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for name, prompt := range map[string]string{portfolio: oldPortfolio, sample: editedSample} {
		profile := &agent.Agent{Settings: agentSettings(prompt)}
		h.profiles.agents[name] = profile
		copied := *profile
		if err := h.store.SaveWorkspaceAgent(home.ID, name, &copied); err != nil {
			t.Fatal(err)
		}
	}
	return home.ID
}

func (h *harness) addChild(t *testing.T, homeID, childID string, pin workspace.AssistantProgramHomeOwner) string {
	t.Helper()
	declaration := homeSurfaces(t, "0.1.0", oldPortfolio, oldSample, "Music Portfolio Manager").AssistantProgramHomes[0].AssistantProgram()
	projectOwner := workspace.AssistantProjectProviderOwner{PluginID: "reaper-plugin", PluginVersion: "0.9.0", BlueprintID: "reaper-song", BlueprintVersion: 1,
		ProjectTeamID: "reaper-song-team", ProjectTeamSchema: 1, ProjectTeamVersion: 1, ProjectTeamDigest: strings.Repeat("c", 64),
		PluginGeneration: 3, ComponentFingerprint: strings.Repeat("d", 64)}
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: testPlugin, ProgramID: testProgram}
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song One"})
	child.ID, child.OwnerUserID, child.ParentID = childID, "local", homeID
	child.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "plugin:reaper-plugin:reaper-song", AssistantProgram: declaration,
		GroupRequirement: &workspace.GroupRequirementSnapshot{
			SchemaVersion: workspace.GroupRequirementSnapshotSchemaVersion, Policy: "required", SelectedComposition: workspace.GroupRequirementCompositionGrouped,
			TemplateID: "plugin:reaper-plugin:reaper-song", DefinitionDigest: strings.Repeat("e", 64), ReviewDigest: strings.Repeat("f", 64),
			OperationDigest: strings.Repeat("1", 64), AppliedAt: h.clock, ProgramKey: &key,
			HomeWorkspaceID: homeID, ProjectLinkID: workspace.AssistantProjectLinkID(homeID, childID),
			HomeProvider: &pin, ProjectProvider: &projectOwner,
		},
	})
	if err := h.store.Save(child); err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.NewAssistantProgramStore(h.store).EnsureProjectStation(childID); err != nil {
		t.Fatal(err)
	}
	return childID
}

func agentSettings(prompt string) types.Settings {
	return types.Settings{Model: "test-model", SystemPrompt: prompt}
}

func (h *harness) to() workspace.AssistantProgramHomeOwner {
	to := h.from
	to.PluginVersion, to.PluginGeneration, to.ComponentFingerprint = "0.1.1", 2, strings.Repeat("b", 64)
	to.DeclarationDigest = projecttemplates.AssistantProgramHomeDigest(h.plugins.next.WorkspaceSurfaces.AssistantProgramHomes[0])
	return to
}

func (h *harness) homeState(t *testing.T) *workspace.AssistantProgramState {
	t.Helper()
	home, err := h.store.Get(h.homeID)
	if err != nil {
		t.Fatal(err)
	}
	return home.GetAssistantProgramState()
}

func (h *harness) childPins(t *testing.T) (link, snapshot workspace.AssistantProgramHomeOwner) {
	t.Helper()
	child, err := h.store.Get(h.childID)
	if err != nil {
		t.Fatal(err)
	}
	return *child.GetAssistantProjectLink().HomeProvider, *child.GetTemplateProvenance().GroupRequirement.HomeProvider
}

func (h *harness) prompts(t *testing.T, name string) (profile, homeCopy string) {
	t.Helper()
	stored, _ := h.profiles.GetAgent(name)
	copied, _, err := h.store.GetWorkspaceAgent(h.homeID, name)
	if err != nil {
		t.Fatal(err)
	}
	return stored.Settings.SystemPrompt, copied.Settings.SystemPrompt
}

func TestReviewAndCommitMoveHomeChildAndUneditedAgents(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if h.plugins.guard(h.plugins.installed, "0.1.1", h.plugins.target.Inspection.Fingerprint) {
		t.Fatal("the guard allowed a replacement before any upgrade was claimed")
	}
	review, err := h.service.Review(ctx, "local", testPlugin)
	if err != nil {
		t.Fatal(err)
	}
	plan := review.Plan
	if plan.FromVersion != "0.1.0" || plan.ToVersion != "0.1.1" || len(plan.Homes) != 1 || len(plan.Homes[0].Projects) != 1 ||
		plan.Homes[0].Projects[0].ID != h.childID || len(plan.Programs) != 1 || len(plan.Programs[0].RolePrompts) != 2 {
		t.Fatalf("plan = %#v", plan)
	}
	agents := plan.Homes[0].Agents
	if len(agents) != 2 || agents[0].RoleID != "portfolio_manager" || agents[0].Profile != AgentReplace || agents[0].HomeCopy != AgentReplace ||
		agents[1].RoleID != "sample_library_manager" || agents[1].Profile != AgentKeep || agents[1].HomeCopy != AgentKeep {
		t.Fatalf("agent plan = %#v", agents)
	}
	if h.plugins.replaced != 0 || h.homeState(t).HomeProvider.PluginVersion != "0.1.0" {
		t.Fatal("review changed something")
	}

	operation, err := h.service.Commit(ctx, "local", testPlugin, review.Token)
	if err != nil || operation.Status != StatusSucceeded || operation.TargetGeneration != 2 {
		t.Fatalf("commit = %#v, %v", operation, err)
	}
	state := h.homeState(t)
	if *state.HomeProvider != h.to() || state.Declaration.Roles[0].SystemPrompt != newPortfolio || len(state.ProviderUpgrades) != 1 ||
		state.ProviderUpgrades[0].OperationID != operation.ID {
		t.Fatalf("Home after commit = %#v", state)
	}
	if h.plugins.installed.Version != "0.1.1" || h.plugins.replaced != 1 {
		t.Fatal("the package was not replaced exactly once")
	}
	if link, snapshot := h.childPins(t); link != h.to() || snapshot != h.to() {
		t.Fatalf("child pins = %#v / %#v", link, snapshot)
	}
	child, _ := h.store.Get(h.childID)
	if status := workspace.EvaluateGroupRequirementLifecycle(child, h.store.Get); status == nil || status.State != workspace.GroupRequirementStatusReadyGrouped {
		t.Fatalf("lifecycle after upgrade = %#v", status)
	}
	if profile, homeCopy := h.prompts(t, "Portfolio Manager local"); profile != newPortfolio || homeCopy != newPortfolio {
		t.Fatalf("unedited agent prompts = %q / %q", profile, homeCopy)
	}
	if profile, homeCopy := h.prompts(t, "Sample Librarian local"); profile != editedSample || homeCopy != editedSample {
		t.Fatalf("edited agent prompts were overwritten: %q / %q", profile, homeCopy)
	}
	if outcome := operation.Outcome.Agents; len(outcome) != 2 || outcome[0].Profile != AgentReplace || outcome[1].Profile != AgentKeep {
		t.Fatalf("agent outcome = %#v", outcome)
	}
	if h.service.AllowsReplacement(h.plugins.installed, "0.1.2", strings.Repeat("c", 64)) {
		t.Fatal("the guard still allows replacements after the operation finished")
	}
	if _, err := h.service.Commit(ctx, "local", testPlugin, review.Token); !errors.Is(err, ErrCurrent) {
		t.Fatalf("a consumed review committed again: %v", err)
	}
	status, err := h.service.Status(ctx, "local", testPlugin)
	if err != nil || status == nil || status.ID != operation.ID || status.Status != StatusSucceeded {
		t.Fatalf("status = %#v, %v", status, err)
	}
}

func TestCommitRefusesAReviewThatNoLongerMatches(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	review, err := h.service.Review(ctx, "local", testPlugin)
	if err != nil {
		t.Fatal(err)
	}
	// The owner edits the staffed agent after reviewing: the plan now keeps it.
	h.profiles.agents["Portfolio Manager local"].Settings.SystemPrompt = "Edited after review."
	if _, err := h.service.Commit(ctx, "local", testPlugin, review.Token); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("drifted plan committed: %v", err)
	}
	h.profiles.agents["Portfolio Manager local"].Settings.SystemPrompt = oldPortfolio
	h.clock = h.clock.Add(reviewTTL + time.Second)
	if _, err := h.service.Commit(ctx, "local", testPlugin, review.Token); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("expired review committed: %v", err)
	}
	if _, err := h.service.Commit(ctx, "someone-else", testPlugin, review.Token); err == nil {
		t.Fatal("another owner committed the review")
	}
	if h.plugins.replaced != 0 || h.homeState(t).HomeProvider.PluginVersion != "0.1.0" {
		t.Fatal("a refused commit changed something")
	}
	fresh, err := h.service.Review(ctx, "local", testPlugin)
	if err != nil {
		t.Fatal(err)
	}
	if operation, err := h.service.Commit(ctx, "local", testPlugin, fresh.Token); err != nil || operation.Status != StatusSucceeded {
		t.Fatalf("fresh review = %#v, %v", operation, err)
	}
}

func TestReviewRefusesWhatItCannotMoveSafely(t *testing.T) {
	ctx := context.Background()
	t.Run("another owner's Home", func(t *testing.T) {
		h := newHarness(t)
		h.addHome(t, "other-owner", h.from)
		if _, err := h.service.Review(ctx, "local", testPlugin); !errors.Is(err, ErrOtherOwner) {
			t.Fatalf("review = %v", err)
		}
	})
	t.Run("child pinned to another release", func(t *testing.T) {
		h := newHarness(t)
		foreign := h.from
		foreign.PluginVersion, foreign.ComponentFingerprint = "0.0.9", strings.Repeat("9", 64)
		if err := h.store.Update(h.childID, func(current *workspace.Workspace) error {
			link := current.GetAssistantProjectLink()
			link.HomeProvider = &foreign
			current.SetAssistantProjectLink(link)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := h.service.Review(ctx, "local", testPlugin); !errors.Is(err, ErrPinMismatch) {
			t.Fatalf("review = %v", err)
		}
	})
	t.Run("project role repair in progress", func(t *testing.T) {
		h := newHarness(t)
		for _, statement := range []string{
			`INSERT INTO project_role_repair_review (token, owner_user_id, home_id, project_id, idempotency_key, evidence_digest, created_at, expires_at)
				VALUES ('repair-review', 'local', '` + h.homeID + `', '` + h.childID + `', 'key', '` + strings.Repeat("a", 64) + `', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			`INSERT INTO project_role_repair_operation (token, owner_user_id, home_id, project_id, idempotency_key, review_token, evidence_digest, status, created_at, updated_at)
				VALUES ('repair', 'local', '` + h.homeID + `', '` + h.childID + `', 'key', 'repair-review', '` + strings.Repeat("a", 64) + `', 'claimed', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		} {
			if _, err := h.db.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := h.service.Review(ctx, "local", testPlugin); !errors.Is(err, ErrRepairActive) {
			t.Fatalf("review = %v", err)
		}
	})
	t.Run("release changes more than guidance", func(t *testing.T) {
		h := newHarness(t)
		h.plugins.target.Inspection.Descriptor.WorkspaceSurfaces = homeSurfaces(t, "0.1.1", newPortfolio, newSample, "Renamed Manager")
		if _, err := h.service.Review(ctx, "local", testPlugin); !errors.Is(err, ErrNotGuidanceOnly) {
			t.Fatalf("review = %v", err)
		}
	})
	t.Run("nothing newer", func(t *testing.T) {
		h := newHarness(t)
		h.plugins.targetErr = ErrCurrent
		if _, err := h.service.Review(ctx, "local", testPlugin); !errors.Is(err, ErrCurrent) {
			t.Fatalf("review = %v", err)
		}
	})
	t.Run("an older release", func(t *testing.T) {
		h := newHarness(t)
		h.plugins.target.Inspection.Descriptor.Version = "0.0.9"
		if _, err := h.service.Review(ctx, "local", testPlugin); !errors.Is(err, ErrCurrent) {
			t.Fatalf("review = %v", err)
		}
	})
	t.Run("no Home uses the package", func(t *testing.T) {
		h := newHarness(t)
		if _, err := h.service.Review(ctx, "local", "another-plugin"); !errors.Is(err, ErrNoHomes) {
			t.Fatalf("review = %v", err)
		}
	})
}

// A crash at every point between claim and success is settled by recovery:
// before the replacement nothing changed and the operation is cancelled; after
// it the idempotent rebind finishes the job.
func TestRecoverySettlesACrashAtEveryStep(t *testing.T) {
	ctx := context.Background()
	claimed := func(t *testing.T, h *harness) Operation {
		t.Helper()
		review, err := h.service.Review(ctx, "local", testPlugin)
		if err != nil {
			t.Fatal(err)
		}
		operation, err := h.service.claim(ctx, "crashed-op", "local", review.Token, review.Plan)
		if err != nil {
			t.Fatal(err)
		}
		return operation
	}
	finished := func(t *testing.T, h *harness) {
		t.Helper()
		if err := h.service.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		status, err := h.service.Status(ctx, "local", testPlugin)
		if err != nil || status == nil || status.Status != StatusSucceeded {
			t.Fatalf("recovered operation = %#v, %v", status, err)
		}
		state := h.homeState(t)
		if *state.HomeProvider != h.to() || len(state.ProviderUpgrades) != 1 {
			t.Fatalf("recovered Home = %#v", state)
		}
		if link, snapshot := h.childPins(t); link != h.to() || snapshot != h.to() {
			t.Fatalf("recovered child pins = %#v / %#v", link, snapshot)
		}
		if profile, homeCopy := h.prompts(t, "Portfolio Manager local"); profile != newPortfolio || homeCopy != newPortfolio {
			t.Fatalf("recovered agent prompts = %q / %q", profile, homeCopy)
		}
	}

	t.Run("after claim, before replacement", func(t *testing.T) {
		h := newHarness(t)
		claimed(t, h)
		if !h.service.AllowsReplacement(h.plugins.installed, "0.1.1", h.plugins.target.Inspection.Fingerprint) {
			t.Fatal("a claimed operation does not allow its own replacement")
		}
		if h.service.AllowsReplacement(h.plugins.installed, "0.1.1", strings.Repeat("c", 64)) {
			t.Fatal("a claimed operation allowed a different replacement")
		}
		if err := h.service.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		status, _ := h.service.Status(ctx, "local", testPlugin)
		if status == nil || status.Status != StatusCancelled || h.plugins.replaced != 0 || h.homeState(t).HomeProvider.PluginVersion != "0.1.0" {
			t.Fatalf("interrupted claim = %#v", status)
		}
		if h.service.AllowsReplacement(h.plugins.installed, "0.1.1", h.plugins.target.Inspection.Fingerprint) {
			t.Fatal("a cancelled operation still allows the replacement")
		}
		if _, err := h.service.Review(ctx, "local", testPlugin); err != nil {
			t.Fatalf("review after cancellation = %v", err)
		}
	})
	t.Run("after replacement, before it was recorded", func(t *testing.T) {
		h := newHarness(t)
		claimed(t, h)
		h.plugins.set(h.plugins.next)
		finished(t, h)
	})
	t.Run("after replacement was recorded, before rebind", func(t *testing.T) {
		h := newHarness(t)
		operation := claimed(t, h)
		if err := h.service.replace(ctx, &operation, &h.plugins.target); err != nil || operation.Status != StatusReplaced {
			t.Fatalf("replace = %#v, %v", operation, err)
		}
		finished(t, h)
	})
	t.Run("after the Home moved, before its child", func(t *testing.T) {
		h := newHarness(t)
		operation := claimed(t, h)
		if err := h.service.replace(ctx, &operation, &h.plugins.target); err != nil {
			t.Fatal(err)
		}
		upgrade, _ := operation.Plan.upgrade(testProgram, operation.ID, operation.TargetGeneration, h.clock)
		if err := h.service.update(h.homeID, upgrade.RebindHome); err != nil {
			t.Fatal(err)
		}
		finished(t, h)
	})
	t.Run("installed package is neither release", func(t *testing.T) {
		h := newHarness(t)
		claimed(t, h)
		other := h.plugins.next
		other.Version, other.ComponentFingerprint = "0.2.0", strings.Repeat("c", 64)
		h.plugins.set(other)
		if err := h.service.Recover(ctx); !errors.Is(err, ErrReconcileRequired) {
			t.Fatalf("recover = %v", err)
		}
		status, _ := h.service.Status(ctx, "local", testPlugin)
		if status == nil || status.Status != StatusReconcileRequired || status.Outcome.Reason == "" {
			t.Fatalf("unaccountable package = %#v", status)
		}
		if _, err := h.service.Review(ctx, "local", testPlugin); !errors.Is(err, ErrUpgradeActive) {
			t.Fatalf("review during reconciliation = %v", err)
		}
		if h.service.AllowsReplacement(other, "0.1.1", h.plugins.target.Inspection.Fingerprint) {
			t.Fatal("reconcile_required allowed a replacement")
		}
	})
}

func TestFailedReplacementCancelsWithoutChangingAnything(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	review, err := h.service.Review(ctx, "local", testPlugin)
	if err != nil {
		t.Fatal(err)
	}
	h.plugins.replaceErr = errors.New("source changed during update")
	if _, err := h.service.Commit(ctx, "local", testPlugin, review.Token); err == nil {
		t.Fatal("a failed replacement reported success")
	}
	status, _ := h.service.Status(ctx, "local", testPlugin)
	if status == nil || status.Status != StatusCancelled || h.homeState(t).HomeProvider.PluginVersion != "0.1.0" {
		t.Fatalf("failed replacement = %#v", status)
	}
	h.plugins.replaceErr = nil
	again, err := h.service.Review(ctx, "local", testPlugin)
	if err != nil {
		t.Fatal(err)
	}
	if operation, err := h.service.Commit(ctx, "local", testPlugin, again.Token); err != nil || operation.Status != StatusSucceeded {
		t.Fatalf("retry = %#v, %v", operation, err)
	}
}

func TestCodesAreStable(t *testing.T) {
	if Code(errors.Join(ErrUnavailable, ErrPinMismatch)) != "home_upgrade_pin_mismatch" || Code(errors.New("other")) != "home_upgrade_unavailable" {
		t.Fatal("unexpected error codes")
	}
}
