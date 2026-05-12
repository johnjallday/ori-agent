package session

import (
	"strings"
)

// Wikilink is one `[[Target]]` or `[[Target|Display]]` reference extracted
// from a note's Markdown body.
type Wikilink struct {
	// Target is the text inside the `[[…]]` before any pipe — typically the
	// title of another note.
	Target string
	// Display is the optional pipe-delimited display text. Empty string means
	// the rendered link should use Target as its label.
	Display string
	// Position is the byte offset of the leading `[` in the source.
	Position int
}

// ParseWikilinks extracts `[[Target]]` and `[[Target|Display]]` references
// from `content`. Skips matches inside fenced code blocks. Designed to stay
// in lockstep with the JS parser in note-wikilinks.js.
//
// Rules:
//   - Inner `[`, `]`, or `|` are not allowed in either Target or Display.
//   - Empty Target (`[[]]`) is rejected.
//   - Whitespace around Target/Display is trimmed; an all-whitespace Target
//     after trim is rejected.
//   - Multiple wikilinks on the same line are all returned.
func ParseWikilinks(content string) []Wikilink {
	if content == "" {
		return nil
	}
	var out []Wikilink
	scanLinesOutsideFences(content, func(line string, lineOffset int) {
		for _, m := range scanLineForWikilinks(line) {
			out = append(out, Wikilink{
				Target:   m.target,
				Display:  m.display,
				Position: lineOffset + m.start,
			})
		}
	})
	return out
}

type rawWikilink struct {
	target  string
	display string
	start   int // byte offset of the leading `[` within the line
}

// scanLineForWikilinks walks a single line character-by-character. Avoiding a
// regex keeps behavior identical to the JS parser (where regex flavors differ
// subtly) and lets us be precise about which characters are allowed.
func scanLineForWikilinks(line string) []rawWikilink {
	var out []rawWikilink
	i := 0
	for i < len(line)-1 {
		if line[i] != '[' || line[i+1] != '[' {
			i++
			continue
		}
		open := i
		i += 2

		// Read target up to '|' or ']]' or another '[' / ']'.
		targetStart := i
		var pipeAt = -1
		end := -1
		for j := i; j < len(line)-1; j++ {
			c := line[j]
			if c == '[' || c == ']' {
				if c == ']' && line[j+1] == ']' {
					end = j
				}
				break
			}
			if c == '|' && pipeAt == -1 {
				pipeAt = j
			}
		}
		if end < 0 {
			i = open + 1
			continue
		}

		var target, display string
		if pipeAt >= 0 && pipeAt < end {
			target = strings.TrimSpace(line[targetStart:pipeAt])
			display = strings.TrimSpace(line[pipeAt+1 : end])
		} else {
			target = strings.TrimSpace(line[targetStart:end])
		}

		if target != "" {
			out = append(out, rawWikilink{
				target:  target,
				display: display,
				start:   open,
			})
		}
		i = end + 2
	}
	return out
}
