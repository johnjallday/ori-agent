package personalassistant

import (
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func validateKnowledge(doc KnowledgeDocument) error {
	if doc.SchemaVersion != KnowledgeSchemaVersion || doc.Version < 1 ||
		doc.Owner.UserID == "" || doc.Owner.AssistantID == "" || doc.Owner.HQWorkspaceID == "" ||
		doc.Owner.HQFolderSlug == "" || doc.Owner.EntryAgentInstanceID == "" {
		return ErrKnowledgeCorrupt
	}
	if len(doc.Items) > knowledgeMaxItems || len(doc.Tombstones) > knowledgeMaxTombstone ||
		len(doc.Receipts) > knowledgeMaxReceipts || len(doc.Admissions) > knowledgeMaxReceipts {
		return ErrKnowledgeLimit
	}
	if doc.Interview != nil {
		switch doc.Interview.Status {
		case KnowledgeInterviewOffered, KnowledgeInterviewDeferred, KnowledgeInterviewCompleted:
		default:
			return ErrKnowledgeCorrupt
		}
		if doc.Interview.OfferedAt.IsZero() || doc.Interview.UpdatedAt.IsZero() || len(doc.Interview.RowReceipts) > knowledgeMaxReceipts {
			return ErrKnowledgeCorrupt
		}
		if doc.Interview.Status == KnowledgeInterviewCompleted {
			if doc.Interview.CompletedAt.IsZero() || doc.Interview.CompletionRequestID == "" || len(doc.Interview.CompletionRequestID) > 128 || len(doc.Interview.SelectionHash) != 64 {
				return ErrKnowledgeCorrupt
			}
		} else if !doc.Interview.CompletedAt.IsZero() || doc.Interview.CompletionRequestID != "" || doc.Interview.SelectionHash != "" {
			return ErrKnowledgeCorrupt
		}
		if prepared := doc.Interview.ProfileWrite; prepared != nil {
			if doc.Interview.Status == KnowledgeInterviewCompleted || doc.Interview.SaveRequestID == "" ||
				prepared.RequestID != doc.Interview.SaveRequestID || prepared.RowID != "communication" ||
				!strings.HasPrefix(prepared.Field, "preferences.") || !allowedKnowledgeProfileField(prepared.Field) ||
				len(prepared.BeforeHash) != 64 || len(prepared.AfterHash) != 64 ||
				prepared.BeforeUpdatedAt.IsZero() || prepared.BeforeRevision < 0 || !prepared.WrittenAt.After(prepared.BeforeUpdatedAt) {
				return ErrKnowledgeCorrupt
			}
			if _, err := hex.DecodeString(prepared.BeforeHash); err != nil {
				return ErrKnowledgeCorrupt
			}
			if _, err := hex.DecodeString(prepared.AfterHash); err != nil {
				return ErrKnowledgeCorrupt
			}
		}
		seenRows := make(map[string]bool, len(doc.Interview.RowReceipts))
		for _, row := range doc.Interview.RowReceipts {
			key := row.RequestID + "/" + row.RowID
			if row.RequestID == "" || len(row.RequestID) > 128 || row.RowID == "" || len(row.RowID) > 80 ||
				len(row.TextHash) != 64 || row.ProfileRevision < 0 || row.Status != "saved" || seenRows[key] || row.CanonicalRef == "" || len(row.CanonicalRef) > 200 || row.SavedAt.IsZero() {
				return ErrKnowledgeCorrupt
			}
			seenRows[key] = true
			if doc.Interview.ProfileWrite != nil && row.RowID == doc.Interview.ProfileWrite.RowID && row.RequestID == doc.Interview.ProfileWrite.RequestID {
				return ErrKnowledgeCorrupt
			}
		}
	}
	seen := make(map[string]bool, len(doc.Items))
	for _, item := range doc.Items {
		if len(item.ID) == 0 || len(item.ID) > 200 || seen[item.ID] || item.Version < 1 ||
			len(item.SemanticKey) == 0 || len(item.SemanticKey) > 200 || len(item.Category) == 0 || len(item.Category) > 64 ||
			len(item.Aliases) > knowledgeMaxEvidence || len(item.Revisions) > knowledgeMaxRevisions {
			return ErrKnowledgeCorrupt
		}
		seen[item.ID] = true
		for _, alias := range item.Aliases {
			if alias == "" || len(alias) > 200 {
				return ErrKnowledgeCorrupt
			}
		}
		if len(item.SourceKind) > 64 || len(item.CurrentRevisionID) > 200 {
			return ErrKnowledgeCorrupt
		}
		switch item.Category {
		case "how_you_work", "people", "projects", "routines", "sources":
		default:
			return ErrKnowledgeCorrupt
		}
		switch item.State {
		case KnowledgeCandidate, KnowledgeApproved, KnowledgeNeedsReview, KnowledgeRejected, KnowledgeForgotten:
		default:
			return ErrKnowledgeCorrupt
		}
		if item.State == KnowledgeForgotten && len(item.Revisions) > 0 {
			return ErrKnowledgeCorrupt // forgotten plaintext must not survive
		}
		if item.State == KnowledgeApproved && (item.Target == nil || item.CurrentRevisionID == "") {
			return ErrKnowledgeCorrupt // pending writes are excluded by the context reader
		}
		if item.Target != nil {
			if item.Target.CanonicalHash == "" || len(item.Target.CanonicalHash) > 128 || len(item.Target.Version) > 128 {
				return ErrKnowledgeCorrupt
			}
			switch item.Target.Kind {
			case "memory":
				if item.Target.Marker == "" || len(item.Target.Marker) > 200 || item.Target.Field != "" {
					return ErrKnowledgeCorrupt
				}
			case "profile":
				if item.Target.Marker != "" || !allowedKnowledgeProfileField(item.Target.Field) {
					return ErrKnowledgeCorrupt
				}
			default:
				return ErrKnowledgeCorrupt
			}
		}
		var hasCurrent bool
		for _, revision := range item.Revisions {
			if revision.ID == item.CurrentRevisionID && revision.Text != "" {
				hasCurrent = true
			}
			if revision.ID == "" || len(revision.ID) > 200 || len(revision.Evidence) > knowledgeMaxEvidence {
				return ErrKnowledgeCorrupt
			}
			if revision.Text != "" {
				clean, err := workspace.ValidateMemoryText(revision.Text)
				if err != nil || clean != revision.Text {
					return ErrKnowledgeCorrupt
				}
			}
			for _, evidence := range revision.Evidence {
				if evidence.SourceID == "" || len(evidence.SourceID) > 200 || len(evidence.SourceKind) > 64 || len(evidence.Summary) > 500 || strings.ContainsAny(evidence.Summary, "\r\n") {
					return ErrKnowledgeCorrupt
				}
				if evidence.Summary != "" {
					if clean, err := workspace.ValidateMemoryText(evidence.Summary); err != nil || clean != evidence.Summary {
						return ErrKnowledgeCorrupt
					}
				}
			}
		}
		if item.State == KnowledgeApproved && !hasCurrent {
			return ErrKnowledgeCorrupt
		}
		if item.Prepared != nil && (item.Prepared.RequestID == "" || len(item.Prepared.RequestID) > 200 || item.Prepared.Kind == "" || len(item.Prepared.Kind) > 64 || len(item.Prepared.OldHash) > 128 || len(item.Prepared.NewHash) > 128) {
			return ErrKnowledgeCorrupt
		}
	}
	for _, tombstone := range doc.Tombstones {
		if tombstone.SemanticKey == "" || len(tombstone.SemanticKey) > 200 {
			return ErrKnowledgeCorrupt
		}
	}
	for _, receipt := range doc.Receipts {
		if receipt.RequestID == "" || len(receipt.RequestID) > 200 || receipt.ItemID == "" || len(receipt.ItemID) > 200 || receipt.Result == "" || len(receipt.Result) > 64 || len(receipt.RevisionID) > 200 {
			return ErrKnowledgeCorrupt
		}
	}
	return nil
}

func allowedKnowledgeProfileField(field string) bool {
	switch field {
	case "display_name", "email", "timezone", "locale", "role_category", "about",
		"preferences.response_style", "preferences.units", "preferences.language":
		return true
	default:
		return false
	}
}

func cloneKnowledge(doc KnowledgeDocument) KnowledgeDocument {
	// The document is bounded and validated at the persistence boundary. A JSON
	// copy keeps pointer fields, slices and nested evidence from aliasing the
	// state returned to a caller (the sidecar is always re-read on mutation).
	data, _ := json.Marshal(doc)
	var copyDoc KnowledgeDocument
	_ = json.Unmarshal(data, &copyDoc)
	copyDoc.Present = doc.Present
	return copyDoc
}
