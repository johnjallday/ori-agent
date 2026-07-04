package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/johnjallday/ori-agent/internal/sensitive"
)

// MemoryFileName is the canonical memory file at the workspace folder root.
// The file on disk is the source of truth; memory is never cached in SQLite.
const MemoryFileName = "MEMORY.md"

// MemoryEntryMaxLen bounds a single entry's text. Longer knowledge belongs in
// a note with a one-line pointer stored here instead.
const MemoryEntryMaxLen = 500

// Memory prompt-injection budget. Memory is injected into every mission run
// and workspace chat, so it is capped to protect the context window. The cap
// is expressed in tokens and converted with a tokenizer-free heuristic
// (~4 chars/token) so there is no model-tokenizer dependency in this package.
const (
	MemoryPromptTokenBudget   = 2000
	memoryPromptCharsPerToken = 4
	memoryPromptCharBudget    = MemoryPromptTokenBudget * memoryPromptCharsPerToken
)

// MemoryEntryType classifies what kind of knowledge an entry carries.
type MemoryEntryType string

const (
	MemoryTypeFact     MemoryEntryType = "fact"
	MemoryTypeFeedback MemoryEntryType = "feedback"
	MemoryTypeDecision MemoryEntryType = "decision"
	MemoryTypeDeadEnd  MemoryEntryType = "dead-end"
	MemoryTypeWatch    MemoryEntryType = "watch"
	MemoryTypeThread   MemoryEntryType = "thread"
)

// MemoryEntryTypes lists the recognized types in display order.
var MemoryEntryTypes = []MemoryEntryType{
	MemoryTypeFact,
	MemoryTypeFeedback,
	MemoryTypeDecision,
	MemoryTypeDeadEnd,
	MemoryTypeWatch,
	MemoryTypeThread,
}

// ValidateMemoryText enforces the entry-text contract shared by the memory
// tools and the HTTP API: collapse to one line, reject empty, cap length, and
// refuse obvious secrets. Returns the cleaned single-line text. Errors are
// worded for whoever reads them (the agent or the API client).
func ValidateMemoryText(raw string) (string, error) {
	text := strings.Join(strings.Fields(raw), " ")
	if text == "" {
		return "", errors.New("memory text must not be empty")
	}
	if len(text) > MemoryEntryMaxLen {
		return "", fmt.Errorf("memory entries are capped at %d characters (got %d) — memory holds one curated line per fact; put long content in a workspace note and store a one-line pointer here instead", MemoryEntryMaxLen, len(text))
	}
	if sensitive.ContainsSecretLikeText(text) {
		return "", errors.New("this text looks like a credential or secret; workspace memory is plaintext on disk and injected into prompts, so secrets are refused — store it in the Vault instead")
	}
	return text, nil
}

// NormalizeMemoryEntryType maps arbitrary input to a recognized type. The
// file is hand-editable, so unrecognized types degrade to "fact" rather than
// failing.
func NormalizeMemoryEntryType(raw string) MemoryEntryType {
	candidate := MemoryEntryType(strings.ToLower(strings.TrimSpace(raw)))
	for _, t := range MemoryEntryTypes {
		if candidate == t {
			return t
		}
	}
	return MemoryTypeFact
}

// MemoryEntry is one structured line of workspace memory.
type MemoryEntry struct {
	Type       MemoryEntryType `json:"type"`
	Date       string          `json:"date"` // YYYY-MM-DD
	Provenance string          `json:"provenance"`
	Text       string          `json:"text"`
}

// Render formats the entry as its canonical markdown list line.
func (e MemoryEntry) Render() string {
	return fmt.Sprintf("- [%s, %s, %s] %s", e.Type, e.Date, e.Provenance, e.Text)
}

// memoryLine is one physical line of the file. Entry is nil for unstructured
// lines (headers, hand-written prose), which must survive every rewrite
// byte-identically via Raw.
type memoryLine struct {
	raw   string
	entry *MemoryEntry
}

// MemoryDocument is the parsed form of a MEMORY.md file.
type MemoryDocument struct {
	lines []memoryLine
}

// memoryEntryPattern matches `- [<type>, <YYYY-MM-DD>, <provenance>] <text>`.
// Type and provenance are free-form within their slots; the date shape is the
// structural anchor that separates entries from ordinary markdown bullets.
var memoryEntryPattern = regexp.MustCompile(`^-\s+\[\s*([^,\]]+?)\s*,\s*(\d{4}-\d{2}-\d{2})\s*,\s*([^\]]+?)\s*\]\s+(\S.*)$`)

// ParseMemoryDocument parses file content leniently: lines matching the entry
// pattern become entries (unknown types degrade to "fact"), everything else
// is preserved verbatim as unstructured.
func ParseMemoryDocument(content string) MemoryDocument {
	if content == "" {
		return MemoryDocument{}
	}
	rawLines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	doc := MemoryDocument{lines: make([]memoryLine, 0, len(rawLines))}
	for _, raw := range rawLines {
		line := memoryLine{raw: raw}
		if m := memoryEntryPattern.FindStringSubmatch(raw); m != nil {
			line.entry = &MemoryEntry{
				Type:       NormalizeMemoryEntryType(m[1]),
				Date:       m[2],
				Provenance: strings.TrimSpace(m[3]),
				Text:       strings.TrimSpace(m[4]),
			}
		}
		doc.lines = append(doc.lines, line)
	}
	return doc
}

// Render reassembles the file. Untouched lines come back byte-identical.
func (d MemoryDocument) Render() string {
	if len(d.lines) == 0 {
		return ""
	}
	raws := make([]string, len(d.lines))
	for i, l := range d.lines {
		raws[i] = l.raw
	}
	return strings.Join(raws, "\n") + "\n"
}

// Entries returns the structured entries in file order.
func (d MemoryDocument) Entries() []MemoryEntry {
	var entries []MemoryEntry
	for _, l := range d.lines {
		if l.entry != nil {
			entries = append(entries, *l.entry)
		}
	}
	return entries
}

// UnstructuredLines returns non-entry, non-blank lines (headers, hand-written
// prose) for UI display.
func (d MemoryDocument) UnstructuredLines() []string {
	var out []string
	for _, l := range d.lines {
		if l.entry == nil && strings.TrimSpace(l.raw) != "" {
			out = append(out, l.raw)
		}
	}
	return out
}

// entryLineIndexes maps entry index -> physical line index.
func (d MemoryDocument) entryLineIndexes() []int {
	var idx []int
	for i, l := range d.lines {
		if l.entry != nil {
			idx = append(idx, i)
		}
	}
	return idx
}

// memoryPromptGuidance is the standing instruction about how to use workspace
// memory. Injected wherever the memory_write / memory_forget tools are
// available (mission runs, workspace chat) so the agent knows what the store
// is for and what must never go in it.
const memoryPromptGuidance = "Persistent knowledge accumulated for this workspace across runs and from the user. " +
	"Treat it as context you already established; verify load-bearing facts before relying on them. " +
	"Use memory_write to record durable operational knowledge (stable facts, decisions and their rationale, dead ends, watch-state/baselines, open threads) and memory_forget to remove what's wrong or stale. " +
	"Do not store deliverables (write a note), work items (create a task), or secrets/credentials (use the Vault) here."

// RenderMemoryPromptSection renders workspace memory as a `## Workspace Memory`
// markdown section for prompt injection. Returns "" when there is nothing to
// say (no entries and no tool guidance requested).
//
// Selection under the char budget keeps the most operationally valuable
// entries: all watch and thread entries first (they carry cross-run cursors
// and open investigations), then the remaining entries newest-first by date.
// Kept entries are rendered in their original file order for stable reading,
// and a single notice line reports how many were dropped. Unstructured lines
// (the file header, hand-written prose) are not injected — the agent consumes
// structured entries, humans read the raw file.
//
// includeToolGuidance adds the standing memory_write/forget guidance; callers
// pass true where those tools are available (mission/chat) and false for the
// native-CLI path, which only reads memory as context.
func RenderMemoryPromptSection(doc MemoryDocument, includeToolGuidance bool) string {
	entries := doc.Entries()
	if len(entries) == 0 && !includeToolGuidance {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Workspace Memory\n\n")
	if includeToolGuidance {
		b.WriteString(memoryPromptGuidance)
		b.WriteString("\n\n")
	}
	if len(entries) == 0 {
		b.WriteString("_(empty — nothing recorded yet)_\n")
		return b.String()
	}

	kept, dropped := selectMemoryEntriesForBudget(entries, memoryPromptCharBudget)
	for _, e := range kept {
		b.WriteString(e.Render())
		b.WriteString("\n")
	}
	if dropped > 0 {
		fmt.Fprintf(&b, "\n(memory truncated — %d entr%s not shown; consider pruning)\n", dropped, plural(dropped, "y", "ies"))
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// selectMemoryEntriesForBudget chooses which entries survive the char budget,
// then returns them in original file order along with the dropped count.
// Priority: watch/thread entries first, then others newest-first by date.
func selectMemoryEntriesForBudget(entries []MemoryEntry, charBudget int) (kept []MemoryEntry, dropped int) {
	type indexed struct {
		entry MemoryEntry
		order int
	}
	priority := make([]indexed, 0, len(entries))
	rest := make([]indexed, 0, len(entries))
	for i, e := range entries {
		if e.Type == MemoryTypeWatch || e.Type == MemoryTypeThread {
			priority = append(priority, indexed{e, i})
		} else {
			rest = append(rest, indexed{e, i})
		}
	}
	// Newest first within the non-priority bucket (YYYY-MM-DD sorts
	// lexicographically == chronologically). Stable so equal dates keep file order.
	sort.SliceStable(rest, func(i, j int) bool {
		return rest[i].entry.Date > rest[j].entry.Date
	})

	used := 0
	selected := make([]indexed, 0, len(entries))
	take := func(buckets ...[]indexed) {
		for _, bucket := range buckets {
			for _, it := range bucket {
				cost := len(it.entry.Render()) + 1 // newline
				if used+cost > charBudget && len(selected) > 0 {
					dropped++
					continue
				}
				selected = append(selected, it)
				used += cost
			}
		}
	}
	take(priority, rest)

	// Restore original file order for readable output.
	sort.Slice(selected, func(i, j int) bool { return selected[i].order < selected[j].order })
	kept = make([]MemoryEntry, len(selected))
	for i, it := range selected {
		kept[i] = it.entry
	}
	return kept, dropped
}

var (
	// ErrMemoryEntryNotFound is returned when a forget/edit target matches no entry.
	ErrMemoryEntryNotFound = errors.New("memory entry not found")
	// ErrMemoryAmbiguousMatch is returned when a forget match hits multiple entries.
	ErrMemoryAmbiguousMatch = errors.New("memory match is ambiguous")
	// ErrMemoryIndexOutOfRange is returned for an invalid entry index.
	ErrMemoryIndexOutOfRange = errors.New("memory entry index out of range")
)

// FolderResolver resolves a workspace ID to its folder path.
// *FileStore satisfies this via GetFolderPath.
type FolderResolver interface {
	GetFolderPath(workspaceID string) (string, error)
}

// memoryMu serializes all MEMORY.md mutations process-wide. MemoryStore
// instances are constructed per tool provider and per HTTP handler, so a
// struct-level mutex would not actually serialize concurrent writers; the
// lock must be shared across instances. Memory writes are tiny and rare, so
// one global lock is sufficient (per-workspace sharding is a future
// optimization if contention ever shows up).
var memoryMu sync.Mutex

// MemoryStore reads and mutates a workspace's MEMORY.md. All mutations are
// serialized process-wide (memoryMu) and re-read the file from disk first, so
// concurrent hand edits are honored on a last-write-wins basis.
type MemoryStore struct {
	resolver FolderResolver
}

// NewMemoryStore creates a memory store backed by the given folder resolver.
func NewMemoryStore(resolver FolderResolver) *MemoryStore {
	return &MemoryStore{resolver: resolver}
}

func (s *MemoryStore) filePath(workspaceID string) (string, error) {
	folder, err := s.resolver.GetFolderPath(workspaceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(folder, MemoryFileName), nil
}

// ReadRaw returns the raw file content. A missing file is empty memory, not
// an error.
func (s *MemoryStore) ReadRaw(workspaceID string) (string, error) {
	path, err := s.filePath(workspaceID)
	if err != nil {
		return "", err
	}
	// Path is filepath.Join(store-resolved folder, constant MemoryFileName);
	// workspaceID is only a lookup key in GetFolderPath, never a path segment,
	// so there is no user-controlled path component to traverse.
	data, err := os.ReadFile(path) // #nosec G304 G703
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read workspace memory: %w", err)
	}
	return string(data), nil
}

// Read returns the parsed memory document.
func (s *MemoryStore) Read(workspaceID string) (MemoryDocument, error) {
	raw, err := s.ReadRaw(workspaceID)
	if err != nil {
		return MemoryDocument{}, err
	}
	return ParseMemoryDocument(raw), nil
}

// Append adds an entry, lazily creating MEMORY.md (with a title header) on
// first write.
func (s *MemoryStore) Append(workspaceID string, entry MemoryEntry) error {
	if strings.TrimSpace(entry.Text) == "" {
		return errors.New("memory entry text must not be empty")
	}
	entry.Type = NormalizeMemoryEntryType(string(entry.Type))

	memoryMu.Lock()
	defer memoryMu.Unlock()

	doc, err := s.Read(workspaceID)
	if err != nil {
		return err
	}
	if len(doc.lines) == 0 {
		doc.lines = append(doc.lines,
			memoryLine{raw: "# Workspace Memory"},
			memoryLine{raw: ""},
		)
	}
	line := memoryLine{raw: entry.Render(), entry: &entry}
	doc.lines = append(doc.lines, line)
	return s.write(workspaceID, doc)
}

// Forget removes exactly one entry. An exact text match wins; otherwise a
// case-insensitive substring match must identify a single entry, or the call
// fails with ErrMemoryAmbiguousMatch listing the candidates.
func (s *MemoryStore) Forget(workspaceID string, match string) (MemoryEntry, error) {
	match = strings.TrimSpace(match)
	if match == "" {
		return MemoryEntry{}, errors.New("memory forget match must not be empty")
	}

	memoryMu.Lock()
	defer memoryMu.Unlock()

	doc, err := s.Read(workspaceID)
	if err != nil {
		return MemoryEntry{}, err
	}

	entryLines := doc.entryLineIndexes()
	var exact, partial []int
	for _, li := range entryLines {
		text := doc.lines[li].entry.Text
		if text == match {
			exact = append(exact, li)
		} else if strings.Contains(strings.ToLower(text), strings.ToLower(match)) {
			partial = append(partial, li)
		}
	}

	candidates := exact
	if len(exact) == 0 {
		candidates = partial
	}
	switch len(candidates) {
	case 0:
		return MemoryEntry{}, fmt.Errorf("%w: no entry matches %q", ErrMemoryEntryNotFound, match)
	case 1:
		removed := *doc.lines[candidates[0]].entry
		doc.lines = append(doc.lines[:candidates[0]], doc.lines[candidates[0]+1:]...)
		if err := s.write(workspaceID, doc); err != nil {
			return MemoryEntry{}, err
		}
		return removed, nil
	default:
		texts := make([]string, len(candidates))
		for i, li := range candidates {
			texts[i] = fmt.Sprintf("%q", doc.lines[li].entry.Text)
		}
		return MemoryEntry{}, fmt.Errorf("%w: %q matches %d entries: %s",
			ErrMemoryAmbiguousMatch, match, len(candidates), strings.Join(texts, ", "))
	}
}

// EditAt replaces the entry at the given entry index (file order, 0-based)
// with the provided entry, rendered canonically.
func (s *MemoryStore) EditAt(workspaceID string, index int, entry MemoryEntry) error {
	if strings.TrimSpace(entry.Text) == "" {
		return errors.New("memory entry text must not be empty")
	}
	entry.Type = NormalizeMemoryEntryType(string(entry.Type))

	memoryMu.Lock()
	defer memoryMu.Unlock()

	doc, err := s.Read(workspaceID)
	if err != nil {
		return err
	}
	entryLines := doc.entryLineIndexes()
	if index < 0 || index >= len(entryLines) {
		return fmt.Errorf("%w: index %d, have %d entries", ErrMemoryIndexOutOfRange, index, len(entryLines))
	}
	doc.lines[entryLines[index]] = memoryLine{raw: entry.Render(), entry: &entry}
	return s.write(workspaceID, doc)
}

// DeleteAt removes the entry at the given entry index (file order, 0-based).
func (s *MemoryStore) DeleteAt(workspaceID string, index int) error {
	memoryMu.Lock()
	defer memoryMu.Unlock()

	doc, err := s.Read(workspaceID)
	if err != nil {
		return err
	}
	entryLines := doc.entryLineIndexes()
	if index < 0 || index >= len(entryLines) {
		return fmt.Errorf("%w: index %d, have %d entries", ErrMemoryIndexOutOfRange, index, len(entryLines))
	}
	li := entryLines[index]
	doc.lines = append(doc.lines[:li], doc.lines[li+1:]...)
	return s.write(workspaceID, doc)
}

func (s *MemoryStore) write(workspaceID string, doc MemoryDocument) error {
	path, err := s.filePath(workspaceID)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(path, []byte(doc.Render())); err != nil {
		return fmt.Errorf("failed to write workspace memory: %w", err)
	}
	return nil
}
