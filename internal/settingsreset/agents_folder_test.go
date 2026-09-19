package settingsreset

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/types"
)

// confirmedAgentsFolder is the second confirmation a reviewed preview needs:
// its agents folder, repeated, or nothing when it has none.
func confirmedAgentsFolder(preview Preview) string {
	if preview.AgentsFolder == nil {
		return ""
	}
	return preview.AgentsFolder.Path
}

func fixtureAgentsFolder(t *testing.T, f *resetfixture.Fixture) string {
	t.Helper()
	resolved, err := resolvePath(filepath.Join(f.Paths().Workspaces, "Agents"))
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestAnAgentsResetPreviewNamesTheAgentsFolderAndItsNotice(t *testing.T) {
	f, _, planner := previewFixture(t)
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAgents})
	mustPreview(t, err)
	if len(preview.Blockers) != 0 {
		t.Fatalf("blockers: %+v", preview.Blockers)
	}
	folder := fixtureAgentsFolder(t, f)
	review := preview.AgentsFolder
	if review == nil || review.Path != folder || review.Notice != AgentsFolderNotice || !review.ConfirmationRequired {
		t.Fatalf("agents folder review = %+v, want %s with the sync notice", review, folder)
	}
	removed := slices.ContainsFunc(preview.Categories[0].Removed, func(l Location) bool { return l.DisplayPath == folder })
	if !removed {
		t.Errorf("the agents folder is not listed as removed: %+v", preview.Categories[0].Removed)
	}

	// A reset that leaves agents alone carries no agents folder at all.
	other, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategorySetupSteps})
	mustPreview(t, err)
	if other.AgentsFolder != nil {
		t.Errorf("a setup-only reset names the agents folder: %+v", other.AgentsFolder)
	}
}

func TestAnAgentsResetIsRefusedWithoutTheSecondConfirmation(t *testing.T) {
	f, _, c, _ := coordinatorFixture(t)
	v, req := coordinatorRequest(t, c, CategoryAgents)
	for _, confirm := range []string{"", filepath.Join(f.Paths().Root, "elsewhere", "Agents")} {
		req.ConfirmAgentsFolder = confirm
		if _, err := c.Stage(t.Context(), req); !errors.Is(err, ErrAgentsFolderUnconfirmed) {
			t.Fatalf("Stage with confirmation %q = %v, want ErrAgentsFolderUnconfirmed", confirm, err)
		}
	}
	if err := c.lease.RequireCleanStart(); err != nil {
		t.Fatalf("a refused reset left state behind: %v", err)
	}
	req.ConfirmAgentsFolder = confirmedAgentsFolder(v)
	if op, err := c.Stage(t.Context(), req); err != nil || op.State != StateAwaitingRestart {
		t.Fatalf("Stage with the reviewed folder = %+v, %v", op, err)
	}
}

func TestAnAgentsFolderOutsideTheRetainedRootBlocksThePreview(t *testing.T) {
	f, owners, planner := previewFixture(t)
	elsewhere := filepath.Join(f.Paths().Root, "elsewhere")
	mustPreview(t, os.MkdirAll(elsewhere, 0o750))
	system, err := store.NewSystemFileStore(filepath.Join(f.Paths().DataDir, "agents.json"), types.Settings{})
	mustPreview(t, err)
	composite, err := store.NewCompositeStore(system, elsewhere, filepath.Join(f.Paths().DataDir, "agent_state"), types.Settings{})
	mustPreview(t, err)
	owners.Agents = composite
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAgents})
	mustPreview(t, err)
	if !hasBlocker(preview, "agents_folder_unverified") || preview.AgentsFolder != nil {
		t.Fatalf("an agents folder outside the retained root was accepted: %+v", preview)
	}
}

func TestTheAgentsFolderBoundaryIsNarrow(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "Users", "me", "Ori Workspaces")
	protected := []string{root, filepath.Join(string(filepath.Separator), "Users", "me", "Vaults")}
	cases := map[string]bool{
		filepath.Join(root, "Agents"):                  true,
		filepath.Join(root, "agents-not"):              false,
		filepath.Join(root, "Studio", "Agents"):        false, // not directly inside
		root:                                           false,
		filepath.Join(string(filepath.Separator), "x"): false,
		filepath.Join(root, "Agents") + "/../Agents":   false, // not clean
	}
	for folder, want := range cases {
		if got := agentsFolderWithinRetainedRoot(folder, protected); got != want {
			t.Errorf("agentsFolderWithinRetainedRoot(%q) = %v, want %v", folder, got, want)
		}
	}
	// A retained path inside the folder would be removed with it.
	nested := append(slices.Clone(protected), filepath.Join(root, "Agents", "vault"))
	if agentsFolderWithinRetainedRoot(filepath.Join(root, "Agents"), nested) {
		t.Error("an agents folder holding a retained path was accepted")
	}
}

func TestRetainedDigestsSkipOnlyTheReviewedAgentsFolder(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "Agents")
	mustPreview(t, os.MkdirAll(filepath.Join(agents, "Scout"), 0o750))
	mustPreview(t, os.WriteFile(filepath.Join(root, "workspace.json"), []byte("a"), 0o600))
	before, err := digestProtectedPath(t.Context(), root, agents)
	mustPreview(t, err)
	mustPreview(t, os.RemoveAll(filepath.Join(agents, "Scout")))
	if after, _ := digestProtectedPath(t.Context(), root, agents); after != before {
		t.Error("removing an agent changed the retained digest")
	}
	if full, _ := digestProtectedPath(t.Context(), root, ""); full == before {
		t.Error("without a reviewed agents folder, Agents must still be hashed")
	}
	mustPreview(t, os.WriteFile(filepath.Join(root, "workspace.json"), []byte("b"), 0o600))
	if after, _ := digestProtectedPath(t.Context(), root, agents); after == before {
		t.Error("a change outside the agents folder was not detected")
	}
}

func TestAgentsResetRemovesAgentFoldersAndStateButKeepsOtherFiles(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("installation lease unsupported")
	}
	fixture, owners, planner := previewFixture(t)
	paths := fixture.Paths()
	folder := fixtureAgentsFolder(t, fixture)
	unrelated := filepath.Join(folder, "README.txt")
	mustPreview(t, os.WriteFile(unrelated, []byte("mine"), 0o600))
	stateDir := filepath.Join(paths.DataDir, "agent_state")
	stateKeys, err := os.ReadDir(stateDir)
	mustPreview(t, err)
	if len(stateKeys) == 0 {
		t.Fatal("the fixture seeded no runtime state")
	}

	lease, err := resetstate.Acquire(paths.DataDir)
	mustPreview(t, err)
	life := &fixtureLifecycle{drain: func(context.Context) error {
		return errors.Join(owners.Workspaces.Close(), owners.Database.Close())
	}}
	preview, err := planner.Create(t.Context(), IntentSelectedData, []CategoryID{CategoryAgents})
	mustPreview(t, err)
	op, err := NewCoordinator(lease, planner, life).Stage(t.Context(), ExecuteRequest{
		PreviewID: preview.ID, RequestID: "agents-folder", Confirmation: "RESET", ConfirmAgentsFolder: confirmedAgentsFolder(preview),
	})
	if err != nil || op.State != StateAwaitingRestart {
		t.Fatalf("Stage = %+v, %v", op, err)
	}
	mustPreview(t, lease.Close())

	relaunched, err := resetstate.Acquire(paths.DataDir)
	mustPreview(t, err)
	t.Cleanup(func() { _ = relaunched.Close() })
	if err := RecoverBeforeStores(t.Context(), relaunched, RecoveryOptions{DataDir: paths.DataDir, SecretStore: fixture.Secrets(), Now: time.Now}); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewCoordinator(relaunched, nil, nil).Status(t.Context(), preview.OperationID)
	mustPreview(t, err)
	if recovered.State != StateCompleted {
		t.Fatalf("recovered = %+v", recovered)
	}
	if _, err := os.Stat(filepath.Join(folder, resetfixture.AgentName)); !os.IsNotExist(err) {
		t.Errorf("the agent's folder survived the reset: %v", err)
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "mine" {
		t.Errorf("an unrelated file in Agents was not kept: %q, %v", data, err)
	}
	if remaining, _ := os.ReadDir(stateDir); len(remaining) != 0 {
		t.Errorf("runtime state survived the reset: %v", remaining)
	}
	if _, err := os.Stat(filepath.Join(paths.Workspaces, "fixture-project", "workspace.json")); err != nil {
		t.Errorf("the retained workspace was touched: %v", err)
	}
	fixture.AssertPreserved(t)
}

// agentsJournal is a staged v1 agents receipt carrying folder as its agents
// folder, reviewed in the preview when reviewed is set.
func agentsJournal(selected []CategoryID, folder string, reviewed bool) *journal {
	j := compatJournal(IntentSelectedData, selected, StateAwaitingRestart, OutcomePending, 2)
	j.Plan.Evidence.AgentsFolder = folder
	if reviewed {
		j.Plan.Preview.AgentsFolder = &AgentsFolderReview{Path: folder, Notice: AgentsFolderNotice, ConfirmationRequired: true}
	}
	return j
}

func TestAJournalCannotPointTheAgentsFolderElsewhere(t *testing.T) {
	agentsOnly := []CategoryID{CategoryAgents}
	valid := filepath.Join(compatRoot, "retained-projects", "Agents")
	if err := validateJournal(agentsJournal(agentsOnly, valid, true)); err != nil {
		t.Fatalf("the retained root's Agents folder was refused: %v", err)
	}
	for _, folder := range []string{
		filepath.Join(compatRoot, "retained-projects"),
		filepath.Join(compatRoot, "retained-projects", "Studio"),
		filepath.Join(compatRoot, "retained-projects", "Studio", "Agents"),
		filepath.Join(compatRoot, "Agents"),
		filepath.Join(string(filepath.Separator), "elsewhere", "Agents"),
	} {
		if err := validateJournal(agentsJournal(agentsOnly, folder, true)); !errors.Is(err, ErrJournalInvalid) {
			t.Errorf("journal agents folder %q = %v, want ErrJournalInvalid", folder, err)
		}
	}
	if err := validateJournal(agentsJournal(agentsOnly, valid, false)); !errors.Is(err, ErrJournalInvalid) {
		t.Errorf("an agents folder the preview never showed = %v, want ErrJournalInvalid", err)
	}
	if err := validateJournal(agentsJournal([]CategoryID{CategorySettings}, valid, true)); !errors.Is(err, ErrJournalInvalid) {
		t.Errorf("an agents folder without Agents selected = %v, want ErrJournalInvalid", err)
	}
}

func TestAgentStateIsAnOptionalAgentsTarget(t *testing.T) {
	agentsOnly := []CategoryID{CategoryAgents}
	state := resolvedTarget{Category: CategoryAgents, Kind: "agent_state", Path: filepath.Join(compatRoot, "agent_state")}

	older := agentsJournal(agentsOnly, "", false)
	if err := validateJournal(older); err != nil {
		t.Fatalf("a receipt without agent_state was refused: %v", err)
	}
	withState := agentsJournal(agentsOnly, "", false)
	withState.Plan.Targets = append(withState.Plan.Targets, state)
	if err := validateJournal(withState); err != nil {
		t.Fatalf("a receipt with agent_state was refused: %v", err)
	}
	twice := agentsJournal(agentsOnly, "", false)
	twice.Plan.Targets = append(twice.Plan.Targets, state, state)
	if err := validateJournal(twice); !errors.Is(err, ErrJournalInvalid) {
		t.Errorf("a duplicated agent_state = %v, want ErrJournalInvalid", err)
	}
	missing := agentsJournal(agentsOnly, "", false)
	missing.Plan.Targets = missing.Plan.Targets[1:]
	if err := validateJournal(missing); !errors.Is(err, ErrJournalInvalid) {
		t.Errorf("a receipt missing a required agents target = %v, want ErrJournalInvalid", err)
	}
}
