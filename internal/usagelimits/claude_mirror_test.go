package usagelimits

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeMirror(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	for _, tt := range []struct {
		name, body string
		age        time.Duration
		want       bool
	}{
		{"fresh", `{"model":{"id":"private"},"context_window":{},"rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1791900000},"seven_day":{"used_percentage":41.2,"resets_at":1792295868}}}`, time.Hour, true},
		{"at cutoff", `{"rate_limits":{"five_hour":{"used_percentage":0}}}`, 24 * time.Hour, true},
		{"stale", `{"rate_limits":{"five_hour":{"used_percentage":0}}}`, 24*time.Hour + time.Second, false},
		{"no limits", `{"model":{}}`, 0, false},
		{"null limits", `{"rate_limits":null}`, 0, false},
		{"empty limits", `{"rate_limits":{}}`, 0, false},
		{"missing percent", `{"rate_limits":{"five_hour":{}}}`, 0, false},
		{"negative percent", `{"rate_limits":{"five_hour":{"used_percentage":-1}}}`, 0, false},
		{"truncated", `{"rate_limits":`, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ClaudeMirrorFilename)
			if err := os.WriteFile(path, []byte(tt.body), 0600); err != nil {
				t.Fatal(err)
			}
			modified := now.Add(-tt.age)
			if err := os.Chtimes(path, modified, modified); err != nil {
				t.Fatal(err)
			}
			p, err := ReadClaudeMirror(path, now)
			if (err == nil) != tt.want {
				t.Fatalf("got %+v, %v", p, err)
			}
			if !tt.want {
				return
			}
			if p.Source != SourceClaudeStatusline || !p.FetchedAt.Equal(modified) {
				t.Fatalf("wrong provenance: %+v", p)
			}
			data, _ := json.Marshal(p)
			if strings.Contains(string(data), "private") || strings.Contains(string(data), "context_window") {
				t.Fatal("non-limit metadata escaped")
			}
		})
	}
}

func TestClaudeLiveFailureUsesMirrorButSignOutDoesNot(t *testing.T) {
	for _, code := range []int{500, 401} {
		r := testClaudeReader(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
		calls := 0
		r.mirror = func() (Provider, error) {
			calls++
			return Provider{State: StateOK, Source: SourceClaudeStatusline, FetchedAt: time.Now()}, nil
		}
		p := r.read(context.Background(), true)
		if code == 500 && (calls != 1 || p.Source != SourceClaudeStatusline || !strings.Contains(p.Detail, "status line reading from")) {
			t.Fatalf("fallback=%+v calls=%d", p, calls)
		}
		if code == 401 && (calls != 0 || p.State != StateNotSignedIn) {
			t.Fatal("auth failure must not resurrect an old mirror")
		}
	}
	r := testClaudeReader(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) })
	r.mirror = func() (Provider, error) { return Provider{}, errors.New("missing") }
	if got := r.read(context.Background(), false); got.State != StateUnavailable {
		t.Fatalf("got %+v", got)
	}
}
