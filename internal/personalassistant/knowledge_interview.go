package personalassistant

import (
	"context"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/sensitive"
)

// InterviewQuestion is a deterministic prompt to the user, never a provider
// call or an inferred commitment. Answers remain browser-local until an exact
// final review and explicit Save these facts action.
type InterviewQuestion struct {
	ID          string `json:"id"`
	Prompt      string `json:"prompt"`
	Hint        string `json:"hint,omitempty"`
	Category    string `json:"category"`
	Destination string `json:"destination"`
}

type KnowledgeInterviewService struct {
	store    *KnowledgeStore
	learning *KnowledgeLearningService
	memory   *MemoryService
	profiles ProfileCASStore
	now      func() time.Time
}

func NewKnowledgeInterviewService(store *KnowledgeStore) *KnowledgeInterviewService {
	return &KnowledgeInterviewService{store: store, now: time.Now}
}

func (s *KnowledgeInterviewService) Questions(ctx context.Context, userID string) ([]InterviewQuestion, error) {
	if s == nil || s.store == nil || s.store.resolver == nil || s.store.resolver.states == nil {
		return nil, ErrRepairNeeded
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return nil, err
	}
	state, err := s.store.resolver.states.GetState(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.store.checkBinding(ctx, binding); err != nil {
		return nil, err
	}
	hint := "Share one priority to keep in mind; nothing is saved until you review it."
	if state != nil {
		if len(state.FocusAreas) > 0 {
			area := strings.TrimSpace(string(state.FocusAreas[0]))
			if len(area) <= 100 && !sensitive.ContainsSecretLikeText(area) {
				hint = "You mentioned " + area + ". What, if anything, should I remember?"
			}
		} else if mandate := strings.TrimSpace(state.Mandate); mandate != "" && len(mandate) <= 100 && !sensitive.ContainsSecretLikeText(mandate) {
			hint = "Your working agreement mentions " + mandate + ". Is there a priority to remember?"
		}
	}
	return []InterviewQuestion{
		{ID: "priority", Prompt: "What priority or project should I keep in mind?", Hint: hint, Category: "projects", Destination: "personal_hq"},
		{ID: "communication", Prompt: "How would you like me to communicate or work with you?", Category: "how_you_work", Destination: "profile_or_personal_hq"},
		{ID: "person_or_routine", Prompt: "Is there a person or recurring routine you'd like me to remember?", Category: "routines", Destination: "personal_hq"},
	}, nil
}

// Read is pure: opening the dossier or Today never creates interview metadata.
func (s *KnowledgeInterviewService) Read(ctx context.Context, userID string) (*KnowledgeInterview, error) {
	if s == nil || s.store == nil {
		return nil, ErrRepairNeeded
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil || doc.Interview == nil {
		return nil, err
	}
	copy := *doc.Interview
	copy.RowReceipts = append([]KnowledgeInterviewRowReceipt(nil), doc.Interview.RowReceipts...)
	if doc.Interview.ProfileWrite != nil {
		prepared := *doc.Interview.ProfileWrite
		copy.ProfileWrite = &prepared
	}
	return &copy, nil
}

func (s *KnowledgeInterviewService) Offer(ctx context.Context, userID string) (*KnowledgeInterview, error) {
	return s.transition(ctx, userID, KnowledgeInterviewOffered)
}

func (s *KnowledgeInterviewService) Defer(ctx context.Context, userID string) (*KnowledgeInterview, error) {
	return s.transition(ctx, userID, KnowledgeInterviewDeferred)
}

// RecordSavedRow stores a content hash/reference only after the caller has
// verified the canonical Remember outcome. It is not an answer-draft store.
func (s *KnowledgeInterviewService) RecordSavedRow(ctx context.Context, userID string, receipt KnowledgeInterviewRowReceipt) error {
	if s == nil || s.store == nil || s.now == nil || !validInterviewID(receipt.RequestID) || !validInterviewID(receipt.RowID) ||
		len(receipt.TextHash) != 64 || receipt.CanonicalRef == "" || len(receipt.CanonicalRef) > 200 ||
		(!strings.HasPrefix(receipt.CanonicalRef, "memory:") && !strings.HasPrefix(receipt.CanonicalRef, "profile:")) ||
		strings.ContainsAny(receipt.CanonicalRef, "\\/\r\n") {
		return ErrConflict
	}
	if _, err := hex.DecodeString(receipt.TextHash); err != nil {
		return ErrConflict
	}
	for range knowledgeRetryLimit {
		doc, err := s.store.Read(ctx, userID)
		if err != nil {
			return err
		}
		if doc.Interview == nil {
			return ErrConflict
		}
		for _, row := range doc.Interview.RowReceipts {
			if row.RequestID == receipt.RequestID && row.RowID == receipt.RowID {
				if row.TextHash == receipt.TextHash && row.CanonicalRef == receipt.CanonicalRef {
					return nil
				}
				return ErrConflict
			}
		}
		if doc.Interview.Status == KnowledgeInterviewCompleted {
			return ErrConflict
		}
		if len(doc.Interview.RowReceipts) >= knowledgeMaxReceipts {
			return ErrKnowledgeLimit
		}
		receipt.Status = "saved"
		receipt.SavedAt = s.now().UTC()
		_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
			if latest.Interview == nil || latest.Interview.Status == KnowledgeInterviewCompleted {
				return ErrConflict
			}
			if strings.HasPrefix(receipt.CanonicalRef, "profile:preferences.") {
				if s.profiles == nil {
					return ErrRepairNeeded
				}
				field := strings.TrimPrefix(receipt.CanonicalRef, "profile:")
				if prepared := latest.Interview.ProfileWrite; prepared != nil {
					if prepared.RequestID != receipt.RequestID || prepared.RowID != receipt.RowID ||
						prepared.AfterHash != receipt.TextHash || field != prepared.Field || prepared.BeforeRevision < 1 {
						return ErrConflict
					}
					receipt.ProfileRevision = prepared.BeforeRevision + 1
				} else if receipt.ProfileRevision < 1 || receipt.ProfileExpectedAt.IsZero() {
					return ErrConflict
				}
				profile, err := s.profiles.Get(ctx, userID)
				if err != nil {
					return err
				}
				value, err := canonicalProfileValue(profile, field)
				if err != nil || hashKnowledgeLine(value) != receipt.TextHash {
					return ErrConflict
				}
				if prepared := latest.Interview.ProfileWrite; prepared != nil {
					if !profile.UpdatedAt.Equal(prepared.WrittenAt) {
						return ErrConflict
					}
				} else if !profile.UpdatedAt.Equal(receipt.ProfileExpectedAt) {
					return ErrConflict
				}
				revision, err := s.profiles.PreferenceRevision(ctx, userID, strings.TrimPrefix(field, "preferences."))
				if err != nil || revision != receipt.ProfileRevision {
					return ErrConflict
				}
				latest.Interview.ProfileWrite = nil
			} else if latest.Interview.ProfileWrite != nil && latest.Interview.ProfileWrite.RowID == receipt.RowID {
				return ErrConflict
			}
			latest.Interview.RowReceipts = append(latest.Interview.RowReceipts, receipt)
			latest.Interview.UpdatedAt = receipt.SavedAt
			return nil
		})
		if errors.Is(err, ErrConflict) {
			continue
		}
		return err
	}
	return ErrConflict
}

// Complete is called only after the exact final review and every selected row
// has a durable saved receipt. An empty selection represents an explicit
// all-skipped finish, not a background completion.
func (s *KnowledgeInterviewService) Complete(ctx context.Context, userID, requestID string, selectedRowIDs []string) (*KnowledgeInterview, error) {
	if s == nil || s.store == nil || s.now == nil || !validInterviewID(requestID) || len(selectedRowIDs) > 3 {
		return nil, ErrConflict
	}
	selected := make(map[string]bool, len(selectedRowIDs))
	for _, row := range selectedRowIDs {
		if !validInterviewID(row) || selected[row] {
			return nil, ErrConflict
		}
		selected[row] = true
	}
	ordered := append([]string(nil), selectedRowIDs...)
	sort.Strings(ordered)
	selectionHash := hashKnowledgeLine(strings.Join(ordered, "\n"))
	for range knowledgeRetryLimit {
		doc, err := s.store.Read(ctx, userID)
		if err != nil {
			return nil, err
		}
		if doc.Interview == nil {
			return nil, ErrConflict
		}
		if doc.Interview.Status == KnowledgeInterviewCompleted {
			if doc.Interview.CompletionRequestID == requestID && doc.Interview.SelectionHash == selectionHash {
				return s.Read(ctx, userID)
			}
			return nil, ErrConflict
		}
		if doc.Interview.ProfileWrite != nil {
			return nil, ErrConflict
		}
		for rowID := range selected {
			found := false
			for _, receipt := range doc.Interview.RowReceipts {
				if receipt.RequestID == requestID && receipt.RowID == rowID && receipt.Status == "saved" {
					found = true
					break
				}
			}
			if !found {
				return nil, ErrConflict
			}
		}
		at := s.now().UTC()
		updated, err := s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
			if latest.Interview == nil || latest.Interview.Status == KnowledgeInterviewCompleted {
				return ErrConflict
			}
			latest.Interview.Status = KnowledgeInterviewCompleted
			latest.Interview.CompletedAt = at
			latest.Interview.UpdatedAt = at
			latest.Interview.CompletionRequestID = requestID
			latest.Interview.SelectionHash = selectionHash
			return nil
		})
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return updated.Interview, nil
	}
	return nil, ErrConflict
}

func validInterviewID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func (s *KnowledgeInterviewService) transition(ctx context.Context, userID string, status KnowledgeInterviewStatus) (*KnowledgeInterview, error) {
	if s == nil || s.store == nil || s.now == nil {
		return nil, ErrRepairNeeded
	}
	for range knowledgeRetryLimit {
		doc, err := s.store.Read(ctx, userID)
		if err != nil {
			return nil, err
		}
		if doc.Interview != nil {
			if doc.Interview.Status == KnowledgeInterviewCompleted || doc.Interview.Status == status || status == KnowledgeInterviewOffered {
				return s.Read(ctx, userID)
			}
		}
		at := s.now().UTC()
		updated, err := s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
			if latest.Interview == nil {
				latest.Interview = &KnowledgeInterview{Status: status, OfferedAt: at, UpdatedAt: at}
				return nil
			}
			if latest.Interview.Status == KnowledgeInterviewCompleted {
				return ErrConflict
			}
			latest.Interview.Status = status
			latest.Interview.UpdatedAt = at
			return nil
		})
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return updated.Interview, nil
	}
	return nil, ErrConflict
}
