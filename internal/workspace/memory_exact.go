package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxExactMemoryBytes = 8 * 1024 * 1024

var ErrMemoryConflict = errors.New("workspace memory changed; refresh before editing")

// MemoryExactTarget identifies a reviewed physical line by its exact content
// and snapshot, not by the index shown in the Memory tab. A duplicate byte-
// identical legacy line remains ambiguous even if the caller saw an index.
type MemoryExactTarget struct {
	FileHash   string `json:"file_hash"`
	LineHash   string `json:"line_hash"`
	Provenance string `json:"provenance"`
}

type MemoryExactEntry struct {
	Target MemoryExactTarget `json:"target"`
	Entry  MemoryEntry       `json:"entry"`
}

type MemoryExactSnapshot struct {
	FileHash string             `json:"file_hash"`
	Entries  []MemoryExactEntry `json:"entries"`
}

type MemoryExactStatus string

const (
	MemoryExactUnchanged MemoryExactStatus = "unchanged"
	MemoryExactChanged   MemoryExactStatus = "changed"
	MemoryExactMissing   MemoryExactStatus = "missing"
	MemoryExactAmbiguous MemoryExactStatus = "ambiguous"
)

// InspectExact reconciles an existing managed marker against direct Memory
// tab/full-file writes without modifying either canonical content or metadata.
// A changed/missing/ambiguous result must be handled as needs-review, not as
// permission to recreate an old sidecar value.
func (s *MemoryStore) InspectExact(workspaceID string, target MemoryExactTarget) (MemoryExactStatus, error) {
	if target.Provenance == "" || target.LineHash == "" {
		return MemoryExactAmbiguous, ErrMemoryConflict
	}
	snapshot, err := s.SnapshotExact(workspaceID)
	if err != nil {
		return MemoryExactAmbiguous, err
	}
	var count int
	var observed string
	for _, entry := range snapshot.Entries {
		if entry.Entry.Provenance == target.Provenance {
			count++
			observed = entry.Target.LineHash
		}
	}
	switch {
	case count == 0:
		return MemoryExactMissing, nil
	case count != 1:
		return MemoryExactAmbiguous, nil
	case observed != target.LineHash:
		return MemoryExactChanged, nil
	default:
		return MemoryExactUnchanged, nil
	}
}

type physicalMemoryLine struct {
	start, end int // end includes any CRLF/LF line terminator
	body       []byte
	entry      *MemoryEntry
}

func hashMemoryBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func splitPhysicalMemoryLines(raw []byte) []physicalMemoryLine {
	var lines []physicalMemoryLine
	for start := 0; start < len(raw); {
		breakAt := bytes.IndexByte(raw[start:], '\n')
		end, bodyEnd := len(raw), len(raw)
		if breakAt >= 0 {
			end = start + breakAt + 1
			bodyEnd = end - 1
			if bodyEnd > start && raw[bodyEnd-1] == '\r' {
				bodyEnd--
			}
		}
		body := raw[start:bodyEnd]
		line := physicalMemoryLine{start: start, end: end, body: body}
		if matches := memoryEntryPattern.FindStringSubmatch(string(body)); matches != nil {
			line.entry = &MemoryEntry{
				Type: NormalizeMemoryEntryType(matches[1]), Date: matches[2],
				Provenance: strings.TrimSpace(matches[3]), Text: strings.TrimSpace(matches[4]),
			}
		}
		lines = append(lines, line)
		start = end
	}
	return lines
}

// SnapshotExact is the canonical edit view. The parser is tolerant of manual
// Markdown, mixed newlines and a missing terminal newline. It never rewrites a
// file simply because it was read.
func (s *MemoryStore) SnapshotExact(workspaceID string) (MemoryExactSnapshot, error) {
	raw, _, err := s.readExact(workspaceID)
	if err != nil {
		return MemoryExactSnapshot{}, err
	}
	out := MemoryExactSnapshot{FileHash: hashMemoryBytes(raw)}
	for _, line := range splitPhysicalMemoryLines(raw) {
		if line.entry == nil {
			continue
		}
		out.Entries = append(out.Entries, MemoryExactEntry{
			Target: MemoryExactTarget{FileHash: out.FileHash, LineHash: hashMemoryBytes(line.body), Provenance: line.entry.Provenance},
			Entry:  *line.entry,
		})
	}
	return out, nil
}

// EditExact replaces one reviewed entry (or deletes it when replacement is
// nil). It changes only that physical line. Older whole-document renderers are
// deliberately not used here: they normalize CRLF and terminal newlines.
func (s *MemoryStore) EditExact(workspaceID string, target MemoryExactTarget, replacement *MemoryEntry) error {
	if target.FileHash == "" || target.LineHash == "" || target.Provenance == "" {
		return ErrMemoryConflict
	}
	if replacement != nil {
		cleaned, err := validateExactMemoryEntry(*replacement)
		if err != nil {
			return err
		}
		replacement = &cleaned
	}
	memoryMu.Lock()
	defer memoryMu.Unlock()
	raw, path, err := s.readExact(workspaceID)
	if err != nil {
		return err
	}
	if hashMemoryBytes(raw) != target.FileHash {
		return ErrMemoryConflict
	}
	var matches []physicalMemoryLine
	for _, line := range splitPhysicalMemoryLines(raw) {
		if line.entry != nil && line.entry.Provenance == target.Provenance && hashMemoryBytes(line.body) == target.LineHash {
			matches = append(matches, line)
		}
	}
	if len(matches) == 0 {
		return ErrMemoryConflict
	}
	if len(matches) != 1 {
		return ErrMemoryAmbiguousMatch
	}
	line := matches[0]
	result := make([]byte, 0, len(raw)+MemoryEntryMaxLen)
	result = append(result, raw[:line.start]...)
	if replacement != nil {
		result = append(result, replacement.Render()...)
		result = append(result, raw[line.start+len(line.body):line.end]...) // original separator, if any
	}
	result = append(result, raw[line.end:]...)
	return s.writeExact(path, result, target.FileHash)
}

// AppendExact adds a confirmed managed entry to the snapshot the user
// reviewed. It preserves all existing bytes, including the terminal-newline
// convention, and returns a stable exact target for subsequent edits.
func (s *MemoryStore) AppendExact(workspaceID, expectedFileHash string, entry MemoryEntry) (MemoryExactTarget, error) {
	cleaned, err := validateExactMemoryEntry(entry)
	if err != nil {
		return MemoryExactTarget{}, err
	}
	if expectedFileHash == "" {
		return MemoryExactTarget{}, ErrMemoryConflict
	}
	memoryMu.Lock()
	defer memoryMu.Unlock()
	raw, path, err := s.readExact(workspaceID)
	if err != nil {
		return MemoryExactTarget{}, err
	}
	if hashMemoryBytes(raw) != expectedFileHash {
		return MemoryExactTarget{}, ErrMemoryConflict
	}
	result := appendMemoryBytes(raw, cleaned)
	if err := s.writeExact(path, result, expectedFileHash); err != nil {
		return MemoryExactTarget{}, err
	}
	return MemoryExactTarget{FileHash: hashMemoryBytes(result), LineHash: hashMemoryBytes([]byte(cleaned.Render())), Provenance: cleaned.Provenance}, nil
}

func validateExactMemoryEntry(entry MemoryEntry) (MemoryEntry, error) {
	text, err := ValidateMemoryText(entry.Text)
	if err != nil {
		return MemoryEntry{}, err
	}
	entry.Text = text
	entry.Type = NormalizeMemoryEntryType(string(entry.Type))
	if _, err := time.Parse("2006-01-02", entry.Date); err != nil {
		return MemoryEntry{}, ErrMemoryConflict
	}
	if strings.TrimSpace(entry.Provenance) == "" || len(entry.Provenance) > 200 ||
		strings.ContainsAny(entry.Provenance, "\r\n],") {
		return MemoryEntry{}, ErrMemoryConflict
	}
	return entry, nil
}

func (s *MemoryStore) readExact(workspaceID string) ([]byte, string, error) {
	if s == nil || s.resolver == nil {
		return nil, "", ErrMemoryConflict
	}
	path, err := s.filePath(workspaceID)
	if err != nil {
		return nil, "", err
	}
	raw, err := readExactPath(path)
	return raw, path, err
}

func readExactPath(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrMemoryConflict
	}
	file, err := os.Open(path) // #nosec G304 -- constant MEMORY.md under the workspace-store-resolved folder
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, ErrMemoryConflict // symlink/path substitution between Lstat and Open
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxExactMemoryBytes+1))
	if err != nil || len(raw) > maxExactMemoryBytes {
		return nil, ErrMemoryConflict
	}
	return raw, nil
}

func (s *MemoryStore) writeExact(path string, data []byte, expectedHash string) error {
	if len(data) > maxExactMemoryBytes {
		return ErrMemoryConflict
	}
	parent := filepath.Dir(path)
	tmp, err := os.CreateTemp(parent, ".memory-exact-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if s.beforeExactRename != nil {
		if err := s.beforeExactRename(); err != nil {
			return err
		}
	}
	// Another process or an external editor may have written while the temp
	// file was prepared. Never replace its newer bytes on a stale snapshot.
	current, err := readExactPath(path)
	if err != nil {
		return err
	}
	if hashMemoryBytes(current) != expectedHash {
		return ErrMemoryConflict
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace workspace memory: %w", err)
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(parent) // #nosec G304 -- parent is the server-resolved workspace folder
		if err != nil {
			return err
		}
		defer func() { _ = dir.Close() }()
		if err := dir.Sync(); err != nil {
			return err
		}
	}
	return nil
}
