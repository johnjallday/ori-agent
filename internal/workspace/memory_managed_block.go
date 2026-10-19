package workspace

import (
	"errors"
	"strings"
)

// Root context files (AGENTS.md, CLAUDE.md, GEMINI.md in a workspace folder)
// belong to the user. When they opt in, Ori keeps one managed block in each,
// between these markers, and never changes a byte outside them (PRD FR24).
const (
	MemoryBlockStart = "<!-- ori:workspace-memory:start -->"
	MemoryBlockEnd   = "<!-- ori:workspace-memory:end -->"
)

// ErrManagedBlockMalformed refuses a file whose markers are out of order,
// unclosed, or repeated: guessing there could overwrite the user's text.
var ErrManagedBlockMalformed = errors.New("the file's Ori memory markers are malformed; fix or remove them by hand")

// ApplyManagedMemoryBlock computes a root context file's next content.
// existing and exists describe the file now; block is the managed text to
// hold ("" removes the block). It returns the content to write and keep=true,
// or keep=false when the file should not exist: it was missing and there is
// nothing to add, or removing the block left only whitespace.
//
//   - missing file: created with only the block.
//   - file without markers: the block is appended after a blank line.
//   - file with markers: only the text between them is replaced.
//   - removing: the block and the blank line Ori added before it go; a file
//     left whitespace-only is deleted.
func ApplyManagedMemoryBlock(existing string, exists bool, block string) (string, bool, error) {
	block = strings.TrimSpace(block)
	managed := ""
	if block != "" {
		managed = MemoryBlockStart + "\n" + block + "\n" + MemoryBlockEnd + "\n"
	}
	if !exists {
		return managed, managed != "", nil
	}

	start := strings.Index(existing, MemoryBlockStart)
	end := strings.Index(existing, MemoryBlockEnd)
	switch {
	case start < 0 && end < 0:
		if managed == "" {
			return existing, true, nil
		}
		if strings.TrimSpace(existing) == "" {
			return managed, true, nil
		}
		if !strings.HasSuffix(existing, "\n") {
			existing += "\n"
		}
		return existing + "\n" + managed, true, nil
	case start < 0 || end < 0 || end < start ||
		strings.Count(existing, MemoryBlockStart) != 1 || strings.Count(existing, MemoryBlockEnd) != 1:
		return "", false, ErrManagedBlockMalformed
	}

	before := existing[:start]
	after := existing[end+len(MemoryBlockEnd):]
	after = strings.TrimPrefix(after, "\n")
	if managed != "" {
		return before + managed + after, true, nil
	}
	// Removing: also drop the separator Ori added when it appended.
	if strings.HasSuffix(before, "\n\n") {
		before = before[:len(before)-1]
	}
	next := before + after
	if strings.TrimSpace(next) == "" {
		return "", false, nil
	}
	return next, true, nil
}
