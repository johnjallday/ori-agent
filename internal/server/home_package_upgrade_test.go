package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/homeupgrade"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// writeHomePackage writes a local Home-only package export: one Home whose
// Portfolio Manager prompt is the only thing that changes between releases.
func writeHomePackage(t *testing.T, root, version, portfolioPrompt string) {
	t.Helper()
	files := map[string]string{
		filepath.Join(".claude-plugin", "plugin.json"): fmt.Sprintf(`{"name":"music-project-management","version":%q}`, version),
		filepath.Join(".ori-plugin", "plugin.json"): fmt.Sprintf(`{
  "schema_version": 1, "name": "music-project-management", "version": %q, "protocol": {"min": 1, "max": 1},
  "requires_host_features": ["independent_program_homes_v1"], "capabilities": [], "services": [], "blueprints": [],
  "assistant_program_homes": [{
    "schema_version": 1, "version": 1, "id": "music-producer-assistant", "station_name": "Music Production Home",
    "default_primary_name": "Portfolio Manager", "hire_title": "Staff your music production Home",
    "roles": [{"id":"portfolio_manager","label":"Music Portfolio Manager","required":true,"primary":true,"role":"orchestrator","system_prompt":%q,"skills":["music-project-management"]}],
    "stages": [{"id":"foundation","label":"Foundation","accepted_completion_threshold":0}],
    "reflection": {"minimum_projects":3,"cadence_hours":168,"max_projects":16,"max_events_per_project":16,"max_candidates":8,"max_evidence":8,"rubric":"Propose bounded improvements."}
  }]
}`, version, portfolioPrompt),
		filepath.Join("skills", "music-project-management", "SKILL.md"): "---\nname: music-project-management\ndescription: Portfolio guidance " + version + ".\n---\nGuidance for " + version + ".\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

type upgradeTestProfiles struct {
	mu     sync.Mutex
	agents map[string]*agent.Agent
}

func (p *upgradeTestProfiles) GetAgent(name string) (*agent.Agent, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	found, ok := p.agents[name]
	return found, ok
}

func (p *upgradeTestProfiles) UpdateAgent(name string, update func(*agent.Agent) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	found, ok := p.agents[name]
	if !ok {
		return fmt.Errorf("agent %q not found", name)
	}
	return update(found)
}

// The Plugins page refuses to strand a Home on a changed local export; the
// reviewed upgrade performs exactly that replacement through the real manager
// and guard, and the moved Home matches the newly installed evidence.
func TestHomePackageUpgradeThroughTheRealManagerAndGuard(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeHomePackage(t, root, "0.1.0", "Coordinate only reviewed Home work.")
	pluginsDir := t.TempDir()
	manager := plugin.NewManager(nil, pluginsDir, filepath.Join(pluginsDir, "src"))
	folders, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	store := workspace.NewSyncStore(workspace.NewInMemoryStore(), folders)
	slot := &homeUpgradeSlot{}
	manager.SetReplacementGuard(func(current plugin.InstalledPlugin, nextVersion, nextFingerprint string) error {
		return refuseUnreviewedHomeReplacement(store, current, nextVersion, nextFingerprint, slot.allows)
	})
	confirm := func(plugin.TrustReport) bool { return true }
	installed, err := manager.Install(root, plugin.FormatClaude, confirm)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetEnabled(installed.Name, true); err != nil {
		t.Fatal(err)
	}
	records, _ := manager.List()
	installed = records[0]
	declaration := installed.WorkspaceSurfaces.AssistantProgramHomes[0]
	owner := workspace.AssistantProgramHomeOwner{
		PluginID: installed.Name, PluginVersion: installed.Version, ProgramID: declaration.ID,
		HomeSchemaVersion: declaration.SchemaVersion, HomeVersion: declaration.Version,
		DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(declaration),
		PluginGeneration:  installed.EvidenceGeneration(), ComponentFingerprint: installed.ComponentFingerprint,
	}
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: installed.Name, ProgramID: declaration.ID}
	home, _, err := workspace.NewAssistantProgramStore(store).EnsureNamedIndependentStation(key, declaration.AssistantProgram(), "Music Production Home", owner)
	if err != nil {
		t.Fatal(err)
	}

	writeHomePackage(t, root, "0.1.1", "Coordinate reviewed Home work and the approved project library.")
	if _, err := manager.Update(installed.Name, confirm); err == nil || !strings.Contains(err.Error(), "reviewed Home/child upgrade") {
		t.Fatalf("the Plugins page update stranded the Home: %v", err)
	}

	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := homeupgrade.New(db, store, homeUpgradePlugins{manager: manager}, &upgradeTestProfiles{agents: map[string]*agent.Agent{}})
	slot.service.Store(service)
	review, err := service.Review(ctx, "local", installed.Name)
	if err != nil || review.Plan.ToVersion != "0.1.1" || len(review.Plan.Programs[0].RolePrompts) != 1 {
		t.Fatalf("review = %#v, %v", review.Plan, err)
	}
	if _, err := manager.Update(installed.Name, confirm); err == nil {
		t.Fatal("a reviewed but unclaimed upgrade let the Plugins page replace the package")
	}
	operation, err := service.Commit(ctx, "local", installed.Name, review.Token)
	if err != nil || operation.Status != homeupgrade.StatusSucceeded {
		t.Fatalf("commit = %#v, %v", operation, err)
	}
	records, _ = manager.List()
	moved, _ := store.Get(home.ID)
	state := moved.GetAssistantProgramState()
	if records[0].Version != "0.1.1" || !plugin.IndependentHomeProviderEvidenceAvailable(records, state.HomeProvider) ||
		state.Declaration.Roles[0].SystemPrompt != "Coordinate reviewed Home work and the approved project library." {
		t.Fatalf("after upgrade: installed %s, Home %#v", records[0].Version, state)
	}

	writeHomePackage(t, root, "0.1.2", "A later change.")
	if _, err := manager.Update(installed.Name, confirm); err == nil {
		t.Fatal("a finished upgrade left later replacements unguarded")
	}
}
