package reapersetup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/pluginworkspace"
	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type runtimeTestSource struct {
	ws     *workspace.Workspace
	folder string
}

func (s *runtimeTestSource) GetFolderWorkspace(id string) (*workspace.Workspace, error) {
	if s.ws == nil || s.ws.ID != id {
		return nil, errors.New("workspace not found")
	}
	return s.ws, nil
}

func (s *runtimeTestSource) GetFolderPath(id string) (string, error) {
	if s.ws == nil || s.ws.ID != id {
		return "", errors.New("workspace not found")
	}
	return s.folder, nil
}

type mutablePluginInspector struct {
	result pluginworkspace.PluginResult
	err    error
}

func (f *mutablePluginInspector) Inspect(string, []string) ([]pluginworkspace.PluginResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []pluginworkspace.PluginResult{f.result}, nil
}

type fixedRuntimeAgentProvider struct {
	provider string
	isCLI    bool
	err      error
}

type runtimeTestRoot string

func (r runtimeTestRoot) Resolve() (string, error) { return string(r), nil }

type failingRuntimeTestRoot struct{ err error }

func (r failingRuntimeTestRoot) Resolve() (string, error) { return "", r.err }

func (f fixedRuntimeAgentProvider) ProviderForAgent(context.Context, string, workspace.AgentInstance) (string, bool, error) {
	return f.provider, f.isCLI, f.err
}

type matrixProbes struct {
	application  ApplicationObservation
	web          WebRemoteObservation
	runner       RunnerObservation
	transport    LiveTransportObservation
	verify       VerificationObservation
	appCalls     int
	verifyCalls  int
	verifiedPort int
}

func (p *matrixProbes) DetectApplication(context.Context) ApplicationObservation {
	p.appCalls++
	return p.application
}
func (p *matrixProbes) DetectWebRemote(context.Context) WebRemoteObservation { return p.web }
func (p *matrixProbes) DetectRunner(context.Context) RunnerObservation       { return p.runner }
func (p *matrixProbes) CheckTransport(context.Context, WebRemoteObservation) LiveTransportObservation {
	return p.transport
}
func (p *matrixProbes) VerifyProject(_ context.Context, target VerificationTarget) VerificationObservation {
	p.verifyCalls++
	p.verifiedPort = target.WebRemote.Port
	return p.verify
}

func runtimeAdapterFixture(t *testing.T) (*RuntimeAdapter, *runtimeTestSource, *mutablePluginInspector, *matrixProbes, runtimecapability.EvaluationRequest) {
	t.Helper()
	folder := t.TempDir()
	projectDir := filepath.Join(folder, "song")
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "song.rpp"), []byte("<REAPER_PROJECT\n>"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song", Agents: []string{"Reaper Producer"}})
	ws.ProjectPath = "song"
	if err := workspace.SetProjectEntryPath(ws.SharedData, "song.rpp"); err != nil {
		t.Fatal(err)
	}
	instance := ws.AgentInstances[0]
	ws.Tasks = append(ws.Tasks, workspace.Task{
		ID: "setup", To: instance.Name, AssignedNodeID: instance.NodeID,
		Status: workspace.TaskStatusPending, Context: map[string]any{TaskContextTemplateSetup: true},
	})
	ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: ModeOriAssisted})
	if _, err := ws.GrantRuntimeCapability(ReaperLiveControlCapability, instance.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	source := &runtimeTestSource{ws: ws, folder: folder}
	plugins := &mutablePluginInspector{result: attachedPlugin()}
	probes := &matrixProbes{
		application: ApplicationObservation{State: ProbeReady},
		web:         WebRemoteObservation{State: ProbeReady, Port: 2307},
		runner:      RunnerObservation{State: ProbeReady, Root: folder, CommandID: "_RS123"},
		transport:   LiveTransportObservation{State: TransportAvailable},
		verify:      VerificationObservation{State: VerificationSucceeded},
	}
	set := ProbeSet{Application: probes, WebRemote: probes, Runner: probes, Transport: probes, Verifier: probes}
	adapter := NewRuntimeAdapter(source, plugins, fixedRuntimeAgentProvider{provider: "codex", isCLI: true}, runtimeTestRoot(folder), set)
	verified := time.Now().UTC().Add(-time.Hour)
	request := runtimecapability.EvaluationRequest{
		WorkspaceID: ws.ID,
		Mode:        workspace.RuntimeOperatingMode{ID: ModeOriAssisted, Label: "Assisted", Description: "Control REAPER.", Requires: []string{ReaperLiveControlCapability}},
		Requirement: workspace.RuntimeRequirement{Key: ReaperLiveControlCapability, Label: "REAPER", Description: "Control REAPER.", Adapter: ReaperLiveControlCapability},
		Persisted:   workspace.RuntimeRequirementState{RequirementKey: ReaperLiveControlCapability, ConfigurationState: runtimecapability.DurableConfigured, FirstVerifiedAt: &verified, LastVerifiedAt: &verified},
	}
	return adapter, source, plugins, probes, request
}

func TestRuntimeAdapterGrantAcceptsOnlySupportedCLIAndTrustedRoot(t *testing.T) {
	request := runtimecapability.GrantValidationRequest{
		WorkspaceID: "ws", Mode: workspace.RuntimeOperatingMode{ID: ModeOriAssisted},
		Requirement: workspace.RuntimeRequirement{Key: ReaperLiveControlCapability, Adapter: ReaperLiveControlCapability},
		Agent:       workspace.AgentInstance{ID: "agent", Name: "Producer"},
	}
	for _, provider := range []string{"codex", "claude_code"} {
		adapter := NewRuntimeAdapter(nil, nil, fixedRuntimeAgentProvider{provider: provider, isCLI: true}, runtimeTestRoot(t.TempDir()), ProbeSet{})
		if err := adapter.ValidateGrant(context.Background(), request); err != nil {
			t.Errorf("%s grant: %v", provider, err)
		}
	}
	for _, test := range []struct {
		name   string
		agents CLIAgentProviderResolver
		root   RunnerRootResolver
	}{
		{name: "cloud provider", agents: fixedRuntimeAgentProvider{provider: "openai"}, root: runtimeTestRoot(t.TempDir())},
		{name: "not CLI", agents: fixedRuntimeAgentProvider{provider: "codex"}, root: runtimeTestRoot(t.TempDir())},
		{name: "agent lookup failed", agents: fixedRuntimeAgentProvider{err: errors.New("missing")}, root: runtimeTestRoot(t.TempDir())},
		{name: "runner unavailable", agents: fixedRuntimeAgentProvider{provider: "codex", isCLI: true}, root: failingRuntimeTestRoot{err: ErrRunnerRootUnavailable}},
		{name: "no agent resolver", root: runtimeTestRoot(t.TempDir())},
		{name: "no root resolver", agents: fixedRuntimeAgentProvider{provider: "codex", isCLI: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := NewRuntimeAdapter(nil, nil, test.agents, test.root, ProbeSet{})
			if err := adapter.ValidateGrant(context.Background(), request); err == nil {
				t.Fatal("unsafe grant accepted")
			}
		})
	}
}

func TestRuntimeAdapterStatusMatrixAndPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*RuntimeAdapter, *runtimeTestSource, *mutablePluginInspector, *matrixProbes)
		live       bool
		wantReason string
		wantLive   string
	}{
		{name: "unsupported platform", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, _ *mutablePluginInspector, p *matrixProbes) {
			p.application.State = ProbeUnsupported
		}, wantReason: ReasonUnsupportedPlatform},
		{name: "application missing", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, _ *mutablePluginInspector, p *matrixProbes) {
			p.application.State = ProbeMissing
		}, wantReason: ReasonApplicationMissing},
		{name: "application unknown", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, _ *mutablePluginInspector, p *matrixProbes) {
			p.application.State = ProbeUnknown
		}, wantReason: ReasonApplicationUnknown},
		{name: "web remote absent", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, _ *mutablePluginInspector, p *matrixProbes) {
			p.web.State = ProbeMissing
		}, wantReason: ReasonWebRemoteMissing},
		{name: "runner missing", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, _ *mutablePluginInspector, p *matrixProbes) {
			p.runner.State = ProbeMissing
		}, wantReason: ReasonRunnerMissing},
		{name: "runner invalid", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, _ *mutablePluginInspector, p *matrixProbes) {
			p.runner.State = ProbeInvalid
		}, wantReason: ReasonRunnerInvalid},
		{name: "plugin missing", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, plugins *mutablePluginInspector, _ *matrixProbes) {
			plugins.result = pluginworkspace.PluginResult{Name: ReaperPluginName, State: pluginworkspace.PluginStateMissing}
		}, wantReason: ReasonPluginMissing},
		{name: "plugin disabled", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, plugins *mutablePluginInspector, _ *matrixProbes) {
			plugins.result.Enabled = false
			plugins.result.State = pluginworkspace.PluginStateDisabled
		}, wantReason: ReasonPluginDisabled},
		{name: "plugin detached", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, plugins *mutablePluginInspector, _ *matrixProbes) {
			plugins.result.State = pluginworkspace.PluginStateDetached
			plugins.result.Missing = []pluginworkspace.Component{{Kind: pluginworkspace.ComponentSkill, Name: "reaper-web-remote"}}
		}, wantReason: ReasonPluginDetached},
		{name: "incompatible agent", mutate: func(adapter *RuntimeAdapter, _ *runtimeTestSource, _ *mutablePluginInspector, _ *matrixProbes) {
			adapter.agents = fixedRuntimeAgentProvider{provider: "openai"}
		}, wantReason: ReasonCLIAgentRequired},
		{name: "grant missing", mutate: func(_ *RuntimeAdapter, source *runtimeTestSource, _ *mutablePluginInspector, _ *matrixProbes) {
			state := source.ws.GetRuntimeState()
			state.Grants = nil
			source.ws.SetRuntimeState(state)
		}, wantReason: ReasonGrantMissing},
		{name: "grant denied", mutate: func(_ *RuntimeAdapter, source *runtimeTestSource, _ *mutablePluginInspector, _ *matrixProbes) {
			instance := source.ws.AgentInstances[0]
			_, _ = source.ws.RevokeRuntimeCapability(ReaperLiveControlCapability, instance.ID, time.Now())
		}, wantReason: ReasonGrantDenied},
		{name: "application offline is transient", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, _ *mutablePluginInspector, p *matrixProbes) {
			p.transport.State = TransportOffline
		}, live: true, wantReason: ReasonReaperOffline, wantLive: runtimecapability.LiveOffline},
		{name: "wrong project", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, _ *mutablePluginInspector, p *matrixProbes) {
			p.verify.State = VerificationWrongProject
		}, live: true, wantReason: ReasonWrongProject, wantLive: runtimecapability.LiveWrongTarget},
		{name: "verification timeout", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, _ *mutablePluginInspector, p *matrixProbes) {
			p.verify.State = VerificationTimedOut
		}, live: true, wantReason: ReasonVerificationTimeout, wantLive: runtimecapability.LiveCheckFailed},
		{name: "runner failure", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, _ *mutablePluginInspector, p *matrixProbes) {
			p.verify.State = VerificationRunnerFailed
		}, live: true, wantReason: ReasonRunnerFailure, wantLive: runtimecapability.LiveCheckFailed},
		{name: "write denied", mutate: func(_ *RuntimeAdapter, _ *runtimeTestSource, _ *mutablePluginInspector, p *matrixProbes) {
			p.verify.State = VerificationPermissionDenied
		}, live: true, wantReason: ReasonWriteDenied, wantLive: runtimecapability.LiveUnavailable},
		{name: "success", mutate: func(*RuntimeAdapter, *runtimeTestSource, *mutablePluginInspector, *matrixProbes) {}, live: true, wantReason: "connected", wantLive: runtimecapability.LiveAvailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, source, plugins, probes, request := runtimeAdapterFixture(t)
			test.mutate(adapter, source, plugins, probes)
			if test.live {
				got, err := adapter.CheckLive(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				if got.State != test.wantLive || got.ReasonCode != test.wantReason {
					t.Fatalf("live = %+v, want state=%s reason=%s", got, test.wantLive, test.wantReason)
				}
				return
			}
			got, err := adapter.EvaluateDurable(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if got.ReasonCode != test.wantReason || got.State != runtimecapability.DurableNeedsAttention {
				t.Fatalf("durable = %+v, want needs_attention/%s", got, test.wantReason)
			}
			if probes.verifyCalls != 0 {
				t.Fatal("durable evaluation must not trigger the runner")
			}
		})
	}
}

func TestRuntimeAdapter_FirstVerificationAndRegressionAreSeparateFromLiveState(t *testing.T) {
	adapter, _, plugins, probes, request := runtimeAdapterFixture(t)
	request.Persisted.FirstVerifiedAt = nil
	request.Persisted.LastVerifiedAt = nil
	got, err := adapter.EvaluateDurable(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != runtimecapability.DurableConfigured || !got.VerificationRequired {
		t.Fatalf("pre-verification durable result = %+v", got)
	}

	request.Persisted.FirstVerifiedAt = ptrTime(time.Now())
	plugins.result.State = pluginworkspace.PluginStateDetached
	plugins.result.Missing = []pluginworkspace.Component{{Kind: pluginworkspace.ComponentSkill, Name: "reaper-web-remote"}}
	regressed, _ := adapter.EvaluateDurable(context.Background(), request)
	if regressed.State != runtimecapability.DurableNeedsAttention || regressed.ReasonCode != ReasonPluginDetached {
		t.Fatalf("regressed = %+v", regressed)
	}

	plugins.result = attachedPlugin()
	probes.transport.State = TransportOffline
	offline, _ := adapter.CheckLive(context.Background(), request)
	if offline.State != runtimecapability.LiveOffline {
		t.Fatalf("offline = %+v", offline)
	}
	durable, _ := adapter.EvaluateDurable(context.Background(), request)
	if durable.State != runtimecapability.DurableConfigured {
		t.Fatalf("offline must not regress durable state: %+v", durable)
	}
}

func TestRuntimeAdapterVerifyRechecksPrerequisitesBeforeAndAfter(t *testing.T) {
	adapter, _, _, probes, request := runtimeAdapterFixture(t)
	request.Persisted.FirstVerifiedAt = nil
	probes.web.Ports = []int{2307, 2308}
	probes.transport.Port = 2308
	got, err := adapter.Verify(context.Background(), runtimecapability.VerificationRequest{EvaluationRequest: request})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Succeeded || probes.verifyCalls != 1 || probes.appCalls < 2 {
		t.Fatalf("verify = %+v, app calls=%d runner calls=%d", got, probes.appCalls, probes.verifyCalls)
	}
	if probes.verifiedPort != 2308 {
		t.Fatalf("verifier used port %d, want responsive configured port 2308", probes.verifiedPort)
	}
}

func TestTrustedVerificationScriptContainsOnlyNoOpProjectQuery(t *testing.T) {
	script := trustedVerificationScript(strings.Repeat("a", 32))
	if !strings.Contains(script, "reaper.EnumProjects") {
		t.Fatal("verification script must read current project identity")
	}
	for _, forbidden := range []string{
		"SetCurrentBPM", "SetTempoTimeSigMarker", "InsertTrack", "DeleteTrack", "AddProjectMarker",
		"SetMediaTrackInfo", "TrackFX_", "OnPlayButton", "OnStopButton", "Main_SaveProject",
		"Undo_BeginBlock", "Undo_EndBlock", "UpdateArrange",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("verification script contains mutating API %q", forbidden)
		}
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
