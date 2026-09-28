package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// continuityImportReview is read-only evidence for the Import Folder entry
// point. No inspected checkpoint is executable by the legacy import path.
// In particular, a damaged modern checkpoint is never a legacy export.
type continuityComponentReview struct {
	Domain       string                           `json:"domain"`
	Availability workspacecontinuity.Availability `json:"availability"`
	Reason       string                           `json:"reason,omitempty"`
	Counts       map[string]int64                 `json:"counts,omitempty"`
}

type continuitySavedWork struct {
	Tasks                 int `json:"tasks"`
	CompletedTasks        int `json:"completed_tasks"`
	InterruptedTasks      int `json:"interrupted_tasks"`
	CollaborationMessages int `json:"collaboration_messages"`
	Attachments           int `json:"attachments"`
	OwnedFiles            int `json:"owned_files"`
	ExternalReferences    int `json:"external_references"`
}

func (saved *continuitySavedWork) include(other *continuitySavedWork) {
	saved.Tasks += other.Tasks
	saved.CompletedTasks += other.CompletedTasks
	saved.InterruptedTasks += other.InterruptedTasks
	saved.CollaborationMessages += other.CollaborationMessages
	saved.Attachments += other.Attachments
	saved.OwnedFiles += other.OwnedFiles
	saved.ExternalReferences += other.ExternalReferences
}

type continuityImportReview struct {
	Status              string                         `json:"status"`
	ImportSupported     bool                           `json:"import_supported"`
	WorkspaceID         string                         `json:"workspace_id,omitempty"`
	CheckpointAt        string                         `json:"checkpoint_at,omitempty"`
	Components          []continuityComponentReview    `json:"components,omitempty"`
	SavedWork           *continuitySavedWork           `json:"saved_work,omitempty"`
	AssistantCandidates []continuityAssistantCandidate `json:"assistant_candidates,omitempty"`
	ModernDirectories   int                            `json:"modern_directories,omitempty"`
	ContainsModernChild bool                           `json:"contains_modern_child,omitempty"`
	// TreeDigest covers verified root+physical child membership, not consent or
	// destination state. It is intentionally absent for legacy/mixed/damaged trees.
	TreeDigest string `json:"tree_digest,omitempty"`
	// This SQL-only observation is not an import grant. Confirmation must
	// re-evaluate it inside the inactive receipt transaction.
	DestinationDigest string `json:"destination_digest,omitempty"`
	// Actions the destination allows for this reviewed tree: "continue"
	// (restore and adopt the one assistant) and/or "workspace_only".
	Actions           []string `json:"actions,omitempty"`
	RecommendedAction string   `json:"recommended_action,omitempty"`
	// AdoptionBlocked explains why "continue" is not offered.
	AdoptionBlocked string `json:"adoption_blocked,omitempty"`
	// Conflict names a destination state that prevents any import.
	Conflict string `json:"conflict,omitempty"`
	// AlreadyImported is the completed operation that restored this exact tree.
	AlreadyImported *continuityPriorImport `json:"already_imported,omitempty"`
	// History summarizes restorable saved history across the whole tree.
	History *continuityHistoryCounts `json:"history,omitempty"`
	Members int                      `json:"members,omitempty"`

	members []continuityImportMember
}

type continuityPriorImport struct {
	OperationID   string `json:"operation_id"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceSlug string `json:"workspace_slug,omitempty"`
}

// continuityHistoryCounts are record counts from the verified checkpoints,
// never content. Missing domains are reported per component, not as zero.
type continuityHistoryCounts struct {
	Sessions       int64 `json:"sessions"`
	Messages       int64 `json:"messages"`
	Uploads        int64 `json:"uploads"`
	ToolCalls      int64 `json:"tool_calls"`
	FollowUps      int64 `json:"follow_ups"`
	BriefRevisions int64 `json:"brief_revisions"`
	Notes          int64 `json:"notes"`
}

// continuityImportMember is one verified workspace of the reviewed tree, kept
// server-side for the confirmed import; it is never serialized.
type continuityImportMember struct {
	Dir        string
	Rel        string
	ParentID   string
	Inspection workspacecontinuity.Inspection
	Candidate  *continuityAssistantCandidate
}

type continuityTreeMember struct {
	Path              string `json:"path"`
	WorkspaceID       string `json:"workspace_id"`
	Generation        string `json:"generation"`
	ManifestDigest    string `json:"manifest_digest"`
	SourceFingerprint string `json:"source_fingerprint"`
}

func (h *continuityHistoryCounts) add(component workspacecontinuity.Component) {
	if component.Availability != workspacecontinuity.Present {
		return
	}
	c := component.Counts
	switch component.Domain {
	case "sessions":
		h.Sessions += c["sessions"]
		h.Messages += c["messages"]
	case "uploads":
		h.Uploads += c["uploads"]
	case "tool_history":
		h.ToolCalls += c["tool_calls"]
	case "followups":
		h.FollowUps += c["followups"]
	case "brief_history":
		h.BriefRevisions += c["revisions"]
	case "notes":
		h.Notes += c["notes"]
	}
}

// inspectSavedWorkspace validates one member's typed work without returning
// private content or treating source intent as destination execution authority.
func inspectSavedWorkspace(ctx context.Context, dir string, inspected workspacecontinuity.Inspection) (*continuitySavedWork, *agentworkspace.Workspace, error) {
	var component *workspacecontinuity.Component
	for i := range inspected.Manifest.Components {
		if inspected.Manifest.Components[i].Domain == "workspace" {
			component = &inspected.Manifest.Components[i]
			break
		}
	}
	if component == nil {
		return nil, nil, workspacecontinuity.ErrIncomplete
	}
	if component.Availability != workspacecontinuity.Present {
		return nil, nil, workspacecontinuity.ErrIncomplete // a workspace with no typed owner cannot be reviewed
	}
	canonical, err := workspacecontinuity.ReadCanonicalFile(ctx, dir, agentworkspace.WorkspaceConfigFile, workspacecontinuity.MaxChunkBytes)
	if err != nil {
		return nil, nil, err
	}
	var saved *continuitySavedWork
	var canonicalWorkspace *agentworkspace.Workspace
	err = workspacecontinuity.ReadComponentRecords(ctx, dir, inspected, "workspace", func(chunk workspacecontinuity.Chunk) error {
		if chunk.Family != "workspaces" || len(chunk.Records) != 1 || saved != nil || chunk.Records[0].ID != inspected.Manifest.WorkspaceID {
			return workspacecontinuity.ErrInvalid
		}
		record := chunk.Records[0]
		var descriptor agentworkspace.ContinuityWorkspace
		if err := workspacecontinuity.DecodeRecord(record, &descriptor); err != nil {
			return err
		}
		if descriptor.SQLMetadata == nil {
			return workspacecontinuity.ErrIncomplete // file alone cannot authorize SQL registration
		}
		original, err := agentworkspace.DecodeContinuityWorkspace(record, canonical)
		if err != nil {
			return err
		}
		projection, err := agentworkspace.ProjectContinuityWorkspace(record, canonical, "")
		if err != nil {
			return err
		}
		canonicalWorkspace = original
		work := projection.Workspace
		saved = &continuitySavedWork{
			Tasks: len(work.Tasks), InterruptedTasks: len(projection.InterruptedTaskIDs),
			CollaborationMessages: len(work.Messages), Attachments: len(work.Attachments),
			ExternalReferences: len(projection.ExternalDirectories),
		}
		for _, task := range work.Tasks {
			if task.Status == agentworkspace.TaskStatusCompleted {
				saved.CompletedTasks++
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if saved == nil || canonicalWorkspace == nil {
		return nil, nil, workspacecontinuity.ErrIncomplete
	}
	return saved, canonicalWorkspace, nil
}

// inspectContinuityImportTree checks the selected directory and physically
// contained children before any legacy registration or SQL insert. A valid
// digest proves bytes, not local consent or a cross-request restore lease.
func inspectContinuityImportTree(ctx context.Context, path string) (continuityImportReview, error) {
	result := continuityImportReview{Status: "legacy", ImportSupported: true}
	count := 0
	modernByPath := make(map[string]string)
	members := make([]continuityTreeMember, 0, 1)
	seenIDs := make(map[string]bool)
	unavailable := func() {
		result.Status = "unavailable"
		result.ImportSupported = false
		result.WorkspaceID, result.CheckpointAt, result.Components, result.SavedWork = "", "", nil, nil
		result.TreeDigest, result.AssistantCandidates, result.History, result.members = "", nil, nil, nil
	}
	history := &continuityHistoryCounts{}
	var walk func(string, bool, string) error
	walk = func(dir string, selected bool, parentID string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > workspacecontinuity.MaxFiles {
			return workspacecontinuity.ErrLimit
		}
		// #nosec G703 -- dir is the operator-selected import root or a physically enumerated child; Lstat is read-only and rejects symlinks before descending.
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return workspacecontinuity.ErrUnsafe
		}
		inspected, err := workspacecontinuity.Inspect(ctx, dir)
		var declaredChildren map[string]bool
		switch {
		case err == nil:
			id := inspected.Manifest.WorkspaceID
			if seenIDs[id] {
				unavailable() // identical IDs in distinct physical folders are ambiguous
				return nil
			}
			seenIDs[id] = true
			modernByPath[dir] = id
			rel, err := filepath.Rel(path, dir)
			if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return workspacecontinuity.ErrUnsafe
			}
			members = append(members, continuityTreeMember{
				Path: filepath.ToSlash(rel), WorkspaceID: id, Generation: inspected.Pointer.Generation,
				ManifestDigest: inspected.Pointer.Digest, SourceFingerprint: inspected.Manifest.SourceFingerprint,
			})
			declaredChildren = make(map[string]bool, len(inspected.Manifest.Children))
			for _, childID := range inspected.Manifest.Children {
				declaredChildren[childID] = true
			}
			result.ModernDirectories++
			saved, canonicalWorkspace, err := inspectSavedWorkspace(ctx, dir, inspected)
			if err != nil {
				unavailable() // every member needs typed owner evidence, not only the selected root
				return nil
			}
			if err := agentworkspace.CheckContinuityFolderCoverage(ctx, dir, inspected.Manifest.Files); err != nil {
				unavailable() // an undeclared file would travel without a reviewed owner
				return nil
			}
			if err := inspectOwnedHistory(ctx, dir, inspected); err != nil {
				unavailable() // verified bytes cannot masquerade as restorable typed history
				return nil
			}
			for _, file := range inspected.Manifest.Files {
				if strings.HasPrefix(file.Path, agentworkspace.FilesDir+"/") {
					saved.OwnedFiles++
				}
			}
			candidate, err := inspectModernAssistant(ctx, dir, inspected, canonicalWorkspace)
			if err != nil || candidate != nil && len(result.AssistantCandidates) >= workspacecontinuity.MaxChildren {
				unavailable() // never advertise an unbound or ambiguous identity
				return nil
			}
			if candidate != nil {
				result.AssistantCandidates = append(result.AssistantCandidates, *candidate)
			}
			for _, component := range inspected.Manifest.Components {
				history.add(component)
			}
			result.members = append(result.members, continuityImportMember{Dir: dir, Rel: filepath.ToSlash(rel),
				ParentID: parentID, Inspection: inspected, Candidate: candidate})
			if !selected {
				result.ContainsModernChild = true
				if result.SavedWork != nil {
					result.SavedWork.include(saved) // only a verified selected root owns the aggregate
				}
			}
			if result.Status == "legacy" {
				result.Status = "review_required"
				result.ImportSupported = false
				if selected {
					result.WorkspaceID = inspected.Manifest.WorkspaceID
					result.CheckpointAt = inspected.Manifest.CheckpointAt.Format("2006-01-02T15:04:05Z07:00")
					result.Components = make([]continuityComponentReview, 0, len(inspected.Manifest.Components))
					for _, component := range inspected.Manifest.Components {
						result.Components = append(result.Components, continuityComponentReview{
							Domain: component.Domain, Availability: component.Availability,
							Reason: component.Reason, Counts: component.Counts,
						})
					}
					result.SavedWork = saved
				}
			}
		case errors.Is(err, workspacecontinuity.ErrLegacy):
			// Only total absence of the reserved directory is legacy.
		case err != nil:
			unavailable()
			return nil // never reinterpret a damaged modern tree as legacy
		}

		children := filepath.Join(dir, agentworkspace.SubWorkspacesDir)
		// #nosec G703 -- children is a fixed sub-workspaces name under the checked import directory; Lstat rejects links before enumeration.
		childInfo, err := os.Lstat(children)
		if errors.Is(err, os.ErrNotExist) {
			if len(declaredChildren) != 0 {
				unavailable() // manifest names children absent from the physical tree
			}
			return nil
		}
		if err != nil || !childInfo.IsDir() || childInfo.Mode()&os.ModeSymlink != 0 {
			return workspacecontinuity.ErrUnsafe
		}
		// Pin child enumeration beneath the opened parent. A replacement link
		// between Lstat and ReadDir may not redirect the inspection outside it.
		parentRoot, err := os.OpenRoot(dir)
		if err != nil {
			return workspacecontinuity.ErrUnsafe
		}
		childFolder, err := parentRoot.Open(agentworkspace.SubWorkspacesDir)
		if err != nil {
			_ = parentRoot.Close()
			return workspacecontinuity.ErrUnsafe
		}
		entries, readErr := childFolder.ReadDir(workspacecontinuity.MaxChildren + 1)
		closeErr := errors.Join(childFolder.Close(), parentRoot.Close())
		if readErr != nil || closeErr != nil {
			return workspacecontinuity.ErrUnsafe
		}
		if len(entries) > workspacecontinuity.MaxChildren {
			return workspacecontinuity.ErrLimit
		}
		seenChildren := make(map[string]bool, len(declaredChildren))
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return workspacecontinuity.ErrUnsafe
			}
			if !entry.IsDir() {
				if declaredChildren != nil {
					unavailable()
					return nil
				} // no undeclared child payload in a modern tree
				continue
			}
			childPath := filepath.Join(children, entry.Name())
			if err := walk(childPath, false, modernByPath[dir]); err != nil {
				return err
			}
			if result.Status == "unavailable" {
				return nil
			}
			if declaredChildren != nil {
				childID := modernByPath[childPath]
				if !declaredChildren[childID] || seenChildren[childID] {
					unavailable() // legacy, extra, or duplicate child is not the declared tree
					return nil
				}
				seenChildren[childID] = true
			}
		}
		if declaredChildren != nil && len(seenChildren) != len(declaredChildren) {
			unavailable()
		}
		return nil
	}
	if err := walk(path, true, ""); err != nil {
		return continuityImportReview{}, fmt.Errorf("inspect import tree: %w", err)
	}
	if result.Status == "review_required" && result.WorkspaceID != "" {
		result.History, result.Members = history, len(result.members)
		// Physical paths matter: reordering a child or replacing a member with a
		// different checkpoint cannot retain this read-only tree commitment.
		sort.Slice(members, func(i, j int) bool { return members[i].Path < members[j].Path })
		encoded, err := json.Marshal(members) // fixed scalar fields cannot fail encoding
		if err != nil {
			return continuityImportReview{}, err
		}
		result.TreeDigest = workspacecontinuity.Digest(encoded)
	}
	return result, nil
}
