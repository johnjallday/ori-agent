package vaultref

import "strings"

// Reference records that a workspace file or note was copied from a private vault
// record, and keeps enough source identity to sync changes back later.
type Reference struct {
	SourceKind     string `json:"source_kind,omitempty"`
	VaultID        string `json:"vault_id,omitempty"`
	VaultName      string `json:"vault_name,omitempty"`
	RecordID       string `json:"record_id,omitempty"`
	RecordLabel    string `json:"record_label,omitempty"`
	RecordType     string `json:"record_type,omitempty"`
	AttachmentID   string `json:"attachment_id,omitempty"`
	AttachmentName string `json:"attachment_name,omitempty"`
	PayloadKey     string `json:"payload_key,omitempty"`
	ImportedAt     string `json:"imported_at,omitempty"`
	LastSyncedAt   string `json:"last_synced_at,omitempty"`
}

// Normalize trims optional display fields and returns nil when the reference
// does not identify a vault record.
func Normalize(ref *Reference) *Reference {
	if ref == nil {
		return nil
	}
	clean := *ref
	clean.SourceKind = strings.TrimSpace(clean.SourceKind)
	clean.VaultID = strings.TrimSpace(clean.VaultID)
	clean.VaultName = strings.TrimSpace(clean.VaultName)
	clean.RecordID = strings.TrimSpace(clean.RecordID)
	clean.RecordLabel = strings.TrimSpace(clean.RecordLabel)
	clean.RecordType = strings.TrimSpace(clean.RecordType)
	clean.AttachmentID = strings.TrimSpace(clean.AttachmentID)
	clean.AttachmentName = strings.TrimSpace(clean.AttachmentName)
	clean.PayloadKey = strings.TrimSpace(clean.PayloadKey)
	clean.ImportedAt = strings.TrimSpace(clean.ImportedAt)
	clean.LastSyncedAt = strings.TrimSpace(clean.LastSyncedAt)
	if clean.RecordID == "" {
		return nil
	}
	return &clean
}
