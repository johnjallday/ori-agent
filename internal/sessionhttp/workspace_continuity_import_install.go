package sessionhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/trigger"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// installContinuityTree puts the reviewed folders in place. A folder chosen
// from outside the Workspace Directory is copied (verified file by file) into
// private scratch beside the user's workspaces, projected there, and renamed
// into place in one step; the original is never modified. A folder already in
// the Workspace Directory is projected where it is. Projection replaces what
// only meant something on the source machine: the workspace file loses local
// connection settings and grants, agent profiles lose keys and permission
// defaults, webhook tokens are reissued, and an adopted HQ's remembered-item
// metadata is rebound to this folder. Retrying repeats only unfinished steps.
func (h *Handler) installContinuityTree(ctx context.Context, op workspacecontinuity.Operation, plan []continuityPlanMember) error {
	all := true
	for _, p := range plan {
		all = all && p.Installed
	}
	if all {
		return h.finishContinuityFileComponents(ctx, op, plan)
	}
	base := h.workspaceStore.BasePath()
	root := plan[0]
	if root.ParentID != "" {
		return workspacecontinuity.ErrInvalid
	}
	if root.InPlace {
		current, err := renameContinuityInPlace(base, plan)
		if err != nil {
			return err
		}
		generations, err := h.continuityGenerations(ctx, op, plan, current)
		if err != nil {
			return err
		}
		for _, p := range plan {
			if err := h.projectContinuityMember(ctx, p, current[p.WorkspaceID], current[p.WorkspaceID], generations[p.WorkspaceID]); err != nil {
				return err
			}
			// The copied checkpoint may carry widened modes from the copy;
			// this installation prepares its own there next.
			if err := workspacecontinuity.AdoptManagedPermissions(current[p.WorkspaceID]); err != nil {
				return err
			}
		}
	} else {
		sources := map[string]string{}
		for _, p := range plan {
			sources[p.WorkspaceID] = p.SourceDir
		}
		generations, err := h.continuityGenerations(ctx, op, plan, sources)
		if err != nil {
			return err
		}
		final := filepath.Join(base, root.FolderSlug)
		if _, err := os.Lstat(final); err == nil {
			// An earlier attempt already renamed its complete copy into place.
			if err := requireContinuityFolder(final, root.WorkspaceID); err != nil {
				return err
			}
		} else {
			staging := filepath.Join(base, ".ori-import-"+op.ID)
			if err := os.RemoveAll(staging); err != nil { // #nosec G703 -- operation-owned scratch under the local Workspace Directory
				return err
			}
			if err := os.MkdirAll(staging, 0o750); err != nil {
				return err
			}
			targets := continuityTargets(staging, plan)
			for _, p := range plan {
				if err := copyContinuityMember(ctx, p.SourceDir, targets[p.WorkspaceID], generations[p.WorkspaceID].Manifest); err != nil {
					_ = os.RemoveAll(staging)
					return err
				}
			}
			for _, p := range plan {
				if err := h.projectContinuityMember(ctx, p, targets[p.WorkspaceID], p.SourceDir, generations[p.WorkspaceID]); err != nil {
					_ = os.RemoveAll(staging)
					return err
				}
			}
			stageRoot := targets[root.WorkspaceID]
			if err := os.Rename(stageRoot, final); err != nil {
				_ = os.RemoveAll(staging)
				return err
			}
			_ = os.Remove(staging)
		}
	}
	if err := h.store.DB().InTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE continuity_installs SET installed=1 WHERE operation_id=?`, op.ID)
		return err
	}); err != nil {
		return err
	}
	return h.finishContinuityFileComponents(ctx, op, plan)
}

// continuityTargets maps each member to its folder under a new root.
func continuityTargets(rootDir string, plan []continuityPlanMember) map[string]string {
	targets := map[string]string{}
	for _, p := range plan {
		if p.ParentID == "" {
			targets[p.WorkspaceID] = filepath.Join(rootDir, p.FolderSlug)
			continue
		}
		targets[p.WorkspaceID] = filepath.Join(targets[p.ParentID], agentworkspace.SubWorkspacesDir, p.FolderSlug)
	}
	return targets
}

// renameContinuityInPlace gives an in-place tree its planned folder names
// (top-down, so each child is found under its parent's current name).
func renameContinuityInPlace(base string, plan []continuityPlanMember) (map[string]string, error) {
	current := map[string]string{}
	for _, p := range plan {
		parent := base
		if p.ParentID != "" {
			parent = filepath.Join(current[p.ParentID], agentworkspace.SubWorkspacesDir)
		}
		want := filepath.Join(parent, p.FolderSlug)
		have := filepath.Join(parent, filepath.Base(p.SourceDir))
		if want != have {
			if _, err := os.Lstat(want); errors.Is(err, os.ErrNotExist) {
				if err := os.Rename(have, want); err != nil {
					return nil, err
				}
			}
		}
		if err := requireContinuityFolder(want, p.WorkspaceID); err != nil {
			return nil, err
		}
		current[p.WorkspaceID] = want
	}
	return current, nil
}

func requireContinuityFolder(dir, workspaceID string) error {
	data, err := workspacecontinuity.ReadCanonicalFile(context.Background(), dir, agentworkspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		return err
	}
	var header struct {
		ID string `json:"id"`
	}
	if err := jsonUnmarshalStrictID(data, &header.ID); err != nil || header.ID != workspaceID {
		return workspacecontinuity.ErrConflict
	}
	return nil
}

// continuityGenerations loads each member's receipt-bound generation from
// the folder that holds it (dirs maps workspace ID to that folder).
func (h *Handler) continuityGenerations(ctx context.Context, op workspacecontinuity.Operation, plan []continuityPlanMember, dirs map[string]string) (map[string]workspacecontinuity.Generation, error) {
	local := workspacecontinuity.NewLocalStore(h.store.DB())
	result := map[string]workspacecontinuity.Generation{}
	for _, p := range plan {
		attachment, err := local.Attachment(ctx, p.WorkspaceID)
		if err != nil {
			return nil, err
		}
		if attachment.OperationID != op.ID {
			return nil, workspacecontinuity.ErrConflict
		}
		generation, err := workspacecontinuity.LoadGeneration(ctx, dirs[p.WorkspaceID], attachment.SourceGeneration, attachment.SourceDigest)
		if err != nil {
			return nil, err
		}
		result[p.WorkspaceID] = generation
	}
	return result, nil
}

// copyContinuityMember copies exactly the reviewed files of one member, each
// verified against its fingerprint, into target. The checkpoint itself is
// not copied: this installation prepares its own once the import completes.
func copyContinuityMember(ctx context.Context, sourceDir, target string, manifest workspacecontinuity.Manifest) error {
	if err := os.MkdirAll(target, 0o750); err != nil {
		return err
	}
	for _, file := range manifest.Files {
		destination := filepath.Join(target, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
			return err
		}
		temporary := filepath.Join(filepath.Dir(destination), ".ori-import-"+uuid.NewString())
		out, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		copyErr := workspacecontinuity.CopyVerifiedFile(ctx, sourceDir, file, out)
		syncErr, closeErr := out.Sync(), out.Close()
		if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
			_ = os.Remove(temporary)
			return err
		}
		if err := os.Rename(temporary, destination); err != nil {
			_ = os.Remove(temporary)
			return err
		}
	}
	return nil
}

// projectContinuityMember rewrites one member's source-machine-only files in
// dir (already holding the reviewed bytes). Records come from the reviewed
// generation in recordsDir. workspace.json is replaced last, so a folder
// whose workspace.json no longer matches the checkpoint is fully projected.
func (h *Handler) projectContinuityMember(ctx context.Context, p continuityPlanMember, dir, recordsDir string, generation workspacecontinuity.Generation) error {
	manifest := generation.Manifest
	if manifest.WorkspaceID == "" {
		return nil // an in-place member whose projection already finished
	}
	files := map[string]workspacecontinuity.Fingerprint{}
	for _, file := range manifest.Files {
		files[file.Path] = file
	}
	workspaceFile := files[agentworkspace.WorkspaceConfigFile]
	current, err := workspacecontinuity.ReadCanonicalFile(ctx, dir, agentworkspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		return err
	}
	if workspacecontinuity.Digest(current) != workspaceFile.Digest {
		return nil // workspace.json is projected last; this member is done
	}
	var record workspacecontinuity.Record
	if err := workspacecontinuity.ReadGenerationRecords(ctx, recordsDir, generation, "workspace", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "workspaces" || len(chunk.Records) != 1 {
			return workspacecontinuity.ErrInvalid
		}
		record = chunk.Records[0]
		return nil
	}); err != nil {
		return err
	}
	ws, err := agentworkspace.DecodeContinuityWorkspace(record, current)
	if err != nil {
		return err
	}
	// Agent profiles: the denied definition replaces the copied file.
	if err := workspacecontinuity.ReadGenerationRecords(ctx, recordsDir, generation, "agents", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "profiles" {
			return nil
		}
		for _, r := range chunk.Records {
			filePath, data, err := agentworkspace.ProjectContinuityProfileFile(r, ws)
			if err != nil {
				return err
			}
			if err := replaceProjectedFile(ctx, dir, filePath, files[filePath], data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil && !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		return err
	}
	if original, ok := files[trigger.TriggersFileName]; ok {
		data, err := workspacecontinuity.ReadCanonicalFile(ctx, dir, trigger.TriggersFileName, int(workspacecontinuity.MaxBlobBytes))
		if err == nil && workspacecontinuity.Digest(data) == original.Digest {
			projected, _, err := trigger.ProjectImportedTriggers(data)
			if err != nil {
				return err
			}
			if err := replaceProjectedFile(ctx, dir, trigger.TriggersFileName, original, projected); err != nil {
				return err
			}
		}
	}
	if p.Disposition == workspacecontinuity.AdoptedHQ {
		if original, ok := files[personalassistant.KnowledgeSidecarPath]; ok {
			if err := h.projectContinuityKnowledge(ctx, recordsDir, generation, dir, p, original); err != nil {
				return err
			}
		}
	}
	projection, err := agentworkspace.ProjectContinuityWorkspace(record, current, p.ParentID)
	if err != nil {
		return err
	}
	projection.Workspace.FolderSlug = p.FolderSlug
	if p.Disposition == workspacecontinuity.AdoptedHQ {
		projection.Workspace.Designation = string(session.WorkspaceDesignationPersonalHQ)
	}
	data, err := agentworkspace.MarshalContinuityWorkspace(projection)
	if err != nil {
		return err
	}
	return workspacecontinuity.ReplaceCanonicalFile(ctx, dir, agentworkspace.WorkspaceConfigFile, workspaceFile.Digest, data)
}

// replaceProjectedFile replaces a copied file only while it still holds the
// reviewed bytes; a different current file means an earlier attempt already
// projected it.
func replaceProjectedFile(ctx context.Context, dir, name string, original workspacecontinuity.Fingerprint, data []byte) error {
	if original.Path == "" {
		return workspacecontinuity.ErrIncomplete
	}
	err := workspacecontinuity.ReplaceCanonicalFile(ctx, dir, name, original.Digest, data)
	if errors.Is(err, workspacecontinuity.ErrChanged) {
		return nil
	}
	return err
}

func (h *Handler) projectContinuityKnowledge(ctx context.Context, recordsDir string, generation workspacecontinuity.Generation, dir string, p continuityPlanMember, original workspacecontinuity.Fingerprint) error {
	var agreement personalassistant.ContinuityAgreement
	if err := workspacecontinuity.ReadGenerationRecords(ctx, recordsDir, generation, "assistant", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "agreements" || len(chunk.Records) != 1 {
			return workspacecontinuity.ErrInvalid
		}
		var err error
		agreement, err = personalassistant.DecodeContinuityAgreement(chunk.Records[0])
		return err
	}); err != nil {
		return err
	}
	data, err := workspacecontinuity.ReadCanonicalFile(ctx, dir, personalassistant.KnowledgeSidecarPath, int(original.Bytes)+1)
	if err != nil {
		return err
	}
	if workspacecontinuity.Digest(data) != original.Digest {
		return nil // already rebound by an earlier attempt
	}
	projected, _, err := personalassistant.ProjectContinuityKnowledge(data, personalassistant.KnowledgeBinding{
		UserID: agreement.SourceUserID, AssistantID: agreement.AssistantID, HQWorkspaceID: p.WorkspaceID,
		HQFolderSlug: p.FolderSlug, EntryAgentInstanceID: agreement.EntryInstanceID})
	if err != nil {
		// Unrebindable metadata stays as copied (it is inert: its owner no
		// longer matches) and the report says remembered items need review.
		return nil
	}
	return replaceProjectedFile(ctx, dir, personalassistant.KnowledgeSidecarPath, original, projected)
}

// finishContinuityFileComponents records the folder-owned components once
// their files are installed: agent profiles and knowledge metadata.
func (h *Handler) finishContinuityFileComponents(ctx context.Context, op workspacecontinuity.Operation, plan []continuityPlanMember) error {
	db := h.store.DB()
	local := workspacecontinuity.NewLocalStore(db)
	installed := continuityTargets(h.workspaceStore.BasePath(), plan)
	for _, p := range plan {
		scope := workspacecontinuity.RestoreScope{OperationID: op.ID, WorkspaceID: p.WorkspaceID, UserID: op.UserID}
		outcomes, err := local.ComponentOutcomes(ctx, scope)
		if err != nil {
			return err
		}
		attachment, err := local.Attachment(ctx, p.WorkspaceID)
		if err != nil {
			return err
		}
		folder := p.SourceDir
		if p.InPlace {
			folder = installed[p.WorkspaceID]
		}
		generation, loadErr := workspacecontinuity.LoadGeneration(ctx, folder, attachment.SourceGeneration, attachment.SourceDigest)
		for _, outcome := range outcomes {
			if outcome.Status != "restoring" || outcome.Domain != "agents" && outcome.Domain != "knowledge" {
				continue
			}
			status, count, reason := "restored", int64(0), ""
			if loadErr != nil {
				// The in-place checkpoint was already replaced; the files are
				// installed, counts come from the plan.
				reason = "count_unavailable"
			} else {
				for _, component := range generation.Manifest.Components {
					if component.Domain != outcome.Domain {
						continue
					}
					switch component.Availability {
					case workspacecontinuity.Present:
						count = component.Counts["profiles"]
						if outcome.Domain == "knowledge" {
							count = continuityKnowledgeItems(ctx, folder, generation, p.WorkspaceID)
						}
					case workspacecontinuity.Empty:
					default:
						status, reason = "unavailable", component.Reason
					}
				}
				if outcome.Domain == "knowledge" && p.Disposition != workspacecontinuity.AdoptedHQ && count > 0 {
					reason = "inactive_until_adopted"
				}
			}
			if err := db.InTransaction(ctx, func(tx *sql.Tx) error {
				return workspacecontinuity.SetComponentOutcome(ctx, tx, scope, workspacecontinuity.ComponentOutcome{Domain: outcome.Domain, Status: status, Count: count, Reason: reason})
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// continuityKnowledgeItems counts remembered items in a member's reviewed
// knowledge summary (not the number of summary records).
func continuityKnowledgeItems(ctx context.Context, folder string, generation workspacecontinuity.Generation, workspaceID string) int64 {
	var items int64
	_ = workspacecontinuity.ReadGenerationRecords(ctx, folder, generation, "knowledge", func(chunk workspacecontinuity.Chunk) error {
		for _, r := range chunk.Records {
			if summary, err := personalassistant.DecodeContinuityKnowledge(r, workspaceID); err == nil {
				items += int64(summary.Items)
			}
		}
		return nil
	})
	return items
}

// jsonUnmarshalStrictID extracts a top-level "id" string.
func jsonUnmarshalStrictID(data []byte, id *string) error {
	var header struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return err
	}
	*id = header.ID
	return nil
}
