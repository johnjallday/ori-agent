package personalassistant

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// FolderSetupDestination is server-resolved disclosure plus a material identity
// witness. It is not accepted from a browser or model. No source path or grant
// is carried here. The record survives with a confirmed run for resume checks.
type FolderSetupDestination struct {
	Status            string `json:"status"` // existing, new, standalone
	WorkspaceID       string `json:"workspace_id,omitempty"`
	Name              string `json:"name,omitempty"`
	Kind              string `json:"kind,omitempty"`
	ParentID          string `json:"parent_id,omitempty"`
	OwnerUserID       string `json:"owner_user_id,omitempty"`
	RecordVersion     int64  `json:"record_version,omitempty"`
	ProgramRevision   int64  `json:"program_revision,omitempty"`
	ProviderPlugin    string `json:"provider_plugin,omitempty"`
	ProviderVersion   string `json:"provider_version,omitempty"`
	ProgramID         string `json:"program_id,omitempty"`
	DeclarationDigest string `json:"declaration_digest,omitempty"`
	CompatibilityHash string `json:"compatibility_hash,omitempty"`
}

func (d FolderSetupDestination) Validate() error {
	switch d.Status {
	case "existing":
		if d.WorkspaceID == "" || d.Name == "" || d.OwnerUserID == "" {
			return fmt.Errorf("%w: destination identity", errFolderDigestInvalid)
		}
	case "new":
		if d.WorkspaceID != "" || d.Name == "" {
			return fmt.Errorf("%w: new destination", errFolderDigestInvalid)
		}
	case "standalone":
		if d.WorkspaceID != "" {
			return fmt.Errorf("%w: standalone destination", errFolderDigestInvalid)
		}
	default:
		return fmt.Errorf("%w: destination status", errFolderDigestInvalid)
	}
	for _, value := range []string{d.WorkspaceID, d.ParentID, d.OwnerUserID, d.ProviderPlugin,
		d.ProviderVersion, d.ProgramID, d.DeclarationDigest, d.CompatibilityHash} {
		if len(value) > 200 || strings.ContainsAny(value, "/\\\x00\r\n") {
			return fmt.Errorf("%w: destination reference", errFolderDigestInvalid)
		}
	}
	if len(d.Name) > 512 || strings.ContainsAny(d.Name, "\x00\r\n") || looksLikeFilesystemPath(d.Name) ||
		d.RecordVersion < 0 || d.ProgramRevision < 0 {
		return fmt.Errorf("%w: destination disclosure", errFolderDigestInvalid)
	}
	return nil
}

// DestinationPlanDigest preserves legacy line-only identities when no witness
// exists. A witness binds IDs and versions even if two Homes share a name.
func DestinationPlanDigest(lines []FolderPlanLine, destination *FolderSetupDestination) string {
	lineDigest := FolderPlanDigest(lines)
	if destination == nil {
		return lineDigest
	}
	if destination.Validate() != nil {
		return ""
	}
	data, err := json.Marshal(struct {
		Lines       string                 `json:"lines"`
		Destination FolderSetupDestination `json:"destination"`
	}{lineDigest, *destination})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
