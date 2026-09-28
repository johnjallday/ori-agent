package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func portableWorkspaceFixture(t *testing.T) ([]byte, workspacecontinuity.Record) {
	t.Helper()
	at := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Portable work", Agents: []string{"Guide"}})
	ws.OwnerUserID, ws.Version, ws.CreatedAt, ws.UpdatedAt = "local", 91, at, at.Add(time.Hour)
	ws.FolderSlug, ws.ProjectPath, ws.Designation = "portable-work", "/old/machine/project", "personal_hq"
	ws.MissionEnabled = true
	ws.SharedData["authored_number"] = json.Number("9007199254740993")
	ws.TicketSequence = 12
	ws.Tasks = []Task{
		{ID: "completed", WorkspaceID: ws.ID, Description: "Already done", CreatedAt: at, AssignmentMode: TaskAssignmentModeLegacyUnknown,
			Status: TaskStatusCompleted, Result: "Exact <result>", TicketNumber: 11, TicketVersion: 42},
		{ID: "unfinished", WorkspaceID: ws.ID, Description: "In progress", CreatedAt: at,
			Status: TaskStatusInProgress, CurrentRunID: "source-process", ScheduleEnabled: true, WakeMacEnabled: true,
			TicketNumber: 12, ExecutionSteps: []TaskExecutionStep{{ID: "step", Status: TaskExecutionStepInProgress}}},
	}
	ws.Messages = []AgentMessage{{ID: "collaboration", From: "Guide", Content: "Historical <script>text</script>", Timestamp: at}}
	ws.Attachments = []Attachment{{ID: "note", WorkspaceID: ws.ID, Type: AttachmentTypeDoc, Title: "Saved note", Body: "Exact note", CreatedAt: at, UpdatedAt: at}}
	ws.DirectoryReferences = []DirectoryReference{{ID: "external", WorkspaceID: ws.ID, Path: "/old/external", Name: "Reference", CreatedAt: at, UpdatedAt: at}}
	ws.MCPBindings = []MCPBinding{{ID: "binding", ServerName: "unavailable", AllowedTools: []string{}}}
	data, err := ws.ToJSON()
	localConfigMust(t, err)
	record, err := SnapshotContinuityWorkspace(data)
	localConfigMust(t, err)
	return data, record
}

func TestPortableWorkspacePreservesWorkAndSeparatesDestinationIntent(t *testing.T) {
	data, record := portableWorkspaceFixture(t)
	before := append([]byte(nil), data...)
	original, err := DecodeContinuityWorkspace(record, data)
	localConfigMust(t, err)
	projected, err := ProjectContinuityWorkspace(record, data, "")
	localConfigMust(t, err)
	got := projected.Workspace
	if !reflect.DeepEqual(got.Messages, original.Messages) || !reflect.DeepEqual(got.Attachments, original.Attachments) ||
		!reflect.DeepEqual(got.Tasks[0], original.Tasks[0]) || !got.CreatedAt.Equal(original.CreatedAt) || !got.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatal("saved work, completed state or dates changed during projection")
	}
	if got.ID != original.ID || got.Version != 1 || got.TicketSequence != 12 || got.Tasks[0].TicketVersion != 42 || got.Designation != "" {
		t.Fatal("identity, ticket sequence or destination ownership changed")
	}
	if got.Tasks[1].Status != TaskStatusFailed || got.Tasks[1].CurrentRunID != "" || got.Tasks[1].WakeMacEnabled ||
		got.Tasks[1].ExecutionSteps[0].Status != TaskExecutionStepFailed || len(projected.InterruptedTaskIDs) != 1 {
		t.Fatal("unfinished execution remained runnable/live")
	}
	if !got.MissionEnabled || !got.Tasks[1].ScheduleEnabled {
		t.Fatal("source intent was silently discarded rather than separately gated")
	}
	if got.Tasks[1].AssignmentMode != TaskAssignmentModeLegacyUnknown {
		t.Fatal("projection skipped the folder store's load-time normalization; the file would be rewritten on first load")
	}
	if got.ProjectPath != "" || len(got.DirectoryReferences) != 0 || projected.SourceProjectPath != original.ProjectPath ||
		!reflect.DeepEqual(projected.ExternalDirectories, original.DirectoryReferences) {
		t.Fatal("external references became destination roots or disappeared from evidence")
	}
	if !bytes.Equal(before, data) {
		t.Fatal("projection changed source evidence")
	}
	staged, err := MarshalContinuityWorkspace(projected)
	localConfigMust(t, err)
	var decoded Workspace
	localConfigMust(t, json.Unmarshal(staged, &decoded))
	if decoded.ID != got.ID || decoded.CreatedAt != got.CreatedAt {
		t.Fatal("staging introduced a creation-time mutation")
	}
	if !bytes.Contains(staged, []byte("9007199254740993")) || bytes.Contains(staged, []byte("9007199254740992")) {
		t.Fatal("user-authored numeric data was rounded during transfer")
	}
}

// Connection settings and grants entered on the source machine are local:
// the checkpoint and every destination projection deny them instead of
// refusing an otherwise ordinary native workspace.
func TestPortableWorkspaceProjectsAwayLocalSettingsAndGrants(t *testing.T) {
	data, _ := portableWorkspaceFixture(t)
	var value Workspace
	localConfigMust(t, json.Unmarshal(data, &value))
	value.MCPBindings[0].Config = map[string]any{"token": "synthetic-source-secret"}
	value.MCPBindings[0].AllowedTools = nil
	value.AllowNativeMCPCLI = true
	native, err := value.ToJSON()
	localConfigMust(t, err)
	record, err := SnapshotContinuityWorkspace(native)
	localConfigMust(t, err)
	if bytes.Contains(record.Data, []byte("synthetic-source-secret")) {
		t.Fatal("checkpoint record carried a connection secret")
	}
	projected, err := ProjectContinuityWorkspace(record, native, "")
	localConfigMust(t, err)
	staged, err := MarshalContinuityWorkspace(projected)
	localConfigMust(t, err)
	if bytes.Contains(staged, []byte("synthetic-source-secret")) || projected.Workspace.AllowNativeMCPCLI ||
		projected.Workspace.MCPBindings[0].Config != nil || len(projected.Workspace.MCPBindings[0].AllowedTools) != 0 {
		t.Fatal("destination projection kept source-machine settings or grants")
	}
	if projected.Workspace.MCPBindings[0].ServerName != "unavailable" {
		t.Fatal("binding definition (intent) was dropped")
	}
}

func TestPortableWorkspaceRejectsOwnershipAndMixedEvidence(t *testing.T) {
	data, record := portableWorkspaceFixture(t)
	for _, change := range []func(*Workspace){
		func(ws *Workspace) { ws.OwnerUserID = "foreign" },
		func(ws *Workspace) { ws.Tasks[0].WorkspaceID = "foreign" },
		func(ws *Workspace) { ws.Tasks[1].ID = ws.Tasks[0].ID },
		func(ws *Workspace) { ws.Tasks[1].TicketNumber = ws.Tasks[0].TicketNumber },
		func(ws *Workspace) { ws.Attachments[0].WorkspaceID = "foreign" },
	} {
		var value Workspace
		localConfigMust(t, json.Unmarshal(data, &value))
		change(&value)
		mutated, err := value.ToJSON()
		localConfigMust(t, err)
		if _, err := SnapshotContinuityWorkspace(mutated); err == nil {
			t.Fatal("unsafe canonical evidence was packaged")
		}
	}
	data = append(data, '\n')
	if _, err := DecodeContinuityWorkspace(record, data); !errors.Is(err, workspacecontinuity.ErrDigest) {
		t.Fatal("changed canonical bytes were accepted under an old checkpoint")
	}
}
