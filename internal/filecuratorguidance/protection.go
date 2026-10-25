package filecuratorguidance

import (
	"context"
	"fmt"
	"time"
)

// ProtectionReader projects stored facts only. It cannot grant access or run a
// provider. Shared by the authorized review endpoint and the role-scoped prompt.
type ProtectionReader interface {
	ReadProtection(context.Context, string, string) (ProtectionFact, error)
}

// ProtectionDestination is host-selected navigation, not an arbitrary URL.
// WorkspaceID identifies an owned, in-scope Backup & Recovery workspace.
type ProtectionDestination struct {
	Kind        string `json:"kind"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Slug        string `json:"slug,omitempty"`
}

// ProtectionFact deliberately contains no path, provider scope or credential.
// BindingID is an opaque owner/workspace/root-generation key for local dismissal.
// AsOf is projection time; ObservedAt is the stored successful provider read.
type ProtectionFact struct {
	WorkspaceID     string                 `json:"workspace_id"`
	WorkspaceSlug   string                 `json:"workspace_slug,omitempty"`
	RootGeneration  string                 `json:"root_generation,omitempty"`
	BindingID       string                 `json:"binding_id,omitempty"`
	State           string                 `json:"state"`
	Reason          string                 `json:"reason"`
	Provider        string                 `json:"provider,omitempty"`
	AsOf            time.Time              `json:"as_of"`
	ObservedAt      *time.Time             `json:"observed_at,omitempty"`
	LastCompletedAt *time.Time             `json:"last_completed_at,omitempty"`
	Destination     *ProtectionDestination `json:"destination,omitempty"`
}

// Prompt includes only fixed host wording and timestamps, never navigation or
// user-editable names. Unavailable roots receive generic guidance, not facts.
func (f ProtectionFact) Prompt() string {
	if f.State == "unavailable" || f.RootGeneration == "" {
		return ""
	}
	var sentence string
	switch f.State {
	case "covered":
		sentence = "Stored evidence reports coverage of the approved File Janitor folder."
	case "configured":
		sentence = "A covering backup source is configured, but no completed backup is observed. Configuration is not a backup."
	case "not_covered":
		sentence = "The approved File Janitor folder is not covered by the backup evidence Ori observes. This does not prove no backup exists elsewhere."
	default:
		sentence = "Protection of the approved File Janitor folder is unknown; stored evidence is unavailable or insufficient."
	}
	text := fmt.Sprintf("\n\n## File Janitor managed-folder protection\nAs of %s (stored metadata only, not a new check): %s", f.AsOf.UTC().Format(time.RFC3339), sentence)
	if f.ObservedAt != nil {
		text += " Evidence observed at " + f.ObservedAt.UTC().Format(time.RFC3339) + "."
	}
	if f.LastCompletedAt != nil {
		text += " Last completed backup at " + f.LastCompletedAt.UTC().Format(time.RFC3339) + "."
	}
	return text + " Check protection is optional navigation to Backup & Recovery, not permission to read a provider or execute a backup."
}
