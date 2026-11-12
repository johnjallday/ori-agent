package sessionhttp

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

// A listing of the library's scopes is bound to one revision of the Home, and the
// Library Manager writes to the Home right after every scan, so a moved revision
// is a busy moment: the read is made again, not given up.
func TestRetryLibraryRead_ABusyHomeIsReadAgainUntilItAnswers(t *testing.T) {
	for _, busy := range []error{projectlibrary.ErrConflict, projectlibrary.ErrUnavailable} {
		calls := 0
		got, err := retryLibraryRead(6, time.Millisecond, func() (string, error) {
			calls++
			if calls < 3 {
				return "", busy
			}
			return "scopes", nil
		})
		if err != nil || got != "scopes" || calls != 3 {
			t.Fatalf("busy=%v: got %q err=%v after %d calls, want scopes after 3", busy, got, err, calls)
		}
	}
}

func TestRetryLibraryRead_AnythingElseFailsAtOnce(t *testing.T) {
	calls := 0
	boom := errors.New("the library could not be read")
	if _, err := retryLibraryRead(6, time.Millisecond, func() (int, error) { calls++; return 0, boom }); !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("err=%v after %d calls, want the error after exactly 1", err, calls)
	}
}

func TestRetryLibraryRead_GivesUpAfterTheLastAttemptWithTheLastError(t *testing.T) {
	calls := 0
	_, err := retryLibraryRead(4, time.Millisecond, func() (int, error) { calls++; return 0, projectlibrary.ErrConflict })
	if !errors.Is(err, projectlibrary.ErrConflict) || calls != 4 {
		t.Fatalf("err=%v after %d calls, want the conflict after exactly 4", err, calls)
	}
}
