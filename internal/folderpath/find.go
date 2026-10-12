package folderpath

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

// Found is a folder path a person wrote inside a longer message.
type Found struct {
	Checked
	// Text is the path as it was written, and Start and End its byte range in
	// the message, so a caller can read the message without it.
	Text       string
	Start, End int
	// Refusal is set instead of Checked when the message names a real folder
	// Ori will not open: the sentence that says why.
	Refusal string
}

// Usable reports whether the folder passed Check.
func (f Found) Usable() bool { return f.Refusal == "" && f.Path != "" }

// Bounds on the search. A message is prose, not a path list: the first few
// places a path could start, and a path of at most this many words, are enough.
const (
	maxPathStarts = 6
	maxPathWords  = 16
)

// Find looks for a folder path in a message: an absolute path, or one starting
// with "~/", that names an existing directory. A path may contain spaces, so
// from each place one could start it tries the longest run of words first and
// returns the longest existing directory. Quotes or backticks around a path
// are honoured.
//
// The second result is false when the message names no existing directory. A
// real directory that Check refuses is returned with Refusal set, so the
// surface can say why instead of pretending not to have seen it.
//
// It only ever asks whether a directory exists; it reads nothing inside one.
func Find(message string, guards filejanitor.RootGuards) (Found, bool) {
	home := strings.TrimSpace(guards.HomeDir)
	var refused *Found
	starts := 0
	for index := 0; index < len(message) && starts < maxPathStarts; index++ {
		if !pathStartsAt(message, index) {
			continue
		}
		starts++
		for _, end := range pathEnds(message, index) {
			text := message[index:end]
			if !isDirectory(expand(text, home)) {
				continue
			}
			found := Found{Text: text, Start: index, End: end}
			checked, err := Check(text, guards)
			if err != nil {
				if refused == nil {
					found.Refusal = Message(err)
					refused = &found
				}
				break // A shorter prefix is a different folder than the one named.
			}
			found.Checked = checked
			return found, true
		}
	}
	if refused != nil {
		return *refused, true
	}
	return Found{}, false
}

// Without returns the message with the found path cut out, for readers that
// must not mistake a folder's name for something else the user named.
func (f Found) Without(message string) string {
	if f.Start < 0 || f.End > len(message) || f.Start >= f.End {
		return message
	}
	return strings.TrimSpace(message[:f.Start] + " " + message[f.End:])
}

// expand turns a leading "~" into the home folder. Check does the same.
func expand(path, home string) string {
	path = strings.TrimSpace(path)
	if home != "" && (path == "~" || strings.HasPrefix(path, "~/")) {
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	return path
}

func isDirectory(path string) bool {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// pathStartsAt reports whether a path could begin at index: a "/" or "~/" at
// the start of the message, after white space, or after an opening quote or
// bracket. A slash inside a word ("and/or") or a link ("https://…") is not one.
func pathStartsAt(message string, index int) bool {
	switch {
	case message[index] == '/':
	case message[index] == '~' && index+1 < len(message) && message[index+1] == '/':
	default:
		return false
	}
	if index == 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(message[:index])
	return unicode.IsSpace(previous) || strings.ContainsRune("\"'`([<“‘", previous)
}

// closingQuote is what ends a path that an opening quote or backtick began.
var closingQuote = map[rune]rune{'"': '"', '\'': '\'', '`': '`', '“': '”', '‘': '’'}

// pathEnds lists where a path starting at index could end, longest first: the
// closing quote when it is quoted, then each word boundary on the same line.
// Trailing sentence punctuation is never part of a path.
func pathEnds(message string, index int) []int {
	line := len(message)
	if newline := strings.IndexAny(message[index:], "\r\n"); newline >= 0 {
		line = index + newline
	}
	var ends []int
	add := func(end int) {
		end = index + len(strings.TrimRightFunc(message[index:end], func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune(".,;:!?)]}>\"'`”’", r)
		}))
		if end <= index+1 {
			return
		}
		for _, seen := range ends {
			if seen == end {
				return
			}
		}
		ends = append(ends, end)
	}
	if index > 0 {
		opening, _ := utf8.DecodeLastRuneInString(message[:index])
		if closing, quoted := closingQuote[opening]; quoted {
			if offset := strings.IndexRune(message[index:line], closing); offset > 0 {
				add(index + offset)
			}
		}
	}
	// Word boundaries, capped, then tried from the longest run of words down.
	var boundaries []int
	inWord := false
	for offset, r := range message[index:line] {
		space := unicode.IsSpace(r)
		if space && inWord {
			boundaries = append(boundaries, index+offset)
			if len(boundaries) == maxPathWords {
				break
			}
		}
		inWord = !space
	}
	if inWord && len(boundaries) < maxPathWords {
		boundaries = append(boundaries, line)
	}
	for i := len(boundaries) - 1; i >= 0; i-- {
		add(boundaries[i])
	}
	return ends
}
