package workspacerun

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filecuratorguidance"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// stubCuratorProtection is the stored protection read, recording who asked.
type stubCuratorProtection struct {
	state       string
	foreign     bool
	err         error
	reads       int
	user, asked string
}

func (s *stubCuratorProtection) ReadProtection(_ context.Context, user, id string) (filecuratorguidance.ProtectionFact, error) {
	s.reads++
	s.user, s.asked = user, id
	if s.foreign {
		id = "another-workspace"
	}
	return filecuratorguidance.ProtectionFact{WorkspaceID: id, RootGeneration: "root-1", State: s.state, AsOf: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)}, s.err
}

// makeJanitorMissionWorkspace is a File Janitor workspace for Downloads whose
// entry agent is its File Curator companion.
func makeJanitorMissionWorkspace(t *testing.T, store workspace.Store) *workspace.Workspace {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Downloads"})
	ws.Mission = "Keep Downloads tidy."
	ws.MissionEnabled = true
	ws.AutonomyPolicy = workspace.AutonomyPropose
	if err := ws.AddAgent("File Curator"); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}
	if err := ws.SetEntryAgentName("File Curator"); err != nil {
		t.Fatalf("SetEntryAgentName: %v", err)
	}
	if len(ws.AgentInstances) != 1 {
		t.Fatalf("agent instances = %+v", ws.AgentInstances)
	}
	if ws.AgentInstances[0].ID == "" {
		ws.AgentInstances[0].ID = "curator-1"
	}
	ws.InstalledCapabilities = []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now(),
		OwnedResources: []workspace.CapabilityResource{{Kind: workspace.ResourceCompanionAgent, ID: ws.AgentInstances[0].ID, Shared: true}}}}
	if err := store.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}
	if _, ok := ws.FileCurator(); !ok {
		t.Fatal("the fixture has no File Curator")
	}
	return ws
}

// bridgeWithRunner is bridgeAnswering with its runner kept, so a test can read
// the prompt the run was given.
func bridgeWithRunner(t *testing.T, wsStore workspace.Store, result string) (*MissionBridge, *stubOriAgentTaskRunner) {
	t.Helper()
	runner := &stubOriAgentTaskRunner{result: result}
	runStore := NewMemoryStore()
	executors := NewExecutorRegistry()
	executors.Register(ExecutorKindOriAgent, NewOriAgentExecutor(runner))
	service := NewService(runStore, NewProfileRegistry(), executors, NewLocalEnvironmentManager(t.TempDir()), NewValidator(), nil)
	bridge := NewMissionBridge(MissionBridgeConfig{RunStore: runStore, Service: service, WorkspaceStore: wsStore,
		OpportunityStore: workspace.NewOpportunityStore(wsStore), Backlog: workspace.NewBacklogService(wsStore)})
	if bridge == nil {
		t.Fatal("NewMissionBridge returned nil")
	}
	return bridge, runner
}

const backupTitle = "Back up Downloads before the first filing batch"

const answerWithBackup = `{"items": [
	{"kind": "task", "title": "Back up Downloads before the first filing batch.", "why": "Nothing protects the originals", "evidence": "Protection is not covered", "priority": "high", "first_step": "Open Check protection and approve one metadata read."},
	{"kind": "task", "title": "Review the installer pile", "why": "Old installers", "evidence": "12 .dmg files", "priority": "medium"}
]}`

func backupTickets(ws *workspace.Workspace) []workspace.Task {
	var out []workspace.Task
	for _, task := range suggestedTickets(ws) {
		if workspace.NormalizeSuggestionTitle(task.Description) == workspace.NormalizeSuggestionTitle(backupTitle) {
			out = append(out, task)
		}
	}
	return out
}

// backup-first FR9: a File Janitor workspace whose folder has no observed
// backup runs with the backup precondition in its prompt, read for the local
// user; FR10: the model's matching task is stored once and tagged as meeting it.
func TestMissionBridge_FileJanitorRunCarriesTheBackupPrecondition(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := makeJanitorMissionWorkspace(t, wsStore)
	bridge, runner := bridgeWithRunner(t, wsStore, answerWithBackup)
	reader := &stubCuratorProtection{state: "not_covered"}
	bridge.SetCuratorProtectionReader(reader)

	if _, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 1); err != nil {
		t.Fatal(err)
	}
	if reader.user != "local" || reader.asked != ws.ID {
		t.Fatalf("read as %q for %q", reader.user, reader.asked)
	}
	prompt := runner.lastTask.Description
	for _, want := range []string{
		"## Preconditions\n",
		"- As of 2026-10-29T09:00:00Z (stored metadata only, not a new check): The approved File Janitor folder is not covered",
		`Suggested task: "` + backupTitle + `". First step: Open Check protection and approve one metadata read.`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	fresh, _ := wsStore.Get(ws.ID)
	tickets := backupTickets(fresh)
	if len(tickets) != 1 || len(suggestedTickets(fresh)) != 2 {
		t.Fatalf("backup tickets = %d of %d", len(tickets), len(suggestedTickets(fresh)))
	}
	reason, _ := tickets[0].Context[workspace.MissionSuggestionContextKey].(map[string]any)
	if reason["precondition"] != "backup_protection" || reason["action"] != "check_protection" || reason["source"] != "model" || reason["why"] != "Nothing protects the originals" {
		t.Fatalf("backup ticket context = %#v", reason)
	}
	for _, task := range suggestedTickets(fresh) {
		other, _ := task.Context[workspace.MissionSuggestionContextKey].(map[string]any)
		if task.ID != tickets[0].ID && (other["precondition"] != nil || other["action"] != nil) {
			t.Fatalf("an ordinary suggestion was tagged: %#v", other)
		}
	}
}

// FR9: no precondition without a File Curator, a reader, an unmet fact, a
// readable fact, or this workspace's own fact.
func TestMissionBridge_NoBackupPreconditionOtherwise(t *testing.T) {
	for name, setup := range map[string]func(*testing.T, workspace.Store, *MissionBridge) *workspace.Workspace{
		"not a File Janitor workspace": func(t *testing.T, s workspace.Store, b *MissionBridge) *workspace.Workspace {
			b.SetCuratorProtectionReader(&stubCuratorProtection{state: "not_covered"})
			return makeMissionWorkspace(t, s)
		},
		"no reader": func(t *testing.T, s workspace.Store, _ *MissionBridge) *workspace.Workspace {
			return makeJanitorMissionWorkspace(t, s)
		},
		"covered": func(t *testing.T, s workspace.Store, b *MissionBridge) *workspace.Workspace {
			b.SetCuratorProtectionReader(&stubCuratorProtection{state: "covered"})
			return makeJanitorMissionWorkspace(t, s)
		},
		"unavailable": func(t *testing.T, s workspace.Store, b *MissionBridge) *workspace.Workspace {
			b.SetCuratorProtectionReader(&stubCuratorProtection{state: "unavailable"})
			return makeJanitorMissionWorkspace(t, s)
		},
		"read error": func(t *testing.T, s workspace.Store, b *MissionBridge) *workspace.Workspace {
			b.SetCuratorProtectionReader(&stubCuratorProtection{state: "not_covered", err: errors.New("store down")})
			return makeJanitorMissionWorkspace(t, s)
		},
		"another workspace's fact": func(t *testing.T, s workspace.Store, b *MissionBridge) *workspace.Workspace {
			b.SetCuratorProtectionReader(&stubCuratorProtection{state: "not_covered", foreign: true})
			return makeJanitorMissionWorkspace(t, s)
		},
	} {
		t.Run(name, func(t *testing.T) {
			wsStore := workspace.NewInMemoryStore()
			bridge, runner := bridgeWithRunner(t, wsStore, answerWithBackup)
			ws := setup(t, wsStore, bridge)
			if _, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 1); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(runner.lastTask.Description, "Preconditions") {
				t.Fatalf("prompt has a precondition:\n%s", runner.lastTask.Description)
			}
			fresh, _ := wsStore.Get(ws.ID)
			for _, task := range suggestedTickets(fresh) {
				if reason, _ := task.Context[workspace.MissionSuggestionContextKey].(map[string]any); reason["precondition"] != nil {
					t.Fatalf("a ticket was tagged without a precondition: %#v", reason)
				}
			}
		})
	}
}

const answerWithoutBackup = `{"items": [
	{"kind": "task", "title": "Review the installer pile", "why": "Old installers", "evidence": "12 .dmg files", "priority": "medium"},
	{"kind": "task", "title": "Sort screenshots by month", "why": "Many screenshots", "evidence": "40 .png files", "priority": "low"}
]}`

// answerWithBackupSecond names the backup task, but not first.
const answerWithBackupSecond = `{"items": [
	{"kind": "task", "title": "Review the installer pile", "why": "Old installers", "evidence": "12 .dmg files", "priority": "medium"},
	{"kind": "task", "title": "Back up Downloads before the first filing batch", "why": "Model reason", "evidence": "Not covered", "priority": "medium"}
]}`

func backlogOrder(t *testing.T, wsStore workspace.Store, id string) []string {
	t.Helper()
	items, err := workspace.NewBacklogService(wsStore).List(id, false)
	if err != nil {
		t.Fatal(err)
	}
	titles := make([]string, 0, len(items))
	for _, item := range items {
		titles = append(titles, item.Task.Description)
	}
	return titles
}

// FR11, FR12: an answer that leaves the backup task out still gets it, from
// the host: tagged source "host", high priority, counted, and first in the
// Backlog ahead of work that was already there.
func TestMissionBridge_HostStoresTheBackupTaskTheAnswerLeftOut(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := makeJanitorMissionWorkspace(t, wsStore)
	if _, err := workspace.NewBacklogService(wsStore).Create(workspace.BacklogCreateInput{WorkspaceID: ws.ID, Description: "My own idea"}); err != nil {
		t.Fatal(err)
	}
	bridge, _ := bridgeWithRunner(t, wsStore, answerWithoutBackup)
	bridge.SetCuratorProtectionReader(&stubCuratorProtection{state: "configured"})
	if _, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 1); err != nil {
		t.Fatal(err)
	}
	fresh, _ := wsStore.Get(ws.ID)
	tickets := backupTickets(fresh)
	if len(tickets) != 1 {
		t.Fatalf("backup tickets = %d", len(tickets))
	}
	ticket := tickets[0]
	reason, _ := ticket.Context[workspace.MissionSuggestionContextKey].(map[string]any)
	if reason["precondition"] != "backup_protection" || reason["action"] != "check_protection" || reason["source"] != "host" ||
		reason["first_step"] != "Open Check protection and approve one metadata read." ||
		reason["why"] != "Filing moves the originals, so a recovery copy of this folder should exist first." {
		t.Fatalf("host ticket context = %#v", reason)
	}
	if ticket.Priority != 2 || ticket.Description != backupTitle || !strings.Contains(ticket.Details, "**First step:** Open Check protection") {
		t.Fatalf("host ticket = %q priority %d details %q", ticket.Description, ticket.Priority, ticket.Details)
	}
	// Never a drive, provider or destination in copy (PRD §6.2).
	if strings.Contains(strings.ToLower(ticket.Details), "time machine") {
		t.Fatalf("host ticket names a provider: %q", ticket.Details)
	}
	if record := fresh.MissionRuns[0]; record.TasksCreated != 3 || !record.Succeeded {
		t.Fatalf("run record = %+v (two answered tasks and the host's)", record)
	}
	if got := backlogOrder(t, wsStore, ws.ID); len(got) != 4 || got[0] != backupTitle || got[1] != "My own idea" {
		t.Fatalf("Backlog order = %q", got)
	}

	// A second run with the ticket still open adds none, on either path.
	if _, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 2); err != nil {
		t.Fatal(err)
	}
	fresh, _ = wsStore.Get(ws.ID)
	if got := len(backupTickets(fresh)); got != 1 {
		t.Fatalf("backup tickets after a second run = %d", got)
	}
}

// FR12: the model's own backup ticket leads the Backlog too, wherever the
// answer put it, and the host adds no second one.
func TestMissionBridge_ModelBackupTaskLeadsTheBacklog(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := makeJanitorMissionWorkspace(t, wsStore)
	bridge, _ := bridgeWithRunner(t, wsStore, answerWithBackupSecond)
	bridge.SetCuratorProtectionReader(&stubCuratorProtection{state: "not_covered"})
	if _, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 1); err != nil {
		t.Fatal(err)
	}
	fresh, _ := wsStore.Get(ws.ID)
	tickets := backupTickets(fresh)
	if len(tickets) != 1 {
		t.Fatalf("backup tickets = %d", len(tickets))
	}
	if reason, _ := tickets[0].Context[workspace.MissionSuggestionContextKey].(map[string]any); reason["source"] != "model" || reason["why"] != "Model reason" {
		t.Fatalf("model ticket context = %#v", reason)
	}
	if got := backlogOrder(t, wsStore, ws.ID); len(got) != 2 || got[0] != backupTitle {
		t.Fatalf("Backlog order = %q", got)
	}
	if record := fresh.MissionRuns[0]; record.TasksCreated != 2 {
		t.Fatalf("run record = %+v", record)
	}
}

// A covered folder, or no reader, gets no host ticket either.
func TestMissionBridge_NoHostBackupTaskWithoutAnUnmetFact(t *testing.T) {
	for name, reader := range map[string]*stubCuratorProtection{"covered": {state: "covered"}, "no reader": nil} {
		t.Run(name, func(t *testing.T) {
			wsStore := workspace.NewInMemoryStore()
			ws := makeJanitorMissionWorkspace(t, wsStore)
			bridge, _ := bridgeWithRunner(t, wsStore, answerWithoutBackup)
			if reader != nil {
				bridge.SetCuratorProtectionReader(reader)
			}
			if _, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 1); err != nil {
				t.Fatal(err)
			}
			fresh, _ := wsStore.Get(ws.ID)
			if got := len(backupTickets(fresh)); got != 0 {
				t.Fatalf("backup tickets = %d", got)
			}
		})
	}
}

// FR13: Not useful on the backup ticket, then a run with the fact still unmet
// and an answer that names it again: no ticket on the model path or the host's.
func TestMissionBridge_NotUsefulOnTheBackupTaskHolds(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := makeJanitorMissionWorkspace(t, wsStore)
	reader := &stubCuratorProtection{state: "unknown"}
	host, _ := bridgeWithRunner(t, wsStore, answerWithoutBackup)
	host.SetCuratorProtectionReader(reader)
	if _, err := host.TriggerMissionRun(context.Background(), ws.ID, 1); err != nil {
		t.Fatal(err)
	}
	fresh, _ := wsStore.Get(ws.ID)
	tickets := backupTickets(fresh)
	if len(tickets) != 1 {
		t.Fatalf("backup tickets = %d", len(tickets))
	}
	if err := workspace.NewBacklogService(wsStore).DismissSuggestion(ws.ID, tickets[0].ID, "I file by hand first"); err != nil {
		t.Fatal(err)
	}
	for _, answer := range []string{answerWithBackup, answerWithoutBackup} {
		bridge, _ := bridgeWithRunner(t, wsStore, answer)
		bridge.SetCuratorProtectionReader(reader)
		if _, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 2); err != nil {
			t.Fatal(err)
		}
		fresh, _ = wsStore.Get(ws.ID)
		if got := backupTickets(fresh); len(got) != 0 {
			t.Fatalf("a dismissed backup task came back: %+v", got)
		}
	}
}

// A backup task already open, or dismissed as Not useful, is not asked for
// again: the prompt carries no precondition for it.
func TestMissionBridge_BackupPreconditionSkipsOpenOrDismissedTask(t *testing.T) {
	wsStore := workspace.NewInMemoryStore()
	ws := makeJanitorMissionWorkspace(t, wsStore)
	bridge, runner := bridgeWithRunner(t, wsStore, answerWithBackup)
	bridge.SetCuratorProtectionReader(&stubCuratorProtection{state: "unknown"})
	if _, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 1); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runner.lastTask.Description, "## Preconditions") {
		t.Fatal("the first run has no precondition")
	}
	// Open: the second run is not asked for it.
	if _, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 2); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(runner.lastTask.Description, "## Preconditions") {
		t.Fatal("an open backup task is asked for again")
	}
	fresh, _ := wsStore.Get(ws.ID)
	tickets := backupTickets(fresh)
	if len(tickets) != 1 {
		t.Fatalf("backup tickets = %d", len(tickets))
	}
	// Dismissed: nor is the third.
	if err := workspace.NewBacklogService(wsStore).DismissSuggestion(ws.ID, tickets[0].ID, "Not now"); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.TriggerMissionRun(context.Background(), ws.ID, 3); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(runner.lastTask.Description, "## Preconditions") {
		t.Fatal("a dismissed backup task is asked for again")
	}
}
