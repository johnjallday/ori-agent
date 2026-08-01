package downloadsjanitorhttp

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/downloadsjanitor"
)

// The console shows this message verbatim, so it must be a sentence written for
// a person — not a wrapped sentinel naming an internal error type and the
// retired product name.
//
// A live smoke returned: "downloads janitor candidate cannot be approved in its
// current state: payload.bin needs a category you choose before it can be
// filed." Everything before the colon is plumbing.
func TestCandidateExplanation_KeepsOnlyTheHumanHalf(t *testing.T) {
	err := fmt.Errorf("%w: payload.bin needs a category you choose before it can be filed",
		downloadsjanitor.ErrCandidateNotActionable)

	got := candidateExplanation(err)

	if strings.Contains(strings.ToLower(got), "downloads janitor") {
		t.Errorf("the retired product name reached the user: %q", got)
	}
	if strings.Contains(got, "cannot be approved in its current state") {
		t.Errorf("the sentinel reached the user: %q", got)
	}
	if got != "payload.bin needs a category you choose before it can be filed" {
		t.Errorf("explanation = %q", got)
	}
}

// A bare sentinel has no explanation to show. The user still gets a sentence
// rather than the sentinel's own text.
func TestCandidateExplanation_FallsBackToAPlainSentence(t *testing.T) {
	got := candidateExplanation(downloadsjanitor.ErrCandidateNotActionable)

	if strings.Contains(strings.ToLower(got), "downloads janitor") {
		t.Errorf("the retired product name reached the user: %q", got)
	}
	if !strings.HasSuffix(got, ".") {
		t.Errorf("expected a sentence, got %q", got)
	}
}

// The whole review surface must be free of the retired name: it is what the
// user reads when an approval is refused.
func TestReviewErrors_NeverNameDownloadsJanitor(t *testing.T) {
	sentinels := []error{
		downloadsjanitor.ErrApprovalRequired,
		downloadsjanitor.ErrApprovalConsumed,
		downloadsjanitor.ErrApprovalExpired,
		downloadsjanitor.ErrApprovalInvalid,
		downloadsjanitor.ErrCandidateNotActionable,
	}
	for _, sentinel := range sentinels {
		wrapped := fmt.Errorf("%w: something about a file", sentinel)
		if !errors.Is(wrapped, sentinel) {
			t.Fatalf("fixture is not wrapping %v", sentinel)
		}
		if strings.Contains(strings.ToLower(candidateExplanation(wrapped)), "downloads janitor") {
			t.Errorf("%v leaks the retired name", sentinel)
		}
	}
}
