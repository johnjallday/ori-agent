package externalmemory

import (
	"bufio"
	"io"
	"regexp"
	"strings"
)

// The memory files are markdown written by other agents. The splitter below
// knows just enough of it to cut them into entries: ATX headings, fenced code,
// list items with their nested lines, and paragraphs.

// mdSection is a heading and the lines up to the next heading of any level. The
// lines before the first heading form a level-0 section.
type mdSection struct {
	Level   int
	Heading string
	// Path holds the heading texts from the outermost level down to this one.
	Path  []string
	Lines []string
	// Line is the 1-based line number of the heading (or 1 for the preamble).
	Line int
}

// Block kinds.
const (
	blockBullet    = "bullet"
	blockNumbered  = "numbered"
	blockParagraph = "paragraph"
)

// mdBlock is one list item (with its nested lines) or one paragraph.
type mdBlock struct {
	Kind string
	// Text is the block with the first line's list marker removed.
	Text string
}

// maxMarkdownLine bounds one line the scanner accepts; a longer line fails the
// file rather than the sync.
const maxMarkdownLine = 4 << 20

var (
	headingPattern  = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t#]*$`)
	bulletPattern   = regexp.MustCompile(`^([ \t]*)([-*+])[ \t]+`)
	numberedPattern = regexp.MustCompile(`^([ \t]*)(\d{1,9})[.)][ \t]+`)
	checkboxPattern = regexp.MustCompile(`^\[[ xX]\][ \t]+`)
	rulePattern     = regexp.MustCompile(`^[ \t]*(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	fencePattern    = regexp.MustCompile("^[ \t]*(```|~~~)")
)

// readSections streams r line by line and groups it into sections. Headings
// inside fenced code are text, not headings.
func readSections(r io.Reader) ([]mdSection, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMarkdownLine)
	sections := []mdSection{{Line: 1}}
	var stack []string
	fence := ""
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimRight(scanner.Text(), " \t\r")
		if fence == "" {
			if m := headingPattern.FindStringSubmatch(line); m != nil {
				level := len(m[1])
				if len(stack) >= level {
					stack = stack[:level-1]
				}
				for len(stack) < level-1 {
					stack = append(stack, "")
				}
				heading := strings.TrimSpace(m[2])
				stack = append(stack, heading)
				sections = append(sections, mdSection{
					Level:   level,
					Heading: heading,
					Path:    append([]string(nil), stack...),
					Line:    lineNo,
				})
				continue
			}
		}
		if m := fencePattern.FindStringSubmatch(line); m != nil {
			switch fence {
			case "":
				fence = m[1]
			case m[1]:
				fence = ""
			}
		}
		last := &sections[len(sections)-1]
		last.Lines = append(last.Lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return sections, nil
}

// sectionText returns section i's own lines followed by every deeper section
// nested under it, headings included, as one trimmed text.
func sectionText(sections []mdSection, i int) string {
	var b strings.Builder
	b.WriteString(strings.Join(sections[i].Lines, "\n"))
	for j := i + 1; j < len(sections) && sections[j].Level > sections[i].Level; j++ {
		b.WriteString("\n" + strings.Repeat("#", sections[j].Level) + " " + sections[j].Heading + "\n")
		b.WriteString(strings.Join(sections[j].Lines, "\n"))
	}
	return strings.TrimSpace(b.String())
}

// splitBlocks cuts lines into list items and paragraphs. A list item keeps the
// nested lines under it (indented items, continuation lines, and indented text
// after a blank line); a blank line ends a paragraph; a horizontal rule ends
// any block. Fenced code stays inside the block it starts in.
func splitBlocks(lines []string) []mdBlock {
	var blocks []mdBlock
	var current []string
	kind := ""
	baseIndent := 0
	blankSeen := false
	fence := ""

	flush := func() {
		if len(current) > 0 {
			text := strings.TrimSpace(strings.Join(current, "\n"))
			if text != "" {
				blocks = append(blocks, mdBlock{Kind: kind, Text: text})
			}
		}
		current, kind, blankSeen = nil, "", false
	}

	for _, line := range lines {
		if fence != "" {
			current = append(current, line)
			if m := fencePattern.FindStringSubmatch(line); m != nil && m[1] == fence {
				fence = ""
			}
			continue
		}
		if m := fencePattern.FindStringSubmatch(line); m != nil {
			if kind == "" || (blankSeen && kind == blockParagraph) {
				flush()
				kind = blockParagraph
			}
			fence = m[1]
			current = append(current, line)
			blankSeen = false
			continue
		}
		if strings.TrimSpace(line) == "" {
			if kind == blockParagraph {
				flush()
				continue
			}
			blankSeen = true
			continue
		}
		if rulePattern.MatchString(line) {
			flush()
			continue
		}
		indent := indentWidth(line)
		itemKind, marker := listItem(line)
		switch {
		case itemKind != "" && (kind == "" || kind == blockParagraph || indent <= baseIndent):
			flush()
			kind, baseIndent = itemKind, indent
			current = []string{strings.TrimSpace(line[len(marker):])}
		case kind == blockBullet || kind == blockNumbered:
			if blankSeen && indent <= baseIndent {
				flush()
				kind = blockParagraph
				current = []string{strings.TrimSpace(line)}
				continue
			}
			current = append(current, line)
			blankSeen = false
		default:
			if kind == "" {
				kind = blockParagraph
			}
			current = append(current, strings.TrimSpace(line))
		}
	}
	flush()
	return blocks
}

// listItem reports a list item's kind and its full marker (indent included).
func listItem(line string) (string, string) {
	if m := bulletPattern.FindString(line); m != "" {
		return blockBullet, m
	}
	if m := numberedPattern.FindString(line); m != "" {
		return blockNumbered, m
	}
	return "", ""
}

func indentWidth(line string) int {
	width := 0
	for _, r := range line {
		switch r {
		case ' ':
			width++
		case '\t':
			width += 4
		default:
			return width
		}
	}
	return width
}

// stripListMarker removes a leading bullet, number, or task checkbox.
func stripListMarker(line string) string {
	line = strings.TrimSpace(line)
	if _, marker := listItem(line); marker != "" {
		line = strings.TrimSpace(line[len(marker):])
	}
	return strings.TrimSpace(checkboxPattern.ReplaceAllString(line, ""))
}

// headerFields reads "key: value" lines (Codex's file headers and thread
// metadata) into a map. The first occurrence of a key wins; keys are
// lowercased.
func headerFields(lines []string) map[string]string {
	fields := map[string]string{}
	for _, line := range lines {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" || strings.ContainsAny(key, " \t`*#[") {
			continue
		}
		if _, seen := fields[key]; !seen {
			fields[key] = strings.TrimSpace(value)
		}
	}
	return fields
}
