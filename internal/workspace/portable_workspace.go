package workspace

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuityWorkspace binds the canonical file already travelling in the folder
// to the checkpoint. Large collaboration/task history is not duplicated into one
// record (the bounded canonical file is independently fingerprinted). No generic
// SQL column/value instruction is accepted.
type ContinuityWorkspace struct {
	Version         int                             `json:"version"`
	WorkspaceID     string                          `json:"workspace_id"`
	SourceVersion   int64                           `json:"source_version"`
	CreatedAt       time.Time                       `json:"created_at"`
	UpdatedAt       time.Time                       `json:"updated_at"`
	FileDigest      string                          `json:"file_digest"`
	FileBytes       int64                           `json:"file_bytes"`
	TaskCount       int                             `json:"task_count"`
	MessageCount    int                             `json:"message_count"`
	AttachmentCount int                             `json:"attachment_count"`
	SQLMetadata     *ContinuityWorkspaceSQLMetadata `json:"sql_metadata"`
}

// SQLMetadata is nil for a file-only description. Only the canonical SQL owner
// can supply it from the shared snapshot before a modern registration restores.
type ContinuityWorkspaceSQLMetadata struct {
	Color      string `json:"color"`
	OrderIndex int    `json:"order_index"` // SQL assigns display order at creation; the file may keep 0
}

func decodePortableWorkspaceFile(data []byte) (*Workspace, error) {
	var ws Workspace
	if err := workspacecontinuity.DecodeRequiredDocument(data, &ws, workspacecontinuity.MaxChunkBytes); err != nil {
		return nil, err
	}
	if !workspacecontinuity.ValidID(ws.ID) || ws.OwnerUserID != "local" || strings.TrimSpace(ws.Name) == "" ||
		ws.CreatedAt.IsZero() || ws.UpdatedAt.IsZero() || ws.Version < 0 || ws.TicketSequence < 0 {
		return nil, workspacecontinuity.ErrInvalid
	}
	switch ws.Status {
	case StatusActive, StatusCompleted, StatusFailed, StatusCancelled:
	default:
		return nil, workspacecontinuity.ErrInvalid
	}
	if ws.ParentID != "" && (!workspacecontinuity.ValidID(ws.ParentID) || ws.ParentID == ws.ID) {
		return nil, workspacecontinuity.ErrInvalid
	}
	if len(ws.AgentInstances) > workspacecontinuity.MaxFiles {
		return nil, workspacecontinuity.ErrLimit
	}
	if len(ws.AgentInstances) > 0 {
		if _, err := profileInstanceIDs(&ws, ws.AgentInstances[0].Name); err != nil {
			return nil, err
		}
	}
	// Strict typed validation above must precede this second decode. Retain raw
	// numeric meaning in user-authored map values instead of rounding large
	// integers through float64 when staging the canonical file/SQL JSON fields.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&ws); err != nil {
		return nil, workspacecontinuity.ErrInvalid
	}
	// A native file may still hold connection settings, runtime grants or a
	// native CLI opt-in written before this feature. They are local to the
	// machine that entered them: every decoded value is the denied projection,
	// so neither a checkpoint record nor a destination install ever carries
	// them. The copied physical file itself is disclosed as private data.
	return validatePortableWorkspace(&ws)
}

func validatePortableWorkspace(native *Workspace) (*Workspace, error) {
	ws, _, err := splitWorkspaceLocalConfig(native)
	if err != nil {
		return nil, err
	}
	ws.WorkspaceLocalConfigID = native.WorkspaceLocalConfigID // non-secret local reference is inert
	seenTasks, numbers := map[string]bool{}, map[int64]bool{}
	for _, task := range ws.Tasks {
		if !workspacecontinuity.ValidID(task.ID) || seenTasks[task.ID] || task.WorkspaceID != ws.ID || task.CreatedAt.IsZero() ||
			task.TicketNumber < 0 || task.TicketNumber > ws.TicketSequence || task.TicketNumber > 0 && numbers[task.TicketNumber] {
			return nil, workspacecontinuity.ErrInvalid
		}
		switch task.Status {
		case TaskStatusBacklog, TaskStatusPending, TaskStatusAssigned, TaskStatusInProgress, TaskStatusWaitingForChoice,
			TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled, TaskStatusTimeout:
		default:
			return nil, workspacecontinuity.ErrInvalid
		}
		seenTasks[task.ID], numbers[task.TicketNumber] = true, true
	}
	seenMessages := map[string]bool{}
	for _, message := range ws.Messages {
		if !workspacecontinuity.ValidID(message.ID) || seenMessages[message.ID] || message.Timestamp.IsZero() {
			return nil, workspacecontinuity.ErrInvalid
		}
		seenMessages[message.ID] = true
	}
	seenAttachments := map[string]bool{}
	for _, attachment := range ws.Attachments {
		if !workspacecontinuity.ValidID(attachment.ID) || seenAttachments[attachment.ID] || attachment.WorkspaceID != ws.ID ||
			attachment.CreatedAt.IsZero() || attachment.UpdatedAt.IsZero() {
			return nil, workspacecontinuity.ErrInvalid
		}
		seenAttachments[attachment.ID] = true
	}
	for _, reference := range ws.DirectoryReferences {
		if !workspacecontinuity.ValidID(reference.ID) || reference.WorkspaceID != ws.ID {
			return nil, workspacecontinuity.ErrInvalid
		}
	}
	for id, workflow := range ws.Workflows {
		if !workspacecontinuity.ValidID(id) || workflow.ID != id || workflow.WorkspaceID != ws.ID {
			return nil, workspacecontinuity.ErrInvalid
		}
	}
	return ws, nil
}

// ProjectContinuityBindingDefinitions lets the canonical SQL snapshot owner
// compare its hydrated binding definitions with the denied physical file. It
// transfers no local configuration and is not import admission or persistence.
func ProjectContinuityBindingDefinitions(mcp []MCPBinding, skills []SkillBinding) ([]MCPBinding, []SkillBinding, error) {
	projected, _, err := splitWorkspaceLocalConfig(&Workspace{MCPBindings: mcp, SkillBindings: skills})
	if err != nil {
		return nil, nil, err
	}
	return projected.MCPBindings, projected.SkillBindings, nil
}

func SnapshotContinuityWorkspace(data []byte) (workspacecontinuity.Record, error) {
	ws, err := decodePortableWorkspaceFile(data)
	if err != nil {
		return workspacecontinuity.Record{}, err
	}
	return workspacecontinuity.EncodeRecord(ws.ID, ContinuityWorkspace{
		Version: 1, WorkspaceID: ws.ID, SourceVersion: ws.Version, CreatedAt: ws.CreatedAt, UpdatedAt: ws.UpdatedAt,
		FileDigest: workspacecontinuity.Digest(data), FileBytes: int64(len(data)), TaskCount: len(ws.Tasks),
		MessageCount: len(ws.Messages), AttachmentCount: len(ws.Attachments),
	})
}

// DecodeContinuityWorkspace validates the reviewed immutable file. It performs no
// FromJSON migrations, current-time initialization, callbacks or scheduling.
func DecodeContinuityWorkspace(record workspacecontinuity.Record, data []byte) (*Workspace, error) {
	var value ContinuityWorkspace
	if err := workspacecontinuity.DecodeRecord(record, &value); err != nil {
		return nil, err
	}
	if value.Version != 1 {
		return nil, workspacecontinuity.ErrVersion
	}
	if value.SQLMetadata != nil {
		var envelope struct {
			SQLMetadata json.RawMessage `json:"sql_metadata"`
		}
		if err := json.Unmarshal(record.Data, &envelope); err != nil {
			return nil, workspacecontinuity.ErrInvalid
		}
		if err := workspacecontinuity.DecodeRequiredDocument(envelope.SQLMetadata, value.SQLMetadata, workspacecontinuity.MaxRecordBytes); err != nil {
			return nil, err
		}
	}
	if value.FileBytes != int64(len(data)) || value.FileDigest != workspacecontinuity.Digest(data) {
		return nil, workspacecontinuity.ErrDigest
	}
	ws, err := decodePortableWorkspaceFile(data)
	if err != nil {
		return nil, err
	}
	if record.ID != ws.ID || value.WorkspaceID != ws.ID || value.SourceVersion != ws.Version ||
		!value.CreatedAt.Equal(ws.CreatedAt) || !value.UpdatedAt.Equal(ws.UpdatedAt) || value.TaskCount != len(ws.Tasks) ||
		value.MessageCount != len(ws.Messages) || value.AttachmentCount != len(ws.Attachments) {
		return nil, workspacecontinuity.ErrInvalid
	}
	return ws, nil
}

// ContinuityWorkspaceProjection is the destination canonical value plus explicit
// unavailable references. Evidence remains in the reviewed checkpoint; it is not
// installed into runtime root/approval fields merely because its spelling exists
// on this machine. Reconnection uses normal local owners after import.
type ContinuityWorkspaceProjection struct {
	Workspace           *Workspace
	ExternalDirectories []DirectoryReference
	SourceProjectPath   string
	InterruptedTaskIDs  []string
	PendingSetup        bool
}

func ProjectContinuityWorkspace(record workspacecontinuity.Record, data []byte, parentID string) (ContinuityWorkspaceProjection, error) {
	var result ContinuityWorkspaceProjection
	ws, err := DecodeContinuityWorkspace(record, data)
	if err != nil {
		return result, err
	}
	if parentID != "" && (!workspacecontinuity.ValidID(parentID) || parentID == ws.ID) {
		return result, workspacecontinuity.ErrInvalid
	}
	// The reviewed physical tree, not a source's parent pointer, owns nesting.
	ws.ParentID = parentID
	ws.Designation = "" // only the confirmed adoption transaction may designate
	ws.WorkspaceLocalConfigID = ""
	ws.Version = 1 // source revision remains evidence, not destination CAS
	result.Workspace = ws
	result.ExternalDirectories, ws.DirectoryReferences = ws.DirectoryReferences, nil
	result.SourceProjectPath, ws.ProjectPath = ws.ProjectPath, ""
	// Even a relative project path is restored only after the coordinator verifies
	// it names an included directory. No automatic external filesystem reads here.
	result.PendingSetup = ws.PendingPlan != nil || len(ws.DynamicAgentRequests) != 0 || ws.SetupWizardProgress != nil
	ws.PendingPlan, ws.DynamicAgentRequests, ws.SetupWizardProgress = nil, nil, nil
	ws.NextMissionRunAt = nil
	for i := range ws.Tasks {
		task := &ws.Tasks[i]
		task.CurrentRunID = "" // old process/run handles never resume
		task.NextRun = nil
		task.WakeMacEnabled = false
		if task.Status == TaskStatusInProgress || task.Status == TaskStatusWaitingForChoice || task.Status == TaskStatusAssigned {
			result.InterruptedTaskIDs = append(result.InterruptedTaskIDs, task.ID)
			task.Status, task.Error = TaskStatusFailed, "Interrupted by workspace transfer; review before running."
		}
		for step := range task.ExecutionSteps {
			if task.ExecutionSteps[step].Status == TaskExecutionStepInProgress {
				task.ExecutionSteps[step].Status = TaskExecutionStepFailed
			}
		}
		// An imported file-output path is not a destination filesystem grant.
		if task.ResultStorage != nil && (filepath.IsAbs(task.ResultStorage.FilePath) || task.ResultStorage.StorageTarget != "workspace_folder") {
			task.ResultStorage.Enabled = false
		}
	}
	// Apply the same load-time normalizations the folder store would, so the
	// installed file and its database row start identical instead of the
	// store rewriting the file on first load.
	if len(ws.ScheduledTasks) > 0 {
		ws.ClearLegacyScheduledTasks()
	}
	backfillTaskAssignmentProvenance(ws)
	for id, workflow := range ws.Workflows {
		if workflow.Status == WorkflowStatusInProgress {
			workflow.Status = WorkflowStatusFailed
		}
		for i := range workflow.Steps {
			if workflow.Steps[i].Status == StepStatusInProgress {
				workflow.Steps[i].Status = StepStatusFailed
				workflow.Steps[i].Error = "Interrupted by workspace transfer; review before running."
			}
		}
		ws.Workflows[id] = workflow
	}
	return result, nil
}

// MarshalContinuityWorkspace is for a private import staging folder, not a normal
// Save (which would migrate tasks, set dates, run callbacks or bump versions).
func MarshalContinuityWorkspace(value ContinuityWorkspaceProjection) ([]byte, error) {
	if value.Workspace == nil {
		return nil, workspacecontinuity.ErrInvalid
	}
	data, err := value.Workspace.ToJSON()
	if err != nil || len(data) > workspacecontinuity.MaxChunkBytes || !json.Valid(data) {
		return nil, workspacecontinuity.ErrLimit
	}
	return data, nil
}
