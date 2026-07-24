package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// This file implements the managed two-way BACKLOG.md synchronization
// (tasks/prd-workspace-backlog.md FR67-91). It reuses task_markdown_sync.go's
// safe-path, atomic-write, frontmatter, hash, and warning patterns, but is a
// fully separate document/parser/settings path: Backlog items are excluded
// from tasks.md (FR90), and this sync is not gated by a settings toggle —
// every managed workspace gets BACKLOG.md unconditionally (FR67-68).

const (
	backlogMarkdownSchemaVersion = 1
	backlogMarkdownDocType       = "ori_workspace_backlog"
	// BacklogMarkdownFileName is the managed file name every workspace's
	// synchronized backlog document is written under.
	BacklogMarkdownFileName = "BACKLOG.md"
	// maxBacklogMarkdownBytes bounds the importer against oversized input
	// (FR82); a hand-edited planning document has no legitimate reason to
	// exceed this.
	maxBacklogMarkdownBytes = 1 << 20 // 1 MiB

	backlogMarkdownSharedDataKey = "backlog_markdown_sync"

	backlogPriorityHigh   = "high"
	backlogPriorityMedium = "medium"
	backlogPriorityLow    = "low"
)

var (
	// backlogMarkdownBulletPattern matches a plain (non-checkbox) bullet row:
	// Backlog rows have no completion checkbox, unlike tasks.md.
	backlogMarkdownBulletPattern = regexp.MustCompile(`^-\s+(.*)$`)
	backlogMarkdownSectionHeader = regexp.MustCompile(`^##\s+(.+?)\s*$`)
)

// --- Frontmatter & document shape ---

type backlogMarkdownFrontmatter struct {
	Type             string `yaml:"type"`
	SchemaVersion    int    `yaml:"schema_version"`
	WorkspaceID      string `yaml:"workspace_id"`
	WorkspaceVersion int64  `yaml:"workspace_version"`
	LastSyncedAt     string `yaml:"last_synced_at"`
	ContentHash      string `yaml:"content_hash"`
}

// backlogMarkdownRow is one parsed bullet line under either section.
type backlogMarkdownRow struct {
	ID        string
	Title     string
	Priority  string // "" | high | medium | low
	Tags      []string
	URL       string
	LineIndex int
	Section   string // "backlog" | "promote"
}

// BacklogMarkdownImportResult reports what an import found and applied.
type BacklogMarkdownImportResult struct {
	Changed  bool
	Warnings []string
}

// BacklogMarkdownCollision describes a non-Ori-managed file already present
// at the managed path (FR89). Callers must not overwrite it automatically;
// present the preview and let the user choose adopt, replace, or leave it.
type BacklogMarkdownCollision struct {
	Path    string
	Preview string
}

// --- Sync state (per-item snapshot for conflict detection, FR86-87) ---

// backlogSyncItemSnapshot captures the supported-field values Ori last wrote
// to the file for one item, and its file-side counterpart, if any. Comparing
// current-Ori vs. this snapshot vs. current-file lets import distinguish a
// pure file edit, a pure Ori edit, and a same-item conflict without needing a
// full 3-way diff library.
type backlogSyncItemSnapshot struct {
	Title    string   `json:"title"`
	Priority string   `json:"priority"`
	Tags     []string `json:"tags"`
	URL      string   `json:"url"`
}

// BacklogSyncConflict retains both versions of a same-item conflict until the
// user resolves it with Use Ori or Use File (FR86-87). Neither version is
// applied automatically.
type BacklogSyncConflict struct {
	ItemID     string                  `json:"item_id"`
	Title      string                  `json:"title"` // display title, from the Ori side
	OriValue   backlogSyncItemSnapshot `json:"ori_value"`
	FileValue  backlogSyncItemSnapshot `json:"file_value"`
	DetectedAt time.Time               `json:"detected_at"`
}

// backlogMarkdownSyncState is the synchronizer's own bookkeeping, persisted
// in Workspace.SharedData (not part of the canonical Task records). It is
// display/reconciliation metadata only — never authoritative over a Task.
type backlogMarkdownSyncState struct {
	LastSyncedAt     time.Time                          `json:"last_synced_at"`
	LastRenderedItem map[string]backlogSyncItemSnapshot `json:"last_rendered_items,omitempty"`
	Conflicts        []BacklogSyncConflict              `json:"conflicts,omitempty"`
	RepairNeeded     bool                               `json:"repair_needed,omitempty"`
	Warning          string                             `json:"warning,omitempty"`
}

func getBacklogMarkdownSyncState(ws *Workspace) backlogMarkdownSyncState {
	var state backlogMarkdownSyncState
	if ws == nil || ws.SharedData == nil {
		return state
	}
	raw, ok := ws.SharedData[backlogMarkdownSharedDataKey]
	if !ok || raw == nil {
		return state
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return state
	}
	_ = json.Unmarshal(data, &state)
	return state
}

func setBacklogMarkdownSyncState(ws *Workspace, state backlogMarkdownSyncState) {
	if ws == nil {
		return
	}
	if ws.SharedData == nil {
		ws.SharedData = map[string]any{}
	}
	ws.SharedData[backlogMarkdownSharedDataKey] = state
}

func snapshotFromTask(t Task) backlogSyncItemSnapshot {
	return backlogSyncItemSnapshot{
		Title:    t.Description,
		Priority: priorityIntToWord(t.Priority),
		Tags:     append([]string(nil), t.Tags...),
		URL:      t.ReferenceURL,
	}
}

func snapshotFromRow(r backlogMarkdownRow) backlogSyncItemSnapshot {
	return backlogSyncItemSnapshot{
		Title:    r.Title,
		Priority: r.Priority,
		Tags:     append([]string(nil), r.Tags...),
		URL:      r.URL,
	}
}

func snapshotsEqual(a, b backlogSyncItemSnapshot) bool {
	if a.Title != b.Title || a.URL != b.URL {
		return false
	}
	aPriority, bPriority := a.Priority, b.Priority
	if aPriority == "" {
		aPriority = backlogPriorityMedium
	}
	if bPriority == "" {
		bPriority = backlogPriorityMedium
	}
	if aPriority != bPriority {
		return false
	}
	return stringSlicesEqualUnordered(a.Tags, b.Tags)
}

func stringSlicesEqualUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}

// --- Priority word <-> int mapping ---
// The file exposes priority as a word (matching the PRD's illustrative
// syntax); Task.Priority stays the existing 1-5 int scale so promotion and
// every other task surface need no new field. 1=high, 3=medium (default),
// 5=low; values in between round to the nearest named tier.

func priorityWordToInt(word string) int {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case backlogPriorityHigh:
		return 1
	case backlogPriorityLow:
		return 5
	default:
		return 3
	}
}

func priorityIntToWord(p int) string {
	switch {
	case p <= 0:
		return backlogPriorityMedium
	case p <= 2:
		return backlogPriorityHigh
	case p >= 4:
		return backlogPriorityLow
	default:
		return backlogPriorityMedium
	}
}

// --- Content root resolution (FR69) ---

// backlogMarkdownContentRoot mirrors sessionhttp.defaultWorkspaceMCPRoots:
// group workspaces' agents are scoped to files/ and notes/ only (their
// folder root is never exposed), so BACKLOG.md must live in the group-owned
// files/ root to remain visible through the default workspace-files
// binding. Normal (non-group) workspaces expose their whole folder, matching
// where tasks.md already lives.
func backlogMarkdownContentRoot(folder string, isGroup bool) string {
	if isGroup {
		return filepath.Join(folder, FilesDir)
	}
	return folder
}

// BacklogMarkdownPath returns the absolute managed path for a workspace
// folder, honoring the group/normal content-root split.
func BacklogMarkdownPath(folder string, isGroup bool) string {
	return filepath.Join(backlogMarkdownContentRoot(folder, isGroup), BacklogMarkdownFileName)
}

// --- Rendering (FR70-73, 75-76) ---

// RenderWorkspaceBacklogMarkdown deterministically renders ws's current
// Backlog items (rank, then creation time, then ID — matching
// BacklogService's own sort) plus the always-empty Promote to Ready intake
// section: a Ready item lives in Tasks/tasks.md, never listed here again
// once promoted (FR79).
func RenderWorkspaceBacklogMarkdown(ws *Workspace) string {
	if ws == nil {
		return ""
	}
	body := renderBacklogMarkdownBody(ws)
	now := time.Now().UTC().Format(time.RFC3339)
	hash := sha256.Sum256([]byte(body))
	frontmatter := backlogMarkdownFrontmatter{
		Type:             backlogMarkdownDocType,
		SchemaVersion:    backlogMarkdownSchemaVersion,
		WorkspaceID:      ws.ID,
		WorkspaceVersion: ws.Version,
		LastSyncedAt:     now,
		ContentHash:      "sha256:" + hex.EncodeToString(hash[:]),
	}
	fmData, err := yaml.Marshal(frontmatter)
	if err != nil {
		return body
	}
	return "---\n" + string(fmData) + "---\n\n" + body
}

func renderBacklogMarkdownBody(ws *Workspace) string {
	items := localBacklogItemViews(ws)
	sortBacklogItems(items)

	var sb strings.Builder
	sb.WriteString("# Backlog\n\n")
	sb.WriteString("> Synchronized by Ori. You may add or rename rows, edit supported priority/tags/reference metadata, reorder Backlog rows, or move a row to Promote to Ready. Use Ori for assignment, scheduling, execution, completion, and deletion.\n\n")
	sb.WriteString("## Backlog\n\n")
	if len(items) == 0 {
		sb.WriteString("_Nothing saved for later. Add an idea without committing it to an agent._\n")
	} else {
		for _, item := range items {
			writeBacklogMarkdownRow(&sb, item.Task)
		}
	}
	sb.WriteString("\n## Promote to Ready\n\n")
	sb.WriteString("_Move a Backlog row here to promote it. Successfully promoted rows leave this file on the next sync._\n")
	return sb.String()
}

func writeBacklogMarkdownRow(sb *strings.Builder, t Task) {
	title := strings.TrimSpace(strings.ReplaceAll(t.Description, "\n", " "))
	if title == "" {
		title = "Untitled idea"
	}
	fmt.Fprintf(sb, "- %s <!-- %s -->\n", title, buildBacklogMarkdownMetadata(t))
}

func buildBacklogMarkdownMetadata(t Task) string {
	parts := []string{"ori:id=" + escapeTaskMarkdownValue(t.ID)}
	parts = append(parts, "priority="+priorityIntToWord(t.Priority))
	if len(t.Tags) > 0 {
		parts = append(parts, "tags="+escapeTaskMarkdownValue(strings.Join(t.Tags, ",")))
	}
	if url := strings.TrimSpace(t.ReferenceURL); url != "" {
		parts = append(parts, "url="+escapeTaskMarkdownValue(url))
	}
	return strings.Join(parts, " ")
}

// --- Parsing (FR71, 73-75, 81-82: bounded, validated, all-or-nothing) ---

type backlogMarkdownDocument struct {
	Frontmatter backlogMarkdownFrontmatter
	BacklogRows []backlogMarkdownRow
	PromoteRows []backlogMarkdownRow
}

// ParseWorkspaceBacklogMarkdown validates and parses content into a document.
// Validation failures (malformed frontmatter, wrong workspace_id, duplicate
// or unknown IDs, oversized input) return an error and no partial document
// (FR82) — callers must not apply anything from a document this rejects.
func ParseWorkspaceBacklogMarkdown(content, workspaceID string, knownBacklogIDs map[string]struct{}) (*backlogMarkdownDocument, error) {
	if len(content) > maxBacklogMarkdownBytes {
		return nil, fmt.Errorf("BACKLOG.md is too large (%d bytes, max %d)", len(content), maxBacklogMarkdownBytes)
	}

	frontmatterRaw, body, err := splitTaskMarkdownFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("BACKLOG.md frontmatter is malformed: %w", err)
	}
	var fm backlogMarkdownFrontmatter
	if strings.TrimSpace(frontmatterRaw) == "" {
		return nil, fmt.Errorf("BACKLOG.md is missing its Ori frontmatter")
	}
	if err := yaml.Unmarshal([]byte(frontmatterRaw), &fm); err != nil {
		return nil, fmt.Errorf("BACKLOG.md frontmatter is malformed: %w", err)
	}
	if fm.Type != "" && fm.Type != backlogMarkdownDocType {
		return nil, fmt.Errorf("unsupported BACKLOG.md type %q", fm.Type)
	}
	if fm.WorkspaceID != "" && workspaceID != "" && fm.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("BACKLOG.md workspace_id %q does not match %q", fm.WorkspaceID, workspaceID)
	}

	doc, err := parseBacklogMarkdownBody(body, knownBacklogIDs)
	if err != nil {
		return nil, err
	}
	doc.Frontmatter = fm
	return doc, nil
}

// parseBacklogMarkdownBody parses the "## Backlog" / "## Promote to Ready"
// bullet rows from body, with no frontmatter involved. Shared by
// ParseWorkspaceBacklogMarkdown (which validates frontmatter first) and file
// adoption (FR89, Tech Consideration 14), which intentionally has no Ori
// frontmatter to validate — that is the entire premise of "adopt".
func parseBacklogMarkdownBody(body string, knownBacklogIDs map[string]struct{}) (*backlogMarkdownDocument, error) {
	doc := &backlogMarkdownDocument{}
	seenIDs := map[string]struct{}{}
	section := "backlog"
	lineIndex := 0

	for _, line := range strings.Split(body, "\n") {
		if h := backlogMarkdownSectionHeader.FindStringSubmatch(line); h != nil {
			switch strings.ToLower(strings.TrimSpace(h[1])) {
			case "backlog":
				section = "backlog"
			case "promote to ready":
				section = "promote"
			default:
				section = "" // unrecognized section: ignore its rows rather than misfiling them
			}
			continue
		}
		match := backlogMarkdownBulletPattern.FindStringSubmatch(line)
		if match == nil || section == "" {
			continue
		}
		raw := strings.TrimSpace(match[1])
		metaText := ""
		if metaMatch := taskMarkdownMetaPattern.FindStringSubmatch(raw); metaMatch != nil {
			metaText = metaMatch[1]
			raw = strings.TrimSpace(taskMarkdownMetaPattern.ReplaceAllString(raw, ""))
		}
		title := strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(raw, " "))
		if title == "" || strings.HasPrefix(title, "_") {
			// Skip empty rows and the italic placeholder/help text Ori itself
			// renders (e.g. "_Nothing saved for later...._").
			continue
		}
		meta := parseTaskMarkdownMetadata(metaText)
		row := backlogMarkdownRow{
			ID:        meta["id"],
			Title:     title,
			Priority:  strings.ToLower(strings.TrimSpace(meta["priority"])),
			Tags:      splitTaskMarkdownIDs(meta["tags"]),
			URL:       meta["url"],
			LineIndex: lineIndex,
			Section:   section,
		}
		lineIndex++

		if row.ID != "" {
			if _, dup := seenIDs[row.ID]; dup {
				return nil, fmt.Errorf("BACKLOG.md has a duplicate item id %q", row.ID)
			}
			seenIDs[row.ID] = struct{}{}
			if knownBacklogIDs != nil {
				if _, known := knownBacklogIDs[row.ID]; !known {
					return nil, fmt.Errorf("BACKLOG.md references unknown item id %q", row.ID)
				}
			}
		}

		switch section {
		case "backlog":
			doc.BacklogRows = append(doc.BacklogRows, row)
		case "promote":
			doc.PromoteRows = append(doc.PromoteRows, row)
		}
	}

	return doc, nil
}

// --- Collision detection (FR89) ---

// detectBacklogMarkdownCollision reports a pre-existing file at path that is
// not Ori-managed. A nil, nil result means "safe to write" (no file, or an
// Ori-managed file already there).
func detectBacklogMarkdownCollision(path string) (*BacklogMarkdownCollision, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is BacklogMarkdownPath(folder, isGroup), where folder comes from a store-resolved workspace ID, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if isOriManagedBacklogMarkdown(data) {
		return nil, nil
	}
	preview := string(data)
	const maxPreview = 2000
	if len(preview) > maxPreview {
		preview = preview[:maxPreview]
	}
	return &BacklogMarkdownCollision{Path: path, Preview: preview}, nil
}

func isOriManagedBacklogMarkdown(data []byte) bool {
	frontmatterRaw, _, err := splitTaskMarkdownFrontmatter(string(data))
	if err != nil || strings.TrimSpace(frontmatterRaw) == "" {
		return false
	}
	var fm backlogMarkdownFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatterRaw), &fm); err != nil {
		return false
	}
	return fm.Type == backlogMarkdownDocType
}
