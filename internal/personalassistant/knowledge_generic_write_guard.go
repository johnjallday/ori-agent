package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// knowledgeTextKey is only a suppression marker, never a second fact value.
// It also prevents a generic agent memory_write from restating an exact
// rejected/forgotten/suspended review under an ordinary provenance marker.
func knowledgeTextKey(text string) string {
	value := strings.Join(strings.Fields(cases.Fold().String(norm.NFKC.String(text))), " ")
	sum := sha256.Sum256([]byte(value))
	return "text:" + hex.EncodeToString(sum[:])
}

func knowledgeSuppressionKeys(item KnowledgeItem) []string {
	keys := append(append([]string(nil), item.Aliases...), item.SemanticKey)
	for _, revision := range item.Revisions {
		if revision.Text != "" {
			keys = append(keys, knowledgeTextKey(revision.Text))
		}
	}
	seen := make(map[string]bool, len(keys))
	unique := make([]string, 0, len(keys))
	for _, key := range keys {
		if key != "" && !seen[key] {
			seen[key] = true
			unique = append(unique, key)
		}
	}
	return unique
}

func hasKnowledgeTombstoneCapacity(doc KnowledgeDocument, keys []string) bool {
	missing := 0
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if !seen[key] && !keySuppressed(doc, key) {
			missing++
		}
		seen[key] = true
	}
	return len(doc.Tombstones)+missing <= knowledgeMaxTombstone
}

// GuardGenericMemoryWrite is called only for server-identified Personal HQ
// generic agent tools. A new explicitly confirmed dossier/Home statement is
// a separate user operation and never calls this guard. Missing or corrupt
// sidecar metadata makes the agent tool fail closed rather than guessing that
// the same rejected text is ordinary operational memory.
func (s *KnowledgeLearningService) GuardGenericMemoryWrite(ctx context.Context, userID, workspaceID, text string) error {
	if s == nil || s.store == nil {
		return ErrRepairNeeded
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil || binding.HQWorkspaceID != workspaceID {
		return ErrRepairNeeded
	}
	clean, err := workspace.ValidateMemoryText(text)
	if err != nil || clean != text {
		return ErrValidation
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return err
	}
	if err := guardGenericText(doc, text); err != nil {
		return err
	}
	return s.store.checkBinding(ctx, binding)
}

func guardGenericText(doc KnowledgeDocument, text string) error {
	if !doc.Present || doc.Version == 0 {
		return ErrRepairNeeded
	}
	key := knowledgeTextKey(text)
	if keySuppressed(doc, key) {
		return workspace.ErrMemoryManaged
	}
	for _, item := range doc.Items {
		for _, revision := range item.Revisions {
			if revision.Text != "" && knowledgeTextKey(revision.Text) == key {
				return workspace.ErrMemoryManaged
			}
		}
	}
	return nil
}

// AppendGenericMemoryWrite serializes the HQ agent tool's suppression check
// AND its canonical append against all lifecycle sidecar transitions. A Forget
// cannot prepare its exclusion between these two steps. Other workspaces keep
// their original independent memory tools; the host selects this path only
// after checking the designated HQ.
func (s *KnowledgeLearningService) AppendGenericMemoryWrite(ctx context.Context, userID, workspaceID string, entry workspace.MemoryEntry) error {
	if s == nil || s.store == nil || s.memory == nil {
		return ErrRepairNeeded
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil || binding.HQWorkspaceID != workspaceID {
		return ErrRepairNeeded
	}
	clean, err := workspace.ValidateMemoryText(entry.Text)
	if err != nil || clean != entry.Text {
		return ErrValidation
	}
	if workspace.IsHQManagedProvenance(entry.Provenance) {
		return workspace.ErrMemoryManaged
	}
	return s.store.withLockedDocument(ctx, binding, func(doc KnowledgeDocument) error {
		if err := guardGenericText(doc, entry.Text); err != nil {
			return err
		}
		return s.memory.Append(binding.HQWorkspaceID, entry)
	})
}

// GuardGenericProfileWrite prevents the pre-existing agent profile_set tool
// from restoring a former reviewed interview preference after another editor
// changed or cleared that canonical field. The caller must commit the returned
// profile snapshot with SetFieldsIfVersion: a read-only guard followed by the
// old unversioned SetFields would race a concurrent user Forget.
func (s *KnowledgeLearningService) GuardGenericProfileWrite(ctx context.Context, userID string, current *userprofile.UserProfile, fields map[string]any) error {
	if s == nil || s.store == nil || current == nil || current.ID != userID {
		return ErrRepairNeeded
	}
	binding, err := s.store.resolve(ctx, userID)
	if err != nil {
		return err
	}
	preferenceWrite := false
	for field := range fields {
		if field = strings.TrimSpace(field); field == "preferences.response_style" || field == "preferences.units" || field == "preferences.language" {
			preferenceWrite = true
			break
		}
	}
	if !preferenceWrite {
		return s.store.checkBinding(ctx, binding) // About has no interview target
	}
	doc, err := s.store.Read(ctx, userID)
	if err != nil {
		return err
	}
	// Absence is indistinguishable from a deleted receipt after Forget. Do not
	// let a global agent tool recreate prior wording while metadata is missing;
	// the explicit user profile editor remains available for voluntary entry.
	if !doc.Present {
		return ErrRepairNeeded
	}
	for rawField, rawValue := range fields {
		field := strings.TrimSpace(rawField)
		if field != "preferences.response_style" && field != "preferences.units" && field != "preferences.language" {
			continue
		}
		if doc.Interview != nil && doc.Interview.ProfileWrite != nil && doc.Interview.ProfileWrite.Field == field {
			return ErrRepairNeeded
		}
		if doc.Interview == nil || doc.Interview.Status != KnowledgeInterviewCompleted {
			continue
		}
		value := ""
		if rawValue != nil {
			value = strings.Join(strings.Fields(fmt.Sprint(rawValue)), " ")
		}
		canonical, err := canonicalProfileValue(current, field)
		if err != nil {
			return ErrRepairNeeded
		}
		if canonical == value || value == "" {
			continue // no resurrection; an explicit clear is still permitted
		}
		for _, receipt := range doc.Interview.RowReceipts {
			if receipt.Status == "saved" && receipt.RowID == "communication" &&
				receipt.CanonicalRef == "profile:"+field && receipt.TextHash == hashKnowledgeLine(value) {
				return workspace.ErrMemoryManaged
			}
		}
	}
	return s.store.checkBinding(ctx, binding)
}
