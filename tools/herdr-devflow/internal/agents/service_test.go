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
	opened           herdr.WorktreeOpenResult
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
	startedReady     bool
	startResult      *herdr.AgentInfo
	promptResult     *herdr.AgentInfo
	readText         string
	focusTarget      string
	lastPromptTarget string
	lastPromptText   string
	lastOpenSource   string
	lastOpenPath     string
	calls            []string
	fail             map[string]error
}

func (f *fakeHerdr) OpenExistingWorktree(_ context.Context, source, path string) (herdr.WorktreeOpenResult, error) {
	f.lastOpenSource = source
	f.lastOpenPath = path
	f.record("worktree.open")
	return f.opened, f.fail["open"]
}

func (f *fakeHerdr) PaneProcessInfo(_ context.Context, paneID string) (herdr.PaneProcessInfo, error) {
	f.record("pane.process-info:" + paneID)
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

func (f *fakeHerdr) ReportWorkspaceMetadata(_ context.Context, _, _ string, _ map[string]string) (json.RawMessage, error) {
	f.record("workspace.report-metadata")
	f.metadataCalls++
	return json.RawMessage(`{"type":"workspace_metadata"}`), f.fail["workspace_metadata"]
}

func (f *fakeHerdr) ReportPaneMetadata(_ context.Context, _, _ string, _ map[string]string) (json.RawMessage, error) {
	f.record("pane.report-metadata")
	f.metadataCalls++
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
	if client.lastOpenSource != "/tmp/source-checkout" || client.lastOpenPath != path {
		t.Fatalf("handoff source/path = %q / %q", client.lastOpenSource, client.lastOpenPath)
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
		filepath.Join(path, "tasks", "prd-bridge.md"),
		filepath.Join(path, "tasks", "tasks-bridge.md"),
		"Next incomplete checklist item: 2.1 Deliver the scheduled continuation",
		"wt done bridge",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("ContinuationPrompt() missing %q:\n%s", want, prompt)
		}
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
			name: "open existing worktree",
			configure: func(client *fakeHerdr, _ *Service) {
				client.fail["open"] = errors.New("socket unavailable")
			},
			stage: "open existing worktree",
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
	prompt := BootstrapPrompt(model.Feature{Name: "bridge", Path: "/tmp/bridge"}, "builder")
	for _, want := range []string{"tasks/prd-bridge.md", "tasks/tasks-bridge.md", "wt pr", "wt done bridge", "[ ] to [x]"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("BootstrapPrompt missing %q: %s", want, prompt)
		}
	}
	if !strings.Contains(prompt, "No detailed task list was found") {
		t.Fatalf("BootstrapPrompt should safely describe a missing task list: %s", prompt)
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
		process: herdr.PaneProcessInfo{PaneID: "w1:p1", ShellPID: int64ptr(42), ForegroundProcesses: []herdr.ForegroundProcess{{PID: 42, Name: "zsh", Cwd: path}}},
		panes:   map[string]herdr.PaneInfo{"w1:p1": root},
		processByPane: map[string]herdr.PaneProcessInfo{
			"w1:p1": {PaneID: "w1:p1", ShellPID: int64ptr(42), ForegroundProcesses: []herdr.ForegroundProcess{{PID: 42, Name: "zsh", Cwd: path}}},
		},
		byName:       make(map[string]herdr.AgentInfo),
		startedReady: true,
		fail:         make(map[string]error),
	}
}

func int64ptr(value int64) *int64 { return &value }
