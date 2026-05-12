package session

// note_text.go holds small line-scanning helpers shared by the heading and
// wikilink parsers. Keeping them here avoids duplicating the fence-detection
// logic between note_headings.go and note_links.go and gives a single place
// to keep the rules in lockstep with the JS parsers.

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

// scanLinesOutsideFences walks `content` line-by-line and invokes `visit` for
// every line that is NOT inside a fenced code block. `offset` is the byte
// position of the line's first character within `content`. Returning an
// error from `visit` short-circuits the scan.
func scanLinesOutsideFences(content string, visit func(line string, offset int)) {
	if content == "" {
		return
	}
	var (
		offset     int
		inFence    bool
		fenceMark  byte
		fenceCount int
	)
	for {
		// Find next newline.
		nl := -1
		for i := offset; i < len(content); i++ {
			if content[i] == '\n' {
				nl = i
				break
			}
		}
		var line string
		if nl < 0 {
			line = content[offset:]
		} else {
			line = content[offset:nl]
		}

		if open, mark, count := detectFence(line); open {
			if !inFence {
				inFence, fenceMark, fenceCount = true, mark, count
			} else if mark == fenceMark && count >= fenceCount {
				inFence = false
			}
		} else if !inFence {
			visit(line, offset)
		}

		if nl < 0 {
			break
		}
		offset = nl + 1
	}
}
