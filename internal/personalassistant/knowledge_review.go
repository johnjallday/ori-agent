package personalassistant

import (
	"context"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// KnowledgeReviewItem is a bounded, inert dossier view. It never serializes
// the sidecar's past revisions, internal suppression keys, recovery hashes or
// canonical paths. For an approved fact its text comes from the canonical
// store only after exact target and source validation, never from an old
// sidecar revision.
type KnowledgeReviewItem struct {
	ID                 string                `json:"id"`
	Version            int64                 `json:"version"`
	State              KnowledgeState        `json:"state"`
	Category           string                `json:"category"`
	SourceKind         string                `json:"source_kind"`
	Text               string                `json:"text,omitempty"`
	Scope              string                `json:"scope"`
	RevisionID         string                `json:"revision_id,omitempty"`
	Evidence           []KnowledgeEvidence   `json:"evidence,omitempty"`
	FreshEvidence      []KnowledgeEvidence   `json:"fresh_evidence,omitempty"`
	EvidenceCheckpoint string                `json:"evidence_checkpoint,omitempty"`
	UpdatedAt          time.Time             `json:"updated_at"`
	ReviewUnavailable  string                `json:"review_unavailable,omitempty"`
	CanResumeForget    bool                  `json:"can_resume_forget,omitempty"`
	CanResumeOperation bool                  `json:"can_resume_operation,omitempty"`
	Source             KnowledgeReviewSource `json:"source"`
}

// KnowledgeReviewSource says where a fact came from, for the Profile page's
// source chip. Kind is the item's source kind, except "interview" for an
// explicit fact the user confirmed in the assistant's interview. A workspace
// agent's proposal also names the workspace, agent and task (or chat) it came
// from; the HTTP layer refreshes the workspace's name and page slug when the
// workspace still exists.
type KnowledgeReviewSource struct {
	Kind          string     `json:"kind"`
	WorkspaceID   string     `json:"workspace_id,omitempty"`
	WorkspaceName string     `json:"workspace_name,omitempty"`
	WorkspaceSlug string     `json:"workspace_slug,omitempty"`
	Agent         string     `json:"agent,omitempty"`
	TaskID        string     `json:"task_id,omitempty"`
	ObservedAt    *time.Time `json:"observed_at,omitempty"`
	// External names the other agent (codex, claude_code, gemini_cli,
	// claude_account) of a fact promoted from the external memory mirror, and
	// ExternalEntryID the mirror entry it came from.
	External        string `json:"external,omitempty"`
	ExternalEntryID string `json:"external_entry_id,omitempty"`
}

// KnowledgeInterviewSourceKind is the review display kind of an explicit fact
// saved from the assistant's interview. It is never a stored source kind.
const KnowledgeInterviewSourceKind = "interview"

func knowledgeReviewSource(item KnowledgeItem, interviewed map[string]bool) KnowledgeReviewSource {
	source := KnowledgeReviewSource{Kind: item.SourceKind}
	if item.SourceKind == "explicit" && interviewed[item.ID] {
		source.Kind = KnowledgeInterviewSourceKind
	}
	if item.SourceKind == "explicit" {
		// A fact the user promoted from a workspace names that workspace; one
		// promoted from another agent's memory names that agent and entry.
		for _, revision := range item.Revisions {
			for _, evidence := range revision.Evidence {
				if evidence.SourceKind == WorkspacePromoteSourceKind && evidence.SourceID != "" {
					source.WorkspaceID = evidence.SourceID
					if !evidence.ObservedAt.IsZero() {
						observed := evidence.ObservedAt
						source.ObservedAt = &observed
					}
					return source
				}
				if evidence.SourceKind == ExternalMemorySourceKind {
					rest, ok := strings.CutPrefix(evidence.SourceID, "external:")
					external, entryID, found := strings.Cut(rest, ":")
					if ok && found && external != "" && entryID != "" {
						source.External, source.ExternalEntryID = external, entryID
						if !evidence.ObservedAt.IsZero() {
							observed := evidence.ObservedAt
							source.ObservedAt = &observed
						}
						return source
					}
				}
			}
		}
		return source
	}
	if item.SourceKind != WorkspaceAgentSourceKind {
		return source
	}
	for _, revision := range item.Revisions {
		origin, ok := WorkspaceProposalOriginOf(revision.Evidence)
		if !ok {
			continue
		}
		source.WorkspaceID, source.WorkspaceName = origin.WorkspaceID, origin.WorkspaceName
		source.Agent, source.TaskID = origin.Agent, origin.TaskID
		if !origin.ObservedAt.IsZero() {
			observed := origin.ObservedAt
			source.ObservedAt = &observed
		}
		break
	}
	return source
}

// ReviewItems performs no writes or source detection. A stale canonical
// target or unavailable source is *displayed* as needing review, but the
// underlying record stays untouched until an explicit lifecycle action.
func (s *KnowledgeLearningService) ReviewItems(ctx context.Context, userID string) ([]KnowledgeReviewItem, error) {
	if s == nil || s.store == nil || s.memory == nil {
		return nil, ErrRepairNeeded
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return nil, err
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.memory.SnapshotExact(binding.HQWorkspaceID)
	if err != nil {
		return nil, err
	}
	// An interview row that saved to memory records "memory:<item ID>".
	interviewed := map[string]bool{}
	if doc.Interview != nil {
		for _, receipt := range doc.Interview.RowReceipts {
			if id, ok := strings.CutPrefix(receipt.CanonicalRef, "memory:"); ok {
				interviewed[id] = true
			}
		}
	}
	var items []KnowledgeReviewItem
	for _, item := range doc.Items {
		if item.State == KnowledgeRejected || item.State == KnowledgeForgotten {
			continue // no erased plaintext, and tombstones are not user facts
		}
		view := KnowledgeReviewItem{
			ID: item.ID, Version: item.Version, State: item.State,
			Category: item.Category, SourceKind: item.SourceKind,
			RevisionID: item.CurrentRevisionID, UpdatedAt: item.UpdatedAt,
			Scope: "personal_hq", Source: knowledgeReviewSource(item, interviewed),
		}
		if item.Target != nil && item.Target.Kind == "profile" {
			view.Scope = "global_profile"
		}
		for _, revision := range item.Revisions {
			if revision.ID == item.CurrentRevisionID {
				view.Evidence = append([]KnowledgeEvidence(nil), revision.Evidence...)
				if item.State == KnowledgeCandidate || item.State == KnowledgeNeedsReview {
					view.Text = revision.Text // displayed only as inactive review text
				}
				break
			}
		}
		if item.State == KnowledgeNeedsReview && item.SourceKind == "file_janitor" && item.Prepared == nil && item.Target == nil {
			if authority, ok := s.authority.(KnowledgeReconfirmationAuthority); ok {
				if fresh, err := authority.FreshEvidence(ctx, binding, item); err == nil && len(fresh) == 3 {
					view.FreshEvidence = fresh
					view.EvidenceCheckpoint = KnowledgeEvidenceCheckpoint(fresh)
				} else {
					view.ReviewUnavailable = "source_unavailable"
				}
			}
		}
		if item.Prepared != nil {
			view.CanResumeForget = item.Prepared.Kind == "forget" || item.Prepared.Kind == "forget_profile"
			view.CanResumeOperation = item.Prepared.Kind == "approve" || item.Prepared.Kind == "edit" || item.Prepared.Kind == "suspend"
			view.ReviewUnavailable = "operation_pending"
			view.Text = "" // no intermediate canonical value is a confirmed fact
		} else if item.State == KnowledgeApproved {
			view.Text, view.ReviewUnavailable = s.canonicalReviewText(ctx, binding, snapshot, item)
			if view.ReviewUnavailable != "" {
				view.State = KnowledgeNeedsReview
			}
		}
		items = append(items, view)
	}
	if err := s.store.checkBinding(ctx, binding); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *KnowledgeLearningService) canonicalReviewText(ctx context.Context, binding KnowledgeBinding, snapshot workspace.MemoryExactSnapshot, item KnowledgeItem) (string, string) {
	if item.Target == nil {
		return "", "canonical_target_unavailable"
	}
	if item.SourceKind != "explicit" && (s.authority == nil || s.authority.Revalidate(ctx, binding, item) != nil) {
		return "", "source_unavailable"
	}
	var value string
	switch item.Target.Kind {
	case "memory":
		if item.Target.Marker != "ori-hq:"+item.ID+":"+item.CurrentRevisionID {
			return "", "canonical_target_unavailable"
		}
		matches := 0
		for _, entry := range snapshot.Entries {
			if entry.Entry.Provenance != item.Target.Marker {
				continue
			}
			matches++
			if entry.Target.LineHash != item.Target.CanonicalHash {
				return "", "canonical_target_changed"
			}
			value = entry.Entry.Text
		}
		if matches != 1 {
			return "", "canonical_target_unavailable"
		}
	case "profile":
		if s.profiles == nil {
			return "", "canonical_target_unavailable"
		}
		profile, err := s.profiles.Get(ctx, binding.UserID)
		if err != nil {
			return "", "canonical_target_unavailable"
		}
		value, err = canonicalProfileValue(profile, item.Target.Field)
		if err != nil || hashKnowledgeLine(value) != item.Target.CanonicalHash {
			return "", "canonical_target_changed"
		}
	default:
		return "", "canonical_target_unavailable"
	}
	for _, revision := range item.Revisions {
		if revision.ID == item.CurrentRevisionID && revision.ApprovedAt != nil && strings.TrimSpace(value) != "" && revision.Text == value {
			return value, ""
		}
	}
	return "", "canonical_target_changed"
}
