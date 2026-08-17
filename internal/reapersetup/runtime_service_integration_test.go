package reapersetup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type runtimeAdapterStore struct {
	ws     *workspace.Workspace
	folder string
}

func (s *runtimeAdapterStore) GetFolderWorkspace(string) (*workspace.Workspace, error) {
	return s.ws, nil
}
func (s *runtimeAdapterStore) GetFolderPath(string) (string, error) { return s.folder, nil }
func (s *runtimeAdapterStore) Update(_ string, mutate func(*workspace.Workspace) error) error {
	return mutate(s.ws)
}

func TestRuntimeServiceRecordsVerificationAndPreservesItWhileOffline(t *testing.T) {
	folder := t.TempDir()
	projectDir := filepath.Join(folder, "song")
	if err := os.Mkdir(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "song.rpp"), []byte("project"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Song", Agents: []string{"Reaper Producer"}})
	ws.ProjectPath = "song"
	if err := workspace.SetProjectEntryPath(ws.SharedData, "song.rpp"); err != nil {
		t.Fatal(err)
	}
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{RuntimeRequirements: &workspace.RuntimeRequirementsContract{
		SchemaVersion: workspace.RuntimeRequirementsSchemaVersion,
		OperatingModes: []workspace.RuntimeOperatingMode{
			{ID: ModeFileOnly, Label: "File only", Description: "Edit the project file."},
			{ID: ModeOriAssisted, Label: "Assisted", Description: "Control REAPER.", Requires: []string{ReaperLiveControlCapability}},
		},
		Requirements: []workspace.RuntimeRequirement{{Key: ReaperLiveControlCapability, Label: "REAPER", Description: "Control REAPER.", Adapter: ReaperLiveControlCapability}},
	}})
	ws.Tasks = append(ws.Tasks, workspace.Task{ID: "setup", To: "Reaper Producer", AssignedNodeID: ws.AgentInstances[0].NodeID, Status: workspace.TaskStatusPending, Context: map[string]any{TaskContextTemplateSetup: true}})
	ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{SelectedModeID: ModeOriAssisted})
	if _, err := ws.GrantRuntimeCapability(ReaperLiveControlCapability, ws.AgentInstances[0].ID, time.Now()); err != nil {
		t.Fatal(err)
	}

	store := &runtimeAdapterStore{ws: ws, folder: folder}
	plugins := &mutablePluginInspector{result: attachedPlugin()}
	probes := &matrixProbes{
		application: ApplicationObservation{State: ProbeReady},
		web:         WebRemoteObservation{State: ProbeReady, Port: 2307},
		runner:      RunnerObservation{State: ProbeReady, Root: folder, CommandID: "_RS123"},
		transport:   LiveTransportObservation{State: TransportAvailable},
		verify:      VerificationObservation{State: VerificationSucceeded},
	}
	adapter := NewRuntimeAdapter(store, plugins, fixedRuntimeAgentProvider{provider: "codex", isCLI: true}, runtimeTestRoot(folder), ProbeSet{
		Application: probes, WebRemote: probes, Runner: probes, Transport: probes, Verifier: probes,
	})
	registry := runtimecapability.NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	service := runtimecapability.NewService(store, registry)

	fileOnly, err := service.SelectMode(context.Background(), ws.ID, ModeFileOnly)
	if err != nil {
		t.Fatal(err)
	}
	if fileOnly.DurableState != runtimecapability.DurableConfigured || fileOnly.LiveState != runtimecapability.LiveNotApplicable || probes.appCalls != 0 {
		t.Fatalf("file-only should complete without REAPER probes: %+v, app calls=%d", fileOnly, probes.appCalls)
	}
	if _, err := service.SelectMode(context.Background(), ws.ID, ModeOriAssisted); err != nil {
		t.Fatal(err)
	}

	before, err := service.Status(context.Background(), ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.DurableState != runtimecapability.DurableInProgress || before.FirstVerifiedAt != nil || before.FirstBlocker == nil || before.FirstBlocker.ReasonCode != runtimecapability.ReasonVerificationRequired {
		t.Fatalf("before = %+v", before)
	}
	claimed, blocked := service.EvaluateTaskCapability(ws.ID, ReaperLiveControlCapability)
	if !claimed || blocked == nil || blocked.Repair == nil || blocked.Repair.Code != "test_reaper_connection" {
		t.Fatalf("pre-verification task block = claimed:%v blocked:%+v", claimed, blocked)
	}

	verified, err := service.Verify(context.Background(), ws.ID, ReaperLiveControlCapability)
	if err != nil {
		t.Fatal(err)
	}
	if verified.DurableState != runtimecapability.DurableConfigured || verified.LiveState != runtimecapability.LiveAvailable || verified.FirstVerifiedAt == nil || verified.LastVerifiedAt == nil {
		t.Fatalf("verified = %+v", verified)
	}
	first := *verified.FirstVerifiedAt
	assigned := workspace.Task{To: "Reaper Producer", AssignedNodeID: ws.AgentInstances[0].NodeID, RequiredCapabilities: []string{ReaperLiveControlCapability}}
	if claimed, blocked := service.EvaluateTaskCapabilityForTask(ws.ID, assigned, ReaperLiveControlCapability); !claimed || blocked != nil {
		t.Fatalf("connected task preflight = claimed:%v blocked:%+v", claimed, blocked)
	}
	other := workspace.AgentInstance{ID: "other-agent", Name: "Other Producer", NodeID: "other-node"}
	ws.AgentInstances = append(ws.AgentInstances, other)
	wrongAgent := workspace.Task{To: other.Name, AssignedNodeID: other.NodeID, RequiredCapabilities: []string{ReaperLiveControlCapability}}
	verifyCalls := probes.verifyCalls
	if claimed, blocked := service.EvaluateTaskCapabilityForTask(ws.ID, wrongAgent, ReaperLiveControlCapability); !claimed || blocked == nil || blocked.ReasonCode != runtimecapability.ReasonTaskGrantRequired || blocked.Repair == nil || blocked.Repair.Code != "grant_reaper_access" {
		t.Fatalf("wrong-agent preflight = claimed:%v blocked:%+v", claimed, blocked)
	}
	if probes.verifyCalls != verifyCalls {
		t.Fatal("wrong-agent preflight should block before live runner verification")
	}

	probes.transport.State = TransportOffline
	offline, err := service.Recheck(context.Background(), ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if offline.DurableState != runtimecapability.DurableConfigured || offline.LiveState != runtimecapability.LiveOffline || offline.FirstVerifiedAt == nil || !offline.FirstVerifiedAt.Equal(first) {
		t.Fatalf("offline = %+v", offline)
	}
	if _, blocked := service.EvaluateTaskCapability(ws.ID, ReaperLiveControlCapability); blocked == nil || blocked.Repair == nil || blocked.Repair.Code != "open_check_reaper" {
		t.Fatalf("offline task block = %+v", blocked)
	}

	probes.transport.State = TransportAvailable
	probes.verify.State = VerificationWrongProject
	if _, blocked := service.EvaluateTaskCapability(ws.ID, ReaperLiveControlCapability); blocked == nil || blocked.Repair == nil || blocked.Repair.Code != "open_correct_project" || blocked.ReasonCode != ReasonWrongProject {
		t.Fatalf("wrong-project task block = %+v", blocked)
	}

	if _, err := service.SelectMode(context.Background(), ws.ID, ModeFileOnly); err != nil {
		t.Fatal(err)
	}
	if _, blocked := service.EvaluateTaskCapability(ws.ID, ReaperLiveControlCapability); blocked == nil || blocked.Repair == nil || blocked.Repair.Code != "review_runtime_setup" {
		t.Fatalf("file-only live task block = %+v", blocked)
	}
}
