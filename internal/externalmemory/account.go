package externalmemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The Claude account keeps its memory on Anthropic's servers with no API, so
// it joins by paste (FR8): the user copies the text from claude.ai and Ori
// splits it into entries. The stored paste is this source's input; pasting
// again replaces it, and the ordinary sync diff marks the old entries removed.

// MaxPasteBytes caps one paste.
const MaxPasteBytes = 256 * 1024

// ErrPasteTooLarge refuses a paste over MaxPasteBytes.
var ErrPasteTooLarge = errors.New("the paste is larger than 256 KB; split it and add it in parts")

// ErrPasteEmpty refuses a paste with no text.
var ErrPasteEmpty = errors.New("the paste is empty")

const pasteDirName = "paste"

// pastePath is where a source's paste is kept, inside the mirror folder.
func (s *Store) pastePath(source string) (string, error) {
	if !ValidSourceID(source) {
		return "", fmt.Errorf("unknown external memory source %q", source)
	}
	return filepath.Join(s.dir, pasteDirName, source+".md"), nil
}

// SavePaste replaces a source's stored paste and records its date. The text
// is never logged.
func (s *Store) SavePaste(source, text string, at time.Time) error {
	if len(text) > MaxPasteBytes {
		return ErrPasteTooLarge
	}
	if strings.TrimSpace(text) == "" {
		return ErrPasteEmpty
	}
	path, err := s.pastePath(source)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if err := os.MkdirAll(filepath.Dir(path), storeDirPerm); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("create paste folder: %w", err)
	}
	err = atomicWriteFile(path, []byte(text))
	s.mu.Unlock()
	if err != nil {
		return err
	}
	_, err = s.UpdateSources(func(doc *SourcesDocument) error {
		state := doc.Source(source)
		pasted := at
		state.PasteDate = &pasted
		doc.Sources[source] = state
		return nil
	})
	return err
}

// LoadPaste returns a source's stored paste, or "" when there is none.
func (s *Store) LoadPaste(source string) (string, bool) {
	path, err := s.pastePath(source)
	if err != nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxPasteBytes {
		return "", false
	}
	// path is the store's own paste file for a validated source id.
	data, err := os.ReadFile(path) // #nosec G304 -- store-owned file built from fixed names and a validated source id
	if err != nil {
		return "", false
	}
	return string(data), true
}

// ClaudeAccountSource reads the stored claude.ai paste.
type ClaudeAccountSource struct {
	store *Store
}

// NewClaudeAccountSource reads the paste kept in store.
func NewClaudeAccountSource(store *Store) *ClaudeAccountSource {
	return &ClaudeAccountSource{store: store}
}

func (s *ClaudeAccountSource) ID() string { return SourceClaudeAccount }

// Location is empty: the account has no file; the card shows a paste box.
func (s *ClaudeAccountSource) Location() string { return "" }

// Available reports whether a paste has been added.
func (s *ClaudeAccountSource) Available() bool {
	_, ok := s.store.LoadPaste(SourceClaudeAccount)
	return ok
}

func (s *ClaudeAccountSource) Read(context.Context) ([]Entry, []ParseError, error) {
	text, ok := s.store.LoadPaste(SourceClaudeAccount)
	if !ok {
		return nil, nil, fmt.Errorf("no claude account paste")
	}
	return ParseAccountPaste(text), nil, nil
}

var (
	// datedLinePattern matches a line that starts with "[2026-10-17]" or
	// "[unknown]", the export format's per-fact prefix.
	datedLinePattern  = regexp.MustCompile(`^\s*(?:[-*+]\s+)?\[(\d{4}-\d{2}-\d{2}|unknown)\]`)
	datePrefixPattern = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2}|unknown)\]\s*[-–—:]?\s*`)
)

// ParseAccountPaste splits pasted account memory into entries: each bullet,
// numbered item, paragraph, or dated line is one entry; the nearest heading
// above it is its category (slugged; "account" when there is none); a leading
// [YYYY-MM-DD] is its date. An entry's id comes from its text alone, so a line
// that is unchanged in the next paste keeps its id and its promoted link.
func ParseAccountPaste(text string) []Entry {
	sections, err := readSections(strings.NewReader(text))
	if err != nil {
		return nil
	}
	keys := sectionKeys{}
	var entries []Entry
	for _, section := range sections {
		category := "account"
		if section.Heading != "" {
			if slug := Slug(section.Heading); slug != "" {
				category = slug
			}
		}
		for _, block := range splitBlocks(section.Lines) {
			for _, item := range splitDatedLines(block) {
				body, date := takeDatePrefix(item)
				if body == "" {
					continue
				}
				sum := sha256.Sum256([]byte(foldText(collapseSpace(body))))
				key := keys.next(hex.EncodeToString(sum[:8]))
				entry := newEntry(SourceClaudeAccount, "paste", key, Entry{
					Category: category, Title: titleFrom(body), Body: body, Date: date,
				})
				entries = append(entries, entry)
			}
		}
	}
	return entries
}

// splitDatedLines cuts a paragraph whose lines each start with a date prefix
// (the import format: "[2026-10-17] - text" on every line) into one item per
// line. Other blocks are returned whole.
func splitDatedLines(block mdBlock) []string {
	if block.Kind != blockParagraph {
		return []string{block.Text}
	}
	lines := strings.Split(block.Text, "\n")
	var items []string
	var current []string
	for _, line := range lines {
		if datedLinePattern.MatchString(line) && len(current) > 0 {
			items = append(items, strings.Join(current, "\n"))
			current = nil
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		items = append(items, strings.Join(current, "\n"))
	}
	return items
}

// takeDatePrefix strips a leading list marker and "[YYYY-MM-DD] - " and
// returns the rest with its date ("" for [unknown] or none).
func takeDatePrefix(item string) (string, string) {
	text := stripListMarker(strings.TrimSpace(item))
	date := ""
	if m := datePrefixPattern.FindStringSubmatch(text); m != nil {
		if m[1] != "unknown" {
			date = normalizeDate(m[1])
		}
		text = strings.TrimSpace(text[len(m[0]):])
	}
	return strings.TrimSpace(text), date
}
