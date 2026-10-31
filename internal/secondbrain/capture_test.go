package secondbrain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var captureNow = time.Date(2026, 10, 31, 14, 7, 0, 0, time.UTC)

func TestNewCapture_NamesTheNoteFromTheFirstLineAndKeepsTheText(t *testing.T) {
	capture, err := NewCapture("  Ask Dana about the pressing run!\nAnd the invoice.  ", captureNow)
	if err != nil {
		t.Fatal(err)
	}
	if capture.Stem != "20261031-1407-ask-dana-about-the-pressing-run" || capture.Rel(1) != "Inbox/20261031-1407-ask-dana-about-the-pressing-run.md" ||
		capture.Rel(3) != "Inbox/20261031-1407-ask-dana-about-the-pressing-run-3.md" {
		t.Fatalf("capture = %+v / %s / %s", capture, capture.Rel(1), capture.Rel(3))
	}
	if want := "Ask Dana about the pressing run!\nAnd the invoice.\n\nCaptured by Ori on 2026-10-31.\n"; string(capture.Body) != want {
		t.Fatalf("body = %q, want %q", capture.Body, want)
	}
	if odd, _ := NewCapture("!!! ???", captureNow); odd.Stem != "20261031-1407-note" {
		t.Fatalf("a first line with no letters named %q", odd.Stem)
	}
	if long, _ := NewCapture(strings.Repeat("word ", 100), captureNow); len(long.Stem) > 14+maxSeedSlugRunes+1 {
		t.Fatalf("a long first line made a %d-byte stem", len(long.Stem))
	}
}

func TestNewCapture_RefusesEmptyOverlongAndSecretLikeText(t *testing.T) {
	for name, tc := range map[string]struct {
		text string
		want error
	}{
		"empty":        {"", ErrCaptureInvalid},
		"blank":        {" \n\t ", ErrCaptureInvalid},
		"too long":     {strings.Repeat("a", MaxCaptureRunes+1), ErrCaptureInvalid},
		"a credential": {"remember this: sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789", ErrCaptureSecretLike},
	} {
		if _, err := NewCapture(tc.text, captureNow); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	if _, err := NewCapture(strings.Repeat("a", MaxCaptureRunes), captureNow); err != nil {
		t.Fatalf("a note at the limit: %v", err)
	}
	if err := func() error {
		_, err := NewCapture("sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789", captureNow)
		return err
	}(); strings.Contains(err.Error(), "sk-ant") {
		t.Fatalf("the refusal repeats the secret: %v", err)
	}
}

func TestNewCapture_ANoteReadsBackAsOris(t *testing.T) {
	tree := newWriterTree(t)
	capture, err := NewCapture("Pick up the vinyl proofs", captureNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newWriterFor(t, tree.vault).Create(capture.Rel(1), NoteKindCapture, capture.Body); err != nil {
		t.Fatal(err)
	}
	if mark := ReadMark([]byte(readTestFile(t, tree.vault, "Inbox", capture.Stem+".md"))); mark.Kind != NoteKindCapture || !mark.OwnedByOri {
		t.Fatalf("mark = %+v", mark)
	}
}
