package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// What a folder taught Ori in the first week (first-week FR16 to FR19): the
// review queue's candidates that came from the user's approved filing, listed on
// one card so they can be answered in one place. The card approves and rejects
// through the same reviewed-memory actions as the Profile page, so nothing here
// writes a fact; this service only reads the queue and tombstones what the user
// turns away.

const (
	// maxSetupLearningItems bounds the card: the setup pool's cap, and the
	// Janitor's filing preferences beside it.
	maxSetupLearningItems = setupBatchPendingLimit + knowledgePendingLimit
	// maxSetupLearningEvidence is how many evidence lines one learning shows.
	maxSetupLearningEvidence = 3
)

// SetupLearningItem is one learning as the card shows it. Version and ID are what
// the Profile page's approve and reject actions take.
type SetupLearningItem struct {
	ID       string   `json:"id"`
	Version  int64    `json:"version"`
	Category string   `json:"category"`
	Text     string   `json:"text"`
	Evidence []string `json:"evidence,omitempty"`
	// Source is "files" for a name that recurs in approved file names, or
	// "filing" for a filing preference the Janitor noticed.
	Source string `json:"source"`
}

// SetupLearningView is GET /api/personal-assistant/setup-learning.
type SetupLearningView struct {
	Items []SetupLearningItem `json:"items"`
	Total int                 `json:"total"`
}

// SetupLearningService reads the setup beat's candidates from the review queue.
type SetupLearningService struct {
	learning *KnowledgeLearningService
}

// NewSetupLearningService builds the service over the reviewed-knowledge queue.
func NewSetupLearningService(learning *KnowledgeLearningService) *SetupLearningService {
	return &SetupLearningService{learning: learning}
}

// List returns the candidates waiting for an answer, newest source kind first.
func (s *SetupLearningService) List(ctx context.Context, userID string) (SetupLearningView, error) {
	view := SetupLearningView{Items: []SetupLearningItem{}}
	if s == nil || s.learning == nil {
		return view, ErrRepairNeeded
	}
	items, err := s.learning.ReviewItems(ctx, userID)
	if err != nil {
		return view, err
	}
	for _, item := range items {
		source := ""
		switch {
		case item.State != KnowledgeCandidate || item.ReviewUnavailable != "" || strings.TrimSpace(item.Text) == "":
			continue
		case item.SourceKind == SetupBatchSourceKind:
			source = "files"
		case item.SourceKind == "file_janitor":
			source = "filing"
		default:
			continue
		}
		learned := SetupLearningItem{ID: item.ID, Version: item.Version, Category: item.Category, Text: item.Text, Source: source}
		for _, evidence := range item.Evidence {
			if summary := strings.TrimSpace(evidence.Summary); summary != "" && len(learned.Evidence) < maxSetupLearningEvidence {
				learned.Evidence = append(learned.Evidence, summary)
			}
		}
		view.Items = append(view.Items, learned)
		if len(view.Items) == maxSetupLearningItems {
			break
		}
	}
	view.Total = len(view.Items)
	return view, nil
}

// Count is how many learnings wait, for the first-batch report's sentence. An
// unreadable queue counts as none, so the report never promises what it cannot
// show.
func (s *SetupLearningService) Count(ctx context.Context, userID string) int {
	view, err := s.List(ctx, userID)
	if err != nil {
		return 0
	}
	return view.Total
}

// Dismiss tombstones every learning still waiting, so they do not stay in the
// queue (FR18). One that was answered in the meantime keeps its answer. A replay
// of the same request returns the same count of turned-away learnings.
func (s *SetupLearningService) Dismiss(ctx context.Context, userID, requestID string) (int, error) {
	if !validInterviewID(requestID) {
		return 0, ErrValidation
	}
	view, err := s.List(ctx, userID)
	if err != nil {
		return 0, err
	}
	dismissed := 0
	for _, item := range view.Items {
		sum := sha256.Sum256([]byte(requestID + ":" + item.ID))
		receipt := "setup-dismiss-" + hex.EncodeToString(sum[:12])
		if _, err := s.learning.SuppressCandidateWithReceipt(ctx, userID, item.ID, item.Version, receipt, KnowledgeRejected); err != nil {
			if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
				continue // answered, or changed, since the card was drawn
			}
			return dismissed, err
		}
		dismissed++
	}
	return dismissed, nil
}
