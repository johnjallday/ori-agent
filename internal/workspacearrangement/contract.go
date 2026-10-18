// Package workspacearrangement defines inert, source-neutral arrangement intent
// and a bounded metadata-only planning loop. It has no filesystem, creator,
// HTTP or approval authority; providers and canonical completion are host-bound.
package workspacearrangement

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	Version              = 1
	MaxSourceItems       = 5000
	MaxDraftBytes        = 32 << 10
	MaxToolResultBytes   = 16 << 10
	MaxTurnEvidenceBytes = 64 << 10
	MaxRequestBytes      = 96 << 10
	MaxModelCalls        = 8
	MaxToolCalls         = 12
	MaxOutputTokens      = 2048
	MaxTurnTokens        = 24000
	TurnTimeout          = 90 * time.Second
	SelectionTTL         = 10 * time.Minute
	ReviewTTL            = 10 * time.Minute
	MaxPageSize          = 50
	ModelPageSize        = 20
	MaxExplicitItems     = 100
	MaxOperations        = 32
	MaxGroupMembers      = 24 // Existing creator limit, not a catalog/display cap.
	MaxActivationItems   = 100
	MaxMemberships       = 20000
)

type SourceKind string

const (
	Attachment  SourceKind = "attachment"
	HomeLibrary SourceKind = "home_library"
)

const (
	CreateCollection = "create_collection"
	RenameCollection = "rename_collection"
	SetMembers       = "set_members"
	RemoveCollection = "remove_collection"
	CreateGroup      = "create_group"
	SelectActivation = "select_activation"
)

var ErrInvalid = errors.New("invalid or stale arrangement intent")

// Selection names exact issued items OR one frozen host-issued selection, never
// a query, future-membership rule, filesystem path or capability. An empty
// selection is useful for an empty logical collection, not a group/activation.
type Selection struct {
	IDs    []string `json:"ids,omitempty"`
	Handle string   `json:"handle,omitempty"`
}

type Operation struct {
	Kind               string    `json:"kind"`
	Name               string    `json:"name,omitempty"`
	CollectionID       string    `json:"collection_id,omitempty"`
	CollectionRevision int64     `json:"collection_revision,omitempty"`
	Members            Selection `json:"members"`
}

type Recommendation struct {
	Items  Selection `json:"items"`
	Reason string    `json:"reason"`
}

// Draft is model/user intent only. No confirmation token, path, provider choice,
// executable plan, parent group, receipt or claimed completion is representable.
// Unmentioned items are unchanged. Overlap between logical collections is legal.
type Draft struct {
	Version        int              `json:"version"`
	SourceRevision string           `json:"source_revision"`
	Goal           string           `json:"goal"`
	Rationale      string           `json:"rationale"`
	Unknowns       []string         `json:"unknowns"`
	Unchanged      Selection        `json:"unchanged"`
	NeedsReview    []Recommendation `json:"needs_review"`
	Operations     []Operation      `json:"operations"`
}

// ValidationView is host-owned, scoped to one reauthorized source and revision.
// Items/Handles contain only references issued by that turn; adapters must check
// owner, instance, source identity, provider/skill pins and handle expiry first.
// Validation by itself never approves a draft or checks live capabilities.
type ValidationView struct {
	Kind        SourceKind
	Revision    string
	Items       map[string]bool
	Handles     map[string][]string
	Collections map[string]int64
}

func (v ValidationView) members(s Selection) ([]string, error) {
	ids := s.IDs
	if len(ids) > MaxExplicitItems || (s.Handle != "" && len(ids) != 0) {
		return nil, ErrInvalid
	}
	if s.Handle != "" {
		if !label(s.Handle, 160) {
			return nil, ErrInvalid
		}
		var ok bool
		ids, ok = v.Handles[s.Handle]
		if !ok || len(ids) == 0 {
			return nil, ErrInvalid
		}
	}
	if len(ids) > MaxSourceItems {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !v.Items[id] || seen[id] || !label(id, 160) {
			return nil, ErrInvalid
		}
		seen[id] = true
	}
	return ids, nil
}

// Validate refuses the whole draft rather than dropping an oversized tail.
// Hosts compile allowed effects only after this pure structural validation.
func (d Draft) Validate(v ValidationView) error {
	if d.Version != Version || !label(v.Revision, 160) || d.SourceRevision != v.Revision ||
		(v.Kind != Attachment && v.Kind != HomeLibrary) || len(v.Items) > MaxSourceItems ||
		!ValidGoal(d.Goal) || !text(d.Rationale, 2000) ||
		len(d.Unknowns) > 16 || len(d.NeedsReview) > 32 || len(d.Operations) > MaxOperations {
		return ErrInvalid
	}
	encoded, err := json.Marshal(d)
	if err != nil || len(encoded) > MaxDraftBytes {
		return ErrInvalid
	}
	for _, unknown := range d.Unknowns {
		if unknown == "" || !text(unknown, 500) {
			return ErrInvalid
		}
	}
	unchanged, err := v.members(d.Unchanged)
	if err != nil {
		return err
	}
	blocked := map[string]bool{}
	for _, id := range unchanged {
		blocked[id] = true
	}
	total := len(unchanged)
	for _, recommendation := range d.NeedsReview {
		ids, err := v.members(recommendation.Items)
		if err != nil || recommendation.Reason == "" || !text(recommendation.Reason, 500) {
			return ErrInvalid
		}
		total += len(ids)
		if total > MaxMemberships {
			return ErrInvalid
		}
		for _, id := range ids {
			blocked[id] = true
		}
	}
	collections, workspaces := map[string]bool{}, map[string]bool{}
	for _, op := range d.Operations {
		members, err := v.members(op.Members)
		if err != nil {
			return err
		}
		total += len(members)
		if total > MaxMemberships {
			return ErrInvalid
		}
		for _, id := range members {
			if blocked[id] {
				return ErrInvalid
			}
		}
		existing := op.Kind == RenameCollection || op.Kind == SetMembers || op.Kind == RemoveCollection
		if existing {
			if !label(op.CollectionID, 160) || op.CollectionRevision < 1 || v.Collections[op.CollectionID] != op.CollectionRevision || collections[op.CollectionID] {
				return ErrInvalid
			}
			collections[op.CollectionID] = true
		} else if op.CollectionID != "" || op.CollectionRevision != 0 {
			return ErrInvalid
		}
		named := op.Kind == CreateCollection || op.Kind == RenameCollection || op.Kind == CreateGroup
		if (named && !label(op.Name, 128)) || (!named && op.Name != "") {
			return ErrInvalid
		}
		switch op.Kind {
		case CreateCollection, RenameCollection, SetMembers, RemoveCollection:
			if v.Kind != HomeLibrary || ((op.Kind == RenameCollection || op.Kind == RemoveCollection) && (len(members) != 0 || op.Members.Handle != "")) {
				return ErrInvalid
			}
		case CreateGroup, SelectActivation:
			if len(members) == 0 || (op.Kind == CreateGroup && (v.Kind != Attachment || len(members) > MaxGroupMembers)) ||
				(op.Kind == SelectActivation && (v.Kind != HomeLibrary || len(members) > MaxActivationItems)) {
				return ErrInvalid
			}
			for _, id := range members {
				if workspaces[id] {
					return ErrInvalid
				}
				workspaces[id] = true
			}
		default:
			return ErrInvalid
		}
	}
	return nil
}

// ValidGoal applies the same bounded, single-line goal contract at entry and
// proposal validation, before callers send any metadata to a model.
func ValidGoal(goal string) bool { return goal != "" && text(goal, 2000) }

func text(s string, max int) bool {
	if len(s) > max || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

func label(s string, max int) bool {
	return s != "" && s != "." && s != ".." && !strings.HasPrefix(s, "~") &&
		!strings.ContainsAny(s, `/\:`) && text(s, max)
}

// DecodeDraft rejects unknown/duplicate fields (including case aliases), null
// documents, oversized, too-deep and trailing JSON. Expressions are never evaluated.
func DecodeDraft(raw string, v ValidationView) (Draft, error) {
	if len(raw) > MaxDraftBytes || !utf8.ValidString(raw) || !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return Draft{}, ErrInvalid
	}
	probe := json.NewDecoder(strings.NewReader(raw))
	if err := uniqueJSON(probe, 0); err != nil {
		return Draft{}, ErrInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var d Draft
	if decoder.Decode(&d) != nil || decoder.Decode(new(any)) != io.EOF || d.Validate(v) != nil {
		return Draft{}, ErrInvalid
	}
	return d, nil
}

func uniqueJSON(decoder *json.Decoder, depth int) error {
	return uniqueJSONKeys(decoder, depth, false, false)
}

// Stored records additionally contain maps whose keys are case-sensitive opaque
// IDs rather than schema fields. Wire intent does not enable that exception.
func uniqueJSONKeys(decoder *json.Decoder, depth int, storedRecord, mapKeys bool) error {
	if depth > 16 {
		return ErrInvalid
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	seen := map[string]bool{}
	for decoder.More() {
		childMap := false
		if delim == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || (!mapKeys && name != strings.ToLower(name)) || seen[name] {
				return ErrInvalid
			}
			seen[name] = true
			childMap = storedRecord && (name == "item_revisions" || name == "collection_revisions")
		}
		if err := uniqueJSONKeys(decoder, depth+1, storedRecord, childMap); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
