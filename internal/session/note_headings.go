package session

import (
	"strings"
)

// Heading represents a single heading extracted from a note's Markdown body.
type Heading struct {
	// Level is the heading level (1 for #, 2 for ##, etc.).
	Level int
	// Text is the heading text without the leading `#` markers or surrounding whitespace.
	Text string
	// Position is the byte offset of the first `#` character in the source.
	Position int
}

// ParseHeadings extracts ATX-style headings (`# Heading`) from a Markdown source.
//
// The rules match the live-preview parser in sessions.js (regex: `^(#{1,6})\s+`)
// with one improvement: headings inside fenced code blocks (``` or ~~~) are excluded.
// The JS parser will be updated to share the same exclusion when the TOC lands in
// task 3.1 — the two implementations must stay in lockstep to avoid TOC/search drift.
//
// Setext headings (underlined with === or ---) are NOT recognised, again to match the
// existing live-preview behaviour.
func ParseHeadings(content string) []Heading {
	if content == "" {
		return nil
	}

	var (
		headings   []Heading
		offset     int
		inFence    bool
		fenceMark  byte // '`' or '~'
		fenceCount int
	)

	for {
		nl := strings.IndexByte(content[offset:], '\n')
		var line string
		if nl < 0 {
			line = content[offset:]
		} else {
			line = content[offset : offset+nl]
		}

		if fenceOpen, mark, count := detectFence(line); fenceOpen {
			if !inFence {
				inFence = true
				fenceMark = mark
				fenceCount = count
			} else if mark == fenceMark && count >= fenceCount {
				inFence = false
			}
		} else if !inFence {
			if level, text, ok := matchATXHeading(line); ok {
				headings = append(headings, Heading{
					Level:    level,
					Text:     text,
					Position: offset,
				})
			}
		}

		if nl < 0 {
			break
		}
		offset += nl + 1
	}

	return headings
}

// matchATXHeading matches the same pattern as the JS live-preview parser:
// one to six `#` characters followed by at least one whitespace character,
// then heading text. Returns (level, text, true) on a hit.
func matchATXHeading(line string) (int, string, bool) {
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	if i == 0 || i > 6 {
		return 0, "", false
	}
	if i >= len(line) {
		return 0, "", false
	}
	if line[i] != ' ' && line[i] != '\t' {
		return 0, "", false
	}
	text := strings.TrimRight(strings.TrimLeft(line[i:], " \t"), " \t")
	if text == "" {
		return 0, "", false
	}
	return i, text, true
}

// detectFence reports whether `line` opens or closes a fenced code block.
// A fence is three or more consecutive backticks or tildes at the start of the
// line (after up to three spaces of indentation, per CommonMark).
func detectFence(line string) (bool, byte, int) {
	i := 0
	for i < 3 && i < len(line) && line[i] == ' ' {
		i++
	}
	if i >= len(line) {
		return false, 0, 0
	}
	mark := line[i]
	if mark != '`' && mark != '~' {
		return false, 0, 0
	}
	count := 0
	for i < len(line) && line[i] == mark {
		i++
		count++
	}
	if count < 3 {
		return false, 0, 0
	}
	return true, mark, count
}
