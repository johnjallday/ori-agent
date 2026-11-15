package filejanitorhttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

func TestErrorResponses_NameFilingWithoutChangingInternalDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{errors.New("unavailable"), http.StatusInternalServerError},
		{filejanitor.ErrInvalidSettings, http.StatusBadRequest},
	} {
		w := httptest.NewRecorder()
		(&Handler{}).respondError(w, tc.err, "Failed to read File Janitor status")
		if w.Code != tc.status || strings.Contains(strings.ToLower(w.Body.String()), "file janitor") || !strings.Contains(strings.ToLower(w.Body.String()), "filing") {
			t.Fatalf("response = %d %s", w.Code, w.Body.String())
		}
	}
}
