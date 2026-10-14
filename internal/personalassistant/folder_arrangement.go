package personalassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// HeldArrangementObservation returns the original bounded discussion snapshot
// for atomic conversation persistence, without resolving or probing its path.
func (s *FolderObservationService) HeldArrangementObservation(ctx context.Context, target foldercontext.Target, id string) (*foldercontext.Observation, error) {
	if _, err := s.ArrangementEvidence(ctx, target, id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	held, ok := s.selections[id]
	if !ok || held.target != target {
		return nil, workspacearrangement.ErrStale
	}
	encoded, err := json.Marshal(held.observation)
	if err != nil {
		return nil, err
	}
	var observation foldercontext.Observation
	if json.Unmarshal(encoded, &observation) != nil || observation.Validate() != nil {
		return nil, workspacearrangement.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &observation, nil
}

// ArrangementEvidence projects only a live held scan. Unlike Resolve, this
// method NEVER resolves/stats the source directory. The caller additionally
// verifies its canonical conversation/attachment revision on every invocation.
// Review still uses the existing directory-identity/ownership checks.
func (s *FolderObservationService) ArrangementEvidence(ctx context.Context, target foldercontext.Target, id string) (workspacearrangement.Snapshot, error) {
	if s == nil || s.digest == nil || s.digest.store == nil || !target.Valid() {
		return workspacearrangement.Snapshot{}, workspacearrangement.ErrUnavailable
	}
	binding, err := s.digest.store.Binding(ctx, target.UserID)
	if err != nil {
		return workspacearrangement.Snapshot{}, workspacearrangement.ErrUnavailable
	}
	out, err := s.heldArrangementEvidence(ctx, target, id, binding)
	if err != nil {
		return workspacearrangement.Snapshot{}, err
	}
	current, err := s.digest.store.Binding(ctx, target.UserID)
	if err != nil || current != binding {
		return workspacearrangement.Snapshot{}, workspacearrangement.ErrStale
	}
	// Recheck the selection after that asynchronous canonical binding read.
	s.mu.Lock()
	defer s.mu.Unlock()
	held, found := s.selections[id]
	if !found || held.target != target || held.binding != binding ||
		s.digest.now().Sub(held.observation.ScannedAt) >= foldercontext.SelectionTTL {
		return workspacearrangement.Snapshot{}, workspacearrangement.ErrStale
	}
	return out, ctx.Err()
}

func (s *FolderObservationService) heldArrangementEvidence(ctx context.Context, target foldercontext.Target, id string, binding KnowledgeBinding) (workspacearrangement.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held, found := s.selections[id]
	now := s.digest.now()
	if !found || held.target != target || held.binding != binding || now.Before(held.observation.ScannedAt) ||
		now.Sub(held.observation.ScannedAt) >= foldercontext.SelectionTTL {
		return workspacearrangement.Snapshot{}, workspacearrangement.ErrStale
	}
	if err := ctx.Err(); err != nil {
		return workspacearrangement.Snapshot{}, err
	}
	// Binding is host-owned scalar identity, not a path. Conversation/draft IDs
	// stay in the caller's authority check, so BindSaved does not change the
	// evidence revision when its first canonical conversation is created.
	authority, err := json.Marshal(binding)
	if err != nil {
		return workspacearrangement.Snapshot{}, workspacearrangement.ErrUnavailable
	}
	out := workspacearrangement.Snapshot{Kind: workspacearrangement.Attachment, Revision: held.observation.ID, Authority: string(authority),
		Coverage: workspacearrangement.Coverage{Origin: "held_attachment", Partial: held.result.Partial,
			MaxDepth: 3, MaxEntries: 5000, SkippedLinks: held.result.SkippedLinks,
			Note: "Already-held metadata only. Candidates are the root and immediate children, not every nested project. Depth, hidden-entry and tree limits apply even when partial is false. No content read or fresh scan."}}
	if held.result.Metadata != nil {
		out.Coverage.TreeOmitted = held.result.Metadata.Omitted
	}
	for i, candidate := range held.result.Candidates {
		// Preserve the existing single software-project boundary: packages are
		// not independently executable children of a repository attachment.
		if held.result.SoftwareRoot() && !candidate.IsRoot {
			continue
		}
		format := "unknown"
		if candidate.Marker != nil && candidate.Marker.ProjectFormat != "" {
			format = candidate.Marker.ProjectFormat
		}
		name := foldercontext.DisplayName(candidate.Name)
		if name == "" {
			name = "Unnamed folder"
		}
		connection := "unlinked"
		if candidate.IsRoot {
			connection = "attached_root"
		}
		modified := ""
		if !candidate.NewestModTime.IsZero() {
			modified = candidate.NewestModTime.UTC().Format(time.RFC3339)
		}
		out.Items = append(out.Items, workspacearrangement.Item{ID: fmt.Sprintf("candidate-%d", i), Revision: held.observation.ID,
			Name: name, Format: format, Connection: connection, Availability: "observed", Files: candidate.FileCount,
			ObservedAt: held.result.ScannedAt, ModifiedAt: modified, Root: candidate.IsRoot,
			Sources: []workspacearrangement.ItemSource{{RootID: held.observation.ID, Format: format, Availability: "observed"}}})
	}
	if held.result.Metadata != nil && len(out.Items) > 0 {
		for _, node := range held.result.Metadata.Nodes {
			name := foldercontext.DisplayName(node.Name)
			if name == "" {
				name = "Unnamed entry"
			}
			out.Items[0].Tree = append(out.Items[0].Tree, workspacearrangement.TreeNode{ID: node.ID, ParentID: node.ParentID, Name: name, Kind: node.Kind})
		}
	}
	return out, nil
}
