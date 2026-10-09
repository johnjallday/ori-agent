// Package foldercontext defines path-free, metadata-only conversation evidence.
// It deliberately contains no filesystem handles or permissions.
package foldercontext

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	Version      = 1
	MaxBytes     = 8192
	MaxNames     = 8
	MaxNameRunes = 96
	MaxEntries   = 5000
	SelectionTTL = 30 * time.Minute
)

var ErrInvalid = errors.New("folder context is invalid")

// Target is supplied by the host after checking canonical conversation ownership.
// DraftID is used only until an answered turn or explicit setup review is saved.
type Target struct {
	UserID         string `json:"user_id"`
	WorkspaceID    string `json:"workspace_id"`
	AgentName      string `json:"agent_name"`
	ConversationID string `json:"conversation_id,omitempty"`
	DraftID        string `json:"draft_id,omitempty"`
}

func (t Target) Valid() bool {
	return t.UserID != "" && t.WorkspaceID != "" && t.AgentName != "" &&
		((t.ConversationID != "" && t.DraftID == "") || (t.ConversationID == "" && t.DraftID != ""))
}

type Kind struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type Project struct {
	ID     string `json:"id"` // opaque within this snapshot; not a pathname
	Name   string `json:"name"`
	Files  int    `json:"files"`
	Marker string `json:"marker,omitempty"` // known marker label, not arbitrary filename
	Root   bool   `json:"root,omitempty"`
}

type Coverage struct {
	MaxDepth        int    `json:"max_depth"`
	MaxEntries      int    `json:"max_entries"`
	BudgetSeconds   int    `json:"budget_seconds"`
	Partial         bool   `json:"partial"`
	PartialReason   string `json:"partial_reason,omitempty"`
	SkippedLinks    int    `json:"skipped_links"`
	KindsOmitted    int    `json:"kinds_omitted"`
	ProjectsOmitted int    `json:"projects_omitted"`
}

// Observation is a dated summary, not an exhaustive inventory or authority to
// inspect again. ContentsRead is intentionally not a field: contents are never read.
type Observation struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Folder    string    `json:"folder"`
	ScannedAt time.Time `json:"scanned_at"`
	Entries   int       `json:"entries"`
	Files     int       `json:"files"`
	Kinds     []Kind    `json:"kinds"`
	Projects  []Project `json:"projects"`
	Coverage  Coverage  `json:"coverage"`
	Tree      *Tree     `json:"tree,omitempty"`
}

// Event is stored only through the internal canonical-message writer. A nil
// Observation detaches context without erasing earlier messages. OfferID is a
// canonical reviewed setup reference, never approval or an executable action.
type Event struct {
	Version     int          `json:"version"`
	Observation *Observation `json:"observation,omitempty"`
	OfferID     string       `json:"offer_id,omitempty"`
	FocusIDs    []string     `json:"focus_ids,omitempty"` // immutable sent-turn topics, not authority
}

// DisplayName bounds metadata and strips controls/separators. Escaping for the
// prompt/DOM remains the responsibility of the consumer; names are always data.
func DisplayName(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, value)
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > MaxNameRunes {
		runes = runes[:MaxNameRunes]
	}
	return string(runes)
}

func count(n int) bool { return n >= 0 && n <= MaxEntries }
func name(s string) bool {
	return s != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= MaxNameRunes && DisplayName(s) == s
}

func (o Observation) Validate() error {
	if o.Version != Version || !name(o.ID) || !name(o.Folder) || o.ScannedAt.IsZero() ||
		!count(o.Entries) || !count(o.Files) || len(o.Kinds) > MaxNames || len(o.Projects) > MaxNames ||
		o.Coverage.MaxDepth != 3 || o.Coverage.MaxEntries != MaxEntries || o.Coverage.BudgetSeconds != 3 ||
		!count(o.Coverage.SkippedLinks) || !count(o.Coverage.KindsOmitted) || !count(o.Coverage.ProjectsOmitted) {
		return ErrInvalid
	}
	if o.Coverage.PartialReason != "" && o.Coverage.PartialReason != "entries" && o.Coverage.PartialReason != "time" {
		return ErrInvalid
	}
	for _, kind := range o.Kinds {
		if !name(kind.Name) || !count(kind.Count) {
			return ErrInvalid
		}
	}
	for _, project := range o.Projects {
		if !name(project.ID) || !name(project.Name) || !count(project.Files) || (project.Marker != "" && !name(project.Marker)) {
			return ErrInvalid
		}
	}
	if o.Tree != nil && (o.Tree.Validate() != nil || len(o.Tree.Nodes) > o.Entries) {
		return ErrInvalid
	}
	data, err := json.Marshal(o)
	if err != nil || len(data) > MaxBytes {
		return ErrInvalid
	}
	return nil
}

func (e Event) Validate() error {
	if e.Version != Version || len(e.OfferID) > 96 {
		return ErrInvalid
	}
	if e.Observation != nil {
		if e.Observation.Validate() != nil {
			return ErrInvalid
		}
		if _, err := e.Observation.ResolveFocus(e.FocusIDs); err != nil {
			return err
		}
		data, err := json.Marshal(e)
		if err != nil || len(data) > MaxBytes+256 {
			return ErrInvalid
		}
		return nil
	}
	if e.OfferID != "" || len(e.FocusIDs) != 0 {
		return ErrInvalid
	}
	return nil
}
