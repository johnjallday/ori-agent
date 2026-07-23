package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	opened        herdr.WorktreeOpenResult
	process       herdr.PaneProcessInfo
	agents        []herdr.AgentInfo
	byName        map[string]herdr.AgentInfo
	startCalls    int
	getCalls      int
	promptCalls   int
	metadataCalls int
	startedReady  bool
	startResult   *herdr.AgentInfo
	promptResult  *herdr.AgentInfo
	fail          map[string]error
}

func (f *fakeHerdr) OpenExistingWorktree(_ context.Context, _ string) (herdr.WorktreeOpenResult, error) {
	return f.opened, f.fail["open"]
}

func (f *fakeHerdr) PaneProcessInfo(_ context.Context, _ string) (herdr.PaneProcessInfo, error) {
	return f.process, f.fail["process"]
}

func (f *fakeHerdr) AgentListInfo(_ context.Context) ([]herdr.AgentInfo, error) {
	return append([]herdr.AgentInfo(nil), f.agents...), f.fail["list"]
}

func (f *fakeHerdr) AgentGetInfo(_ context.Context, target string) (herdr.AgentInfo, error) {
	f.getCalls++
	if err := f.fail["get"]; err != nil {
		return herdr.AgentInfo{}, err
	}
	agent, ok := f.byName[target]
	if !ok {
		return herdr.AgentInfo{}, &model.StageError{Stage: "Herdr API", Code: model.ErrAgentMissing, Message: "agent not found"}
	}
	return agent, nil
}

func (f *fakeHerdr) AgentStartInfo(_ context.Context, name, kind, paneID string, _ time.Duration) (herdr.AgentInfo, error) {
	if err := f.fail["start"]; err != nil {
		return herdr.AgentInfo{}, err
	}
	f.startCalls++
	agent := f.opened.RootPane
	agent.Name = name
	agent.Agent = kind
	agent.PaneID = paneID
	agent.InteractiveReady = f.startedReady
	agent.LaunchPending = !f.startedReady
	agent.AgentStatus = model.AgentIdle
	agent.AgentSession = &model.NativeSession{Source: "herdr:claude", Agent: "claude", Kind: "id", Value: "session-1"}
	if f.startResult != nil {
		agent = *f.startResult
	}
	f.byName[name] = agent
	return agent, nil
}

func (f *fakeHerdr) AgentPromptInfo(_ context.Context, target, _ string, _ time.Duration) (herdr.AgentInfo, error) {
	if err := f.fail["prompt"]; err != nil {
		return herdr.AgentInfo{}, err
	}
	f.promptCalls++
	if f.promptResult != nil {
		return *f.promptResult, nil
	}
	return f.byName[target], nil
}

func (f *fakeHerdr) ReportWorkspaceMetadata(_ context.Context, _, _ string, _ map[string]string) (json.RawMessage, error) {
	f.metadataCalls++
	return json.RawMessage(`{"type":"workspace_metadata"}`), f.fail["workspace_metadata"]
}

func (f *fakeHerdr) ReportPaneMetadata(_ context.Context, _, _ string, _ map[string]string) (json.RawMessage, error) {
	f.metadataCalls++
	return json.RawMessage(`{"type":"pane_metadata"}`), f.fail["pane_metadata"]
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
	if !first.PromptDelivered || client.metadataCalls != 2 {
		t.Fatalf("first handoff delivery/metadata = %#v, %d", first, client.metadataCalls)
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
	if feature.Handoff.Stage != model.HandoffPrompted || !feature.Handoff.BootstrapPrompted || feature.Agents["builder"].NativeSession.Value != "session-1" {
		t.Fatalf("stored handoff state = %#v", feature)
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
	service := newService(client, newMemoryStore(), path)
	_, err := service.Handoff(context.Background(), HandoffRequest{FeatureName: "bridge", WorktreePath: path, Branch: "feature/bridge"})
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Stage != "resolve root pane" || client.startCalls != 0 {
		t.Fatalf("Handoff() = %v; starts=%d", err, client.startCalls)
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
		Inspector:    fakeInspector{worktree: worktree.GitWorktree{Path: path, Branch: "feature/bridge", CommonDir: "/tmp/common.git"}},
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
		process:      herdr.PaneProcessInfo{PaneID: "w1:p1", ShellPID: int64ptr(42), ForegroundProcesses: []herdr.ForegroundProcess{{PID: 42, Name: "zsh", Cwd: path}}},
		byName:       make(map[string]herdr.AgentInfo),
		startedReady: true,
		fail:         make(map[string]error),
	}
}

func int64ptr(value int64) *int64 { return &value }
