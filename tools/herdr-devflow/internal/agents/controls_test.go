package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

func TestScopedControlsAddPromptRenameFocusAndRead(t *testing.T) {
	t.Parallel()
	service, client, store, path := seededFeature(t)

	added, err := service.Add(context.Background(), AddRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer", Kind: "codex", Model: "openai/codex-review"})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if added.Reused || added.Agent.Role != "reviewer" || added.Agent.Kind != "codex" || added.Agent.Model != "openai/codex-review" || added.Agent.PaneID == "w1:p1" || client.splitCalls != 1 || client.startCalls != 2 {
		t.Fatalf("Add() = %#v; splits=%d starts=%d", added, client.splitCalls, client.startCalls)
	}
	repeated, err := service.Add(context.Background(), AddRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer", Kind: "codex", Model: "openai/codex-review"})
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
	if renamed.Agent.Role != "tester" || renamed.Agent.Name != expectedName || renamed.Agent.Model != "openai/codex-review" || client.renameCalls != 1 {
		t.Fatalf("Rename() = %#v; renames=%d", renamed, client.renameCalls)
	}
	state, _ := store.Load()
	feature := state.Features[service.RepositoryID+":bridge"]
	if _, oldRoleExists := feature.Agents["reviewer"]; oldRoleExists || feature.Agents["tester"].NativeSession.Value == "" || feature.Agents["tester"].Model != "openai/codex-review" || feature.Schedules["future"].Role != "tester" {
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

func TestRoleAgentResolvesKindAndModelAsAPair(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		configure      func(*Service)
		requestedKind  string
		requestedModel string
		wantKind       string
		wantModel      string
	}{
		{
			name: "default role pair",
			configure: func(service *Service) {
				service.Config.Roles.DefaultKind = "pi"
				service.Config.Roles.DefaultModel = "openai/fallback"
				delete(service.Config.Roles.Defaults, "analyst")
			},
			wantKind: "pi", wantModel: "openai/fallback",
		},
		{
			name: "role-specific pair",
			configure: func(service *Service) {
				service.Config.Roles.Defaults["analyst"] = "pi"
				service.Config.Roles.Models["analyst"] = "openai/analyst"
			},
			wantKind: "pi", wantModel: "openai/analyst",
		},
		{
			name: "model-only override",
			configure: func(service *Service) {
				service.Config.Roles.Defaults["analyst"] = "pi"
				service.Config.Roles.Models["analyst"] = "openai/configured"
			},
			requestedModel: "openai/override", wantKind: "pi", wantModel: "openai/override",
		},
		{
			name: "changed kind clears stale model",
			configure: func(service *Service) {
				service.Config.Roles.Defaults["analyst"] = "pi"
				service.Config.Roles.Models["analyst"] = "openai/configured"
			},
			requestedKind: "claude", wantKind: "claude",
		},
		{
			name: "explicit pair",
			configure: func(service *Service) {
				service.Config.Roles.Defaults["analyst"] = "pi"
				service.Config.Roles.Models["analyst"] = "openai/configured"
			},
			requestedKind: "codex", requestedModel: "openai/codex-max", wantKind: "codex", wantModel: "openai/codex-max",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service, client, store, path := seededFeature(t)
			testCase.configure(service)
			result, err := service.Add(context.Background(), AddRequest{
				Context: ContextRequest{WorktreePath: path}, Role: "analyst",
				Kind: testCase.requestedKind, Model: testCase.requestedModel,
			})
			if err != nil {
				t.Fatalf("Add() error = %v", err)
			}
			if result.Agent.Kind != testCase.wantKind || result.Agent.Model != testCase.wantModel {
				t.Fatalf("role pair = %q/%q, want %q/%q", result.Agent.Kind, result.Agent.Model, testCase.wantKind, testCase.wantModel)
			}
			last := client.startRequests[len(client.startRequests)-1]
			if last.Kind != testCase.wantKind || last.Model != testCase.wantModel {
				t.Fatalf("role launch = %#v, want %q/%q", last, testCase.wantKind, testCase.wantModel)
			}
			state, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			saved := state.Features[service.RepositoryID+":bridge"].Agents["analyst"]
			if saved.Kind != testCase.wantKind || saved.Model != testCase.wantModel {
				t.Fatalf("saved role pair = %#v, want %q/%q", saved, testCase.wantKind, testCase.wantModel)
			}
		})
	}
}

func TestRoleAgentRetainsPartialLaunchPairAcrossFailureRetryReuseAndRebind(t *testing.T) {
	t.Parallel()
	service, client, store, path := seededFeature(t)
	service.Config.Roles.Defaults["reviewer"] = "pi"
	service.Config.Roles.Models["reviewer"] = "openai/reviewer"
	client.fail["start"] = errors.New("role launch failed")

	_, err := service.Add(context.Background(), AddRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer"})
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Stage != "start role agent" {
		t.Fatalf("failed Add() error = %#v", err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	saved := state.Features[service.RepositoryID+":bridge"].Agents["reviewer"]
	if saved.Kind != "pi" || saved.Model != "openai/reviewer" || saved.PaneID == "" {
		t.Fatalf("partial role pair was not persisted: %#v", saved)
	}
	failedLaunch := client.startRequests[len(client.startRequests)-1]
	t.Logf("failed launch intent: name=%q kind=%q model=%q pane=%q", failedLaunch.Name, failedLaunch.Kind, failedLaunch.Model, failedLaunch.PaneID)
	_, err = service.Add(context.Background(), AddRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer", Kind: "pi", Model: "openai/other"})
	if !errors.As(err, &stage) || stage.Code != model.ErrAgentAmbiguous {
		t.Fatalf("conflicting partial model error = %#v", err)
	}

	delete(client.fail, "start")
	service.Config.Roles.Defaults["reviewer"] = "codex"
	service.Config.Roles.Models["reviewer"] = "openai/new-default"
	retried, err := service.Add(context.Background(), AddRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer"})
	if err != nil {
		t.Fatalf("retry Add() error = %v", err)
	}
	if retried.Agent.Kind != "pi" || retried.Agent.Model != "openai/reviewer" {
		t.Fatalf("retry did not retain pair: %#v", retried.Agent)
	}
	last := client.startRequests[len(client.startRequests)-1]
	if last.Kind != "pi" || last.Model != "openai/reviewer" {
		t.Fatalf("retry launch = %#v, want recorded pair", last)
	}
	t.Logf("retry after defaults changed to codex/openai/new-default: name=%q kind=%q model=%q pane=%q", last.Name, last.Kind, last.Model, last.PaneID)

	reused, err := service.Add(context.Background(), AddRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer"})
	if err != nil || !reused.Reused || reused.Agent.Model != "openai/reviewer" {
		t.Fatalf("reused role lost model intent: %#v, %v", reused, err)
	}

	external := client.byName[retried.Agent.Name]
	external.Name = "manual-reviewer"
	delete(client.byName, retried.Agent.Name)
	client.byName[external.Name] = external
	rebound, err := service.Rebind(context.Background(), RebindRequest{Context: ContextRequest{WorktreePath: path}, Role: "reviewer", Target: external.Name})
	if err != nil {
		t.Fatalf("Rebind() error = %v", err)
	}
	if rebound.Agent.Model != "openai/reviewer" {
		t.Fatalf("rebind lost model intent: %#v", rebound.Agent)
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

func TestControlIdentityValidationRejectsTraversalMetacharactersAndControls(t *testing.T) {
	t.Parallel()
	for _, role := range []string{"../builder", "builder;echo", "builder$(touch)", "builder\nnext", "Builder", ""} {
		if err := validateRole(role); err == nil {
			t.Fatalf("validateRole(%q) accepted unsafe role", role)
		}
		if _, err := ScopedAgentName("repo-123", "feature", role); err == nil {
			t.Fatalf("ScopedAgentName accepted unsafe role %q", role)
		}
	}
	for _, feature := range []string{"../feature", "feature;echo", "feature\nnext", ""} {
		if _, err := ScopedAgentName("repo-123", feature, "builder"); err == nil {
			t.Fatalf("ScopedAgentName accepted unsafe feature %q", feature)
		}
	}
	for _, target := range []string{"builder;echo", "builder$(touch)", "builder\nnext", "../builder", ""} {
		if lowLevelTargetPattern.MatchString(target) {
			t.Fatalf("low-level target pattern accepted unsafe target %q", target)
		}
	}
	if !lowLevelTargetPattern.MatchString("ori-repo-feature-builder:1") {
		t.Fatal("low-level target pattern rejected a documented Herdr-style target")
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

// TestSavedRoleRecoversFromTheFeatureWorktree covers the 2026-07-26 dead end:
// the saved agent name no longer resolved, a healthy agent was running in the
// feature's worktree, and both `wt herd retry` and `wt herd handoff` refused
// with agent_missing rather than using it.
func TestSavedRoleRecoversFromTheFeatureWorktree(t *testing.T) {
	t.Parallel()
	service, client, store, path := seededFeature(t)

	saved := savedBuilder(t, store)
	// The workspace was closed: the saved name resolves to nothing.
	delete(client.byName, saved.Name)

	// A replacement agent is running in the same worktree under a new pane.
	replacement := herdr.AgentInfo{
		Name: "hand-started", Agent: saved.Kind, AgentStatus: model.AgentIdle,
		WorkspaceID: "wNEW", PaneID: "wNEW:p1", TerminalID: "term-new",
		Cwd: path, InteractiveReady: true,
	}
	client.byName[replacement.Name] = replacement
	client.agents = append(client.agents, replacement)

	promptsBefore := client.promptCalls
	if _, err := service.Prompt(context.Background(), PromptRequest{
		Context: ContextRequest{WorktreePath: path}, Role: "builder", Text: "continue",
	}); err != nil {
		t.Fatalf("Prompt() = %v, want recovery through the feature worktree", err)
	}
	if client.promptCalls != promptsBefore+1 {
		t.Fatalf("prompt calls = %d, want the recovered agent prompted", client.promptCalls)
	}
}

func TestSavedRoleDoesNotRecoverFromAnotherWorktree(t *testing.T) {
	t.Parallel()
	service, client, store, path := seededFeature(t)

	saved := savedBuilder(t, store)
	delete(client.byName, saved.Name)

	// An agent in a different worktree must never be adopted for this feature.
	elsewhere := herdr.AgentInfo{
		Name: "someone-else", Agent: saved.Kind, AgentStatus: model.AgentIdle,
		WorkspaceID: "wOTHER", PaneID: "wOTHER:p1", TerminalID: "term-other",
		Cwd: filepath.Join(t.TempDir(), "different-feature"), InteractiveReady: true,
	}
	client.byName[elsewhere.Name] = elsewhere
	client.agents = append(client.agents, elsewhere)

	_, err := service.Prompt(context.Background(), PromptRequest{
		Context: ContextRequest{WorktreePath: path}, Role: "builder", Text: "must not send",
	})
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrAgentMissing {
		t.Fatalf("Prompt() = %v, want agent_missing — no agent runs in this worktree", err)
	}
}

func TestSavedRoleRefusesToGuessBetweenSeveralWorktreeAgents(t *testing.T) {
	t.Parallel()
	service, client, store, path := seededFeature(t)

	saved := savedBuilder(t, store)
	delete(client.byName, saved.Name)

	for index, name := range []string{"first", "second"} {
		candidate := herdr.AgentInfo{
			Name: name, Agent: saved.Kind, AgentStatus: model.AgentIdle,
			WorkspaceID: "wNEW", PaneID: fmt.Sprintf("wNEW:p%d", index+1),
			TerminalID: fmt.Sprintf("term-%d", index+1), Cwd: path, InteractiveReady: true,
		}
		client.byName[name] = candidate
		client.agents = append(client.agents, candidate)
	}

	_, err := service.Prompt(context.Background(), PromptRequest{
		Context: ContextRequest{WorktreePath: path}, Role: "builder", Text: "must not send",
	})
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrAgentAmbiguous {
		t.Fatalf("Prompt() = %v, want agent_ambiguous — the operator must settle this", err)
	}
}

// savedBuilder returns the seeded builder role from the store.
func savedBuilder(t *testing.T, store *memoryStore) model.RoleAgent {
	t.Helper()
	state, err := store.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	for _, feature := range state.Features {
		if saved, ok := feature.Agents["builder"]; ok {
			return saved
		}
	}
	t.Fatal("no builder role was seeded")
	return model.RoleAgent{}
}

// TestExistingSavedRecordsResolveWithoutMigration writes a record in the
// current on-disk format and proves it still resolves — no migration step, no
// re-run of wt start.
func TestExistingSavedRecordsResolveWithoutMigration(t *testing.T) {
	t.Parallel()
	service, client, store, path := seededFeature(t)

	// Reload exactly what the bridge wrote, as an older build would have.
	state, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var reloaded model.BridgeState
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := store.Save(reloaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The saved agent is still live and unchanged: the ordinary path must not
	// have regressed while the fallback was added.
	promptsBefore := client.promptCalls
	result, err := service.Prompt(context.Background(), PromptRequest{
		Context: ContextRequest{WorktreePath: path}, Role: "builder", Text: "still works",
	})
	if err != nil {
		t.Fatalf("Prompt() on an unmigrated record = %v", err)
	}
	if result.Agent.Model != "" {
		t.Fatalf("legacy role invented a model: %#v", result.Agent)
	}
	if client.promptCalls != promptsBefore+1 {
		t.Fatal("the saved-identity path stopped working")
	}
}

// TestWorkspaceReopenIsRecognisedWithoutABridgeCommand covers closing a
// workspace and opening a fresh pane in the same worktree: the feature must be
// recognised again with no rebind, retry, or wt start in between.
func TestWorkspaceReopenIsRecognisedWithoutABridgeCommand(t *testing.T) {
	t.Parallel()
	service, client, store, path := seededFeature(t)

	saved := savedBuilder(t, store)
	// Close the workspace: every saved identity field becomes unresolvable.
	delete(client.byName, saved.Name)

	// Reopen: a new workspace, new pane, new terminal — same worktree.
	reopened := herdr.AgentInfo{
		Name: "reopened-agent", Agent: saved.Kind, AgentStatus: model.AgentIdle,
		WorkspaceID: "wREOPEN", PaneID: "wREOPEN:p1", TerminalID: "term-reopen",
		Cwd: path, InteractiveReady: true,
	}
	client.byName[reopened.Name] = reopened
	client.agents = append(client.agents, reopened)

	// No bridge command is run between the close and this call.
	if _, err := service.Prompt(context.Background(), PromptRequest{
		Context: ContextRequest{WorktreePath: path}, Role: "builder", Text: "recognised",
	}); err != nil {
		t.Fatalf("Prompt() after a workspace reopen = %v, want recognition by worktree path", err)
	}
}

// TestHandoffAdoptsAnAgentAlreadyInTheWorktree covers the wt start failure
// seen on 2026-07-26: an agent was already running in the worktree, and the
// handoff failed with "root pane is busy with a non-shell foreground process"
// rather than using it.
func TestHandoffAdoptsAnAgentAlreadyInTheWorktree(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "adopted")
	client := newFakeHerdr(path)
	store := newMemoryStore()
	service := newService(client, store, path)
	service.RepositoryID = "repo-adopt"

	// Someone already started an agent in this worktree, under a name the
	// bridge would never have chosen.
	existing := herdr.AgentInfo{
		Name: "hand-started", Agent: "claude", AgentStatus: model.AgentIdle,
		WorkspaceID: "wHAND", PaneID: "wHAND:p1", TerminalID: "term-hand",
		Cwd: path, InteractiveReady: true,
	}
	client.byName[existing.Name] = existing
	client.agents = append(client.agents, existing)

	startsBefore := client.startCalls
	result, err := service.Handoff(context.Background(), HandoffRequest{
		FeatureName: "adopted", WorktreePath: path, Branch: "feature/adopted",
	})
	if err != nil {
		t.Fatalf("Handoff() = %v, want adoption of the existing agent", err)
	}
	if client.startCalls != startsBefore {
		t.Fatalf("a second agent was launched beside the existing one: starts=%d", client.startCalls)
	}
	if result.Primary.Name != existing.Name {
		t.Fatalf("adopted agent = %q, want the one already in the worktree (%q)", result.Primary.Name, existing.Name)
	}
}

func TestHandoffStillLaunchesWhenTheWorktreeIsEmpty(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "empty")
	client := newFakeHerdr(path)
	store := newMemoryStore()
	service := newService(client, store, path)
	service.RepositoryID = "repo-empty"

	startsBefore := client.startCalls
	if _, err := service.Handoff(context.Background(), HandoffRequest{
		FeatureName: "empty", WorktreePath: path, Branch: "feature/empty",
	}); err != nil {
		t.Fatalf("Handoff() = %v", err)
	}
	if client.startCalls != startsBefore+1 {
		t.Fatalf("start calls = %d, want one launch when nothing is running", client.startCalls)
	}
}
