// Package continuityprep keeps each local workspace folder's portable
// continuity checkpoint current. It composes the domain collectors over one
// shared database read view and publishes through workspacecontinuity; it owns
// no live data of its own and never grants attachment or execution.
package continuityprep

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"sort"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// Collector gathers every supported domain for one workspace folder. All SQL
// reads use the caller's single read view so the checkpoint is one consistent
// moment of the database; file reads are fingerprinted and rechecked by the
// preparation fence before publication.
type Collector struct {
	Sessions    *session.SQLiteStore
	Briefs      *dailybrief.SQLiteStore
	Assistants  *personalassistant.SQLiteStore
	UploadsBase func() string
}

// Collect returns the canonical file fingerprints and the immediate physical
// child workspace IDs of folder.
func (c *Collector) Collect(ctx context.Context, q workspacecontinuity.Queryer, workspaceID, folder string, spool *workspacecontinuity.Spool) ([]workspacecontinuity.Fingerprint, []string, error) {
	if c == nil || c.Sessions == nil || c.Briefs == nil || c.Assistants == nil {
		return nil, nil, workspacecontinuity.ErrInvalid
	}
	canonical, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, workspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		return nil, nil, reason("workspace", err)
	}
	record, err := c.Sessions.SnapshotContinuityWorkspace(ctx, q, canonical)
	if err != nil {
		return nil, nil, reason("workspace", err)
	}
	if record.ID != workspaceID {
		return nil, nil, reason("workspace", workspacecontinuity.ErrInvalid)
	}
	if err := spool.AddRecord(ctx, "workspace", "workspaces", record); err != nil {
		return nil, nil, reason("workspace", err)
	}
	files := []workspacecontinuity.Fingerprint{{Path: workspace.WorkspaceConfigFile, Digest: workspacecontinuity.Digest(canonical), Bytes: int64(len(canonical))}}
	ws, err := workspace.DecodeContinuityWorkspace(record, canonical)
	if err != nil {
		return nil, nil, reason("workspace", err)
	}
	profiles, err := workspace.CollectContinuityProfiles(ctx, folder, ws, spool)
	if err != nil {
		return nil, nil, reason("agents", err)
	}
	files = append(files, profiles...)
	binding, err := assistantBinding(ctx, folder, ws)
	if err != nil {
		return nil, nil, reason("assistant", err)
	}
	steps := []struct {
		domain string
		run    func() error
	}{
		{"assistant", func() error { return c.Assistants.CollectContinuityAgreement(ctx, q, workspaceID, binding, spool) }},
		{"followups", func() error { return followup.CollectContinuityFollowUps(ctx, q, workspaceID, spool) }},
		{"brief_config", func() error { return c.Briefs.CollectContinuityConfig(ctx, q, workspaceID, spool) }},
		{"brief_history", func() error { return dailybrief.CollectContinuityRevisions(ctx, q, workspaceID, spool) }},
		{"sessions", func() error { return session.CollectContinuitySessions(ctx, q, workspaceID, spool) }},
		{"tool_history", func() error { return session.CollectContinuityToolHistory(ctx, q, workspaceID, spool) }},
		{"notes", func() error { return session.CollectContinuityNotes(ctx, q, workspaceID, spool) }},
		{"uploads", func() error { return c.collectUploads(ctx, q, workspaceID, spool) }},
		{"knowledge", func() error { return personalassistant.CollectContinuityKnowledge(ctx, folder, workspaceID, spool) }},
		{"setup", func() error { return personalassistant.CollectContinuitySetup(ctx, q, workspaceID, spool) }},
	}
	for _, step := range steps {
		if err := step.run(); err != nil {
			return nil, nil, reason(step.domain, err)
		}
	}
	owned, err := workspace.CollectContinuityFolderFiles(ctx, folder)
	if err != nil {
		return nil, nil, reason("folder", err)
	}
	files = append(files, owned...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	children, err := PhysicalChildren(ctx, folder)
	if err != nil {
		return nil, nil, reason("children", err)
	}
	return files, children, nil
}

func (c *Collector) collectUploads(ctx context.Context, q workspacecontinuity.Queryer, workspaceID string, spool *workspacecontinuity.Spool) error {
	uploadsBase := ""
	if c.UploadsBase != nil {
		uploadsBase = c.UploadsBase()
	}
	if uploadsBase != "" {
		return sessionfiles.CollectContinuityUploads(ctx, q, uploadsBase, workspaceID, spool)
	}
	if err := requireNoUploads(ctx, q, workspaceID); err != nil {
		return err
	}
	return spool.SetAvailability(ctx, "uploads", workspacecontinuity.Empty, "")
}

// reason labels a collector failure with a stable code such as
// "sessions_limit" or "folder_unsafe" for the status shown to the user. A
// concurrent change stays unlabeled so status reports it as a retryable update.
func reason(domain string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, workspacecontinuity.ErrChanged), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, workspacecontinuity.ErrLimit):
		return workspacecontinuity.WithReason(domain+"_limit", err)
	case errors.Is(err, workspacecontinuity.ErrUnsafe):
		return workspacecontinuity.WithReason(domain+"_unsafe", err)
	case errors.Is(err, workspacecontinuity.ErrIncomplete):
		return workspacecontinuity.WithReason(domain+"_incomplete", err)
	case errors.Is(err, workspacecontinuity.ErrInvalid), errors.Is(err, workspacecontinuity.ErrDigest), errors.Is(err, workspacecontinuity.ErrVersion):
		return workspacecontinuity.WithReason(domain+"_invalid", err)
	case errors.Is(err, workspacecontinuity.ErrConflict):
		return workspacecontinuity.WithReason(domain+"_conflict", err)
	default:
		return workspacecontinuity.WithReason(domain+"_failed", err)
	}
}

// requireNoUploads fails closed when no uploads store is configured but the
// workspace has sessions that could own uploads: those cannot be declared empty.
func requireNoUploads(ctx context.Context, q workspacecontinuity.Queryer, workspaceID string) error {
	var sessions int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE workspace_id=?`, workspaceID).Scan(&sessions); err != nil {
		return err
	}
	if sessions != 0 {
		return workspacecontinuity.ErrIncomplete
	}
	return nil
}

// assistantBinding builds exact assistant evidence for a workspace that
// presents a personal assistant: its single entry instance and that
// instance's own workspace profile file. An ordinary workspace has none.
func assistantBinding(ctx context.Context, folder string, ws *workspace.Workspace) (*personalassistant.ContinuityBinding, error) {
	presented := false
	for key := range ws.SharedData {
		if key == personalhq.PersonalAssistantPresentationKey {
			presented = true
		}
	}
	if !presented {
		return nil, nil
	}
	entry := ""
	for _, instance := range ws.AgentInstances {
		if instance.EntryPoint {
			if entry != "" {
				return nil, workspacecontinuity.ErrInvalid
			}
			entry = instance.Name
		}
	}
	if entry == "" {
		return nil, workspacecontinuity.ErrIncomplete
	}
	profilePath := path.Join(workspace.WorkspaceAgentsDir, workspace.Slugify(entry), workspace.WorkspaceAgentConfigFile)
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, folder, profilePath, workspacecontinuity.MaxRecordBytes)
	if err != nil {
		return nil, err
	}
	var profile agent.Agent
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, workspacecontinuity.ErrInvalid
	}
	binding, err := personalassistant.NewContinuityBinding(session.ConvertAgentWorkspace(ws), &profile)
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

// PhysicalChildren lists the workspace IDs directly inside folder's
// sub-workspaces directory. Physical placement, not a stored parent pointer,
// decides membership of a copied tree.
func PhysicalChildren(ctx context.Context, folder string) ([]string, error) {
	root, err := os.OpenRoot(folder)
	if err != nil {
		return nil, workspacecontinuity.ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(workspace.SubWorkspacesDir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, workspacecontinuity.ErrUnsafe
	}
	dir, err := root.Open(workspace.SubWorkspacesDir)
	if err != nil {
		return nil, workspacecontinuity.ErrUnsafe
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if readErr != nil || closeErr != nil {
		return nil, workspacecontinuity.ErrUnsafe
	}
	if len(entries) > workspacecontinuity.MaxChildren {
		return nil, workspacecontinuity.ErrLimit
	}
	return childIDs(ctx, folder, entries)
}

func childIDs(ctx context.Context, folder string, entries []os.DirEntry) ([]string, error) {
	ids := []string{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, workspacecontinuity.ErrUnsafe
		}
		if !entry.IsDir() {
			continue
		}
		child := folder + string(os.PathSeparator) + workspace.SubWorkspacesDir + string(os.PathSeparator) + entry.Name()
		data, err := workspacecontinuity.ReadCanonicalFile(ctx, child, workspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
		if errors.Is(err, workspacecontinuity.ErrIncomplete) {
			continue // not a workspace folder
		}
		if err != nil {
			return nil, err
		}
		var header struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(data, &header); err != nil || !workspacecontinuity.ValidID(header.ID) {
			return nil, workspacecontinuity.ErrInvalid
		}
		ids = append(ids, header.ID)
	}
	sort.Strings(ids)
	return ids, nil
}
