package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// KnowledgeInterviewReviewedRow contains only a user's final, explicit review.
// A blank/skipped answer is omitted, never turned into a learned fact.
type KnowledgeInterviewReviewedRow struct {
	RowID                    string    `json:"row_id"`
	Destination              string    `json:"destination"`
	Category                 string    `json:"category"`
	Text                     string    `json:"text"`
	Preference               string    `json:"preference,omitempty"`
	ExpectedProfileValue     string    `json:"expected_profile_value,omitempty"`
	ExpectedProfileUpdatedAt time.Time `json:"expected_profile_updated_at,omitempty"`
}

type KnowledgeInterviewSaveRequest struct {
	StateVersion int64                           `json:"state_version"`
	RequestID    string                          `json:"request_id"`
	Rows         []KnowledgeInterviewReviewedRow `json:"rows"`
	ResetPartial bool                            `json:"reset_partial,omitempty"`
}

type KnowledgeInterviewSaveResult struct {
	Status      KnowledgeInterviewStatus `json:"status"`
	SavedRows   []string                 `json:"saved_rows"`
	PendingRows []string                 `json:"pending_rows,omitempty"`
}

type KnowledgeInterviewProfileSnapshot struct {
	UpdatedAt   time.Time         `json:"updated_at"`
	Preferences map[string]string `json:"preferences"`
}

func (s *KnowledgeInterviewService) ProfileSnapshot(ctx context.Context, userID string) (*KnowledgeInterviewProfileSnapshot, error) {
	if s == nil || s.store == nil || s.profiles == nil {
		return nil, ErrRepairNeeded
	}
	if _, err := s.store.resolve(ctx, userID); err != nil {
		return nil, err
	}
	profile, err := s.profiles.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &KnowledgeInterviewProfileSnapshot{UpdatedAt: profile.UpdatedAt,
		Preferences: map[string]string{
			"response_style": profile.Preferences["response_style"],
			"units":          profile.Preferences["units"],
			"language":       profile.Preferences["language"],
		},
	}, nil
}

func (s *KnowledgeInterviewService) StateVersion(ctx context.Context, userID string) (int64, error) {
	if s == nil || s.store == nil {
		return 0, ErrRepairNeeded
	}
	binding, err := s.store.resolve(ctx, userID)
	return binding.StateVersion, err
}

func (s *KnowledgeInterviewService) SetReviewedMemoryWriter(writer *MemoryService) {
	if s != nil {
		s.memory = writer
	}
}

func (s *KnowledgeInterviewService) SetCanonicalSavers(learning *KnowledgeLearningService, profiles ProfileCASStore) {
	if s != nil {
		s.learning = learning
		s.profiles = profiles
	}
}

func validateInterviewRows(rows []KnowledgeInterviewReviewedRow) error {
	if len(rows) > 3 {
		return ErrValidation
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if seen[row.RowID] {
			return ErrValidation
		}
		seen[row.RowID] = true
		clean, err := workspace.ValidateMemoryText(row.Text)
		if err != nil || clean != row.Text {
			return ErrValidation
		}
		switch row.RowID {
		case "priority":
			if row.Category != "projects" || row.Destination != "personal_hq" {
				return ErrValidation
			}
		case "communication":
			if row.Category != "how_you_work" || (row.Destination != "personal_hq" && row.Destination != "profile") {
				return ErrValidation
			}
		case "person_or_routine":
			if (row.Category != "people" && row.Category != "routines") || row.Destination != "personal_hq" {
				return ErrValidation
			}
		default:
			return ErrValidation
		}
		if row.Destination == "profile" {
			if row.Preference != "response_style" && row.Preference != "units" && row.Preference != "language" ||
				row.ExpectedProfileUpdatedAt.IsZero() {
				return ErrValidation
			}
		} else if row.Preference != "" || !row.ExpectedProfileUpdatedAt.IsZero() || row.ExpectedProfileValue != "" {
			return ErrValidation
		}
	}
	return nil
}

func interviewRowRequestID(saveID, rowID string) string {
	sum := sha256.Sum256([]byte(saveID + ":" + rowID))
	return "interview-row-" + hex.EncodeToString(sum[:16])
}

// SaveReviewed binds the whole final-review payload before any canonical
// mutation, then saves each row independently. Lost responses resume by exact
// per-row ID/text: an HQ row replays its prepared canonical write and a global
// profile row is never overwritten if it changed since the reviewed snapshot.
// A partial error returns the durable saved rows so the API can report them
// honestly; no fake all-saved response or rollback of another completed row.
func (s *KnowledgeInterviewService) SaveReviewed(ctx context.Context, userID string, request KnowledgeInterviewSaveRequest) (KnowledgeInterviewSaveResult, error) {
	result := KnowledgeInterviewSaveResult{SavedRows: []string{}}
	if s == nil || s.store == nil || s.learning == nil || s.now == nil ||
		!validInterviewID(request.RequestID) || request.StateVersion < 1 || validateInterviewRows(request.Rows) != nil {
		return result, ErrValidation
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return result, err
	}
	if binding.StateVersion != request.StateVersion {
		return result, ErrConflict
	}
	payload, err := json.Marshal(request.Rows)
	if err != nil {
		return result, ErrValidation
	}
	reviewHash := hashKnowledgeLine(string(payload))
	if _, err := s.Offer(ctx, userID); err != nil {
		return result, err
	}
	for range knowledgeRetryLimit {
		doc, err := s.store.Read(ctx, userID)
		if err != nil || doc.Interview == nil {
			return result, ErrRepairNeeded
		}
		if doc.Interview.Status == KnowledgeInterviewCompleted {
			if doc.Interview.SaveRequestID != request.RequestID || doc.Interview.ReviewHash != reviewHash {
				return result, ErrConflict
			}
			result.Status = KnowledgeInterviewCompleted
			for _, row := range request.Rows {
				result.SavedRows = append(result.SavedRows, row.RowID)
			}
			return result, nil
		}
		if doc.Interview.SaveRequestID != "" {
			if doc.Interview.SaveRequestID == request.RequestID {
				if doc.Interview.ReviewHash != reviewHash {
					return result, ErrConflict
				}
				break
			}
			if !request.ResetPartial || !interviewSavedRowsIncluded(doc.Interview.RowReceipts, request.Rows) {
				return result, ErrConflict
			}
			_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
				if latest.Interview == nil || latest.Interview.Status == KnowledgeInterviewCompleted ||
					latest.Interview.SaveRequestID != doc.Interview.SaveRequestID ||
					!interviewSavedRowsIncluded(latest.Interview.RowReceipts, request.Rows) {
					return ErrConflict
				}
				latest.Interview.SaveRequestID = request.RequestID
				latest.Interview.ReviewHash = reviewHash
				// A new explicit final review supersedes only the prior pending
				// profile intent, never any already verified saved row.
				latest.Interview.ProfileWrite = nil
				latest.Interview.UpdatedAt = s.now().UTC()
				return nil
			})
			if errors.Is(err, ErrConflict) {
				continue
			}
			if err != nil {
				return result, err
			}
			break
		}
		_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
			if latest.Interview == nil || latest.Interview.Status == KnowledgeInterviewCompleted || latest.Interview.SaveRequestID != "" {
				return ErrConflict
			}
			latest.Interview.SaveRequestID = request.RequestID
			latest.Interview.ReviewHash = reviewHash
			latest.Interview.UpdatedAt = s.now().UTC()
			return nil
		})
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return result, err
		}
		break
	}
	// If a concurrent writer prevented the reservation, fail before a save.
	status, err := s.Read(ctx, userID)
	if err != nil || status == nil || status.SaveRequestID != request.RequestID || status.ReviewHash != reviewHash {
		return result, ErrConflict
	}
	for index, row := range request.Rows {
		rowID := interviewRowRequestID(request.RequestID, row.RowID)
		var canonicalRef string
		if previous, ok := interviewPreviouslySaved(status.RowReceipts, row); ok {
			// A receipt is the durable proof for any exact replay, including
			// after a lost completion response. Revalidate its current canonical
			// value instead of preparing another SQL mutation for this row.
			canonicalRef, err = s.verifyPreviousInterviewRow(ctx, userID, row, previous)
		} else if row.Destination == "profile" {
			canonicalRef, err = s.saveReviewedProfileRow(ctx, binding, request.RequestID, row)
		} else if s.memory != nil {
			var saved *RememberResult
			saved, err = s.memory.Remember(ctx, userID, RememberRequest{
				IfVersion: request.StateVersion, Destination: MemoryDestinationPersonalHQ,
				RequestID: rowID, Category: row.Category, Text: row.Text,
			})
			if err == nil {
				canonicalRef = "memory:" + saved.ItemID
			}
		} else {
			// Standalone domain fixtures may have no HTTP/MemoryService wrapper.
			// Production wires it so confirmed HQ rows cross the existing
			// MemoryService.Remember authority boundary before the journal.
			var saved KnowledgeItem
			saved, err = s.learning.SaveExplicit(ctx, userID, request.StateVersion, rowID, row.Category, row.Text)
			canonicalRef = "memory:" + saved.ID
		}
		if err == nil {
			receipt := KnowledgeInterviewRowReceipt{
				RequestID: request.RequestID, RowID: row.RowID, TextHash: hashKnowledgeLine(row.Text), CanonicalRef: canonicalRef,
			}
			if row.Destination == "profile" {
				receipt.ProfileExpectedAt = row.ExpectedProfileUpdatedAt
				if row.Text == row.ExpectedProfileValue {
					// No SQL write was needed. RecordSavedRow checks the exact
					// reviewed profile snapshot and per-field generation.
					receipt.ProfileRevision, err = s.profiles.PreferenceRevision(ctx, userID, row.Preference)
				}
			}
			if err == nil {
				err = s.RecordSavedRow(ctx, userID, receipt)
			}
		}
		if err != nil {
			for _, pending := range request.Rows[index:] {
				result.PendingRows = append(result.PendingRows, pending.RowID)
			}
			return result, err
		}
		result.SavedRows = append(result.SavedRows, row.RowID)
	}
	selected := make([]string, 0, len(request.Rows))
	for _, row := range request.Rows {
		selected = append(selected, row.RowID)
	}
	final, err := s.Complete(ctx, userID, request.RequestID, selected)
	if err != nil {
		return result, err
	}
	result.Status = final.Status
	return result, nil
}

func interviewSavedRowsIncluded(receipts []KnowledgeInterviewRowReceipt, rows []KnowledgeInterviewReviewedRow) bool {
	for _, receipt := range receipts {
		if receipt.Status != "saved" {
			continue
		}
		matched := false
		for _, row := range rows {
			if row.RowID == receipt.RowID && receipt.TextHash == hashKnowledgeLine(row.Text) &&
				((row.Destination == "profile" && receipt.CanonicalRef == "profile:preferences."+row.Preference) ||
					(row.Destination == "personal_hq" && strings.HasPrefix(receipt.CanonicalRef, "memory:"))) {
				matched = true
				break
			}
		}
		if !matched {
			return false // a new attempt cannot drop or rewrite an already saved row
		}
	}
	return true
}

func interviewPreviouslySaved(receipts []KnowledgeInterviewRowReceipt, row KnowledgeInterviewReviewedRow) (KnowledgeInterviewRowReceipt, bool) {
	for _, receipt := range receipts {
		if receipt.Status == "saved" && receipt.RowID == row.RowID && receipt.TextHash == hashKnowledgeLine(row.Text) {
			return receipt, true
		}
	}
	return KnowledgeInterviewRowReceipt{}, false
}

func (s *KnowledgeInterviewService) verifyPreviousInterviewRow(ctx context.Context, userID string, row KnowledgeInterviewReviewedRow, saved KnowledgeInterviewRowReceipt) (string, error) {
	if row.Destination == "profile" {
		if saved.CanonicalRef != "profile:preferences."+row.Preference || s.profiles == nil {
			return "", ErrConflict
		}
		profile, err := s.profiles.Get(ctx, userID)
		if err != nil {
			return "", err
		}
		value, err := canonicalProfileValue(profile, strings.TrimPrefix(saved.CanonicalRef, "profile:"))
		if err != nil || value != row.Text {
			return "", ErrConflict
		}
		return saved.CanonicalRef, nil
	}
	if !strings.HasPrefix(saved.CanonicalRef, "memory:") || s.learning == nil {
		return "", ErrConflict
	}
	itemID := strings.TrimPrefix(saved.CanonicalRef, "memory:")
	views, err := s.learning.ReviewItems(ctx, userID)
	if err != nil {
		return "", err
	}
	for _, view := range views {
		if view.ID == itemID && view.State == KnowledgeApproved && view.SourceKind == "explicit" &&
			view.Text == row.Text && view.ReviewUnavailable == "" {
			return saved.CanonicalRef, nil
		}
	}
	return "", ErrConflict
}

func (s *KnowledgeInterviewService) saveReviewedProfileRow(ctx context.Context, binding KnowledgeBinding, requestID string, row KnowledgeInterviewReviewedRow) (string, error) {
	if s.profiles == nil || s.now == nil {
		return "", ErrRepairNeeded
	}
	field := "preferences." + row.Preference
	want := strings.Join(strings.Fields(row.Text), " ")
	for range knowledgeRetryLimit {
		doc, err := s.store.Read(ctx, binding.UserID)
		if err != nil {
			return "", err
		}
		if doc.Interview == nil || doc.Interview.SaveRequestID != requestID {
			return "", ErrConflict
		}
		if previous, ok := interviewPreviouslySaved(doc.Interview.RowReceipts, row); ok {
			if previous.RequestID != requestID {
				return "", ErrConflict
			}
			return s.verifyPreviousInterviewRow(ctx, binding.UserID, row, previous)
		}
		if doc.Interview.Status == KnowledgeInterviewCompleted {
			return "", ErrConflict
		}
		profile, err := s.profiles.Get(ctx, binding.UserID)
		if err != nil {
			return "", err
		}
		value, err := canonicalProfileValue(profile, field)
		if err != nil {
			return "", err
		}
		prepared := doc.Interview.ProfileWrite
		if prepared == nil {
			// Identical wording from an outside editor after final review is
			// not evidence that this request wrote it.
			if !profile.UpdatedAt.Equal(row.ExpectedProfileUpdatedAt) || value != row.ExpectedProfileValue {
				return "", ErrConflict
			}
			if value == want {
				if err := s.store.checkBinding(ctx, binding); err != nil {
					return "", err
				}
				return "profile:" + field, nil
			}
			at := s.now().UTC()
			if !at.After(profile.UpdatedAt) {
				at = profile.UpdatedAt.Add(time.Nanosecond)
			}
			beforeRevision, err := s.profiles.PreferenceRevision(ctx, binding.UserID, row.Preference)
			if err != nil || beforeRevision < 1 {
				return "", ErrRepairNeeded
			}
			intent := &KnowledgeInterviewProfileWrite{
				RequestID: requestID, RowID: row.RowID, Field: field,
				BeforeHash: hashKnowledgeLine(value), AfterHash: hashKnowledgeLine(want),
				BeforeUpdatedAt: profile.UpdatedAt, BeforeRevision: beforeRevision, WrittenAt: at,
			}
			_, err = s.store.Update(ctx, binding.UserID, doc.Version, func(latest *KnowledgeDocument) error {
				if latest.Interview == nil || latest.Interview.SaveRequestID != requestID || latest.Interview.ProfileWrite != nil ||
					latest.Interview.Status == KnowledgeInterviewCompleted {
					return ErrConflict
				}
				latest.Interview.ProfileWrite = intent
				latest.Interview.UpdatedAt = s.now().UTC()
				return nil
			})
			if errors.Is(err, ErrConflict) {
				continue
			}
			if err != nil {
				return "", err
			}
			continue
		}
		if prepared.RequestID != requestID || prepared.RowID != row.RowID || prepared.Field != field ||
			prepared.BeforeRevision < 1 || prepared.BeforeHash != hashKnowledgeLine(row.ExpectedProfileValue) ||
			prepared.AfterHash != hashKnowledgeLine(want) || !prepared.BeforeUpdatedAt.Equal(row.ExpectedProfileUpdatedAt) {
			return "", ErrConflict
		}
		if value == want && profile.UpdatedAt.Equal(prepared.WrittenAt) {
			if err := s.store.checkBinding(ctx, binding); err != nil {
				return "", err
			}
			return "profile:" + field, nil // exact earlier SQL write, not matching outside wording
		}
		if !profile.UpdatedAt.Equal(prepared.BeforeUpdatedAt) || hashKnowledgeLine(value) != prepared.BeforeHash {
			return "", ErrConflict
		}
		if err := s.store.checkBinding(ctx, binding); err != nil {
			return "", err
		}
		if _, err := s.profiles.UpdateFieldCASAt(ctx, binding.UserID, field, prepared.BeforeUpdatedAt, value, want, prepared.WrittenAt); err != nil {
			if errors.Is(err, userprofile.ErrProfileConflict) {
				continue // a concurrent exact retry may have won; verify its version
			}
			return "", err // prepared intent survives a transient SQL failure
		}
		verified, err := s.profiles.Get(ctx, binding.UserID)
		if err != nil {
			return "", err
		}
		current, err := canonicalProfileValue(verified, field)
		if err != nil || current != want || !verified.UpdatedAt.Equal(prepared.WrittenAt) {
			return "", ErrConflict
		}
		if err := s.store.checkBinding(ctx, binding); err != nil {
			return "", err
		}
		return "profile:" + field, nil
	}
	return "", ErrConflict
}
