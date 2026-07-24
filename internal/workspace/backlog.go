package workspace

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// This file is the Group-2 capture/query/mutation/promotion service for the
// Backlog lifecycle stage (tasks/prd-workspace-backlog.md). It is the single
// hierarchy-safe entry point every capture surface (manual UI, assistants,
// Action Center, and eventually BACKLOG.md import) funnels through, built on
// the lifecycle primitives in task_backlog.go/task_status.go and the
// existing task mutation primitives in workspace_tasks.go/coordinator.go.

// Backlog capture provenance source types (FR5, 20-29).
const (
	BacklogSourceManual       = "manual"
	BacklogSourceAssistant    = "assistant"
	BacklogSourceActionCenter = "action_center"
	BacklogSourceBacklogFile  = "backlog_markdown"
)

// BacklogCreateInput describes a manual/assistant/Action Center/file capture
// request (FR4-5, 19-22). Description is the required title; every other
// field is optional planning metadata.
type BacklogCreateInput struct {
	WorkspaceID  string
	Description  string
	Details      string
	Tags         []string
	Priority     int
	ReferenceURL string
	SourceType   string
	SourceID     string
}

// BacklogUpdateInput describes a supported-field edit to an existing Backlog
// item (FR6, 20, 31, 75). Pointer fields distinguish "not sent" from
// "cleared" — nil leaves the field untouched. There is deliberately no field
// for lifecycle, workspace ownership, provenance, stable ID, or any runtime
// state: those cannot be overwritten through this path (FR6).
type BacklogUpdateInput struct {
	Description  *string
	Details      *string
	Tags         *[]string
	Priority     *int
	ReferenceURL *string
}

// BacklogItemView pairs a task with its owning workspace's identity so
// roll-up cards can show ownership without copying the item or changing its
// WorkspaceID (FR63-65).
type BacklogItemView struct {
	Task                Task   `json:"task"`
	OwningWorkspaceID   string `json:"owning_workspace_id"`
	OwningWorkspaceName string `json:"owning_workspace_name"`
}

// BacklogSyncStatus is the compact sync-health summary shown in the Details
// panel, drawer, and Quest Board (FR84, 91). It is produced by whatever
// BacklogSynchronizer is wired in; see noopBacklogSynchronizer for the
// zero-value behavior before Group 3 wires a real one.
type BacklogSyncStatus struct {
	Enabled      bool       `json:"enabled"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	Warning      string     `json:"warning,omitempty"`
	Conflict     bool       `json:"conflict,omitempty"`
}

// BacklogSynchronizer lets the service and its HTTP layer trigger and
// observe BACKLOG.md synchronization without depending on the concrete file
// implementation (Group 3; FR68, 73, 83-84, 88, 91). BacklogService works
// standalone with no synchronizer configured — every method on
// noopBacklogSynchronizer is a safe no-op — so capture/query/mutation is
// fully functional before file sync exists.
type BacklogSynchronizer interface {
	// ImportBeforeRead imports any pending file-side changes before serving a
	// direct Backlog list/detail read (FR84).
	ImportBeforeRead(workspaceID string) error
	// RenderAfterMutation regenerates BACKLOG.md after a capture, edit,
	// reorder, delete, or promotion (FR83). A failure here must not roll back
	// the already-persisted structured mutation (FR88), so callers only log it.
	RenderAfterMutation(workspaceID string) error
	// Status returns the current sync-health summary for a workspace.
	Status(workspaceID string) BacklogSyncStatus
	// Conflicts returns every unresolved same-item conflict for a workspace
	// (FR86-87). Both versions are retained until ResolveConflict is called.
	Conflicts(workspaceID string) []BacklogSyncConflict
	// ResolveConflict applies the chosen side for one conflicted item —
	// useFile true applies the retained file version to the structured task,
	// false discards it and keeps Ori's current value — and clears the
	// conflict record. No silent last-write-wins: this is always an explicit
	// user choice (FR87).
	ResolveConflict(workspaceID, itemID string, useFile bool) error
}

type noopBacklogSynchronizer struct{}

func (noopBacklogSynchronizer) ImportBeforeRead(string) error          { return nil }
func (noopBacklogSynchronizer) RenderAfterMutation(string) error       { return nil }
func (noopBacklogSynchronizer) Status(string) BacklogSyncStatus        { return BacklogSyncStatus{} }
func (noopBacklogSynchronizer) Conflicts(string) []BacklogSyncConflict { return nil }
func (noopBacklogSynchronizer) ResolveConflict(string, string, bool) error {
	return fmt.Errorf("no backlog synchronizer configured")
}

// BacklogService is the single hierarchy-safe entry point for Backlog
// capture, query, mutation, ordering, deletion, and promotion.
type BacklogService struct {
	store    Store
	eventBus *EventBus
	sync     BacklogSynchronizer
}

// NewBacklogService constructs a BacklogService over the given store. Call
// SetEventBus/SetSynchronizer to wire in optional collaborators; both are
// safe to leave unset.
func NewBacklogService(store Store) *BacklogService {
	return &BacklogService{store: store, sync: noopBacklogSynchronizer{}}
}

// SetEventBus wires event publication for capture/update/reorder/delete/
// promotion (FR31, 47, 54). Optional; nil (the default) means no events.
func (s *BacklogService) SetEventBus(bus *EventBus) {
	if s == nil {
		return
	}
	s.eventBus = bus
}

// SetSynchronizer wires BACKLOG.md synchronization (Group 3). Passing nil
// restores the no-op synchronizer rather than leaving callers to nil-check.
func (s *BacklogService) SetSynchronizer(sync BacklogSynchronizer) {
	if s == nil {
		return
	}
	if sync == nil {
		sync = noopBacklogSynchronizer{}
	}
	s.sync = sync
}

// SyncStatus returns the current sync-health summary for a workspace (FR84, 91).
func (s *BacklogService) SyncStatus(workspaceID string) BacklogSyncStatus {
	return s.sync.Status(workspaceID)
}

// SyncNow triggers an on-demand import-then-render, satisfying the drawer/
// panel's manual Sync Now control so missed file-watch events cannot leave
// state permanently stale (FR84).
func (s *BacklogService) SyncNow(workspaceID string) error {
	if err := s.sync.ImportBeforeRead(workspaceID); err != nil {
		return err
	}
	return s.sync.RenderAfterMutation(workspaceID)
}

// Conflicts returns every unresolved same-item BACKLOG.md conflict for a
// workspace (FR86-87).
func (s *BacklogService) Conflicts(workspaceID string) []BacklogSyncConflict {
	return s.sync.Conflicts(workspaceID)
}

// ResolveConflict applies the user's whole-item Use Ori / Use File choice
// for a conflicted Backlog item (FR87).
func (s *BacklogService) ResolveConflict(workspaceID, itemID string, useFile bool) error {
	return s.sync.ResolveConflict(workspaceID, itemID, useFile)
}

// WorkspaceName returns workspaceID's display name, or "" if it cannot be
// read. HTTP response envelopes use this to attach owning-workspace identity
// to a mutation result without a second full item fetch.
func (s *BacklogService) WorkspaceName(workspaceID string) string {
	ws, err := s.store.Get(workspaceID)
	if err != nil {
		return ""
	}
	return ws.Name
}

func normalizeBacklogPriority(priority int) int {
	if priority < 1 || priority > 5 {
		return 3
	}
	return priority
}

// buildCaptureTask validates a capture input and constructs the Task to
// persist at the given initial status. Shared by Create (Backlog) and
// CreateReadyUnassigned (direct Ready creation, FR2.10/56/78).
func buildCaptureTask(input BacklogCreateInput, status TaskStatus) (Task, error) {
	wsID := strings.TrimSpace(input.WorkspaceID)
	if wsID == "" {
		return Task{}, fmt.Errorf("workspace_id is required")
	}
	description := strings.TrimSpace(input.Description)
	if description == "" {
		return Task{}, fmt.Errorf("title is required")
	}
	referenceURL, err := NormalizeReferenceURL(input.ReferenceURL)
	if err != nil {
		return Task{}, err
	}
	tags, err := ValidateWorkspaceTags(input.Tags)
	if err != nil {
		return Task{}, err
	}
	sourceType := strings.TrimSpace(input.SourceType)
	if sourceType == "" {
		sourceType = BacklogSourceManual
	}

	task := Task{
		ID:           uuid.New().String(),
		WorkspaceID:  wsID,
		Description:  description,
		Details:      strings.TrimSpace(input.Details),
		Tags:         tags,
		Priority:     normalizeBacklogPriority(input.Priority),
		ReferenceURL: referenceURL,
		SourceType:   sourceType,
		SourceID:     strings.TrimSpace(input.SourceID),
		Status:       status,
		CreatedAt:    time.Now(),
	}
	if status != TaskStatusBacklog {
		// Direct Ready creation stays quiescent until an explicit
		// assignment/run/schedule action (FR11-12).
		task.AwaitingExecutionIntent = true
	}
	return task, nil
}

// Create captures a new Backlog item (FR4-5, 19-22). The new item is ranked
// last among the workspace's existing Backlog items (deterministic initial
// rank).
func (s *BacklogService) Create(input BacklogCreateInput) (*Task, error) {
	task, err := buildCaptureTask(input, TaskStatusBacklog)
	if err != nil {
		return nil, err
	}
	if err := ValidateBacklogTaskInvariants(&task); err != nil {
		return nil, err
	}

	var created Task
	err = s.store.Update(task.WorkspaceID, func(ws *Workspace) error {
		task.BacklogRank = nextBacklogRank(ws)
		if err := ws.AddTask(task); err != nil {
			return err
		}
		got, err := ws.GetTask(task.ID)
		if err != nil {
			return err
		}
		created = *got
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.publish(EventTaskBacklogCaptured, created.WorkspaceID, created.ID, created)
	s.renderAfterMutation(created.WorkspaceID)
	return &created, nil
}

// CreateReadyUnassigned creates a task directly in the Ready stage without
// assigning, scheduling, or executing it (FR11-12, 22, 56, 78) — used by New
// Quest's direct-Ready choice and by BACKLOG.md rows added straight under
// "Promote to Ready". It intentionally bypasses entry-agent defaulting: the
// caller explicitly chose not to go through Backlog capture, but that choice
// must not silently become an assignment too. Callers that need the
// coordinator default (the ordinary create-task flow) should keep using
// Workspace.AddTask + Workspace.ApplyEntryAgentDefault directly instead of
// this method.
func (s *BacklogService) CreateReadyUnassigned(input BacklogCreateInput) (*Task, error) {
	task, err := buildCaptureTask(input, TaskStatusPending)
	if err != nil {
		return nil, err
	}

	var created Task
	err = s.store.Update(task.WorkspaceID, func(ws *Workspace) error {
		if err := ws.AddTask(task); err != nil {
			return err
		}
		got, err := ws.GetTask(task.ID)
		if err != nil {
			return err
		}
		created = *got
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.publish(EventTaskCreated, created.WorkspaceID, created.ID, created)
	return &created, nil
}

// nextBacklogRank returns the rank to assign a newly captured Backlog item:
// one past the current maximum among the workspace's existing Backlog items,
// so new captures sort last by default (FR20-22).
func nextBacklogRank(ws *Workspace) int64 {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	var max int64
	for _, t := range ws.Tasks {
		if t.Status == TaskStatusBacklog && t.BacklogRank > max {
			max = t.BacklogRank
		}
	}
	return max + 1
}

// Get fetches a single Backlog item, importing any pending file-side changes
// first (FR84). Returns an error if the item does not exist or is no longer
// in Backlog.
func (s *BacklogService) Get(workspaceID, taskID string) (*BacklogItemView, error) {
	_ = s.sync.ImportBeforeRead(workspaceID)

	ws, err := s.store.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	task, err := ws.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	if task.Status != TaskStatusBacklog {
		return nil, fmt.Errorf("item %s is not in Backlog", taskID)
	}
	return &BacklogItemView{Task: *task, OwningWorkspaceID: ws.ID, OwningWorkspaceName: ws.Name}, nil
}

// List returns workspaceID's Backlog items sorted by persistent rank, then
// creation time, then stable ID (FR43, 46, 49, 61, 76). With
// includeDescendants it also rolls up every descendant workspace's items,
// each tagged with its own owning workspace identity (FR45-48, 60-66) —
// ownership and mutation authority are never changed by the roll-up (FR65).
func (s *BacklogService) List(workspaceID string, includeDescendants bool) ([]BacklogItemView, error) {
	_ = s.sync.ImportBeforeRead(workspaceID)

	ws, err := s.store.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	items := localBacklogItemViews(ws)

	if includeDescendants {
		descendantIDs, err := s.descendantWorkspaceIDs(workspaceID)
		if err != nil {
			return nil, err
		}
		for _, id := range descendantIDs {
			child, err := s.store.Get(id)
			if err != nil {
				continue // a missing/unreadable descendant must not fail the whole roll-up
			}
			items = append(items, localBacklogItemViews(child)...)
		}
	}

	sortBacklogItems(items)
	return items, nil
}

// localBacklogItemViews returns ws's own Backlog items — excluding subtasks
// (child tasks) and every Ready-or-later record (FR43, 46, 49, 61).
func localBacklogItemViews(ws *Workspace) []BacklogItemView {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	out := make([]BacklogItemView, 0, 4)
	for _, t := range ws.Tasks {
		if t.Status != TaskStatusBacklog {
			continue
		}
		if t.ParentTaskID != "" {
			continue
		}
		out = append(out, BacklogItemView{Task: t, OwningWorkspaceID: ws.ID, OwningWorkspaceName: ws.Name})
	}
	return out
}

func sortBacklogItems(items []BacklogItemView) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].Task, items[j].Task
		if a.BacklogRank != b.BacklogRank {
			return a.BacklogRank < b.BacklogRank
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
}

// descendantWorkspaceIDs returns every workspace transitively parented under
// rootID, via a BFS over Workspace.ParentID. Descendant identity/ownership is
// never altered by this traversal — it is used only for an opt-in read
// roll-up (FR62-66).
func (s *BacklogService) descendantWorkspaceIDs(rootID string) ([]string, error) {
	allIDs, err := s.store.List()
	if err != nil {
		return nil, err
	}
	childrenOf := make(map[string][]string, len(allIDs))
	for _, id := range allIDs {
		ws, err := s.store.Get(id)
		if err != nil {
			continue
		}
		if ws.ParentID != "" {
			childrenOf[ws.ParentID] = append(childrenOf[ws.ParentID], ws.ID)
		}
	}

	var out []string
	queue := append([]string(nil), childrenOf[rootID]...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		out = append(out, id)
		queue = append(queue, childrenOf[id]...)
	}
	return out, nil
}

// Update applies a supported-field edit to an existing Backlog item (FR6, 20,
// 31, 75). It targets taskID's owning workspace directly — callers must pass
// the item's actual owning workspace ID (from BacklogItemView), never a
// parent roll-up or global Map context, so a roll-up view can never be used
// as mutation authority (FR48-50, 60, 63-65).
func (s *BacklogService) Update(workspaceID, taskID string, input BacklogUpdateInput) (*Task, error) {
	var updated Task
	err := s.store.Update(workspaceID, func(ws *Workspace) error {
		return ws.MutateTask(taskID, func(t *Task) error {
			if t.Status != TaskStatusBacklog {
				return fmt.Errorf("item %s is not in Backlog", taskID)
			}
			if input.Description != nil {
				d := strings.TrimSpace(*input.Description)
				if d == "" {
					return fmt.Errorf("title cannot be empty")
				}
				t.Description = d
			}
			if input.Details != nil {
				t.Details = strings.TrimSpace(*input.Details)
			}
			if input.Tags != nil {
				tags, err := ValidateWorkspaceTags(*input.Tags)
				if err != nil {
					return err
				}
				t.Tags = tags
			}
			if input.Priority != nil {
				t.Priority = normalizeBacklogPriority(*input.Priority)
			}
			if input.ReferenceURL != nil {
				url, err := NormalizeReferenceURL(*input.ReferenceURL)
				if err != nil {
					return err
				}
				t.ReferenceURL = url
			}
			updated = *t
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	s.publish(EventTaskBacklogUpdated, updated.WorkspaceID, updated.ID, updated)
	s.renderAfterMutation(updated.WorkspaceID)
	return &updated, nil
}

// Reorder atomically assigns sequential BacklogRank values to workspaceID's
// Backlog items in the order given by orderedIDs (FR31, 43, 49, 76). Every ID
// must name an existing Backlog item owned by workspaceID; the whole
// operation fails without a partial write if any ID is invalid or duplicated
// (store.Update only saves after the mutation function returns nil).
// Returns the complete updated ordering, as every projection needs it.
func (s *BacklogService) Reorder(workspaceID string, orderedIDs []string) ([]Task, error) {
	err := s.store.Update(workspaceID, func(ws *Workspace) error {
		seen := make(map[string]bool, len(orderedIDs))
		for i, id := range orderedIDs {
			if seen[id] {
				return fmt.Errorf("duplicate item id in reorder request: %s", id)
			}
			seen[id] = true
			rank := int64(i)
			if err := ws.MutateTask(id, func(t *Task) error {
				if t.Status != TaskStatusBacklog {
					return fmt.Errorf("item %s is not in Backlog", id)
				}
				t.BacklogRank = rank
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Render BACKLOG.md to match the just-persisted order BEFORE calling
	// List(): List() always imports the file first (FR84), and until the file
	// is regenerated here it still reflects the PREVIOUS order — importing it
	// at that point would treat the now-stale file as authoritative and
	// silently revert the reorder that store.Update just persisted. This bit
	// a real second-reorder-in-a-row case (found via live/e2e testing): a
	// workspace's first-ever reorder looked fine (no file existed yet to
	// clobber it), but every reorder after that got immediately undone.
	s.renderAfterMutation(workspaceID)

	items, err := s.List(workspaceID, false)
	if err != nil {
		return nil, err
	}
	result := make([]Task, 0, len(items))
	for _, it := range items {
		result = append(result, it.Task)
	}

	s.publish(EventTaskBacklogReordered, workspaceID, "", map[string]any{"ordered_ids": orderedIDs})
	return result, nil
}

// Delete removes a Backlog item through the existing task-deletion
// safeguards (Workspace.DeleteTask), keeping layout/index cleanup identical
// to every other task deletion (FR18, 30).
func (s *BacklogService) Delete(workspaceID, taskID string) error {
	err := s.store.Update(workspaceID, func(ws *Workspace) error {
		t, err := ws.GetTask(taskID)
		if err != nil {
			return err
		}
		if t.Status != TaskStatusBacklog {
			return fmt.Errorf("item %s is not in Backlog", taskID)
		}
		return ws.DeleteTask(taskID)
	})
	if err != nil {
		return err
	}

	s.publish(EventTaskDeleted, workspaceID, taskID, nil)
	s.renderAfterMutation(workspaceID)
	return nil
}

// Promote atomically and idempotently promotes a Backlog item to Ready
// (FR9-12, 31): it preserves identity/metadata, clears the Backlog-only
// rank, and leaves AwaitingExecutionIntent set so background coordinator/
// scheduler sweeps still treat it as quiescent until an explicit assignment,
// run, or schedule action. Promoting an item already at Ready-or-later is a
// no-op that returns the current item unchanged rather than erroring, so a
// repeated client call or a race with another promotion is safe.
func (s *BacklogService) Promote(workspaceID, taskID string) (*Task, error) {
	var result Task
	alreadyPromoted := false
	err := s.store.Update(workspaceID, func(ws *Workspace) error {
		return ws.MutateTask(taskID, func(t *Task) error {
			if t.Status != TaskStatusBacklog {
				alreadyPromoted = true
				result = *t
				return nil
			}
			if err := t.SetStatus(TaskStatusPending); err != nil {
				return fmt.Errorf("cannot promote item to Ready: %w", err)
			}
			t.BacklogRank = 0
			t.AwaitingExecutionIntent = true
			result = *t
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	if !alreadyPromoted {
		s.publish(EventTaskBacklogPromoted, result.WorkspaceID, result.ID, result)
		s.renderAfterMutation(result.WorkspaceID)
	}
	return &result, nil
}

func (s *BacklogService) publish(eventType EventType, workspaceID, taskID string, data any) {
	if s.eventBus == nil {
		return
	}
	dataMap, _ := data.(map[string]any)
	if dataMap == nil {
		dataMap = map[string]any{}
		if t, ok := data.(Task); ok {
			dataMap["task_id"] = t.ID
			dataMap["description"] = t.Description
			dataMap["status"] = t.Status
		}
	}
	s.eventBus.Publish(NewTaskEvent(eventType, workspaceID, taskID, "", dataMap))
}

// renderAfterMutation triggers a BACKLOG.md re-render after a successful
// structured mutation (FR83). A failure here must not roll back the
// already-persisted mutation (FR88) — noopBacklogSynchronizer.
// RenderAfterMutation always returns nil until Group 3 wires a real one, and
// even a real implementation's error is intentionally swallowed here for the
// same reason; sync-health surfaces the warning instead (FR84, 91).
func (s *BacklogService) renderAfterMutation(workspaceID string) {
	_ = s.sync.RenderAfterMutation(workspaceID)
}
