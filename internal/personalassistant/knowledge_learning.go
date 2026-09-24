package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	knowledgeDailyLimit   = 3
	knowledgePendingLimit = 6
	knowledgeRetryLimit   = 16
)

var (
	ErrKnowledgeQuota      = errors.New("personal assistant: review queue is full")
	ErrKnowledgeSuppressed = errors.New("personal assistant: this source meaning was rejected or forgotten")
	ErrKnowledgePaused     = errors.New("personal assistant: automatic suggestions are paused")
)

// KnowledgeProposal is constructed only by a server-owned, authorized source
// adapter. None of these fields is accepted as trusted from a browser, agent
// tool, or HTTP request. Source-specific adapters must verify evidence and
// current scope at proposal and again at approval.
type KnowledgeProposal struct {
	SourceKind string
	ScopeID    string
	SubjectID  string
	Predicate  string
	Value      string
	Category   string
	Text       string
	Evidence   []KnowledgeEvidence
}

// KnowledgeLearningService admits bounded, review-only hypotheses. Canonical
// profile and MEMORY.md writes remain exclusive to later reviewed operations.
type KnowledgeLearningService struct {
	store     *KnowledgeStore
	memory    *workspace.MemoryStore
	profiles  ProfileCASStore
	authority KnowledgeSourceAuthority
	now       func() time.Time
}

// ProfileCASStore changes a single reviewed profile field atomically; an
// agent/browser cannot select arbitrary destination fields through this seam.
type ProfileCASStore interface {
	Get(ctx context.Context, id string) (*userprofile.UserProfile, error)
	PreferenceRevision(ctx context.Context, id, field string) (int64, error)
	UpdateFieldCAS(ctx context.Context, userID, field string, expectedUpdatedAt time.Time, expectedValue, newValue string) (*userprofile.UserProfile, error)
	UpdateFieldCASAt(ctx context.Context, userID, field string, expectedUpdatedAt time.Time, expectedValue, newValue string, writtenAt time.Time) (*userprofile.UserProfile, error)
}

func (s *KnowledgeLearningService) SetProfileCAS(store ProfileCASStore) {
	if s != nil {
		s.profiles = store
	}
}

func NewKnowledgeLearningService(store *KnowledgeStore) *KnowledgeLearningService {
	return &KnowledgeLearningService{store: store, now: time.Now}
}

// KnowledgeSourceAuthority must independently check the live, server-owned
// evidence before activation. No default or browser-provided authority exists.
type KnowledgeSourceAuthority interface {
	Revalidate(ctx context.Context, binding KnowledgeBinding, item KnowledgeItem) error
}

func NewKnowledgeLifecycleService(store *KnowledgeStore, memory *workspace.MemoryStore, authority KnowledgeSourceAuthority) *KnowledgeLearningService {
	return &KnowledgeLearningService{store: store, memory: memory, authority: authority, now: time.Now}
}

func (s *KnowledgeLearningService) Propose(ctx context.Context, userID string, proposal KnowledgeProposal) (KnowledgeItem, bool, error) {
	if s == nil || s.store == nil || s.now == nil {
		return KnowledgeItem{}, false, ErrRepairNeeded
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return KnowledgeItem{}, false, err
	}
	if binding.Paused {
		return KnowledgeItem{}, false, ErrKnowledgePaused
	}
	proposal, err = validateKnowledgeProposal(proposal)
	if err != nil {
		return KnowledgeItem{}, false, err
	}
	key := proposalSemanticKey(binding, proposal)
	for range knowledgeRetryLimit {
		doc, err := s.store.Read(ctx, userID)
		if err != nil {
			return KnowledgeItem{}, false, err
		}
		if keySuppressed(doc, key) {
			return KnowledgeItem{}, false, ErrKnowledgeSuppressed
		}
		for _, item := range doc.Items {
			if item.SemanticKey == key || hasKnowledgeAlias(item, key) {
				if item.State == KnowledgeRejected || item.State == KnowledgeForgotten {
					return KnowledgeItem{}, false, ErrKnowledgeSuppressed
				}
				return item, false, nil
			}
		}
		at := s.now().UTC()
		item := KnowledgeItem{
			ID: uuid.NewString(), Version: 1, State: KnowledgeCandidate,
			SemanticKey: key, Category: proposal.Category, SourceKind: proposal.SourceKind,
			CreatedAt: at, UpdatedAt: at,
		}
		item.Revisions = []KnowledgeRevision{{ID: uuid.NewString(), Text: proposal.Text, Evidence: append([]KnowledgeEvidence(nil), proposal.Evidence...), CreatedAt: at}}
		item.CurrentRevisionID = item.Revisions[0].ID
		_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
			if keySuppressed(*latest, key) {
				return ErrKnowledgeSuppressed
			}
			for _, existing := range latest.Items {
				if existing.SemanticKey == key || hasKnowledgeAlias(existing, key) {
					return ErrConflict
				}
			}
			var pending int
			for _, existing := range latest.Items {
				if existing.State == KnowledgeCandidate {
					pending++
				}
			}
			if pending >= knowledgePendingLimit || len(latest.Items) >= knowledgeMaxItems {
				return ErrKnowledgeQuota
			}
			cutoff := at.Add(-24 * time.Hour)
			active := make([]time.Time, 0, len(latest.Admissions)+1)
			for _, admitted := range latest.Admissions {
				if admitted.After(at) {
					return ErrKnowledgeCorrupt // do not silently erase future quota receipts
				}
				if admitted.After(cutoff) {
					active = append(active, admitted)
				}
			}
			if len(active) >= knowledgeDailyLimit {
				return ErrKnowledgeQuota
			}
			latest.Admissions = append(active, at)
			latest.Items = append(latest.Items, item)
			return nil
		})
		if errors.Is(err, ErrConflict) {
			continue // a concurrent writer may have proposed the same meaning
		}
		return item, err == nil, err
	}
	return KnowledgeItem{}, false, ErrConflict
}

// EditCandidate only creates a new inert review revision. Evidence remains
// attached to the original observation, never to a new autonomous source
// assertion. The explicit approval transition is separate.
func (s *KnowledgeLearningService) EditCandidate(ctx context.Context, userID, itemID string, expectedVersion int64, text string) (KnowledgeItem, error) {
	return s.editCandidate(ctx, userID, itemID, expectedVersion, "", text)
}

// EditCandidateWithReceipt binds an explicit review edit to a durable retry
// key. A lost response may replay only the same still-current revision; it
// cannot silently supersede a later edit or count the old edit twice.
func (s *KnowledgeLearningService) EditCandidateWithReceipt(ctx context.Context, userID, itemID string, expectedVersion int64, requestID, text string) (KnowledgeItem, error) {
	if !validInterviewID(requestID) {
		return KnowledgeItem{}, ErrConflict
	}
	return s.editCandidate(ctx, userID, itemID, expectedVersion, requestID, text)
}

func (s *KnowledgeLearningService) editCandidate(ctx context.Context, userID, itemID string, expectedVersion int64, requestID, text string) (KnowledgeItem, error) {
	if s == nil || s.store == nil || s.now == nil || itemID == "" || expectedVersion < 1 {
		return KnowledgeItem{}, ErrConflict
	}
	text, err := workspace.ValidateMemoryText(text)
	if err != nil {
		return KnowledgeItem{}, err
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return KnowledgeItem{}, err
	}
	if requestID != "" {
		if receipt, found := knowledgeReceiptByID(doc, requestID); found {
			item, ok := knowledgeItemByID(doc, itemID)
			if ok && receipt.ItemID == itemID && receipt.Result == "candidate_edited" &&
				item.State == KnowledgeCandidate && item.CurrentRevisionID == receipt.RevisionID {
				for _, revision := range item.Revisions {
					if revision.ID == receipt.RevisionID && revision.Text == text {
						return item, nil
					}
				}
			}
			return KnowledgeItem{}, ErrConflict
		}
	}
	var changed KnowledgeItem
	_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
		if requestID != "" {
			if _, used := knowledgeReceiptByID(*latest, requestID); used {
				return ErrConflict
			}
		}
		for i := range latest.Items {
			item := &latest.Items[i]
			if item.ID != itemID {
				continue
			}
			if item.Version != expectedVersion || item.State != KnowledgeCandidate || item.Target != nil || item.Prepared != nil {
				return ErrConflict
			}
			if len(item.Revisions) >= knowledgeMaxRevisions || (requestID != "" && len(latest.Receipts) >= knowledgeMaxReceipts) {
				return ErrKnowledgeLimit
			}
			at := s.now().UTC()
			var evidence []KnowledgeEvidence
			if len(item.Revisions) > 0 {
				evidence = append(evidence, item.Revisions[0].Evidence...)
			}
			revision := KnowledgeRevision{ID: uuid.NewString(), Text: text, Evidence: evidence, CreatedAt: at, EditedAt: &at}
			item.Revisions = append(item.Revisions, revision)
			item.CurrentRevisionID = revision.ID
			item.Version++
			item.UpdatedAt = at
			if requestID != "" {
				latest.Receipts = append(latest.Receipts, KnowledgeReceipt{RequestID: requestID, ItemID: itemID, Result: "candidate_edited", RevisionID: revision.ID, CreatedAt: at})
			}
			changed = *item
			return nil
		}
		return ErrNotFound
	})
	return changed, err
}

// SuppressCandidate handles inert candidates only; approved values require the
// canonical remove-and-journal transaction (task 1.6). The returned tombstone
// holds only semantic hashes and stable IDs, not proposal plaintext.
func (s *KnowledgeLearningService) SuppressCandidate(ctx context.Context, userID, itemID string, expectedVersion int64, state KnowledgeState) (KnowledgeItem, error) {
	return s.suppressCandidate(ctx, userID, itemID, expectedVersion, "", state)
}

// SuppressCandidateWithReceipt atomically tombstones a rejected candidate
// together with its replay receipt. A retry cannot resurrect erased text.
func (s *KnowledgeLearningService) SuppressCandidateWithReceipt(ctx context.Context, userID, itemID string, expectedVersion int64, requestID string, state KnowledgeState) (KnowledgeItem, error) {
	if !validInterviewID(requestID) {
		return KnowledgeItem{}, ErrConflict
	}
	return s.suppressCandidate(ctx, userID, itemID, expectedVersion, requestID, state)
}

func (s *KnowledgeLearningService) suppressCandidate(ctx context.Context, userID, itemID string, expectedVersion int64, requestID string, state KnowledgeState) (KnowledgeItem, error) {
	if s == nil || s.store == nil || s.now == nil || itemID == "" || expectedVersion < 1 ||
		(state != KnowledgeRejected && state != KnowledgeForgotten) {
		return KnowledgeItem{}, ErrConflict
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return KnowledgeItem{}, err
	}
	if requestID != "" {
		if receipt, found := knowledgeReceiptByID(doc, requestID); found {
			item, ok := knowledgeItemByID(doc, itemID)
			if ok && receipt.ItemID == itemID && receipt.Result == string(state) && item.State == state {
				return item, nil
			}
			return KnowledgeItem{}, ErrConflict
		}
	}
	var changed KnowledgeItem
	_, err = s.store.Update(ctx, userID, doc.Version, func(latest *KnowledgeDocument) error {
		if requestID != "" {
			if _, used := knowledgeReceiptByID(*latest, requestID); used {
				return ErrConflict
			}
		}
		for i := range latest.Items {
			item := &latest.Items[i]
			if item.ID != itemID {
				continue
			}
			if item.Version != expectedVersion || item.State != KnowledgeCandidate || item.Target != nil || item.Prepared != nil {
				return ErrConflict
			}
			keys := knowledgeSuppressionKeys(*item)
			if !hasKnowledgeTombstoneCapacity(*latest, keys) || (requestID != "" && len(latest.Receipts) >= knowledgeMaxReceipts) {
				return ErrKnowledgeLimit
			}
			at := s.now().UTC()
			for _, key := range keys {
				if !keySuppressed(*latest, key) {
					latest.Tombstones = append(latest.Tombstones, KnowledgeTombstone{SemanticKey: key, ItemID: item.ID, CreatedAt: at})
				}
			}
			item.Revisions = nil // reject and forget do not retain source plaintext
			item.CurrentRevisionID = ""
			item.State = state
			item.Version++
			item.UpdatedAt = at
			if requestID != "" {
				latest.Receipts = append(latest.Receipts, KnowledgeReceipt{RequestID: requestID, ItemID: itemID, Result: string(state), CreatedAt: at})
			}
			changed = *item
			return nil
		}
		return ErrNotFound
	})
	return changed, err
}

func keySuppressed(doc KnowledgeDocument, key string) bool {
	for _, tombstone := range doc.Tombstones {
		if tombstone.SemanticKey == key {
			return true
		}
	}
	return false
}

func hasKnowledgeAlias(item KnowledgeItem, key string) bool {
	for _, alias := range item.Aliases {
		if alias == key {
			return true
		}
	}
	return false
}

func validateKnowledgeProposal(input KnowledgeProposal) (KnowledgeProposal, error) {
	switch input.SourceKind {
	case "saved_app", "file_janitor":
	default:
		return KnowledgeProposal{}, fmt.Errorf("%w: unsupported source", ErrValidation)
	}
	for _, value := range []string{input.ScopeID, input.SubjectID, input.Predicate, input.Value} {
		if value == "" || len(value) > 200 || strings.ContainsAny(value, "\r\n") {
			return KnowledgeProposal{}, fmt.Errorf("%w: invalid source identity", ErrValidation)
		}
	}
	switch input.Category {
	case "how_you_work", "people", "projects", "routines", "sources":
	default:
		return KnowledgeProposal{}, fmt.Errorf("%w: unsupported category", ErrValidation)
	}
	text, err := workspace.ValidateMemoryText(input.Text)
	if err != nil {
		return KnowledgeProposal{}, fmt.Errorf("%w: unsafe proposal", ErrValidation)
	}
	input.Text = text
	if len(input.Evidence) == 0 || len(input.Evidence) > knowledgeMaxEvidence {
		return KnowledgeProposal{}, fmt.Errorf("%w: invalid evidence count", ErrValidation)
	}
	for _, evidence := range input.Evidence {
		if evidence.SourceKind != input.SourceKind || evidence.SourceID == "" || len(evidence.SourceID) > 200 {
			return KnowledgeProposal{}, fmt.Errorf("%w: foreign evidence", ErrValidation)
		}
		if evidence.Summary != "" {
			clean, err := workspace.ValidateMemoryText(evidence.Summary)
			if err != nil || clean != evidence.Summary {
				return KnowledgeProposal{}, fmt.Errorf("%w: unsafe evidence", ErrValidation)
			}
		}
	}
	return input, nil
}

// KnowledgeSemanticKey is for trusted host-owned source adapters that must
// revalidate a persisted item's meaning without storing a second plaintext
// source descriptor. Never expose it as a browser-selected identity.
func KnowledgeSemanticKey(binding KnowledgeBinding, proposal KnowledgeProposal) string {
	return proposalSemanticKey(binding, proposal)
}

// proposalSemanticKey uses server-controlled source IDs and stable meaning
// dimensions, never the display text, timestamp, action/batch ID or evidence
// wording. Punctuation in the opaque scope ID is preserved to avoid merging
// distinct workspace/root generations; subject/predicate/value words are folded.
func proposalSemanticKey(binding KnowledgeBinding, input KnowledgeProposal) string {
	parts := []string{
		"v1", binding.UserID, binding.HQWorkspaceID, input.SourceKind,
		strings.TrimSpace(input.ScopeID), normalizeKnowledgeWords(input.SubjectID),
		normalizeKnowledgeWords(input.Predicate), normalizeKnowledgeWords(input.Value),
	}
	encoded, _ := json.Marshal(parts)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func normalizeKnowledgeWords(value string) string {
	value = cases.Fold().String(norm.NFKC.String(value))
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	}), " ")
}
