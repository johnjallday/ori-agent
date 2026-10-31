package secondbrain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/sensitive"
)

// MaxCaptureRunes bounds one captured note (FR15).
const MaxCaptureRunes = 4000

var (
	// ErrCaptureInvalid refuses an empty note or one over MaxCaptureRunes.
	ErrCaptureInvalid = errors.New("secondbrain: a note is 1 to 4,000 characters")
	// ErrCaptureSecretLike refuses text that looks like a credential. Nothing
	// of it is written, and the error never repeats it.
	ErrCaptureSecretLike = errors.New("secondbrain: that looks like a secret")
)

// Capture is one chat or station note ready to write to Inbox: the file name
// stem, the body, and the date it records.
type Capture struct {
	// Stem is "YYYYMMDD-HHMM-<slug>"; Rel adds the folder and extension for
	// attempt 1. Later attempts number the stem when the name is taken.
	Stem string
	Body []byte
}

// NewCapture validates text and names its note (FR15). The text is kept as the
// user wrote it, trimmed; the file name is its first line folded to a slug.
func NewCapture(text string, now time.Time) (Capture, error) {
	trimmed := strings.TrimSpace(strings.ToValidUTF8(text, ""))
	if trimmed == "" || utf8.RuneCountInString(trimmed) > MaxCaptureRunes {
		return Capture{}, ErrCaptureInvalid
	}
	if sensitive.ContainsSecretLikeText(trimmed) {
		return Capture{}, ErrCaptureSecretLike
	}
	firstLine, _, _ := strings.Cut(trimmed, "\n")
	stem := now.Format("20060102-1504") + "-" + noteSlug(firstLine, "note")
	body := trimmed + "\n\nCaptured by Ori on " + now.Format("2006-01-02") + ".\n"
	return Capture{Stem: stem, Body: []byte(body)}, nil
}

// Rel is the note's path for the nth attempt at a free name (1 is the plain
// stem).
func (c Capture) Rel(attempt int) string {
	if attempt <= 1 {
		return fmt.Sprintf("%s/%s.md", FolderInbox, c.Stem)
	}
	return fmt.Sprintf("%s/%s-%d.md", FolderInbox, c.Stem, attempt)
}
