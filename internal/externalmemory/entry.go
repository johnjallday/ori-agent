// Package externalmemory mirrors other agents' memory into a read-only,
// provenance-tagged tier that only the personal assistant reads.
//
// Codex, Claude Code and the Gemini CLI each keep their own memory of the user
// on disk; the Claude account keeps it on Anthropic's servers and joins through
// a paste. Ori never writes to any of those places. A sync reads each enabled
// source, splits it into entries with stable ids, and diffs them against the
// mirror by content hash, so a re-sync touches only what changed and an entry
// that disappears is marked removed rather than deleted (a promoted entry keeps
// its link to the Personal HQ item it became).
package externalmemory

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Source identifiers (PRD FR1). They are also the entries file names, so they
// must stay path-safe.
const (
	SourceCodex         = "codex"
	SourceClaudeCode    = "claude_code"
	SourceGeminiCLI     = "gemini_cli"
	SourceClaudeAccount = "claude_account"
)

// SourceIDs lists every source in display order.
var SourceIDs = []string{SourceCodex, SourceClaudeCode, SourceGeminiCLI, SourceClaudeAccount}

// ValidSourceID reports whether id names a known source.
func ValidSourceID(id string) bool {
	return slices.Contains(SourceIDs, id)
}

// SourceLabel is the name the Profile page and the assistant use for a source.
func SourceLabel(id string) string {
	switch id {
	case SourceCodex:
		return "Codex"
	case SourceClaudeCode:
		return "Claude Code"
	case SourceGeminiCLI:
		return "Gemini CLI"
	case SourceClaudeAccount:
		return "Claude account"
	}
	return id
}

// Entry states. A removed entry is kept so a promoted link survives, but it is
// hidden from search and from the assistant.
const (
	StateActive  = "active"
	StateRemoved = "removed"
)

// MaxBodyBytes caps one entry's stored body (FR10). Longer bodies are cut at a
// rune boundary, marked, and flagged Truncated.
const MaxBodyBytes = 64 * 1024

// MaxStoreBytes caps the whole mirror (FR10). Beyond it a sync stops adding
// entries and the source card shows a warning.
const MaxStoreBytes = 200 << 20

// truncationMarker ends a body that was cut at MaxBodyBytes.
const truncationMarker = "\n\n[… truncated by Ori at 64 KB]"

// Entry is one mirrored memory item.
type Entry struct {
	// ID is stable across syncs: a hash of the source, the file, and the
	// section key, so re-reading the same section gives the same id.
	ID     string `json:"id"`
	Source string `json:"source"`
	// Origin is the file path and section anchor ("<path>#<key>"); for the
	// account paste it is "paste#<key>".
	Origin string `json:"origin"`
	// Folder is the project folder the memory applies to, or empty when it is
	// about the user in general.
	Folder   string `json:"folder"`
	Category string `json:"category"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	// Date is YYYY-MM-DD when the source records one.
	Date        string    `json:"date,omitempty"`
	ContentHash string    `json:"content_hash"`
	State       string    `json:"state"`
	SyncedAt    time.Time `json:"synced_at"`
	// RemovedAt is when a sync first found the entry gone.
	RemovedAt *time.Time `json:"removed_at,omitempty"`
	// PromotedItemID is the Personal HQ knowledge item this entry became.
	PromotedItemID string `json:"promoted_item_id,omitempty"`
	Truncated      bool   `json:"truncated,omitempty"`
	// FolderUnverified marks a Claude Code project key that could not be
	// decoded to an existing folder; Folder then holds the raw key.
	FolderUnverified bool `json:"folder_unverified,omitempty"`
}

// Active reports whether the entry is live (not removed).
func (e Entry) Active() bool { return e.State != StateRemoved }

// EntryID returns the stable id for a section of a source file.
func EntryID(source, path, sectionKey string) string {
	sum := sha256.Sum256([]byte(source + "\x00" + path + "\x00" + sectionKey))
	return hex.EncodeToString(sum[:16])
}

// contentHash fingerprints everything a reader sees, so any visible change
// counts as an update.
func contentHash(e Entry) string {
	h := sha256.New()
	for _, part := range []string{e.Folder, e.Category, e.Title, e.Date, e.Body} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// newEntry builds an entry with its id, origin, capped body, and content hash.
func newEntry(source, path, sectionKey string, e Entry) Entry {
	e.Source = source
	e.ID = EntryID(source, path, sectionKey)
	e.Origin = path + "#" + sectionKey
	e.Title = strings.TrimSpace(e.Title)
	e.Category = strings.TrimSpace(e.Category)
	e.Body, e.Truncated = capBody(strings.TrimSpace(e.Body))
	e.State = StateActive
	e.ContentHash = contentHash(e)
	return e
}

// capBody cuts body to MaxBodyBytes at a rune boundary.
func capBody(body string) (string, bool) {
	if len(body) <= MaxBodyBytes {
		return body, false
	}
	cut := MaxBodyBytes - len(truncationMarker)
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut] + truncationMarker, true
}

// Slug lowercases text and joins its letters and digits with dashes. Letters
// of every script survive, so a Korean heading keeps a readable key.
func Slug(text string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(text) {
		if r == '\'' || r == '’' {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	return b.String()
}

// maxKeySlugRunes bounds a section key built from an item's first line.
const maxKeySlugRunes = 60

// keySlug is a bounded slug for a section key. A long line is cut and a short
// hash of the whole line keeps two lines that share a prefix apart.
func keySlug(text string) string {
	slug := Slug(text)
	if utf8.RuneCountInString(slug) <= maxKeySlugRunes {
		if slug == "" {
			return "item"
		}
		return slug
	}
	runes := []rune(slug)
	sum := sha256.Sum256([]byte(text))
	return strings.TrimRight(string(runes[:maxKeySlugRunes]), "-") + "-" + hex.EncodeToString(sum[:3])
}

// maxTitleRunes bounds a title taken from an item's first line.
const maxTitleRunes = 120

// titleFrom returns the first line of text, without list markers, cut at a word
// boundary near maxTitleRunes.
func titleFrom(text string) string {
	line := strings.TrimSpace(text)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	line = stripListMarker(line)
	runes := []rune(line)
	if len(runes) <= maxTitleRunes {
		return line
	}
	cut := maxTitleRunes
	for i := maxTitleRunes; i > maxTitleRunes/2; i-- {
		if unicode.IsSpace(runes[i]) {
			cut = i
			break
		}
	}
	return strings.TrimSpace(string(runes[:cut])) + "…"
}

// normalizeDate turns the timestamps the sources use (RFC 3339 with or without
// fractional seconds, plain dates, Codex's compact 20260716T235137Z) into
// YYYY-MM-DD. Unknown shapes yield "".
func normalizeDate(raw string) string {
	raw = strings.Trim(strings.TrimSpace(raw), `"'`)
	if raw == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02", "20060102T150405Z"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC().Format("2006-01-02")
		}
	}
	if len(raw) >= 10 {
		if t, err := time.Parse("2006-01-02", raw[:10]); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return ""
}
