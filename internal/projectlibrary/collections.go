package projectlibrary

import (
	"strings"
	"time"
	"unicode"
)

// LogicalCollection is a Home-owned reference set, not a folder, grant, project
// link or workspace parent. Reviewed mutation services are separate from this
// storage foundation. There is deliberately no arbitrary collection writer.
type LogicalCollection struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Revision      int64     `json:"revision"`
	EntryIDs      []string  `json:"entry_ids"`
	Author        string    `json:"author"`
	Origin        string    `json:"origin"`
	ArrangementID string    `json:"arrangement_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func collectionLabel(value string, maximum int) bool {
	if value == "" || value == "." || value == ".." || !validText(value, maximum) || strings.TrimSpace(value) != value || strings.HasPrefix(value, "~") || strings.ContainsAny(value, `/\:`) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

func (c LogicalCollection) valid(entries map[string]bool) bool {
	if !collectionLabel(c.ID, 160) || !collectionLabel(c.Name, 128) ||
		c.Revision < 1 || c.Author == "" || !validText(c.Author, 160) || (c.ArrangementID != "" && !collectionLabel(c.ArrangementID, 160)) ||
		(c.Origin != "model" && c.Origin != "manual" && c.Origin != "rule_based") || c.CreatedAt.IsZero() || c.UpdatedAt.Before(c.CreatedAt) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range c.EntryIDs {
		if !entries[id] || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
