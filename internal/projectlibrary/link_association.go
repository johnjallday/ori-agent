package projectlibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"slices"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// PendingLink is Home-owned navigation metadata. It contains no source path,
// project files, child content, or authority to associate a catalog record.
type PendingLink struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
}

type PendingLinks struct {
	Revision int64         `json:"revision"`
	Total    int           `json:"total"`
	Rows     []PendingLink `json:"rows"`
}

type LinkAssociationReview struct {
	Token       string    `json:"token"`
	WorkspaceID string    `json:"workspace_id"`
	ProjectName string    `json:"project_name"`
	EntryID     string    `json:"entry_id,omitempty"`
	LinkOnly    bool      `json:"link_only"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type LinkAssociationResult struct {
	EntryID     string `json:"entry_id"`
	WorkspaceID string `json:"workspace_id"`
	Replay      bool   `json:"replay"`
}

// exactLinkedChild checks Home membership and the current child, not a name or
// client-supplied path. Portable locator metadata may live in the folder
// mirror on SQLite-primary stores; identity and the link remain primary-owned.
func (s *Store) exactLinkedChild(scope Scope, state *workspace.AssistantProgramState, projectID string) (*workspace.Workspace, *workspace.DirectoryReference, *workspace.ProjectEntryLocator, ExactLink, error) {
	if state == nil || !slices.Contains(state.LinkedProjectIDs, projectID) || projectID == scope.HomeID {
		return nil, nil, nil, ExactLink{}, ErrConflict
	}
	child, err := s.workspaces.Get(projectID)
	if err != nil || child == nil || child.OwnerUserID != scope.OwnerUserID || child.Status == workspace.StatusTrashed || child.Status == workspace.StatusMissing {
		return nil, nil, nil, ExactLink{}, ErrConflict
	}
	link := child.GetAssistantProjectLink()
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	if link == nil || link.ID != workspace.AssistantProjectLinkID(scope.HomeID, projectID) ||
		link.StationWorkspaceID != scope.HomeID || link.Key.Normalize() != key.Normalize() || link.StateRevision < 1 {
		return nil, nil, nil, ExactLink{}, ErrConflict
	}
	portable := child
	if mirror, ok := s.workspaces.(workspace.MirrorWorkspaceProvider); ok {
		folder, mirrored, mirrorErr := mirror.GetMirrorWorkspace(projectID)
		if mirrored {
			if mirrorErr != nil || folder == nil || folder.OwnerUserID != child.OwnerUserID {
				return nil, nil, nil, ExactLink{}, ErrMirrorDiverged
			}
			folderLink := folder.GetAssistantProjectLink()
			if folderLink == nil || folderLink.ID != link.ID || folderLink.StationWorkspaceID != link.StationWorkspaceID ||
				folderLink.StateRevision != link.StateRevision || folderLink.Key.Normalize() != link.Key.Normalize() {
				return nil, nil, nil, ExactLink{}, ErrMirrorDiverged
			}
			portable = folder
		}
	}
	locator, err := workspace.GetProjectEntryLocator(portable.SharedData)
	if err != nil || locator == nil || locator.Kind != workspace.ProjectEntryDirectoryReference ||
		locator.DirectoryReferenceID == "" || filepath.Base(locator.RelativePath) != locator.RelativePath {
		return nil, nil, nil, ExactLink{}, ErrConflict
	}
	ref, err := portable.GetDirectoryReference(locator.DirectoryReferenceID)
	if err != nil || ref == nil || ref.WorkspaceID != child.ID || ref.Purpose == "sample_library" ||
		!filepath.IsAbs(ref.Path) || filepath.Clean(ref.Path) != ref.Path || !validText(ref.Path, 4096) {
		return nil, nil, nil, ExactLink{}, ErrConflict
	}
	return child, ref, locator, ExactLink{WorkspaceID: child.ID, LinkID: link.ID, Revision: link.StateRevision}, nil
}

// exactAssociation uses only an already reviewed root/observation to join an
// existing catalog entry. A revoked, changed or ambiguous observed folder
// cannot be replaced by a same-name guess. Without such a record, association
// is link-only and grants the Home no discovery/file access.
func exactAssociation(doc Document, state *workspace.AssistantProgramState, ref *workspace.DirectoryReference, locator *workspace.ProjectEntryLocator, link ExactLink) (string, int64, error) {
	// A link-only association with no approved roots is purely stored metadata:
	// it must work even while the child's independent project drive is offline.
	// Project references can retain macOS's /var spelling while an approved
	// root is persisted under /private/var. Only when comparing existing root
	// observations, canonicalize the reference without reading file contents.
	if len(doc.Roots) == 0 {
		for _, entry := range doc.Entries {
			if entry.Link != nil && entry.Link.WorkspaceID == link.WorkspaceID {
				return "", 0, ErrConflict
			}
		}
		return "", 0, nil
	}
	canonical, err := filepath.EvalSymlinks(ref.Path)
	if err != nil {
		return "", 0, ErrUnavailable
	}
	found := ""
	var revision int64
	for _, entry := range doc.Entries {
		if entry.Link != nil && entry.Link.WorkspaceID == link.WorkspaceID {
			return "", 0, ErrConflict // Already associated; never make a second record.
		}
		for _, observed := range entry.Observations {
			for _, root := range doc.Roots {
				if root.ID != observed.RootID || filepath.Join(root.Path, observed.RelativeFolder) != canonical {
					continue
				}
				marker, markerOK := folderdigest.MatchMarker(locator.RelativePath, false)
				if root.RevokedAt != nil || slices.Contains(state.ProjectLibraryInactiveRoots, root.ID) ||
					!slices.Contains(observed.Alternates, locator.RelativePath) || !markerOK || marker.ProjectFormat != observed.Format ||
					!directoryMatches(root, observed.RelativeFolder, observed.FileIdentity) || entry.Link != nil ||
					(found != "" && found != entry.ID) {
					return "", 0, ErrConflict
				}
				found, revision = entry.ID, entry.Revision
			}
		}
	}
	return found, revision, nil
}

func associationDigest(scope Scope, link ExactLink, ref *workspace.DirectoryReference, locator *workspace.ProjectEntryLocator, entryID string, entryRevision int64) (string, error) {
	encoded, err := json.Marshal(struct {
		Scope         Scope     `json:"scope"`
		Link          ExactLink `json:"link"`
		ReferenceID   string    `json:"reference_id"`
		Path          string    `json:"path"`
		SelectedFile  string    `json:"selected_file"`
		EntryID       string    `json:"entry_id"`
		EntryRevision int64     `json:"entry_revision"`
	}{scope, link, ref.ID, ref.Path, locator.RelativePath, entryID, entryRevision})
	if err != nil {
		return "", ErrCorrupt
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// PendingLinkedProjects is inert and bounded. It never adopts a link on GET.
func (s *Store) PendingLinkedProjects(scope Scope) (PendingLinks, error) {
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return PendingLinks{}, err
	}
	view := PendingLinks{Revision: doc.Revision, Rows: []PendingLink{}}
	for _, id := range state.LinkedProjectIDs {
		already := false
		for _, entry := range doc.Entries {
			already = already || entry.Link != nil && entry.Link.WorkspaceID == id
		}
		if already {
			continue
		}
		child, _, _, _, linkErr := s.exactLinkedChild(scope, state, id)
		if linkErr != nil || !validText(child.Name, 160) || child.Name == "" {
			continue // A broken/foreign link is not a candidate to adopt.
		}
		view.Total++
		if len(view.Rows) < 20 {
			view.Rows = append(view.Rows, PendingLink{WorkspaceID: id, Name: child.Name})
		}
	}
	return view, nil
}

func (s *Store) ReviewLinkedProject(scope Scope, projectID string, expected int64) (LinkAssociationReview, error) {
	if s == nil || s.providerEvidence == nil || projectID == "" || !validText(projectID, 160) || expected < 1 {
		return LinkAssociationReview{}, ErrUnavailable
	}
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	doc, state, err := s.readSnapshot(scope)
	if err != nil || doc.Revision != expected {
		return LinkAssociationReview{}, ErrConflict
	}
	child, ref, locator, link, err := s.exactLinkedChild(scope, state, projectID)
	if err != nil || !validText(child.Name, 160) || child.Name == "" {
		return LinkAssociationReview{}, ErrConflict
	}
	entryID, entryRevision, err := exactAssociation(doc, state, ref, locator, link)
	if err != nil {
		return LinkAssociationReview{}, err
	}
	digest, err := associationDigest(scope, link, ref, locator, entryID, entryRevision)
	if err != nil {
		return LinkAssociationReview{}, err
	}
	token, at := newID(), s.now().UTC()
	review := ReviewReceipt{Token: token, Action: "associate_link", TargetID: projectID,
		Digest: digest, EntryRevision: entryRevision, Revision: doc.Revision + 1, ExpiresAt: at.Add(10 * time.Minute)}
	_, _, err = s.mutate(scope, doc.Revision, operation{key: token, action: "review_associate_link", digest: digest}, func(current *Document) (string, error) {
		if len(current.Reviews) >= maxReviews {
			return "", ErrLimit
		}
		current.Reviews = append(current.Reviews, review)
		return token, nil
	})
	if err != nil {
		return LinkAssociationReview{}, err
	}
	return LinkAssociationReview{Token: token, WorkspaceID: projectID, ProjectName: child.Name,
		EntryID: entryID, LinkOnly: entryID == "", ExpiresAt: review.ExpiresAt}, nil
}

// CommitLinkedProject creates no workspace, project link, root, file selection,
// agent or filesystem read grant. It records only an association to the exact
// Home-listed reciprocal child after a separate owner confirmation.
func (s *Store) CommitLinkedProject(scope Scope, projectID, token, key string) (LinkAssociationResult, error) {
	if s == nil || s.providerEvidence == nil || projectID == "" || token == "" || key == "" ||
		!validText(projectID, 160) || !validText(token, 160) || !validText(key, 160) {
		return LinkAssociationResult{}, ErrConflict
	}
	gate := rootAccessGate(scope)
	gate.RLock()
	defer gate.RUnlock()
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return LinkAssociationResult{}, err
	}
	review, ok := findReview(doc, token, "associate_link")
	if !ok || review.TargetID != projectID {
		return LinkAssociationResult{}, ErrConflict
	}
	_, ref, locator, link, err := s.exactLinkedChild(scope, state, projectID)
	if err != nil {
		return LinkAssociationResult{}, err
	}
	entryID, entryRevision, matchErr := exactAssociation(doc, state, ref, locator, link)
	if matchErr != nil {
		// An operation may already have committed this association; handle its
		// exact replay below without accepting a different or broken link.
		for _, prior := range doc.Operations {
			if prior.Key == key && prior.Action == "associate_link" && prior.Digest == review.Digest {
				for _, entry := range doc.Entries {
					if entry.ID == prior.ConsequenceID && entry.Link != nil && *entry.Link == link {
						return LinkAssociationResult{EntryID: entry.ID, WorkspaceID: projectID, Replay: true}, nil
					}
				}
			}
		}
		return LinkAssociationResult{}, matchErr
	}
	digest, err := associationDigest(scope, link, ref, locator, entryID, entryRevision)
	if err != nil || digest != review.Digest || review.ConsumedAt != nil || review.Revision != doc.Revision ||
		!review.ExpiresAt.After(s.now().UTC()) {
		return LinkAssociationResult{}, ErrConflict
	}
	createdID := ""
	var liveState *workspace.AssistantProgramState
	receipt, replay, err := s.mutateWithHomePolicy(scope, doc.Revision, operation{key: key, action: "associate_link", digest: digest},
		func(current *workspace.AssistantProgramState, _ *workspace.Workspace) bool {
			liveState = current
			_, freshRef, freshLocator, freshLink, childErr := s.exactLinkedChild(scope, current, projectID)
			if childErr != nil || freshLink != link {
				return false
			}
			freshDigest, digestErr := associationDigest(scope, freshLink, freshRef, freshLocator, entryID, entryRevision)
			return digestErr == nil && freshDigest == digest
		}, func(current *Document) (string, error) {
			matched, version, matchErr := exactAssociation(*current, liveState, ref, locator, link)
			if matchErr != nil || matched != entryID || version != entryRevision {
				return "", ErrConflict
			}
			for i := range current.Reviews {
				r := &current.Reviews[i]
				if r.Token == token && r.Action == "associate_link" && r.Digest == digest && r.ConsumedAt == nil &&
					r.Revision == current.Revision && r.ExpiresAt.After(s.now().UTC()) {
					at := s.now().UTC()
					r.ConsumedAt = &at
					if entryID != "" {
						entry := sessionEntry(*current, entryID)
						entry.Link = &link
						entry.Revision++
						return entryID, nil
					}
					if len(current.Entries) >= maxEntries {
						return "", ErrLimit
					}
					createdID = newID()
					current.Entries = append(current.Entries, Entry{ID: createdID, Revision: 1, Link: &link})
					return createdID, nil
				}
			}
			return "", ErrConflict
		})
	if err != nil {
		return LinkAssociationResult{}, err
	}
	return LinkAssociationResult{EntryID: receipt.ConsequenceID, WorkspaceID: projectID, Replay: replay}, nil
}
