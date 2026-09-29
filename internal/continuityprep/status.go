package continuityprep

import (
	"context"
	"strings"
	"time"
)

// Status is what the workspace view shows about moving this folder: whether
// its latest saved work is in a completed checkpoint, and whether background
// routines are allowed here. It carries reason codes, never private content.
type Status struct {
	WorkspaceID  string     `json:"workspace_id"`
	State        string     `json:"state"` // preparing | ready | update_needed | unavailable
	CheckpointAt *time.Time `json:"checkpoint_at,omitempty"`
	Reason       string     `json:"reason,omitempty"`
	Attachment   string     `json:"attachment"`
	Imported     bool       `json:"imported"`
	Background   bool       `json:"background_allowed"`
	Manual       bool       `json:"manual_allowed"`
	Version      int64      `json:"attachment_version,omitempty"`
	Children     []Status   `json:"children,omitempty"`
	TreeReady    bool       `json:"tree_ready"`
}

const (
	StatePreparing    = "preparing"
	StateReady        = "ready"
	StateUpdateNeeded = "update_needed"
	StateUnavailable  = "unavailable"
)

// transientReasons are failures a later attempt normally resolves on its own.
var transientReasons = map[string]bool{"source_changed": true, "file_mutation_pending": true}

// Status reports one workspace and, recursively, its physical children.
func (w *Worker) Status(ctx context.Context, workspaceID string) (Status, error) {
	return w.status(ctx, workspaceID, 0)
}

func (w *Worker) status(ctx context.Context, workspaceID string, depth int) (Status, error) {
	w.init()
	result := Status{WorkspaceID: workspaceID}
	policy, err := w.Local.Policy(ctx, workspaceID)
	if err != nil {
		return result, err
	}
	result.Attachment, result.Imported, result.Background, result.Manual, result.Version =
		string(policy.State), policy.Imported, policy.Automatic, policy.Manual, policy.Version
	prep, err := w.Local.Preparation(ctx, workspaceID)
	if err != nil {
		return result, err
	}
	result.CheckpointAt = prep.CheckpointAt
	switch {
	case policy.Attached && !policy.Manual:
		result.State, result.Reason = StateUnavailable, "not_attached"
	case w.isPreparing(workspaceID):
		result.State = StatePreparing
	case prep.Ready():
		result.State = StateReady
	case prep.LastError != "" && !transientReasons[prep.LastError]:
		result.State, result.Reason = StateUnavailable, prep.LastError
	case prep.Sequence == 0 && !policy.Attached:
		result.State = StatePreparing // registered on the next pass
	default:
		result.State = StateUpdateNeeded
		result.Reason = prep.LastError
	}
	result.TreeReady = result.State == StateReady
	if depth < 32 && w.Folders != nil {
		if folder, err := w.Folders.GetFolderPath(workspaceID); err == nil && folder != "" {
			if children, err := PhysicalChildren(ctx, folder); err == nil {
				for _, child := range children {
					childStatus, err := w.status(ctx, child, depth+1)
					if err != nil {
						continue
					}
					result.TreeReady = result.TreeReady && childStatus.TreeReady
					result.Children = append(result.Children, childStatus)
				}
			}
		}
	}
	return result, nil
}

// ReasonDomain returns the domain a reason code refers to ("sessions" for
// "sessions_limit"), for grouping in the UI.
func ReasonDomain(reason string) string {
	if i := strings.LastIndexByte(reason, '_'); i > 0 {
		return reason[:i]
	}
	return reason
}
