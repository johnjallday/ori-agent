package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file implements FileBacklogSynchronizer, the concrete
// workspace.BacklogSynchronizer over the on-disk BACKLOG.md managed by
// backlog_markdown_sync.go's parser/renderer, plus workspace scaffolding and
// idempotent backfill (FR67-68, 83-91, 99).

// groupWorkspaceKind mirrors session.WorkspaceKindGroup ("group"). Duplicated
// as a local constant rather than imported: internal/session already imports
// internal/workspace, so importing the other way would cycle.
const groupWorkspaceKind = "group"

// FileBacklogSynchronizer implements BacklogSynchronizer over BACKLOG.md,
// reusing the same folder-resolution mechanism as task_markdown_sync.go.
type FileBacklogSynchronizer struct {
	store Store
	// forceRender skips the "was this generated file hand-edited?" guard.
	// Only the explicit user-chosen replace path sets it — see
	// ReplaceCollisionForce (FR-122).
	forceRender bool
}

// NewFileBacklogSynchronizer constructs a synchronizer over store.
func NewFileBacklogSynchronizer(store Store) *FileBacklogSynchronizer {
	return &FileBacklogSynchronizer{store: store}
}

// ReplaceCollisionForce regenerates BACKLOG.md even though it was edited
// outside Ori, discarding those edits (FR-122).
//
// This exists as a SEPARATE entry point so the overwrite can only happen as a
// deliberate act. RenderAfterMutation — which runs automatically after every
// ticket change — refuses to clobber a hand-edited file precisely so that this
// choice stays with the user.
func (s *FileBacklogSynchronizer) ReplaceCollisionForce(workspaceID string) error {
	forced := &FileBacklogSynchronizer{store: s.store, forceRender: true}
	return forced.RenderAfterMutation(workspaceID)
}

func (s *FileBacklogSynchronizer) resolve(workspaceID string) (path string, ws *Workspace, ok bool, err error) {
	folder, ok, err := workspaceFolderForTaskMarkdown(s.store, workspaceID)
	if err != nil || !ok {
		return "", nil, ok, err
	}
	ws, err = s.store.Get(workspaceID)
	if err != nil {
		return "", nil, false, err
	}
	isGroup := ws.Kind == groupWorkspaceKind
	return BacklogMarkdownPath(folder, isGroup), ws, true, nil
}

func (s *FileBacklogSynchronizer) persistSyncState(workspaceID string, mutate func(*backlogMarkdownSyncState)) error {
	return s.store.Update(workspaceID, func(ws *Workspace) error {
		state := getBacklogMarkdownSyncState(ws)
		mutate(&state)
		setBacklogMarkdownSyncState(ws, state)
		return nil
	})
}

// ImportBeforeRead imports pending file-side changes; errors are surfaced as
// a repair-needed sync warning rather than returned, so a read (list/detail)
// never fails merely because the file is temporarily unreadable or invalid
// (FR84, 88).
// Once the final import has run, this is a NO-OP: BACKLOG.md has become a
// generated, non-authoritative index, so manual edits to it must never mutate
// a Ticket (tasks/prd-workspace-ticket-management.md FR-120, FR-121).
//
// That is the whole point of the switch. While the file was authoritative,
// editing it changed records; once it is generated, editing it changes
// nothing and the next render replaces it.
func (s *FileBacklogSynchronizer) ImportBeforeRead(workspaceID string) error {
	ws, err := s.store.Get(workspaceID)
	if err == nil && backlogMarkdownIsGenerated(ws) {
		return nil
	}
	_, err = s.Import(workspaceID)
	return err
}

// FinalizeBacklogMarkdownImport performs the ONE-TIME final import and then
// switches this workspace to generated-index behavior (FR-119, FR-120).
//
// It runs the existing two-way importer first, so file-side work a user did
// before upgrading is adopted rather than discarded. Only after that import
// succeeds is the switch recorded — a failed import leaves the workspace in
// two-way mode so nothing is lost and the next attempt can retry.
//
// Unresolved conflicts block the switch for the same reason: flipping to
// generated mode with conflicts outstanding would strand the file-side version
// of that work with no way to adopt it.
func (s *FileBacklogSynchronizer) FinalizeBacklogMarkdownImport(workspaceID string) (*BacklogMarkdownImportResult, error) {
	ws, err := s.store.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	if backlogMarkdownIsGenerated(ws) {
		// Already finalized; idempotent.
		return &BacklogMarkdownImportResult{}, nil
	}

	result, err := s.Import(workspaceID)
	if err != nil {
		return result, err
	}
	if conflicts := s.Conflicts(workspaceID); len(conflicts) > 0 {
		return result, fmt.Errorf(
			"%d backlog item(s) have unresolved BACKLOG.md conflicts; resolve them before the file becomes a generated index",
			len(conflicts))
	}

	if err := s.persistSyncState(workspaceID, func(state *backlogMarkdownSyncState) {
		state.FinalImportAt = time.Now()
	}); err != nil {
		return result, err
	}
	// Regenerate immediately so the file on disk carries the generated header
	// rather than continuing to claim it is editable.
	return result, s.RenderAfterMutation(workspaceID)
}

// Import reads, validates, and applies BACKLOG.md's current content to the
// workspace's structured Backlog/Ready records. A missing file is not an
// error (nothing to import yet). A validation or apply failure persists a
// repair-needed sync-warning status without touching any already-persisted
// structured data (FR88) and is returned to the caller.
func (s *FileBacklogSynchronizer) Import(workspaceID string) (*BacklogMarkdownImportResult, error) {
	result := &BacklogMarkdownImportResult{}
	path, ws, ok, err := s.resolve(workspaceID)
	if err != nil || !ok {
		return result, err
	}

	data, err := os.ReadFile(path) // #nosec G304 -- path comes from s.resolve(workspaceID), which resolves through the store, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		warnErr := fmt.Errorf("read BACKLOG.md: %w", err)
		_ = s.persistSyncState(workspaceID, func(state *backlogMarkdownSyncState) {
			state.RepairNeeded = true
			state.Warning = warnErr.Error()
		})
		return result, warnErr
	}

	known := map[string]struct{}{}
	for _, item := range localBacklogItemViews(ws) {
		known[item.Task.ID] = struct{}{}
	}

	doc, err := ParseWorkspaceBacklogMarkdown(string(data), workspaceID, known)
	if err != nil {
		_ = s.persistSyncState(workspaceID, func(state *backlogMarkdownSyncState) {
			state.RepairNeeded = true
			state.Warning = err.Error()
		})
		return result, err
	}

	changed, warnings, applyErr := s.applyDocument(workspaceID, ws, doc)
	result.Changed = changed
	result.Warnings = warnings
	if applyErr != nil {
		_ = s.persistSyncState(workspaceID, func(state *backlogMarkdownSyncState) {
			state.RepairNeeded = true
			state.Warning = applyErr.Error()
		})
		return result, applyErr
	}

	_ = s.persistSyncState(workspaceID, func(state *backlogMarkdownSyncState) {
		state.RepairNeeded = false
		state.Warning = ""
	})

	if changed {
		// Reflect the merged state back into the file immediately so a
		// promoted/created row leaves the document on this same cycle
		// (FR79) instead of waiting for an unrelated future mutation.
		_ = s.RenderAfterMutation(workspaceID)
	}
	return result, nil
}

// applyDocument reconciles a parsed document against current structured
// state. It uses a bare BacklogService (no synchronizer wired) so applying
// rows does not itself trigger nested file writes mid-loop; Import renders
// once, explicitly, after every row is applied.
func (s *FileBacklogSynchronizer) applyDocument(workspaceID string, ws *Workspace, doc *backlogMarkdownDocument) (bool, []string, error) {
	svc := NewBacklogService(s.store)
	var warnings []string
	changed := false

	handledIDs := map[string]struct{}{}

	// 1. Promote to Ready section: existing Backlog rows moved here call the
	// same atomic Promote operation (FR77); new ID-less rows create a direct
	// unassigned Ready item (FR78).
	for _, row := range doc.PromoteRows {
		if row.ID == "" {
			if _, err := svc.CreateReadyUnassigned(BacklogCreateInput{
				WorkspaceID:  workspaceID,
				Description:  row.Title,
				Priority:     priorityWordToInt(row.Priority),
				Tags:         row.Tags,
				ReferenceURL: row.URL,
				SourceType:   BacklogSourceBacklogFile,
			}); err != nil {
				warnings = append(warnings, fmt.Sprintf("failed to create Ready item %q from BACKLOG.md: %v", row.Title, err))
				continue
			}
			changed = true
			continue
		}
		if _, err := svc.Promote(workspaceID, row.ID); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to promote item %s from BACKLOG.md: %v", row.ID, err))
			continue
		}
		handledIDs[row.ID] = struct{}{}
		changed = true
	}

	// 2. Backlog section: new ID-less rows create new items (FR74); existing
	// rows update in place or surface a conflict per the 3-way comparison
	// against the last-rendered snapshot (FR73, 86-87).
	lastSynced := getBacklogMarkdownSyncState(ws).LastRenderedItem
	currentByID := map[string]Task{}
	for _, item := range localBacklogItemViews(ws) {
		currentByID[item.Task.ID] = item.Task
	}

	var newConflicts []BacklogSyncConflict
	presentBacklogIDs := map[string]struct{}{}
	// fileOrder tracks the resolved ID for every successfully processed
	// Backlog row, in file order, so a pure reorder (no field edits at all)
	// still updates BacklogRank (FR76).
	fileOrder := make([]string, 0, len(doc.BacklogRows))

	for _, row := range doc.BacklogRows {
		if row.ID == "" {
			created, err := svc.Create(BacklogCreateInput{
				WorkspaceID:  workspaceID,
				Description:  row.Title,
				Priority:     priorityWordToInt(row.Priority),
				Tags:         row.Tags,
				ReferenceURL: row.URL,
				SourceType:   BacklogSourceBacklogFile,
			})
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("failed to import new backlog row %q: %v", row.Title, err))
				continue
			}
			changed = true
			fileOrder = append(fileOrder, created.ID)
			continue
		}

		presentBacklogIDs[row.ID] = struct{}{}
		fileOrder = append(fileOrder, row.ID)
		task, exists := currentByID[row.ID]
		if !exists {
			// Already handled via promotion above, or (defensively) a
			// duplicate across sections that parsing should have already
			// rejected; nothing further to do either way.
			continue
		}

		fileSnap := snapshotFromRow(row)
		oriSnap := snapshotFromTask(task)
		if snapshotsEqual(fileSnap, oriSnap) {
			continue
		}

		lastSnap, hadLast := lastSynced[row.ID]
		applyFile := func() {
			priority := priorityWordToInt(row.Priority)
			desc := row.Title
			url := row.URL
			tags := row.Tags
			if _, err := svc.Update(workspaceID, row.ID, BacklogUpdateInput{
				Description:  &desc,
				Priority:     &priority,
				Tags:         &tags,
				ReferenceURL: &url,
			}); err != nil {
				warnings = append(warnings, fmt.Sprintf("failed to apply BACKLOG.md edit for %s: %v", row.ID, err))
				return
			}
			changed = true
		}

		switch {
		case !hadLast:
			// No prior snapshot to compare against (e.g. the very first
			// import after this item was created directly through the file
			// itself). Treat the file as authoritative, matching the plain
			// file-only-change rule (FR86).
			applyFile()
		case snapshotsEqual(oriSnap, lastSnap) && !snapshotsEqual(fileSnap, lastSnap):
			// Pure file-side edit.
			applyFile()
		case !snapshotsEqual(oriSnap, lastSnap) && snapshotsEqual(fileSnap, lastSnap):
			// Pure Ori-side edit; file is stale for this item. The next
			// render (below) brings the file back in line — no action here.
		default:
			// Both sides changed the same item: conflict. Neither version is
			// applied; both are retained until the user resolves it (FR87).
			newConflicts = append(newConflicts, BacklogSyncConflict{
				ItemID:     row.ID,
				Title:      task.Description,
				OriValue:   oriSnap,
				FileValue:  fileSnap,
				DetectedAt: time.Now(),
			})
		}
	}

	// File order is meaningful (FR76): if the file's row order differs from
	// the current persistent rank order, apply it. This also catches a pure
	// reorder with no other field edits, which the per-row diff above never
	// touches (BacklogRank is not part of the snapshot comparison).
	if len(fileOrder) > 0 {
		// Re-fetch: ws is the snapshot from the start of this call and does
		// not reflect the creates/updates already applied above (each went
		// through its own store.Update).
		freshWS, err := s.store.Get(workspaceID)
		if err != nil {
			return changed, append(warnings, fmt.Sprintf("failed to re-read workspace for reorder check: %v", err)), nil
		}
		currentOrder := make([]string, 0, len(fileOrder))
		for _, item := range localBacklogItemViews(freshWS) {
			if _, present := presentBacklogIDs[item.Task.ID]; present {
				currentOrder = append(currentOrder, item.Task.ID)
			}
		}
		if !stringSlicesEqual(currentOrder, fileOrder) {
			if _, err := svc.Reorder(workspaceID, fileOrder); err != nil {
				warnings = append(warnings, fmt.Sprintf("failed to apply BACKLOG.md row order: %v", err))
			} else {
				changed = true
			}
		}
	}

	// 3. Removed rows: a Backlog row present at last render but missing now
	// (and not explicitly promoted this pass) is NOT deleted — Ori restores
	// it on the next render and the caller sees an explicit warning (FR80).
	for id, snap := range lastSynced {
		if _, stillListed := presentBacklogIDs[id]; stillListed {
			continue
		}
		if _, wasPromoted := handledIDs[id]; wasPromoted {
			continue
		}
		task, stillExists := currentByID[id]
		if !stillExists {
			continue // deleted through Ori itself; nothing to restore
		}
		if task.Status != TaskStatusBacklog {
			continue // left Backlog through some other legitimate path
		}
		title := task.Description
		if title == "" {
			title = snap.Title
		}
		warnings = append(warnings, fmt.Sprintf(
			"a BACKLOG.md row for %q was removed but the item still exists in Ori; delete it through Ori to remove it — it will reappear on the next sync", title))
	}

	if len(newConflicts) > 0 {
		if err := s.persistSyncState(workspaceID, func(state *backlogMarkdownSyncState) {
			state.Conflicts = mergeBacklogSyncConflicts(state.Conflicts, newConflicts)
		}); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to persist sync conflicts: %v", err))
		}
	}

	return changed, warnings, nil
}

func mergeBacklogSyncConflicts(existing []BacklogSyncConflict, fresh []BacklogSyncConflict) []BacklogSyncConflict {
	byID := map[string]BacklogSyncConflict{}
	for _, c := range existing {
		byID[c.ItemID] = c
	}
	for _, c := range fresh {
		byID[c.ItemID] = c // fresh detection replaces a stale one for the same item
	}
	out := make([]BacklogSyncConflict, 0, len(byID))
	for _, c := range byID {
		out = append(out, c)
	}
	return out
}

// RenderAfterMutation regenerates BACKLOG.md from current structured state
// and records the per-item snapshot used for the next import's 3-way
// comparison (FR83, 85-86). A write failure persists a repair-needed status
// without touching any structured task data (FR88).
func (s *FileBacklogSynchronizer) RenderAfterMutation(workspaceID string) error {
	path, ws, ok, err := s.resolve(workspaceID)
	if err != nil || !ok {
		return err
	}

	// Once the file is a generated index, a regeneration must never silently
	// overwrite edits someone made to it (FR-122). The stored hash of what we
	// last wrote is the evidence: if the file no longer matches, a human
	// changed it, and replacing it is a decision they have to make.
	//
	// The mutation that triggered this render is already persisted — the
	// structured record is the source of truth — so refusing to write the file
	// loses nothing. It only leaves the index stale until the collision is
	// resolved, which the sync status reports.
	if !s.forceRender && backlogMarkdownIsGenerated(ws) {
		edited, hashErr := backlogMarkdownEditedSinceRender(path, getBacklogMarkdownSyncState(ws))
		if hashErr == nil && edited {
			return s.persistSyncState(workspaceID, func(state *backlogMarkdownSyncState) {
				state.RepairNeeded = true
				state.Warning = "BACKLOG.md was edited outside Ori. It is a generated index, so those edits were not imported; choose replace or export to continue."
			})
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { // #nosec G301 -- 0755 matches this package's established workspace-folder directory permission (see task_markdown_sync.go and the other ~20 os.MkdirAll call sites in this package)
		return err
	}
	content := RenderWorkspaceBacklogMarkdown(ws)
	if err := atomicWriteFile(path, []byte(content)); err != nil {
		writeErr := fmt.Errorf("write BACKLOG.md: %w", err)
		_ = s.persistSyncState(workspaceID, func(state *backlogMarkdownSyncState) {
			state.RepairNeeded = true
			state.Warning = writeErr.Error()
		})
		return writeErr
	}

	snapshots := make(map[string]backlogSyncItemSnapshot)
	for _, item := range localBacklogItemViews(ws) {
		snapshots[item.Task.ID] = snapshotFromTask(item.Task)
	}
	// Record what we just wrote, so the next render can tell an untouched
	// file from a hand-edited one.
	rendered := backlogMarkdownContentHash(content)
	return s.persistSyncState(workspaceID, func(state *backlogMarkdownSyncState) {
		state.LastSyncedAt = time.Now()
		state.LastRenderedItem = snapshots
		state.LastRenderedHash = rendered
		state.RepairNeeded = false
		state.Warning = ""
	})
}

// backlogMarkdownContentHash hashes a rendered document for edit detection.
func backlogMarkdownContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// backlogMarkdownEditedSinceRender reports whether the file on disk differs
// from what this synchronizer last wrote.
//
// A missing file is NOT an edit — it simply needs regenerating. An unreadable
// file is treated as unedited so a transient IO error cannot permanently block
// regeneration; the write itself will surface any real problem.
func backlogMarkdownEditedSinceRender(path string, state backlogMarkdownSyncState) (bool, error) {
	if state.LastRenderedHash == "" {
		// Nothing was ever rendered under the new regime, so there is no
		// baseline to compare against and nothing to protect yet.
		return false, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is the workspace's own managed BACKLOG.md, resolved by s.resolve
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return backlogMarkdownContentHash(string(data)) != state.LastRenderedHash, nil
}

// Status returns the current sync-health summary (FR84, 91).
func (s *FileBacklogSynchronizer) Status(workspaceID string) BacklogSyncStatus {
	ws, err := s.store.Get(workspaceID)
	if err != nil {
		return BacklogSyncStatus{}
	}
	state := getBacklogMarkdownSyncState(ws)
	status := BacklogSyncStatus{Enabled: true}
	if !state.LastSyncedAt.IsZero() {
		t := state.LastSyncedAt
		status.LastSyncedAt = &t
	}
	if state.RepairNeeded {
		status.Warning = state.Warning
	}
	if len(state.Conflicts) > 0 {
		status.Conflict = true
		if status.Warning == "" {
			status.Warning = fmt.Sprintf("%d backlog item(s) have unresolved sync conflicts", len(state.Conflicts))
		}
	}
	return status
}

// Conflicts returns every unresolved same-item conflict for a workspace.
func (s *FileBacklogSynchronizer) Conflicts(workspaceID string) []BacklogSyncConflict {
	ws, err := s.store.Get(workspaceID)
	if err != nil {
		return nil
	}
	return getBacklogMarkdownSyncState(ws).Conflicts
}

// ResolveConflict applies the user's whole-item choice and clears the
// conflict record (FR87). useFile=true writes the retained file version to
// the task; useFile=false keeps Ori's current value as-is. Either way the
// next render reflects the resolution and the conflict is cleared.
func (s *FileBacklogSynchronizer) ResolveConflict(workspaceID, itemID string, useFile bool) error {
	ws, err := s.store.Get(workspaceID)
	if err != nil {
		return err
	}
	state := getBacklogMarkdownSyncState(ws)
	var conflict *BacklogSyncConflict
	remaining := make([]BacklogSyncConflict, 0, len(state.Conflicts))
	for i := range state.Conflicts {
		if state.Conflicts[i].ItemID == itemID {
			c := state.Conflicts[i]
			conflict = &c
			continue
		}
		remaining = append(remaining, state.Conflicts[i])
	}
	if conflict == nil {
		return fmt.Errorf("no conflict recorded for item %s", itemID)
	}

	if useFile {
		svc := NewBacklogService(s.store)
		desc := conflict.FileValue.Title
		priority := priorityWordToInt(conflict.FileValue.Priority)
		tags := conflict.FileValue.Tags
		url := conflict.FileValue.URL
		if _, err := svc.Update(workspaceID, itemID, BacklogUpdateInput{
			Description:  &desc,
			Priority:     &priority,
			Tags:         &tags,
			ReferenceURL: &url,
		}); err != nil {
			return err
		}
	}

	if err := s.persistSyncState(workspaceID, func(s *backlogMarkdownSyncState) {
		s.Conflicts = remaining
	}); err != nil {
		return err
	}
	return s.RenderAfterMutation(workspaceID)
}

// --- Collision handling (FR89, Tech Consideration 14) ---

// PreviewCollision reports a non-Ori-managed file already present at the
// managed path, if any. A nil result means it is safe to write (no file, or
// an Ori-managed file already there).
func (s *FileBacklogSynchronizer) PreviewCollision(workspaceID string) (*BacklogMarkdownCollision, error) {
	path, _, ok, err := s.resolve(workspaceID)
	if err != nil || !ok {
		return nil, err
	}
	return detectBacklogMarkdownCollision(path)
}

// ReplaceCollision explicitly overwrites a non-Ori-managed file at the
// managed path with a fresh Ori render, discarding its prior content. Only
// called after the user explicitly chooses "replace".
func (s *FileBacklogSynchronizer) ReplaceCollision(workspaceID string) error {
	return s.RenderAfterMutation(workspaceID)
}

// AdoptCollision attempts to parse a non-Ori-managed file's existing bullet
// rows as Backlog items and imports the ones that parse cleanly, then
// renders Ori's frontmatter over the file going forward. Only called after
// the user explicitly chooses "adopt" from a PreviewCollision result.
func (s *FileBacklogSynchronizer) AdoptCollision(workspaceID string) (*BacklogMarkdownImportResult, error) {
	path, ws, ok, err := s.resolve(workspaceID)
	if err != nil || !ok {
		return &BacklogMarkdownImportResult{}, err
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from s.resolve(workspaceID), which resolves through the store, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return &BacklogMarkdownImportResult{}, nil
		}
		return &BacklogMarkdownImportResult{}, err
	}

	// Adoption is lenient about the missing/foreign frontmatter (that is the
	// whole point of adopting an unmanaged file) but otherwise uses the same
	// bounded parser: unknown IDs and duplicates are still rejected, and a
	// document with no recognizable "## Backlog" section simply yields zero
	// rows rather than erroring, so adopting a plain empty/unrelated file is
	// harmless (it becomes the standard empty Backlog document on render).
	if len(data) > maxBacklogMarkdownBytes {
		return &BacklogMarkdownImportResult{}, fmt.Errorf("BACKLOG.md is too large (%d bytes, max %d)", len(data), maxBacklogMarkdownBytes)
	}
	body := stripForeignFrontmatterForAdoption(string(data))
	known := map[string]struct{}{}
	for _, item := range localBacklogItemViews(ws) {
		known[item.Task.ID] = struct{}{}
	}
	doc, err := parseBacklogMarkdownBody(body, known)
	if err != nil {
		return &BacklogMarkdownImportResult{}, err
	}

	changed, warnings, err := s.applyDocument(workspaceID, ws, doc)
	if err != nil {
		return &BacklogMarkdownImportResult{Warnings: warnings}, err
	}
	// Always render after adoption, even with no rows: this is what turns
	// the file Ori-managed (correct frontmatter) so future collision checks
	// pass.
	if renderErr := s.RenderAfterMutation(workspaceID); renderErr != nil {
		warnings = append(warnings, renderErr.Error())
	}
	return &BacklogMarkdownImportResult{Changed: changed, Warnings: warnings}, nil
}

// stripForeignFrontmatterForAdoption removes any non-Ori frontmatter block so
// adoption can parse the remaining body as plain Backlog rows regardless of
// what the unmanaged file's own header looked like.
func stripForeignFrontmatterForAdoption(content string) string {
	frontmatter, body, err := splitTaskMarkdownFrontmatter(content)
	if err != nil {
		return content
	}
	if strings.TrimSpace(frontmatter) == "" {
		return content
	}
	return body
}

// --- Scaffolding & backfill (FR67-68, 92, 99) ---

// EnsureBacklogMarkdownFile creates BACKLOG.md at the workspace-owned root if
// it does not already exist, used by both initial scaffolding (FR67) and
// idempotent repair/backfill (FR68, 99). It never overwrites a pre-existing
// non-Ori file — a non-nil collision result means the caller must ask the
// user to adopt, replace, or leave it (FR89). Calling this repeatedly when
// the file already exists and is Ori-managed simply re-renders it, which is
// safe and produces no duplicates.
func (s *FileBacklogSynchronizer) EnsureBacklogMarkdownFile(workspaceID string) (*BacklogMarkdownCollision, error) {
	collision, err := s.PreviewCollision(workspaceID)
	if err != nil {
		return nil, err
	}
	if collision != nil {
		return collision, nil
	}
	return nil, s.RenderAfterMutation(workspaceID)
}

// BackfillBacklogMarkdownForAllWorkspaces ensures BACKLOG.md exists for every
// workspace the store can list, for repairing managed workspaces created
// before this feature shipped. It is safe to rerun (FR68, 99): existing
// Ori-managed files are simply re-rendered, and unmanaged collisions are
// left untouched and reported for the caller to log/surface, never fatal to
// the sweep. Returns the number of files it wrote.
func BackfillBacklogMarkdownForAllWorkspaces(store Store) (int, []error) {
	sync := NewFileBacklogSynchronizer(store)
	ids, err := store.List()
	if err != nil {
		return 0, []error{err}
	}
	written := 0
	var errs []error
	for _, id := range ids {
		ws, err := store.Get(id)
		if err != nil || ws == nil {
			continue
		}
		if ws.Status == StatusTrashed || ws.Status == StatusMissing {
			continue
		}
		collision, err := sync.EnsureBacklogMarkdownFile(id)
		if err != nil {
			errs = append(errs, fmt.Errorf("workspace %s: %w", id, err))
			continue
		}
		if collision != nil {
			continue // left untouched by design; not an error
		}
		written++
	}
	return written, errs
}
