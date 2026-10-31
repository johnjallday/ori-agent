package secondbrain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

// The kinds of note Ori writes into a folder it created.
const (
	NoteKindJournal = "journal"
	NoteKindCapture = "capture"
	NoteKindSeed    = "seed"
)

const (
	markerOpen  = "---\n"
	markerClose = "---\n"
)

var markerPattern = regexp.MustCompile(`\Aori: (journal|capture|seed)\nori_hash: ([0-9a-f]{64})\n\z`)

// NoteMark is what a note's first lines say about who wrote it.
type NoteMark struct {
	// Kind is the marker's kind, or "" when the note carries no marker.
	Kind string
	// OwnedByOri says the note is exactly as Ori wrote it: its marker is the
	// only frontmatter and the hash matches the body.
	OwnedByOri bool
	// UserModified is the opposite, and the writer's refusal: no marker, a
	// hash that no longer matches, or frontmatter the user added. Anything
	// Ori cannot prove it wrote counts as the user's.
	UserModified bool
}

// Marked reports whether the note carries Ori's marker at all, edited or not.
// It is what "Written by Ori" counts (FR16).
func (m NoteMark) Marked() bool { return m.Kind != "" }

// ReadMark reads a note's marker. The check is on the whole note: a body edit
// changes the hash, and a frontmatter edit (an added tag, say) changes the
// exact two-line shape, so replacing the note could never lose either.
func ReadMark(content []byte) NoteMark {
	notOri := NoteMark{UserModified: true}
	if !bytes.HasPrefix(content, []byte(markerOpen)) {
		return notOri
	}
	rest := content[len(markerOpen):]
	front, body, found := cutFrontmatter(rest)
	if !found {
		return notOri
	}
	kind, hash, ok := parseMarker(front)
	if !ok {
		return markInFrontmatter(front)
	}
	if hash != bodyHash(body) {
		return NoteMark{Kind: kind, UserModified: true}
	}
	return NoteMark{Kind: kind, OwnedByOri: true}
}

// cutFrontmatter splits what follows the opening fence at the closing one.
func cutFrontmatter(rest []byte) (front, body []byte, found bool) {
	if bytes.HasPrefix(rest, []byte(markerClose)) {
		return nil, rest[len(markerClose):], true
	}
	index := bytes.Index(rest, []byte("\n"+markerClose))
	if index < 0 {
		return nil, nil, false
	}
	return rest[:index+1], rest[index+1+len(markerClose):], true
}

func parseMarker(front []byte) (kind, hash string, ok bool) {
	match := markerPattern.FindSubmatch(front)
	if match == nil {
		return "", "", false
	}
	return string(match[1]), string(match[2]), true
}

var markerKindLine = regexp.MustCompile(`(?m)^ori: (journal|capture|seed)$`)

// markInFrontmatter handles frontmatter the user extended: Ori's kind is still
// there, so the note counts as written by Ori, but it is no longer Ori's to
// replace.
func markInFrontmatter(front []byte) NoteMark {
	match := markerKindLine.FindSubmatch(front)
	if match == nil {
		return NoteMark{UserModified: true}
	}
	return NoteMark{Kind: string(match[1]), UserModified: true}
}

func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func validNoteKind(kind string) bool {
	return kind == NoteKindJournal || kind == NoteKindCapture || kind == NoteKindSeed
}

// RenderNote is a note as Ori writes it: the marker, then the body.
func RenderNote(kind string, body []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(body) + 128)
	out.WriteString(markerOpen)
	out.WriteString("ori: ")
	out.WriteString(kind)
	out.WriteString("\nori_hash: ")
	out.WriteString(bodyHash(body))
	out.WriteString("\n")
	out.WriteString(markerClose)
	out.Write(body)
	return out.Bytes()
}
