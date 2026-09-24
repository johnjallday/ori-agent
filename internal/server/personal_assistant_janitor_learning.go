package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/userprofile"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// janitorKnowledgeProducer only asks the verified source reader for bounded
// action identities. It neither approves memory nor invokes Janitor apply,
// undo, classification or filesystem scanning.
type janitorKnowledgeProducer struct {
	reader   *janitorKnowledgeReader
	learning *personalassistant.KnowledgeLearningService
	recovery *janitorKnowledgeRecovery
}

func (p janitorKnowledgeProducer) Check(ctx context.Context, userID string) ([]personalassistant.KnowledgeItem, error) {
	if p.reader == nil || p.learning == nil {
		return nil, errJanitorKnowledgeSourceUnavailable
	}
	if p.recovery != nil {
		if err := p.recovery.Reconcile(ctx, userID); err != nil {
			return nil, err
		}
	}
	patterns, err := p.reader.ReadFresh(ctx, userID)
	if err != nil {
		return nil, err
	}
	var items []personalassistant.KnowledgeItem
	for _, pattern := range patterns {
		proposal, ok := janitorKnowledgeProposal(pattern)
		if !ok {
			continue
		}
		item, _, err := p.learning.Propose(ctx, userID, proposal)
		if errors.Is(err, personalassistant.ErrKnowledgeSuppressed) {
			continue
		}
		if err != nil {
			return items, err
		}
		items = append(items, item)
	}
	return items, nil
}

func janitorKnowledgeProposal(pattern janitorKnowledgeEvidence) (personalassistant.KnowledgeProposal, bool) {
	if pattern.WorkspaceID == "" || pattern.RootID == "" || len(pattern.ActionIDs) != 3 || len(pattern.CompletedAt) != 3 ||
		pattern.Category == filejanitor.CategoryOther {
		return personalassistant.KnowledgeProposal{}, false
	}
	definition, err := filejanitor.LookupCategory(string(pattern.Category))
	if err != nil {
		return personalassistant.KnowledgeProposal{}, false
	}
	label := definition.Label // fixed host-owned category label, never a filename or workspace name
	proposal := personalassistant.KnowledgeProposal{
		SourceKind: "file_janitor", ScopeID: pattern.WorkspaceID + ":" + pattern.RootID,
		SubjectID: string(pattern.Category), Predicate: "prefers filing", Value: string(pattern.Category),
		Category: "how_you_work", Text: "You may prefer filing " + strings.ToLower(label) + " in your approved File Janitor folder.",
	}
	for index, id := range pattern.ActionIDs {
		proposal.Evidence = append(proposal.Evidence, personalassistant.KnowledgeEvidence{
			SourceKind: "file_janitor", SourceID: id,
			ObservedAt: pattern.CompletedAt[index], Summary: "User-approved move to " + label,
		})
	}
	return proposal, true
}

// scopedKnowledgeAuthority dispatches only known host-owned source kinds.
// Current Janitor scope, folder authority and the exact supporting actions
// are reread on each approval and context read; lost notifications cannot
// make a successfully undone action remain active in the prompt.
type scopedKnowledgeAuthority struct {
	apps    personalassistant.SavedAppAuthority
	janitor *janitorKnowledgeReader
}

func (a scopedKnowledgeAuthority) Revalidate(ctx context.Context, binding personalassistant.KnowledgeBinding, item personalassistant.KnowledgeItem) error {
	switch item.SourceKind {
	case "saved_app":
		return a.apps.Revalidate(ctx, binding, item)
	case "file_janitor":
		if a.janitor == nil {
			return errJanitorKnowledgeSourceUnavailable
		}
		patterns, err := a.janitor.ReadFresh(ctx, binding.UserID)
		if err != nil {
			return err
		}
		for _, pattern := range patterns {
			proposal, ok := janitorKnowledgeProposal(pattern)
			if !ok || personalassistant.KnowledgeSemanticKey(binding, proposal) != item.SemanticKey {
				continue
			}
			var evidence []personalassistant.KnowledgeEvidence
			for _, revision := range item.Revisions {
				if revision.ID == item.CurrentRevisionID {
					evidence = revision.Evidence
					break
				}
			}
			if len(evidence) != 3 {
				return errJanitorKnowledgeSourceUnavailable
			}
			available := make(map[string]bool, len(pattern.SupportIDs))
			for _, id := range pattern.SupportIDs {
				available[id] = true
			}
			seen := make(map[string]bool, len(evidence))
			for _, support := range evidence {
				if support.SourceKind != "file_janitor" || !available[support.SourceID] || seen[support.SourceID] {
					return errJanitorKnowledgeSourceUnavailable
				}
				seen[support.SourceID] = true
			}
			return nil
		}
		return errJanitorKnowledgeSourceUnavailable
	default:
		return errJanitorKnowledgeSourceUnavailable
	}
}

// janitorKnowledgeRecovery reconciles durable successful undos even when the
// event was lost (explicit Check); failures cannot change file operations.
// Only evidence IDs on the current approved revision may trigger suspension.
type janitorKnowledgeRecovery struct {
	reader   *janitorKnowledgeReader
	store    *personalassistant.KnowledgeStore
	learning *personalassistant.KnowledgeLearningService
	producer janitorKnowledgeProducer
}

func (s *janitorKnowledgeRecovery) Reconcile(ctx context.Context, userID string) error {
	if s == nil || s.reader == nil || s.store == nil || s.learning == nil {
		return errJanitorKnowledgeSourceUnavailable
	}
	undone, err := s.reader.UndoneSupportIDs(ctx, userID)
	if err != nil {
		return err
	}
	if len(undone) == 0 {
		return nil
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return err
	}
	for _, item := range doc.Items {
		if item.SourceKind != "file_janitor" || item.Target == nil ||
			(item.State != personalassistant.KnowledgeApproved &&
				(item.State != personalassistant.KnowledgeNeedsReview || item.Prepared == nil || item.Prepared.Kind != "suspend")) {
			continue
		}
		for _, revision := range item.Revisions {
			if revision.ID != item.CurrentRevisionID {
				continue
			}
			for _, support := range revision.Evidence {
				if support.SourceKind != "file_janitor" || !undone[support.SourceID] {
					continue
				}
				requestID := janitorSuspensionRequest(item.ID, support.SourceID)
				if item.Prepared != nil {
					requestID = item.Prepared.RequestID
				}
				if _, err := s.learning.SuspendApproved(ctx, userID, item.ID, item.Version, requestID); err != nil {
					return err
				}
				break
			}
			break
		}
	}
	return nil
}

func janitorSuspensionRequest(itemID, actionID string) string {
	sum := sha256.Sum256([]byte(itemID + ":" + actionID))
	return "janitor-undo-" + hex.EncodeToString(sum[:16])
}

// The File Janitor observer is host-owned. It sees only IDs after the
// journal's durable status transition and never passes raw action data to
// knowledge. A missing HQ, stale scope or failed sidecar is retried by the
// explicit Check; it must not roll back or replay a real file move or undo.
func (s *janitorKnowledgeRecovery) MoveApplied(_, _ string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.producer.Check(ctx, userprofile.LocalUserID); err != nil {
		logger.Warn("File Janitor knowledge update deferred", logger.Fields{"reason": "source_unavailable"})
	}
}

func (s *janitorKnowledgeRecovery) UndoSucceeded(_, _ string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Reconcile(ctx, userprofile.LocalUserID); err != nil {
		logger.Warn("File Janitor knowledge undo reconciliation deferred", logger.Fields{"reason": "source_unavailable"})
	}
}

func (a scopedKnowledgeAuthority) FreshEvidence(ctx context.Context, binding personalassistant.KnowledgeBinding, item personalassistant.KnowledgeItem) ([]personalassistant.KnowledgeEvidence, error) {
	if item.SourceKind != "file_janitor" || a.janitor == nil {
		return nil, errJanitorKnowledgeSourceUnavailable
	}
	patterns, err := a.janitor.ReadFresh(ctx, binding.UserID)
	if err != nil {
		return nil, err
	}
	for _, pattern := range patterns {
		proposal, ok := janitorKnowledgeProposal(pattern)
		if ok && personalassistant.KnowledgeSemanticKey(binding, proposal) == item.SemanticKey {
			return proposal.Evidence, nil
		}
	}
	return nil, errJanitorKnowledgeSourceUnavailable
}

var _ personalassistant.KnowledgeSourceAuthority = scopedKnowledgeAuthority{}
var _ personalassistant.KnowledgeReconfirmationAuthority = scopedKnowledgeAuthority{}
var _ filejanitor.ReviewedKnowledgeObserver = (*janitorKnowledgeRecovery)(nil)
