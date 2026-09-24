package personalassistant

import (
	"context"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ConfirmedInterviewPreference is read from the current canonical global
// profile, never the receipt's former answer text. Only a completed interview
// receipt with an exact field/value hash may contribute a preference to Today.
type ConfirmedInterviewPreference struct {
	Field      string
	Text       string
	ReviewedAt time.Time // the actual saved receipt, not the profile's global last-write time
}

func (s *KnowledgeInterviewService) ConfirmedProfilePreferences(ctx context.Context, userID string) ([]ConfirmedInterviewPreference, error) {
	if s == nil || s.store == nil || s.profiles == nil {
		return nil, ErrRepairNeeded
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return nil, err
	}
	if binding.Paused {
		return nil, nil
	}
	interview, err := s.Read(ctx, userID)
	if err != nil || interview == nil || interview.Status != KnowledgeInterviewCompleted {
		return nil, err
	}
	var approved []KnowledgeInterviewRowReceipt
	for _, receipt := range interview.RowReceipts {
		if receipt.Status == "saved" && receipt.RowID == "communication" &&
			strings.HasPrefix(receipt.CanonicalRef, "profile:preferences.") {
			approved = append(approved, receipt)
		}
	}
	if len(approved) == 0 {
		return nil, nil
	}
	profile, err := s.profiles.Get(ctx, binding.UserID)
	if err != nil {
		return nil, err
	}
	var results []ConfirmedInterviewPreference
	for _, receipt := range approved {
		field := strings.TrimPrefix(receipt.CanonicalRef, "profile:")
		if !allowedKnowledgeProfileField(field) {
			continue
		}
		value, err := canonicalProfileValue(profile, field)
		if err != nil || value == "" || hashKnowledgeLine(value) != receipt.TextHash {
			continue
		}
		if clean, err := workspace.ValidateMemoryText(value); err != nil || clean != value {
			continue
		}
		// Matching wording does not prove an unchanged review: a user may have
		// cleared the preference and later re-entered exactly the same text.
		// The SQL-owned field generation survives unrelated profile edits and
		// detects that cycle even across restarts and legacy whole-profile PUT.
		revision, err := s.profiles.PreferenceRevision(ctx, binding.UserID, strings.TrimPrefix(field, "preferences."))
		if err != nil {
			return nil, err
		}
		if receipt.ProfileRevision < 1 || revision != receipt.ProfileRevision {
			continue
		}
		results = append(results, ConfirmedInterviewPreference{Field: field, Text: value, ReviewedAt: receipt.SavedAt})
	}
	if err := s.store.checkBinding(ctx, binding); err != nil {
		return nil, err
	}
	return results, nil
}
