package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

type memoryStore struct {
	mu    sync.Mutex
	state model.BridgeState
	saves int
}

func newMemoryStore() *memoryStore { return &memoryStore{state: model.NewBridgeState()} }

func (s *memoryStore) Load() (model.BridgeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneState(s.state), nil
}

func (s *memoryStore) Save(state model.BridgeState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = cloneState(state)
	s.saves++
	return nil
}

func cloneState(value model.BridgeState) model.BridgeState {
	encoded, _ := json.Marshal(value)
	var copy model.BridgeState
	_ = json.Unmarshal(encoded, &copy)
	if copy.Features == nil {
		copy.Features = make(map[string]model.FeatureState)
	}
	return copy
}

type fakeInspector struct {
	worktree worktree.GitWorktree
	err      error
}

func (f fakeInspector) Inspect(_ context.Context, _, _, _ string) (worktree.GitWorktree, error) {
	return f.worktree, f.err
}

type fakeHerdr struct {
	// opened seeds the canonical workspace, tab, and root-pane identities the
	// tests assert against. Nothing calls worktree open any more; the first
	// created tab reuses these identities so pane-level assertions stay stable.
	opened           herdr.WorktreeOpenResult
	workspaces       []herdr.WorkspaceInfo
	tabs             map[string]herdr.TabInfo
	process          herdr.PaneProcessInfo
	panes            map[string]herdr.PaneInfo
	processByPane    map[string]herdr.PaneProcessInfo
	agents           []herdr.AgentInfo
	byName           map[string]herdr.AgentInfo
	startCalls       int
	splitCalls       int
	renameCalls      int
	focusCalls       int
	readCalls        int
	getCalls         int
	promptCalls      int
	metadataCalls    int
	tabCreateCalls   int
	startedReady     bool
	startResult      *herdr.AgentInfo
	promptResult     *herdr.AgentInfo
	readText         string
	focusTarget      string
	lastPromptTarget string
	lastPromptText   string
	lastTabWorkspace string
	lastTabCwd       string
	lastTabLabel     string
	// settleAfter models a freshly created tab whose shell reports a foreground
	// process for the first N process-info polls, the way a real starting shell
	// does before it settles.
	settleAfter int
	settlePolls map[string]int
	// paneTokens records the last display tokens reported to each pane, which
	// is where per-feature metadata lives now that a workspace holds many.
	paneTokens map[string]map[string]string
	calls      []string
	fail       map[string]error
}

func (f *fakeHerdr) FocusedWorkspace(_ context.Context) (herdr.WorkspaceInfo, error) {
	f.record("workspace.list")
	if err := f.fail["focused"]; err != nil {
		return herdr.WorkspaceInfo{}, err
	}
	for _, workspace := range f.workspaces {
		if workspace.Focused {
			return workspace, nil
		}
	}
	return herdr.WorkspaceInfo{}, &model.StageError{Stage: "resolve focused workspace", Code: model.ErrNoFocusedWorkspace, Message: "no Herdr workspace reports focus", Recovery: "focus a Herdr workspace, then run wt herd retry"}
}

func (f *fakeHerdr) TabCreateInfo(_ context.Context, workspaceID, cwd, label string) (herdr.TabCreateResult, error) {
	f.record("tab.create:" + workspaceID)
	if err := f.fail["tab_create"]; err != nil {
		return herdr.TabCreateResult{}, err
	}
	f.tabCreateCalls++
	f.lastTabWorkspace, f.lastTabCwd, f.lastTabLabel = workspaceID, cwd, label
	pane := f.opened.RootPane
	if f.tabCreateCalls > 1 {
		pane.PaneID = workspaceID + ":p" + strconv.Itoa(f.tabCreateCalls)
		pane.TerminalID = "term-tab-" + strconv.Itoa(f.tabCreateCalls)
	}
	tabID := workspaceID + ":t" + strconv.Itoa(f.tabCreateCalls)
	pane.WorkspaceID, pane.TabID = workspaceID, tabID
	pane.Cwd, pane.ForegroundCwd = cwd, cwd
	pane.Name, pane.Agent = "", ""
	pane.AgentStatus = model.AgentUnknown
	tab := herdr.TabInfo{TabID: tabID, WorkspaceID: workspaceID, Label: label, Number: f.tabCreateCalls, PaneCount: 1}
	f.tabs[tabID] = tab
	f.panes[pane.PaneID] = pane
	f.processByPane[pane.PaneID] = herdr.PaneProcessInfo{
		PaneID:              pane.PaneID,
		ShellPID:            int64ptr(int64(100 + f.tabCreateCalls)),
		ForegroundProcesses: []herdr.ForegroundProcess{{PID: int64(100 + f.tabCreateCalls), Name: "zsh", Cwd: cwd}},
	}
	return herdr.TabCreateResult{Type: "tab_created", Tab: tab, RootPane: pane}, nil
}

func (f *fakeHerdr) TabGetInfo(_ context.Context, tabID string) (herdr.TabInfo, error) {
	f.record("tab.get:" + tabID)
	if err := f.fail["tab_get"]; err != nil {
		return herdr.TabInfo{}, err
	}
	tab, ok := f.tabs[tabID]
	if !ok {
		return herdr.TabInfo{}, &model.StageError{Stage: "Herdr API", Code: model.ErrAgentMissing, Message: "tab not found", Recovery: "wt herd retry"}
	}
	return tab, nil
}

func (f *fakeHerdr) PaneGetInfo(_ context.Context, paneID string) (herdr.PaneInfo, error) {
	f.record("pane.get:" + paneID)
	if err := f.fail["pane_get"]; err != nil {
		return herdr.PaneInfo{}, err
	}
	pane, ok := f.panes[paneID]
	if !ok {
		return herdr.PaneInfo{}, &model.StageError{Stage: "Herdr API", Code: model.ErrAgentMissing, Message: "pane not found", Recovery: "wt herd retry"}
	}
	return pane, nil
}

func (f *fakeHerdr) PaneProcessInfo(_ context.Context, paneID string) (herdr.PaneProcessInfo, error) {
	f.record("pane.process-info:" + paneID)
	if f.settleAfter > 0 {
		f.settlePolls[paneID]++
		if f.settlePolls[paneID] <= f.settleAfter {
			return herdr.PaneProcessInfo{
				PaneID:              paneID,
				ShellPID:            int64ptr(7000),
				ForegroundProcesses: []herdr.ForegroundProcess{{PID: 7001, Name: "zsh", Cwd: f.lastTabCwd}},
			}, nil
		}
	}
	if paneID == f.process.PaneID {
		return f.process, f.fail["process"]
	}
	if process, ok := f.processByPane[paneID]; ok {
		return process, f.fail["process"]
	}
	return herdr.PaneProcessInfo{}, &model.StageError{Stage: "Herdr API", Code: model.ErrAgentMissing, Message: "pane not found"}
}

func (f *fakeHerdr) PaneSplitInfo(_ context.Context, paneID, _ string, cwd string) (herdr.PaneInfo, error) {
	f.record("pane.split:" + paneID)
	if err := f.fail["split"]; err != nil {
		return herdr.PaneInfo{}, err
	}
	parent, ok := f.panes[paneID]
	if !ok {
		return herdr.PaneInfo{}, &model.StageError{Stage: "Herdr API", Code: model.ErrAgentMissing, Message: "source pane not found"}
	}
	f.splitCalls++
	created := parent
	created.PaneID = "w1:p" + strconv.Itoa(f.splitCalls+1)
	created.TerminalID = "term-" + strconv.Itoa(f.splitCalls+1)
	created.Cwd = cwd
	created.ForegroundCwd = cwd
	created.Name = ""
	created.Agent = ""
	created.AgentStatus = model.AgentUnknown
	created.InteractiveReady = false
	created.LaunchPending = false
	created.AgentSession = nil
	f.panes[created.PaneID] = created
	f.processByPane[created.PaneID] = herdr.PaneProcessInfo{PaneID: created.PaneID, ShellPID: int64ptr(int64(42 + f.splitCalls)), ForegroundProcesses: []herdr.ForegroundProcess{{PID: int64(42 + f.splitCalls), Name: "zsh", Cwd: cwd}}}
	return created, nil
}

func (f *fakeHerdr) AgentListInfo(_ context.Context) ([]herdr.AgentInfo, error) {
	f.record("agent.list")
	if err := f.fail["list"]; err != nil {
		return nil, err
	}
	agents := append([]herdr.AgentInfo(nil), f.agents...)
	seen := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		seen[agent.Name] = struct{}{}
	}
	for name, agent := range f.byName {
		if _, ok := seen[name]; !ok {
			agents = append(agents, agent)
		}
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	return agents, nil
}

func (f *fakeHerdr) AgentGetInfo(_ context.Context, target string) (herdr.AgentInfo, error) {
	f.record("agent.get:" + target)
	f.getCalls++
	if err := f.fail["get"]; err != nil {
		return herdr.AgentInfo{}, err
	}
	agent, ok := f.byName[target]
	if !ok {
		for _, candidate := range f.agents {
			if candidate.PaneID == target {
				agent, ok = candidate, true
				break
			}
		}
	}
	if !ok {
		return herdr.AgentInfo{}, &model.StageError{Stage: "Herdr API", Code: model.ErrAgentMissing, Message: "agent not found"}
	}
	return agent, nil
}

func (f *fakeHerdr) AgentStartInfo(_ context.Context, name, kind, paneID string, _ time.Duration) (herdr.AgentInfo, error) {
	f.record("agent.start:" + name + ":" + kind + ":" + paneID)
	if err := f.fail["start"]; err != nil {
		return herdr.AgentInfo{}, err
	}
	f.startCalls++
	agent, ok := f.panes[paneID]
	if !ok {
		agent = f.opened.RootPane
	}
	agent.Name = name
	agent.Agent = kind
	agent.PaneID = paneID
	agent.InteractiveReady = f.startedReady
	agent.LaunchPending = !f.startedReady
	agent.AgentStatus = model.AgentIdle
	agent.AgentSession = &model.NativeSession{Source: "herdr:" + kind, Agent: kind, Kind: "id", Value: "session-" + strconv.Itoa(f.startCalls)}
	if f.startResult != nil {
		agent = *f.startResult
	}
	f.byName[name] = agent
	return agent, nil
}

func (f *fakeHerdr) AgentRenameInfo(_ context.Context, target, name string) (herdr.AgentInfo, error) {
	f.record("agent.rename:" + target + ":" + name)
	if err := f.fail["rename"]; err != nil {
		return herdr.AgentInfo{}, err
	}
	agent, ok := f.byName[target]
	if !ok {
		for _, candidate := range f.agents {
			if candidate.PaneID == target {
				agent, ok = candidate, true
				break
			}
		}
	}
	if !ok {
		return herdr.AgentInfo{}, &model.StageError{Stage: "Herdr API", Code: model.ErrAgentMissing, Message: "agent not found"}
	}
	f.renameCalls++
	delete(f.byName, agent.Name)
	agent.Name = name
	f.byName[name] = agent
	for index := range f.agents {
		if f.agents[index].PaneID == agent.PaneID {
			f.agents[index] = agent
		}
	}
	return agent, nil
}

func (f *fakeHerdr) FocusAgent(_ context.Context, target string) error {
	f.record("agent.focus:" + target)
	if err := f.fail["focus"]; err != nil {
		return err
	}
	if _, ok := f.byName[target]; !ok {
		return &model.StageError{Stage: "Herdr API", Code: model.ErrAgentMissing, Message: "agent not found"}
	}
	f.focusCalls++
	f.focusTarget = target
	return nil
}

func (f *fakeHerdr) AgentReadText(_ context.Context, target string, _ int) (string, error) {
	f.record("agent.read:" + target)
	if err := f.fail["read"]; err != nil {
		return "", err
	}
	if _, ok := f.byName[target]; !ok {
		return "", &model.StageError{Stage: "Herdr API", Code: model.ErrAgentMissing, Message: "agent not found"}
	}
	f.readCalls++
	return f.readText, nil
}

func (f *fakeHerdr) AgentPromptInfo(_ context.Context, target, text string, _ time.Duration) (herdr.AgentInfo, error) {
	f.record("agent.prompt:" + target)
	if err := f.fail["prompt"]; err != nil {
		return herdr.AgentInfo{}, err
	}
	f.promptCalls++
	f.lastPromptTarget = target
	f.lastPromptText = text
	if f.promptResult != nil {
		return *f.promptResult, nil
	}
	return f.byName[target], nil
}

func (f *fakeHerdr) ReportPaneMetadata(_ context.Context, paneID, _ string, tokens map[string]string) (json.RawMessage, error) {
	f.record("pane.report-metadata:" + paneID)
	f.metadataCalls++
	if f.paneTokens == nil {
		f.paneTokens = make(map[string]map[string]string)
	}
	f.paneTokens[paneID] = tokens
	return json.RawMessage(`{"type":"pane_metadata"}`), f.fail["pane_metadata"]
}

func (f *fakeHerdr) record(call string) {
	f.calls = append(f.calls, call)
}

func TestHandoffLaunchesOnePrimaryAndDoesNotResendConfirmedPrompt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	name, err := ScopedAgentName("repo-123456", "bridge", "builder")
	if err != nil {
		t.Fatal(err)
	}
	client := newFakeHerdr(path)
	store := newMemoryStore()
	service := newService(client, store, path)
	request := HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"}
	first, err := service.Handoff(context.Background(), request)
	if err != nil {
		t.Fatalf("Handoff() error = %v", err)
	}
	if first.Primary.Name != name || client.startCalls != 1 || client.promptCalls != 1 {
		t.Fatalf("first handoff = %#v, starts=%d prompts=%d", first, client.startCalls, client.promptCalls)
	}
	if first.Primary.Kind != "claude" {
		t.Fatalf("default primary kind = %q, want claude", first.Primary.Kind)
	}
	if !first.PromptDelivered || client.metadataCalls != 2 {
		t.Fatalf("first handoff delivery/metadata = %#v, %d", first, client.metadataCalls)
	}
	// The feature landed as a tab in the focused workspace, labeled with its slug.
	if client.tabCreateCalls != 1 || client.lastTabWorkspace != "w1" || client.lastTabCwd != path || client.lastTabLabel != "bridge" {
		t.Fatalf("tab create = %d calls, workspace %q, cwd %q, label %q", client.tabCreateCalls, client.lastTabWorkspace, client.lastTabCwd, client.lastTabLabel)
	}
	if first.TabID != "w1:t1" || first.WorkspaceID != "w1" {
		t.Fatalf("handoff placement = workspace %q tab %q", first.WorkspaceID, first.TabID)
	}
	second, err := service.Handoff(context.Background(), request)
	if err != nil {
		t.Fatalf("retry Handoff() error = %v", err)
	}
	if !second.PromptSkipped || client.startCalls != 1 || client.promptCalls != 1 {
		t.Fatalf("idempotent handoff = %#v, starts=%d prompts=%d", second, client.startCalls, client.promptCalls)
	}
	state, _ := store.Load()
	feature := state.Features["repo-123456:bridge"]
	if feature.Handoff.Stage != model.HandoffPrompted || feature.Handoff.PrimaryKind != "claude" || !feature.Handoff.BootstrapPrompted || feature.Agents["builder"].NativeSession.Value != "session-1" {
		t.Fatalf("stored handoff state = %#v", feature)
	}
}

func TestHandoffPersistsRequestedPrimaryKindForRetries(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	client := newFakeHerdr(path)
	store := newMemoryStore()
	service := newService(client, store, path)
	client.fail["start"] = errors.New("agent launch failed")

	_, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge", PrimaryKind: "codex"})
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Stage != "start primary agent" {
		t.Fatalf("initial Codex handoff error = %#v", err)
	}
	state, err := store.Load()
	if err != nil || state.Features["repo-123456:bridge"].Handoff.PrimaryKind != "codex" {
		t.Fatalf("primary kind was not retained after failed handoff: state=%#v err=%v", state, err)
	}
	_, err = service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge", PrimaryKind: "claude"})
	if !errors.As(err, &stage) || stage.Stage != "record handoff" {
		t.Fatalf("conflicting primary-kind override error = %#v", err)
	}

	delete(client.fail, "start")
	result, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"})
	if err != nil {
		t.Fatalf("retry Handoff() error = %v", err)
	}
	if result.Primary.Kind != "codex" || client.startCalls != 1 {
		t.Fatalf("retry should launch the saved Codex kind: result=%#v starts=%d calls=%#v", result, client.startCalls, client.calls)
	}
}

func TestContinuationPromptIsPlanningAwareWithoutEmbeddingTaskContents(t *testing.T) {
	path := t.TempDir()
	if err := os.MkdirAll(filepath.Join(path, "tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "AGENTS.md"), []byte("follow local rules\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "tasks", "tasks-bridge.md"), []byte("- [ ] 2.1 Deliver the scheduled continuation\n"), 0600); err != nil {
		t.Fatal(err)
	}
	feature := model.Feature{Name: "bridge", Path: path}
	prompt := ContinuationPrompt(feature, "builder")
	for _, want := range []string{
		"scheduled continuation",
		"Read and follow: " + filepath.Join(path, "AGENTS.md"),
		"PRD: none (task-list-sized)",
		filepath.Join(path, "tasks", "tasks-bridge.md"),
		"Next incomplete checklist item: 2.1 Deliver the scheduled continuation",
		"wt done bridge",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("ContinuationPrompt() missing %q:\n%s", want, prompt)
		}
	}
	// No prd-bridge.md was written into this fixture; the prompt must not
	// name one as though it existed (AR30's boundary applies to scheduled
	// continuations as well as the initial bootstrap prompt).
	if strings.Contains(prompt, filepath.Join(path, "tasks", "prd-bridge.md")) {
		t.Fatalf("ContinuationPrompt() named a PRD that does not exist:\n%s", prompt)
	}
}

func TestHandoffDoesNotReplaceSavedMissingAgent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	client := newFakeHerdr(path)
	store := newMemoryStore()
	service := newService(client, store, path)
	request := HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"}
	if _, err := service.Handoff(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	client.byName = map[string]herdr.AgentInfo{}
	_, err := service.Handoff(context.Background(), request)
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrAgentMissing {
		t.Fatalf("retry error = %#v, want missing-agent recovery", err)
	}
	if client.startCalls != 1 {
		t.Fatalf("missing saved agent started a replacement: %d starts", client.startCalls)
	}
}

func TestHandoffFailsAtEachHerdrStageWithoutReplacingTheCheckout(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		configure func(*fakeHerdr, *Service)
		stage     string
		starts    int
		prompts   int
	}{
		{
			name: "resolve focused workspace",
			configure: func(client *fakeHerdr, _ *Service) {
				client.fail["focused"] = errors.New("socket unavailable")
			},
			stage: "resolve focused workspace",
		},
		{
			name: "no workspace holds focus",
			configure: func(client *fakeHerdr, _ *Service) {
				for index := range client.workspaces {
					client.workspaces[index].Focused = false
				}
			},
			stage: "resolve focused workspace",
		},
		{
			name: "tab pane never settles",
			configure: func(client *fakeHerdr, service *Service) {
				service.Config.Bootstrap.TimeoutSeconds = 1
				client.settleAfter = rootShellSettleAttempts + 1
			},
			stage: "resolve root pane",
		},
		{
			name: "create feature tab",
			configure: func(client *fakeHerdr, _ *Service) {
				client.fail["tab_create"] = errors.New("socket unavailable")
			},
			stage: "create feature tab",
		},
		{
			name: "root pane process lookup",
			configure: func(client *fakeHerdr, _ *Service) {
				client.fail["process"] = errors.New("pane disappeared")
			},
			stage: "inspect root pane",
		},
		{
			name: "primary start",
			configure: func(client *fakeHerdr, _ *Service) {
				client.fail["start"] = errors.New("Claude is unavailable")
			},
			stage: "start primary agent",
		},
		{
			name: "readiness timeout",
			configure: func(client *fakeHerdr, service *Service) {
				client.startedReady = false
				service.Config.Bootstrap.TimeoutSeconds = 0
			},
			stage:  "wait for primary agent",
			starts: 1,
		},
		{
			name: "agent identity",
			configure: func(client *fakeHerdr, _ *Service) {
				wrong := client.opened.RootPane
				wrong.Name = "ori-repo-bridge-builder"
				wrong.WorkspaceID = "other-workspace"
				wrong.Agent = "claude"
				wrong.InteractiveReady = true
				wrong.AgentSession = &model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "other"}
				client.startResult = &wrong
			},
			stage:  "resolve primary agent",
			starts: 1,
		},
		{
			name: "bootstrap prompt",
			configure: func(client *fakeHerdr, _ *Service) {
				client.fail["prompt"] = errors.New("prompt acknowledgement timed out")
			},
			stage:  "deliver bootstrap prompt",
			starts: 1,
		},
	}

	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := newFakeHerdr(path)
			store := newMemoryStore()
			service := newService(client, store, path)
			test.configure(client, service)
			_, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"})
			var stage *model.StageError
			if !errors.As(err, &stage) || stage.Stage != test.stage {
				t.Fatalf("Handoff() error = %#v, want stage %q", err, test.stage)
			}
			// The degradation contract (FR-32): every Herdr-side failure is a
			// classified StageError carrying a command the user can act on.
			// Without both, the shell can only say "something went wrong" while
			// holding a worktree that is actually ready to use.
			if stage.Code == "" || stage.Message == "" || stage.Recovery == "" {
				t.Fatalf("failure at %q is not actionable: code=%q message=%q recovery=%q", test.stage, stage.Code, stage.Message, stage.Recovery)
			}
			if client.startCalls != test.starts || client.promptCalls != test.prompts {
				t.Fatalf("calls after %s: starts=%d prompts=%d, want %d/%d", test.name, client.startCalls, client.promptCalls, test.starts, test.prompts)
			}
			if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
				t.Fatalf("handoff failure removed or changed checkout: %v", statErr)
			}
			state, loadErr := store.Load()
			if loadErr != nil || state.Features["repo-123456:bridge"].Feature.Path != path {
				t.Fatalf("handoff record was not retained: state=%#v err=%v", state, loadErr)
			}
		})
	}
}

func TestHandoffReusesLivePrimaryAndRequiresExplicitPromptResend(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	client := newFakeHerdr(path)
	client.opened.AlreadyOpen = true
	name, err := ScopedAgentName("repo-123456", "bridge", "builder")
	if err != nil {
		t.Fatal(err)
	}
	live := client.opened.RootPane
	live.Name = name
	live.Agent = "claude"
	live.InteractiveReady = true
	live.AgentStatus = model.AgentIdle
	live.AgentSession = &model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "restored-session"}
	client.agents = []herdr.AgentInfo{live}
	client.byName[name] = live
	service := newService(client, newMemoryStore(), path)
	request := HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"}
	if _, err := service.Handoff(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if client.startCalls != 0 || client.promptCalls != 1 {
		t.Fatalf("pre-existing primary calls: starts=%d prompts=%d", client.startCalls, client.promptCalls)
	}
	if _, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge", Resend: true}); err != nil {
		t.Fatal(err)
	}
	if client.startCalls != 0 || client.promptCalls != 2 {
		t.Fatalf("explicit resend calls: starts=%d prompts=%d", client.startCalls, client.promptCalls)
	}
}

func TestHandoffRejectsMismatchedPromptAcknowledgement(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	client := newFakeHerdr(path)
	wrong := client.opened.RootPane
	wrong.Name = "other-feature-builder"
	wrong.Agent = "claude"
	wrong.InteractiveReady = true
	client.promptResult = &wrong
	service := newService(client, newMemoryStore(), path)
	_, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"})
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Stage != "deliver bootstrap prompt" || stage.Code != model.ErrAgentAmbiguous {
		t.Fatalf("Handoff() error = %#v", err)
	}
}

func TestHandoffRejectsBusyOrGuessedRootPane(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	client := newFakeHerdr(path)
	client.process.ForegroundProcesses = append(client.process.ForegroundProcesses, herdr.ForegroundProcess{PID: 99, Name: "vim", Cwd: path})
	store := newMemoryStore()
	service := newService(client, store, path)
	_, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"})
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Stage != "resolve root pane" || client.startCalls != 0 {
		t.Fatalf("Handoff() = %v; starts=%d", err, client.startCalls)
	}
	state, loadErr := store.Load()
	if loadErr != nil || state.Features["repo-123456:bridge"].WorkspaceID != "w1" {
		t.Fatalf("busy root should retain the opened workspace for retry: state=%#v err=%v", state, loadErr)
	}
}

func TestHandoffResumesSavedPrimaryAfterItOwnsTheRootPane(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	client := newFakeHerdr(path)
	store := newMemoryStore()
	service := newService(client, store, path)
	request := HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"}
	if _, err := service.Handoff(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	feature := state.Features["repo-123456:bridge"]
	feature.Handoff.BootstrapPrompted = false
	feature.Handoff.Stage = model.HandoffPrimaryStarted
	state.Features["repo-123456:bridge"] = feature
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	client.process.ForegroundProcesses = append(client.process.ForegroundProcesses, herdr.ForegroundProcess{PID: 99, Name: "claude", Cwd: path})
	if _, err := service.Handoff(context.Background(), request); err != nil {
		t.Fatalf("saved-primary retry should not require an idle root shell: %v", err)
	}
	if client.startCalls != 1 || client.promptCalls != 2 {
		t.Fatalf("retry started a replacement or skipped the pending prompt: starts=%d prompts=%d", client.startCalls, client.promptCalls)
	}
}

func TestScopedAgentNameAndBootstrapPromptAreSafeAndSpecific(t *testing.T) {
	t.Parallel()
	left, err := ScopedAgentName("repo-abcdef", "very-long-feature-name-that-needs-truncation", "builder")
	if err != nil {
		t.Fatal(err)
	}
	right, err := ScopedAgentName("repo-abcdef", "very-long-feature-name-that-needs-truncation-2", "builder")
	if err != nil || left == right || len(left) > 32 || !strings.HasPrefix(left, "ori-") {
		t.Fatalf("scoped names = %q, %q, err=%v", left, right, err)
	}
	// No PRD or task list exists on disk at this path: the prompt must say so
	// honestly rather than naming a prd-bridge.md that was never written
	// (AR30) — a task-list-only feature must never look PRD-backed.
	prompt := BootstrapPrompt(model.Feature{Name: "bridge", Path: "/tmp/bridge"}, "builder")
	for _, want := range []string{"tasks/tasks-bridge.md", "wt pr", "wt done bridge", "[ ] to [x]", "PRD: none (task-list-sized)"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("BootstrapPrompt missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "tasks/prd-bridge.md") {
		t.Fatalf("BootstrapPrompt named a PRD that does not exist: %s", prompt)
	}
	if !strings.Contains(prompt, "No detailed task list was found") {
		t.Fatalf("BootstrapPrompt should safely describe a missing task list: %s", prompt)
	}
}

// TestBootstrapPromptNamesAnExistingPRDAndIssueSnapshot is the other half of
// AR30: when a PRD (and an Issue snapshot from wt plan) genuinely exist in
// the worktree, the prompt names them by their real path.
func TestBootstrapPromptNamesAnExistingPRDAndIssueSnapshot(t *testing.T) {
	t.Parallel()
	path := t.TempDir()
	if err := os.MkdirAll(filepath.Join(path, "tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "tasks", "prd-bridge.md"), []byte("# PRD\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "tasks", "issue-bridge.md"), []byte("# Issue\n"), 0600); err != nil {
		t.Fatal(err)
	}
	prompt := BootstrapPrompt(model.Feature{Name: "bridge", Path: path}, "builder")
	for _, want := range []string{
		"PRD: " + filepath.Join(path, "tasks", "prd-bridge.md"),
		"Issue snapshot: " + filepath.Join(path, "tasks", "issue-bridge.md"),
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("BootstrapPrompt missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "task-list-sized") {
		t.Fatalf("BootstrapPrompt called a PRD-backed feature task-list-sized: %s", prompt)
	}
}

func newService(client *fakeHerdr, store *memoryStore, path string) *Service {
	cfg := config.Default()
	cfg.Bootstrap.TimeoutSeconds = 3
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	return &Service{
		Config:       cfg,
		RepositoryID: "repo-123456",
		GitCommonDir: "/tmp/common.git",
		Client:       client,
		Store:        store,
		Inspector:    fakeInspector{worktree: worktree.GitWorktree{Path: path, Branch: "feature/bridge", CommonDir: "/tmp/common.git", SourcePath: "/tmp/source-checkout"}},
		Now:          func() time.Time { return now },
	}
}

func newFakeHerdr(path string) *fakeHerdr {
	root := herdr.PaneInfo{
		PaneID:        "w1:p1",
		TerminalID:    "term-1",
		WorkspaceID:   "w1",
		TabID:         "w1:t1",
		Cwd:           path,
		ForegroundCwd: path,
		AgentStatus:   model.AgentUnknown,
	}
	return &fakeHerdr{
		opened: herdr.WorktreeOpenResult{
			Type:      "worktree_opened",
			Workspace: herdr.WorkspaceInfo{WorkspaceID: "w1", Cwd: path},
			Tab:       herdr.TabInfo{TabID: "w1:t1", WorkspaceID: "w1"},
			RootPane:  root,
			Worktree:  herdr.WorktreeInfo{Path: path, Branch: "feature/bridge"},
		},
		workspaces: []herdr.WorkspaceInfo{
			{WorkspaceID: "w1", Label: "ori-agent-dev", Focused: true, ActiveTabID: "w1:t1"},
			{WorkspaceID: "w2", Label: "elsewhere"},
		},
		tabs:        make(map[string]herdr.TabInfo),
		settlePolls: make(map[string]int),
		process:     herdr.PaneProcessInfo{PaneID: "w1:p1", ShellPID: int64ptr(42), ForegroundProcesses: []herdr.ForegroundProcess{{PID: 42, Name: "zsh", Cwd: path}}},
		panes:       map[string]herdr.PaneInfo{"w1:p1": root},
		processByPane: map[string]herdr.PaneProcessInfo{
			"w1:p1": {PaneID: "w1:p1", ShellPID: int64ptr(42), ForegroundProcesses: []herdr.ForegroundProcess{{PID: 42, Name: "zsh", Cwd: path}}},
		},
		byName:       make(map[string]herdr.AgentInfo),
		startedReady: true,
		fail:         make(map[string]error),
	}
}

func int64ptr(value int64) *int64 { return &value }

// The point of the feature: features are tabs inside the workspace the user is
// already in. Nothing in the handoff path may reach for a workspace-creating
// call, because that is what produced one top-level workspace per branch.
func TestHandoffPlacesEachFeatureAsATabInTheFocusedWorkspace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first := filepath.Join(root, "alpha")
	second := filepath.Join(root, "beta")

	client := newFakeHerdr(first)
	store := newMemoryStore()

	alpha := newService(client, store, first)
	if _, err := alpha.Handoff(context.Background(), HandoffRequest{FeatureName: "alpha", WorktreePath: first, Branch: "feature/bridge"}); err != nil {
		t.Fatalf("first Handoff() error = %v", err)
	}

	beta := newService(client, store, second)
	beta.Inspector = fakeInspector{worktree: worktree.GitWorktree{Path: second, Branch: "feature/bridge", CommonDir: "/tmp/common.git", SourcePath: "/tmp/source-checkout"}}
	betaResult, err := beta.Handoff(context.Background(), HandoffRequest{FeatureName: "beta", WorktreePath: second, Branch: "feature/bridge"})
	if err != nil {
		t.Fatalf("second Handoff() error = %v", err)
	}

	if client.tabCreateCalls != 2 {
		t.Fatalf("tab creates = %d, want one per feature", client.tabCreateCalls)
	}
	if betaResult.WorkspaceID != "w1" || betaResult.TabID != "w1:t2" {
		t.Fatalf("second feature placement = workspace %q tab %q, want a second tab in w1", betaResult.WorkspaceID, betaResult.TabID)
	}
	// Two features, two tabs, two distinct panes — one shared workspace.
	state, _ := store.Load()
	alphaState, betaState := state.Features["repo-123456:alpha"], state.Features["repo-123456:beta"]
	if alphaState.WorkspaceID != betaState.WorkspaceID {
		t.Fatalf("features landed in different workspaces: %q vs %q", alphaState.WorkspaceID, betaState.WorkspaceID)
	}
	if alphaState.TabID == betaState.TabID || alphaState.TabID == "" || betaState.TabID == "" {
		t.Fatalf("features share or lack a tab: %q vs %q", alphaState.TabID, betaState.TabID)
	}
	if alphaState.Handoff.RootPaneID == betaState.Handoff.RootPaneID {
		t.Fatalf("features share pane %q", alphaState.Handoff.RootPaneID)
	}
	for _, call := range client.calls {
		if strings.HasPrefix(call, "worktree.") || strings.HasPrefix(call, "workspace.create") {
			t.Fatalf("handoff reached a workspace-creating API: %v", client.calls)
		}
	}
}

// A retry must re-enter the recorded tab. `worktree open` was idempotent;
// `tab create` is not, so without this a second `wt herd retry` would pile up
// an abandoned tab per attempt.
func TestHandoffRetryReusesTheRecordedTabInsteadOfCreatingAnother(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	client := newFakeHerdr(path)
	store := newMemoryStore()
	service := newService(client, store, path)
	request := HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"}
	if _, err := service.Handoff(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	retried, err := service.Handoff(context.Background(), request)
	if err != nil {
		t.Fatalf("retry Handoff() error = %v", err)
	}
	if client.tabCreateCalls != 1 {
		t.Fatalf("retry created %d tabs, want the original reused", client.tabCreateCalls)
	}
	if !retried.TabReused || retried.TabID != "w1:t1" {
		t.Fatalf("retry placement = %#v, want the recorded tab reused", retried)
	}
}

// If the user closes the feature's tab by hand, retry must rebuild it rather
// than failing on a stale identity.
func TestHandoffRecreatesTheTabWhenTheRecordedOneIsGone(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	client := newFakeHerdr(path)
	store := newMemoryStore()
	service := newService(client, store, path)
	request := HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"}
	if _, err := service.Handoff(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	// Close the tab and forget the agent, the way a user closing a tab does.
	delete(client.tabs, "w1:t1")
	delete(client.panes, "w1:p1")
	client.agents = nil
	client.byName = make(map[string]herdr.AgentInfo)
	state, _ := store.Load()
	feature := state.Features["repo-123456:bridge"]
	feature.Agents = make(map[string]model.RoleAgent)
	feature.Handoff.PrimaryAgentName = ""
	state.Features["repo-123456:bridge"] = feature
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}

	recovered, err := service.Handoff(context.Background(), request)
	if err != nil {
		t.Fatalf("Handoff() after the tab was closed = %v", err)
	}
	if client.tabCreateCalls != 2 || recovered.TabID != "w1:t2" {
		t.Fatalf("recovery = %d creates, tab %q; want a fresh tab", client.tabCreateCalls, recovered.TabID)
	}
	after, _ := store.Load()
	if after.Features["repo-123456:bridge"].TabID != "w1:t2" {
		t.Fatalf("state kept the closed tab: %#v", after.Features["repo-123456:bridge"])
	}
}

// Adoption predates tabs and must survive them. An agent already working in the
// worktree supplies its own placement, so no tab is created beside it.
func TestHandoffAdoptsAnExistingWorktreeAgentWithoutCreatingATab(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	client := newFakeHerdr(path)
	adopted := client.opened.RootPane
	adopted.PaneID = "w1:p9"
	adopted.TerminalID = "term-adopted"
	adopted.TabID = "w1:t9"
	adopted.Name = "human-started-claude"
	adopted.Agent = "claude"
	adopted.InteractiveReady = true
	adopted.AgentStatus = model.AgentIdle
	client.agents = []herdr.AgentInfo{adopted}
	client.byName[adopted.Name] = adopted
	client.panes[adopted.PaneID] = adopted

	store := newMemoryStore()
	service := newService(client, store, path)
	result, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"})
	if err != nil {
		t.Fatalf("Handoff() error = %v", err)
	}
	if client.tabCreateCalls != 0 {
		t.Fatalf("adoption created %d tabs beside the agent it adopted", client.tabCreateCalls)
	}
	if client.startCalls != 0 || result.Primary.Name != adopted.Name {
		t.Fatalf("adoption = %d starts, primary %q", client.startCalls, result.Primary.Name)
	}
	if result.TabID != "w1:t9" || result.RootPaneID != "w1:p9" {
		t.Fatalf("adopted placement = tab %q pane %q", result.TabID, result.RootPaneID)
	}
}

// FR-3: with no focused workspace the handoff reports a distinct, recoverable
// condition. It must never quietly fall back to minting a workspace.
func TestHandoffReportsNoFocusedWorkspaceWithoutCreatingOne(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	client := newFakeHerdr(path)
	for index := range client.workspaces {
		client.workspaces[index].Focused = false
	}
	service := newService(client, newMemoryStore(), path)
	_, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"})
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrNoFocusedWorkspace {
		t.Fatalf("Handoff() with no focus = %#v, want ErrNoFocusedWorkspace", err)
	}
	if client.tabCreateCalls != 0 || client.startCalls != 0 {
		t.Fatalf("unfocused handoff still mutated Herdr: tabs=%d starts=%d", client.tabCreateCalls, client.startCalls)
	}
}

// PRD open question 3: a focused workspace bound to another repository is a
// warning, not a refusal — refusing would strand the worktree over a placement
// that still works.
func TestHandoffWarnsWhenTheFocusedWorkspaceBelongsToAnotherRepository(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	client := newFakeHerdr(path)
	client.workspaces[0].Worktree = &herdr.WorktreeBinding{RepoRoot: "/tmp/some-other-repo", RepoName: "other-project"}
	service := newService(client, newMemoryStore(), path)
	result, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"})
	if err != nil {
		t.Fatalf("Handoff() error = %v", err)
	}
	if client.tabCreateCalls != 1 {
		t.Fatalf("cross-repository placement was refused: %d tab creates", client.tabCreateCalls)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "other-project") {
		t.Fatalf("warnings = %#v, want one naming the foreign repository", result.Warnings)
	}
}

// `tab create` returns as soon as the pane exists, so a brand-new tab's shell
// can still look busy for a beat. That transient must not fail the handoff —
// but the same guard must still refuse instantly for a pane we did not create,
// which is where a second agent would land on top of a working one.
func TestHandoffWaitsOutAStartingShellButRefusesABusyExistingPane(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")

	settling := newFakeHerdr(path)
	settling.settleAfter = 3
	service := newService(settling, newMemoryStore(), path)
	if _, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"}); err != nil {
		t.Fatalf("Handoff() failed on a shell that was merely starting: %v", err)
	}
	if settling.startCalls != 1 {
		t.Fatalf("settling handoff starts = %d, want 1", settling.startCalls)
	}

	// The same symptom on a pane the handoff adopted rather than created is a
	// genuinely busy pane, and is refused without waiting.
	busy := newFakeHerdr(path)
	name, err := ScopedAgentName("repo-123456", "bridge", "builder")
	if err != nil {
		t.Fatal(err)
	}
	occupied := busy.opened.RootPane
	occupied.TabID = "w1:t7"
	busy.tabs["w1:t7"] = herdr.TabInfo{TabID: "w1:t7", WorkspaceID: "w1"}
	busy.panes["w1:p1"] = occupied
	busy.processByPane["w1:p1"] = herdr.PaneProcessInfo{
		PaneID:              "w1:p1",
		ShellPID:            int64ptr(42),
		ForegroundProcesses: []herdr.ForegroundProcess{{PID: 99, Name: "vim", Cwd: path}},
	}
	busy.process = busy.processByPane["w1:p1"]

	store := newMemoryStore()
	seeded := model.NewBridgeState()
	seeded.Features["repo-123456:bridge"] = model.FeatureState{
		Feature: model.Feature{RepositoryID: "repo-123456", Name: "bridge", Branch: "feature/bridge", Path: path},
		TabID:   "w1:t7",
		Handoff: model.HandoffState{Stage: model.HandoffTabCreated, RootPaneID: "w1:p1", PrimaryRole: "builder", PrimaryKind: "claude", PrimaryAgentName: name},
	}
	if err := store.Save(seeded); err != nil {
		t.Fatal(err)
	}
	busyService := newService(busy, store, path)
	_, err = busyService.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"})
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Stage != "resolve root pane" {
		t.Fatalf("Handoff() into a busy adopted pane = %#v, want a refusal", err)
	}
	if busy.startCalls != 0 {
		t.Fatalf("an agent was launched into a busy pane")
	}
}

// FR-26 and PRD open question 4: an ad-hoc feature has no PRD and no checklist,
// so it gets an agent but no bootstrap prompt — there is nothing truthful for
// one to point at. The decision is persisted, because a later retry knows
// nothing about how the feature was created and would otherwise send a prompt
// naming planning documents that were never going to exist.
func TestAdHocHandoffStartsAnAgentWithoutABootstrapPrompt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bridge")
	client := newFakeHerdr(path)
	store := newMemoryStore()
	service := newService(client, store, path)
	request := HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge", SkipPrompt: true}

	first, err := service.Handoff(context.Background(), request)
	if err != nil {
		t.Fatalf("Handoff() error = %v", err)
	}
	if client.startCalls != 1 {
		t.Fatalf("ad-hoc handoff started %d agents, want 1", client.startCalls)
	}
	if client.promptCalls != 0 || first.PromptDelivered || !first.PromptSkipped {
		t.Fatalf("ad-hoc handoff prompted: prompts=%d result=%#v", client.promptCalls, first)
	}
	if first.TabID == "" {
		t.Fatalf("ad-hoc handoff got no tab: %#v", first)
	}

	stored, _ := store.Load()
	if !stored.Features["repo-123456:bridge"].Handoff.SkipBootstrapPrompt {
		t.Fatalf("the no-prompt decision was not persisted: %#v", stored.Features["repo-123456:bridge"].Handoff)
	}

	// A retry carries no SkipPrompt of its own; the recorded decision is what
	// keeps it quiet. --resend must not talk it round either.
	retried, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge", Resend: true})
	if err != nil {
		t.Fatalf("retry Handoff() error = %v", err)
	}
	if client.promptCalls != 0 || !retried.PromptSkipped {
		t.Fatalf("retry prompted an ad-hoc feature: prompts=%d result=%#v", client.promptCalls, retried)
	}
}
