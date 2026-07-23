package agents

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

func TestScopedControlsAddPromptRenameFocusAndRead(t *testing.T) {
	t.Parallel()
	service, client, store, path := seededFeature(t)

	added, err := service.Add(context.Background(), AddRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer", Kind: "codex"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if added.Reused || added.Agent.Role != "reviewer" || added.Agent.Kind != "codex" || added.Agent.PaneID == "w1:p1" || client.splitCalls != 1 || client.startCalls != 2 {
		t.Fatalf("Add() = %#v; splits=%d starts=%d", added, client.splitCalls, client.startCalls)
	}
	repeated, err := service.Add(context.Background(), AddRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer", Kind: "codex"})
	if err != nil || !repeated.Reused || client.splitCalls != 1 || client.startCalls != 2 {
		t.Fatalf("repeat Add() = %#v, %v; splits=%d starts=%d", repeated, err, client.splitCalls, client.startCalls)
	}

	if _, err := service.Prompt(context.Background(), PromptRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer", Text: "Review the feature."}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if client.lastPromptTarget != added.Agent.Name || client.lastPromptText != "Review the feature." {
		t.Fatalf("prompt routed to %q with %q", client.lastPromptTarget, client.lastPromptText)
	}
	if focused, _, err := service.Focus(context.Background(), TargetRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer"}); err != nil || focused.Name != added.Agent.Name || client.focusTarget != added.Agent.Name {
		t.Fatalf("Focus() = %#v, %v; target=%q", focused, err, client.focusTarget)
	}
	client.readText = "review result\n"
	read, err := service.Read(context.Background(), TargetRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer"}, 120)
	if err != nil || read.Text != "review result\n" || client.readCalls != 1 {
		t.Fatalf("Read() = %#v, %v; reads=%d", read, err, client.readCalls)
	}
	stateBeforeRename, _ := store.Load()
	featureBeforeRename := stateBeforeRename.Features[service.RepositoryID+":bridge"]
	if featureBeforeRename.Schedules == nil {
		featureBeforeRename.Schedules = make(map[string]model.Schedule)
	}
	featureBeforeRename.Schedules["future"] = model.Schedule{ID: "future", Role: "reviewer"}
	stateBeforeRename.Features[service.RepositoryID+":bridge"] = featureBeforeRename
	if err := store.Save(stateBeforeRename); err != nil {
		t.Fatal(err)
	}

	renamed, err := service.Rename(context.Background(), RenameRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer", NewRole: "tester"})
	if err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	expectedName, _ := ScopedAgentName(service.RepositoryID, "bridge", "tester")
	if renamed.Agent.Role != "tester" || renamed.Agent.Name != expectedName || client.renameCalls != 1 {
		t.Fatalf("Rename() = %#v; renames=%d", renamed, client.renameCalls)
	}
	state, _ := store.Load()
	feature := state.Features[service.RepositoryID+":bridge"]
	if _, oldRoleExists := feature.Agents["reviewer"]; oldRoleExists || feature.Agents["tester"].NativeSession.Value == "" || feature.Schedules["future"].Role != "tester" {
		t.Fatalf("role mapping after rename = %#v", feature.Agents)
	}
	assertCallsInOrder(t, client.calls,
		"pane.split:w1:p1",
		"agent.start:"+added.Agent.Name+":codex:",
		"agent.prompt:"+added.Agent.Name,
		"agent.focus:"+added.Agent.Name,
		"agent.read:"+added.Agent.Name,
		"agent.rename:"+added.Agent.Name+":"+expectedName,
	)
	startsBefore := client.startCalls
	promptsBefore := client.promptCalls
	_, err = service.Prompt(context.Background(), PromptRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer", Text: "wrong target"})
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrAgentMissing || client.startCalls != startsBefore || client.promptCalls != promptsBefore {
		t.Fatalf("stale role prompt = %v; starts=%d prompts=%d", err, client.startCalls, client.promptCalls)
	}
}

func TestScopedPrimaryNamesKeepSameRoleInDifferentFeatureWorktreesIsolated(t *testing.T) {
	t.Parallel()
	serviceA, clientA, _, pathA := seededFeatureFor(t, "repo-shared", "bridge-a")
	serviceB, clientB, _, pathB := seededFeatureFor(t, "repo-shared", "bridge-b")
	clientA.promptCalls = 0
	clientB.promptCalls = 0
	left, err := serviceA.Prompt(context.Background(), PromptRequest{Context: ContextRequest{WorktreePath: pathA}, Role: "builder", Text: "A only"})
	if err != nil {
		t.Fatal(err)
	}
	right, err := serviceB.Prompt(context.Background(), PromptRequest{Context: ContextRequest{WorktreePath: pathB}, Role: "builder", Text: "B only"})
	if err != nil {
		t.Fatal(err)
	}
	if left.Agent.Name == right.Agent.Name || clientA.lastPromptTarget != left.Agent.Name || clientB.lastPromptTarget != right.Agent.Name || clientA.promptCalls != 1 || clientB.promptCalls != 1 {
		t.Fatalf("cross-worktree routing left=%#v right=%#v A=%q B=%q", left, right, clientA.lastPromptTarget, clientB.lastPromptTarget)
	}
}

func TestControlsRestoreNativeSessionAndExplicitRebindWithoutReplacement(t *testing.T) {
	t.Parallel()
	service, client, store, path := seededFeature(t)
	added, err := service.Add(context.Background(), AddRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	startsBefore := client.startCalls
	state, _ := store.Load()
	primary := state.Features[service.RepositoryID+":bridge"].Agents["builder"]
	delete(client.byName, primary.Name)
	restored := client.opened.RootPane
	restored.PaneID = "w1:p9"
	restored.TerminalID = "term-9"
	restored.Name = "restored-builder"
	restored.Agent = "claude"
	restored.AgentStatus = model.AgentIdle
	restored.InteractiveReady = true
	restored.AgentSession = &primary.NativeSession
	client.panes[restored.PaneID] = restored
	client.agents = []herdr.AgentInfo{restored}
	client.byName[restored.Name] = restored
	if _, err := service.Prompt(context.Background(), PromptRequest{Context: ContextRequest{WorktreePath: path}, Text: "Resume safely."}); err != nil {
		t.Fatalf("native restore prompt = %v", err)
	}
	if client.startCalls != startsBefore || client.lastPromptTarget != restored.Name {
		t.Fatalf("native restore started replacement or routed incorrectly: starts=%d target=%q", client.startCalls, client.lastPromptTarget)
	}
	state, _ = store.Load()
	if state.Features[service.RepositoryID+":bridge"].Agents["builder"].PaneID != restored.PaneID {
		t.Fatalf("restored primary was not rebound: %#v", state.Features[service.RepositoryID+":bridge"].Agents["builder"])
	}

	delete(client.byName, added.Agent.Name)
	external := client.opened.RootPane
	external.PaneID = "w1:p10"
	external.TerminalID = "term-10"
	external.Name = "manual-review"
	external.Agent = "claude"
	external.AgentStatus = model.AgentIdle
	external.InteractiveReady = true
	external.AgentSession = &model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "review-restored"}
	client.panes[external.PaneID] = external
	client.agents = append(client.agents, external)
	client.byName[external.Name] = external
	rebound, err := service.Rebind(context.Background(), RebindRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer", Target: external.PaneID})
	if err != nil {
		t.Fatalf("Rebind() error = %v", err)
	}
	wantName, _ := ScopedAgentName(service.RepositoryID, "bridge", "reviewer")
	if rebound.Agent.Name != wantName || client.renameCalls == 0 || client.startCalls != startsBefore {
		t.Fatalf("Rebind() = %#v; renames=%d starts=%d", rebound, client.renameCalls, client.startCalls)
	}
}

func TestRenamePrimaryPreservesFutureHandoffIdentity(t *testing.T) {
	t.Parallel()
	service, client, store, path := seededFeature(t)
	startsBefore := client.startCalls
	renamed, err := service.Rename(context.Background(), RenameRequest{Context: ContextRequest{WorktreePath: path}, Role: "builder", NewRole: "lead"})
	if err != nil {
		t.Fatalf("Rename(primary) error = %v", err)
	}
	if renamed.Agent.Role != "lead" {
		t.Fatalf("renamed primary = %#v", renamed.Agent)
	}
	state, _ := store.Load()
	feature := state.Features[service.RepositoryID+":bridge"]
	if feature.Handoff.PrimaryRole != "lead" || feature.Handoff.PrimaryAgentName != renamed.Agent.Name {
		t.Fatalf("primary handoff state after rename = %#v", feature.Handoff)
	}
	result, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"})
	if err != nil || !result.PromptSkipped || result.Primary.Name != renamed.Agent.Name || client.startCalls != startsBefore {
		t.Fatalf("retry after primary rename = %#v, %v; starts=%d", result, err, client.startCalls)
	}
}

func TestControlsRejectCollisionAndStaleRoleTargetsWithoutPrompting(t *testing.T) {
	t.Parallel()
	service, client, _, path := seededFeature(t)
	collisionName, _ := ScopedAgentName(service.RepositoryID, "bridge", "reviewer")
	collision := client.opened.RootPane
	collision.PaneID = "w1:p7"
	collision.TerminalID = "term-7"
	collision.Name = collisionName
	collision.Agent = "claude"
	collision.AgentStatus = model.AgentIdle
	collision.InteractiveReady = true
	client.byName[collision.Name] = collision
	_, err := service.Add(context.Background(), AddRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer"})
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrAgentAmbiguous || client.startCalls != 1 {
		t.Fatalf("collision Add() = %v; starts=%d", err, client.startCalls)
	}

	delete(client.byName, collision.Name)
	added, err := service.Add(context.Background(), AddRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	stale := client.byName[added.Agent.Name]
	stale.PaneID = "w1:p99"
	stale.TerminalID = "term-99"
	stale.AgentSession = nil
	client.byName[added.Agent.Name] = stale
	promptsBefore := client.promptCalls
	startsBefore := client.startCalls
	_, err = service.Prompt(context.Background(), PromptRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer", Text: "Do not send."})
	if !errors.As(err, &stage) || stage.Code != model.ErrAgentAmbiguous || client.promptCalls != promptsBefore || client.startCalls != startsBefore {
		t.Fatalf("stale target prompt = %v; starts=%d prompts=%d", err, client.startCalls, client.promptCalls)
	}
}

func seededFeature(t *testing.T) (*Service, *fakeHerdr, *memoryStore, string) {
	return seededFeatureFor(t, "repo-123456", "bridge")
}

func seededFeatureFor(t *testing.T, repositoryID, featureName string) (*Service, *fakeHerdr, *memoryStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), featureName)
	client := newFakeHerdr(path)
	store := newMemoryStore()
	service := newService(client, store, path)
	service.RepositoryID = repositoryID
	if _, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: featureName, WorktreePath: path, Branch: "feature/" + featureName}); err != nil {
		t.Fatalf("seed Handoff() error = %v", err)
	}
	return service, client, store, path
}

func assertCallsInOrder(t *testing.T, calls []string, expected ...string) {
	t.Helper()
	position := 0
	for _, want := range expected {
		for position < len(calls) && !strings.HasPrefix(calls[position], want) {
			position++
		}
		if position == len(calls) {
			t.Fatalf("fake Herdr calls did not contain %q after prior operations: %#v", want, calls)
		}
		position++
	}
}
