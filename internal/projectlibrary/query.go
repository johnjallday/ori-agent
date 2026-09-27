package projectlibrary

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Search is a bounded metadata-only query. The caller authenticates the
// current user before supplying the Home scope; no path is accepted or emitted.
type Search struct {
	Text         string `json:"text,omitempty"`
	Stage        string `json:"stage,omitempty"`
	Status       string `json:"status,omitempty"`
	Format       string `json:"format,omitempty"`
	Connection   string `json:"connection,omitempty"`
	Availability string `json:"availability,omitempty"`
	RootID       string `json:"root_id,omitempty"`
	Priority     *int   `json:"priority,omitempty"`
	Sort         string `json:"sort,omitempty"`
	Direction    string `json:"direction,omitempty"`
	PageSize     int    `json:"page_size,omitempty"`
	Cursor       string `json:"cursor,omitempty"`
}

type SearchRow struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Stage          string    `json:"stage"`
	Status         string    `json:"status"`
	NextAction     string    `json:"next_action,omitempty"`
	Priority       *int      `json:"priority,omitempty"`
	Format         string    `json:"format,omitempty"`
	Connection     string    `json:"connection"`
	Availability   string    `json:"last_observed_availability"`
	Alternatives   int       `json:"alternatives"`
	FieldsRevision int64     `json:"fields_revision"`
	ActivityAt     time.Time `json:"sourced_activity_at,omitempty"`
	ActivitySource string    `json:"activity_source,omitempty"`
	LastScannedAt  time.Time `json:"last_scanned_at,omitempty"`
	NeedsChoice    bool      `json:"needs_choice,omitempty"`
}

type SearchPage struct {
	Rows             []SearchRow `json:"rows"`
	Total            int         `json:"total"`
	Revision         int64       `json:"revision"`
	NextCursor       string      `json:"next_cursor,omitempty"`
	ProviderReadOnly bool        `json:"provider_read_only"`
}

// Detail deliberately excludes absolute grant paths, child workspace state,
// private contents and unbounded source history. Relative names are inert
// evidence for an owner to review, never a resolver input or permission.
// Detail has its own byte budget: a valid entry can have 64 sources with 64
// project-file alternatives each. Listing all of them would dwarf a search
// page and expose an unbounded amount of historical source metadata.
const (
	maxDetailSources    = 5
	maxDetailAlternates = 16
	maxDetailBytes      = 120 << 10 // Leave room for the HTTP success envelope.
)

type Detail struct {
	Row              SearchRow      `json:"row"`
	EntryRevision    int64          `json:"entry_revision"`
	Fields           Fields         `json:"fields"`
	Sources          []DetailSource `json:"sources,omitempty"`
	TotalSources     int            `json:"total_sources"`
	Revision         int64          `json:"revision"`
	ProviderReadOnly bool           `json:"provider_read_only"`
}

type DetailSource struct {
	RootID          string    `json:"root_id"`
	RelativeFolder  string    `json:"relative_folder"`
	Format          string    `json:"format"`
	Alternates      []string  `json:"alternates,omitempty"`
	TotalAlternates int       `json:"total_alternates"`
	Availability    string    `json:"last_observed_availability"`
	ScannedAt       time.Time `json:"scanned_at"`
	FileModifiedAt  time.Time `json:"observed_file_modified_at,omitempty"`
}

func (s *Store) Detail(scope Scope, entryID string) (Detail, error) {
	if entryID == "" || !validText(entryID, 160) {
		return Detail{}, ErrConflict
	}
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return Detail{}, err
	}
	for _, entry := range doc.Entries {
		if entry.ID != entryID {
			continue
		}
		linked := map[string]bool{}
		for _, id := range state.LinkedProjectIDs {
			linked[id] = true
		}
		roots := make(map[string]Root, len(doc.Roots))
		for _, root := range doc.Roots {
			roots[root.ID] = root
		}
		inactive := make(map[string]bool, len(state.ProjectLibraryInactiveRoots))
		for _, id := range state.ProjectLibraryInactiveRoots {
			inactive[id] = true
		}
		row := s.projectSearchRow(scope, entry, roots, linked, inactive)
		row.applySessionActivity(latestSessionActivity(doc.Sessions)[entry.ID])
		result := Detail{Row: row,
			EntryRevision: entry.Revision, Fields: entry.Fields, Revision: doc.Revision, ProviderReadOnly: !state.PluginAvailable}
		result.TotalSources = len(entry.Observations)
		for _, observed := range entry.Observations {
			availability := observed.Availability
			if roots[observed.RootID].RevokedAt != nil || inactive[observed.RootID] {
				availability = "revoked_source"
			}
			alternates := observed.Alternates
			if len(alternates) > maxDetailAlternates {
				alternates = alternates[:maxDetailAlternates]
			}
			result.Sources = append(result.Sources, DetailSource{RootID: observed.RootID,
				RelativeFolder: observed.RelativeFolder, Format: observed.Format,
				Alternates: append([]string(nil), alternates...), TotalAlternates: len(observed.Alternates),
				Availability: availability, ScannedAt: observed.ScannedAt,
				FileModifiedAt: observed.FileModifiedAt})
		}
		// Surface currently usable sources before revoked history. The rest
		// is still represented by TotalSources, not silently declared absent.
		sort.Slice(result.Sources, func(i, j int) bool {
			a, b := result.Sources[i], result.Sources[j]
			if a.Availability == "revoked_source" && b.Availability != "revoked_source" {
				return false
			}
			if a.Availability != "revoked_source" && b.Availability == "revoked_source" {
				return true
			}
			if !a.ScannedAt.Equal(b.ScannedAt) {
				return a.ScannedAt.After(b.ScannedAt)
			}
			return a.RootID < b.RootID
		})
		if len(result.Sources) > maxDetailSources {
			result.Sources = result.Sources[:maxDetailSources]
		}
		// JSON escapes can expand even a validated 240-byte filename to six
		// times its stored size. Bound the actual wire representation, not a
		// guessed sum of unescaped lengths. Keep honest original counts.
		for {
			encoded, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				return Detail{}, ErrCorrupt
			}
			if len(encoded) <= maxDetailBytes {
				break
			}
			pruned := false
			for i := len(result.Sources) - 1; i >= 0; i-- {
				if n := len(result.Sources[i].Alternates); n > 0 {
					result.Sources[i].Alternates = result.Sources[i].Alternates[:n-1]
					pruned = true
					break
				}
			}
			if !pruned && len(result.Sources) > 0 {
				result.Sources = result.Sources[:len(result.Sources)-1]
				pruned = true
			}
			if !pruned {
				return Detail{}, ErrLimit
			}
		}
		return result, nil
	}
	return Detail{}, ErrConflict
}

type pageCursor struct {
	Revision int64  `json:"revision"`
	Query    string `json:"query"`
	LastID   string `json:"last_id"`
	LastKey  string `json:"last_key"`
}

func (q *Search) normalize(doc Document) error {
	if q == nil || !validText(q.Text, 120) || (q.Priority != nil && (*q.Priority < 0 || *q.Priority > 5)) {
		return ErrConflict
	}
	q.Text = strings.TrimSpace(q.Text)
	if q.PageSize == 0 {
		q.PageSize = 25
	}
	if q.PageSize < 1 || q.PageSize > 50 {
		return ErrLimit
	}
	if q.Sort == "" {
		q.Sort = "name"
	}
	if q.Direction == "" {
		q.Direction = "asc"
	}
	if q.Direction != "asc" && q.Direction != "desc" {
		return ErrConflict
	}
	switch q.Sort {
	case "name", "status", "priority", "next_action", "sourced_activity", "scanned_at":
	default:
		return ErrConflict
	}
	switch q.Stage {
	case "", "unknown", "idea", "writing", "recording", "editing", "mixing", "mastering":
	default:
		return ErrConflict
	}
	switch q.Status {
	case "", "unknown", "planning", "active", "on_hold", "complete", "archived":
	default:
		return ErrConflict
	}
	if q.Format != "" && !folderdigest.KnownProjectFormat(q.Format) {
		return ErrConflict
	}
	switch q.Connection {
	case "", "connected", "catalog_only", "needs_review":
	default:
		return ErrConflict
	}
	switch q.Availability {
	case "", "available", "ambiguous", "unavailable", "revoked_source", "not_scanned":
	default:
		return ErrConflict
	}
	if q.RootID != "" {
		for _, root := range doc.Roots {
			if root.ID == q.RootID {
				return nil
			}
		}
		return ErrConflict
	}
	return nil
}

func queryDigest(q Search) string {
	q.Cursor = ""
	encoded, _ := json.Marshal(q) // Pure typed value with no custom marshalers.
	value := sha256.Sum256(encoded)
	return hex.EncodeToString(value[:])
}

func decodeCursor(encoded string, revision int64, digest string) (pageCursor, error) {
	if len(encoded) > 2048 {
		return pageCursor{}, ErrConflict
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(payload) > 1024 {
		return pageCursor{}, ErrConflict
	}
	var cursor pageCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.LastID == "" ||
		cursor.Query != digest || cursor.Revision != revision {
		return pageCursor{}, ErrConflict
	}
	return cursor, nil
}

// Query reads and projects at most one page. A revision-bound cursor refuses
// a stale page rather than silently skipping entries after another edit/scan.
// Availability is explicitly a *last observation*, not a fresh OS/media or
// live-DAW readiness assertion; the host must project current root state too.
func (s *Store) Query(scope Scope, query Search) (SearchPage, error) {
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		return SearchPage{}, err
	}
	if err := query.normalize(doc); err != nil {
		return SearchPage{}, err
	}
	digest := queryDigest(query)
	var cursor pageCursor
	if query.Cursor != "" {
		cursor, err = decodeCursor(query.Cursor, doc.Revision, digest)
		if err != nil {
			return SearchPage{}, err
		}
	}
	linked := make(map[string]bool, len(state.LinkedProjectIDs))
	for _, id := range state.LinkedProjectIDs {
		linked[id] = true
	}
	roots := make(map[string]Root, len(doc.Roots))
	for _, root := range doc.Roots {
		roots[root.ID] = root
	}
	inactive := make(map[string]bool, len(state.ProjectLibraryInactiveRoots))
	for _, id := range state.ProjectLibraryInactiveRoots {
		inactive[id] = true
	}
	sessionActivity := latestSessionActivity(doc.Sessions)
	rows := make([]SearchRow, 0, len(doc.Entries))
	for _, entry := range doc.Entries {
		row := s.projectSearchRow(scope, entry, roots, linked, inactive)
		row.applySessionActivity(sessionActivity[entry.ID])
		if query.matches(row, entry) {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return searchLess(rows[i], rows[j], query.Sort, query.Direction) })
	page := SearchPage{Rows: make([]SearchRow, 0, query.PageSize), Total: len(rows),
		Revision: doc.Revision, ProviderReadOnly: !state.PluginAvailable}
	start := 0
	if cursor.LastID != "" {
		start = -1
		for i, row := range rows {
			if row.ID == cursor.LastID && searchKey(row, query.Sort) == cursor.LastKey {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return SearchPage{}, ErrConflict
		}
	}
	end := start + query.PageSize
	if end > len(rows) {
		end = len(rows)
	}
	page.Rows = append(page.Rows, rows[start:end]...)
	if end < len(rows) {
		encoded, err := json.Marshal(pageCursor{Revision: doc.Revision, Query: digest, LastID: rows[end-1].ID,
			LastKey: searchKey(rows[end-1], query.Sort)})
		if err != nil {
			return SearchPage{}, ErrCorrupt
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return page, nil
}

func latestSessionActivity(records []StudioSession) map[string]time.Time {
	latest := make(map[string]time.Time)
	for _, record := range records {
		if record.State == "accepted" && record.UpdatedAt.After(latest[record.EntryID]) {
			latest[record.EntryID] = record.UpdatedAt
		}
	}
	return latest
}

func (row *SearchRow) applySessionActivity(at time.Time) {
	if at.After(row.ActivityAt) {
		row.ActivityAt = at
		row.ActivitySource = "reviewed_studio_session" // User note, never inferred task/DAW progress.
	}
}

func (s *Store) projectSearchRow(scope Scope, entry Entry, roots map[string]Root, linked, inactive map[string]bool) SearchRow {
	row := SearchRow{ID: entry.ID, Name: entry.Fields.DisplayName,
		Stage: entry.Fields.Stage, Status: entry.Fields.Status, NextAction: entry.Fields.NextAction,
		Priority: entry.Fields.Priority, FieldsRevision: entry.Fields.Revision,
		Connection: "catalog_only", Availability: "not_scanned", ActivityAt: entry.Fields.UpdatedAt}
	if !row.ActivityAt.IsZero() {
		row.ActivitySource = entry.Fields.Source
	}
	if row.Stage == "" {
		row.Stage = "unknown"
	}
	if row.Status == "" {
		row.Status = "unknown"
	}
	chosenRank := 4
	var chosenAt time.Time
	for _, observed := range entry.Observations {
		root := roots[observed.RootID]
		rank := 1 // An unavailable active source is better than revoked history.
		if root.RevokedAt != nil || inactive[root.ID] {
			rank = 2
		} else if observed.Availability == "available" || observed.Availability == "ambiguous" {
			rank = 0
		}
		if rank < chosenRank || (rank == chosenRank && observed.ScannedAt.After(chosenAt)) {
			chosenRank, chosenAt = rank, observed.ScannedAt
			row.Format, row.Alternatives = observed.Format, len(observed.Alternates)
			if entry.Fields.DisplayName == "" {
				if observed.RelativeFolder == "" {
					row.Name = filepath.Base(root.Path)
				} else {
					row.Name = filepath.Base(observed.RelativeFolder)
				}
			}
		}
		if observed.ScannedAt.After(row.LastScannedAt) {
			row.LastScannedAt = observed.ScannedAt
		}
		if (root.RevokedAt != nil || inactive[root.ID]) && row.Availability == "not_scanned" {
			row.Availability = "revoked_source"
		} else if root.RevokedAt == nil && !inactive[root.ID] {
			switch observed.Availability {
			case "ambiguous":
				row.Availability = "ambiguous"
			case "available":
				if row.Availability != "ambiguous" {
					row.Availability = "available"
				}
			case "unavailable":
				if row.Availability == "not_scanned" || row.Availability == "revoked_source" {
					row.Availability = "unavailable"
				}
			}
		}
		row.NeedsChoice = row.NeedsChoice || (observed.Availability == "ambiguous" && root.RevokedAt == nil && !inactive[root.ID])
	}
	if entry.Link == nil || !linked[entry.Link.WorkspaceID] ||
		entry.Link.LinkID != workspace.AssistantProjectLinkID(scope.HomeID, entry.Link.WorkspaceID) {
		if entry.Link != nil {
			row.Connection = "needs_review"
		}
		return row
	}
	child, err := s.workspaces.Get(entry.Link.WorkspaceID)
	if err != nil || child == nil || child.Status == workspace.StatusTrashed || child.Status == workspace.StatusMissing ||
		child.OwnerUserID != scope.OwnerUserID {
		row.Connection = "needs_review"
		return row
	}
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	link := child.GetAssistantProjectLink()
	if link == nil || link.ID != entry.Link.LinkID || link.StationWorkspaceID != scope.HomeID ||
		link.Key.Normalize() != key.Normalize() || link.StateRevision != entry.Link.Revision {
		row.Connection = "needs_review"
		return row
	}
	if mirror, ok := s.workspaces.(workspace.MirrorWorkspaceProvider); ok {
		folder, mirrored, folderErr := mirror.GetMirrorWorkspace(child.ID)
		if mirrored && (folderErr != nil || folder == nil || folder.OwnerUserID != child.OwnerUserID) {
			row.Connection = "needs_review"
			return row
		}
		if mirrored {
			folderLink := folder.GetAssistantProjectLink()
			if folderLink == nil || folderLink.ID != link.ID || folderLink.StateRevision != link.StateRevision ||
				folderLink.StationWorkspaceID != link.StationWorkspaceID || folderLink.Key.Normalize() != link.Key.Normalize() {
				row.Connection = "needs_review"
				return row
			}
		}
	}
	row.Connection = "connected"
	if entry.Fields.DisplayName == "" {
		row.Name = child.Name // exact verified link, not a name-based match
	}
	return row
}

func (q Search) matches(row SearchRow, entry Entry) bool {
	if q.Stage != "" && row.Stage != q.Stage ||
		q.Status != "" && row.Status != q.Status ||
		q.Format != "" && row.Format != q.Format || q.Connection != "" && row.Connection != q.Connection ||
		q.Availability != "" && row.Availability != q.Availability {
		return false
	}
	if q.Priority != nil && (row.Priority == nil || *q.Priority != *row.Priority) {
		return false
	}
	if q.RootID != "" {
		found := false
		for _, observation := range entry.Observations {
			found = found || observation.RootID == q.RootID
		}
		if !found {
			return false
		}
	}
	needle := strings.ToLower(q.Text)
	return needle == "" || strings.Contains(strings.ToLower(row.Name), needle) ||
		strings.Contains(strings.ToLower(row.NextAction), needle) ||
		strings.Contains(strings.ToLower(entry.Fields.Purpose), needle)
}

func searchKey(row SearchRow, sortField string) string {
	switch sortField {
	case "status":
		return row.Status
	case "priority":
		if row.Priority != nil {
			return strconv.Itoa(*row.Priority)
		}
		return ""
	case "next_action":
		return strings.ToLower(row.NextAction)
	case "sourced_activity":
		return row.ActivityAt.UTC().Format(time.RFC3339Nano)
	case "scanned_at":
		return row.LastScannedAt.UTC().Format(time.RFC3339Nano)
	default:
		return strings.ToLower(row.Name)
	}
}

func searchLess(a, b SearchRow, sortField, direction string) bool {
	left, right := searchKey(a, sortField), searchKey(b, sortField)
	if left != right {
		if direction == "desc" {
			return left > right
		}
		return left < right
	}
	return a.ID < b.ID // deterministic tie-breaker in either direction
}
