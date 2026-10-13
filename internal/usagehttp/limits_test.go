package usagehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/usagelimits"
)

func TestGetLimitsShapeCacheAndPrivacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("PATH", home)
	for name, data := range map[string]string{
		"codex":     "#!/bin/sh\nexit 1\n",
		"auth.json": `{"tokens":{"access_token":"invented-secret","account_id":"private-account","email":"private@example.test"}}`,
	} {
		mode := os.FileMode(0600)
		if name == "codex" {
			mode = 0700
		}
		if err := os.WriteFile(filepath.Join(home, name), []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	logDir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(logDir, 0750); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "rollout-fixture.jsonl")
	event := `{"timestamp":"2026-10-13T01:08:31Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"plan_type":"prolite","primary":{"used_percent":16,"window_minutes":10080,"resets_at":1792295868},"access_token":"invented-secret","account_id":"private-account","email":"private@example.test"}}}`
	if err := os.WriteFile(logPath, []byte(event), 0600); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil)
	h.SetLimitsService(usagelimits.NewService())
	get := func() usagelimits.Snapshot {
		t.Helper()
		rr := httptest.NewRecorder()
		h.GetLimits(rr, httptest.NewRequest(http.MethodGet, "/api/usage/limits", nil))
		if rr.Code != http.StatusOK || rr.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("response: %v", rr)
		}
		for _, private := range []string{"access_token", "account_id", "email", "invented-secret", "private-account", "private@example.test"} {
			if strings.Contains(rr.Body.String(), private) {
				t.Fatalf("private field leaked: %s", private)
			}
		}
		var result usagelimits.Snapshot
		if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.GeneratedAt.IsZero() || len(result.Providers) != 1 {
			t.Fatalf("shape = %+v", result)
		}
		return result
	}
	first := get()
	if first.Providers[0].Windows[0].ID != "weekly" {
		t.Fatalf("wrong window: %+v", first)
	}
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	second := get()
	if !second.GeneratedAt.Equal(first.GeneratedAt) || second.Providers[0].State != usagelimits.StateOK {
		t.Fatal("expected cached response after log removal")
	}
}

func TestGetLimitsLiveRefreshFloor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("PATH", home)
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"invented"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf x >> "$CODEX_HOME/calls"
read -r init
printf '%s\n' '{"id":1,"result":{}}'
read -r initialized
read -r request
printf '%s\n' '{"id":2,"result":{"rateLimits":{"planType":"prolite","primary":{"usedPercent":16,"windowDurationMins":10080,"resetsAt":1792295868}}}}'
read -r hold
`
	if err := os.WriteFile(filepath.Join(home, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil)
	h.SetLimitsService(usagelimits.NewService())
	var first time.Time
	for range 2 {
		rr := httptest.NewRecorder()
		h.GetLimits(rr, httptest.NewRequest(http.MethodGet, "/api/usage/limits?refresh=1", nil))
		var snapshot usagelimits.Snapshot
		if err := json.Unmarshal(rr.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Providers) != 1 || snapshot.Providers[0].Source != usagelimits.SourceCodexAppServer {
			t.Fatalf("got %+v", snapshot)
		}
		fetched := snapshot.Providers[0].FetchedAt
		if first.IsZero() {
			first = fetched
		} else if !fetched.Equal(first) {
			t.Fatal("refresh inside floor fetched again")
		}
	}
	calls, err := os.ReadFile(filepath.Join(home, "calls"))
	if err != nil || string(calls) != "x" {
		t.Fatalf("calls=%q, err=%v", calls, err)
	}
}

type limitsSpy struct{ refresh bool }

func (s *limitsSpy) Snapshot(_ context.Context, refresh bool) usagelimits.Snapshot {
	s.refresh = refresh
	return usagelimits.Snapshot{GeneratedAt: time.Now().UTC(), Providers: []usagelimits.Provider{}}
}

func TestGetLimitsRefreshQueryAndMethod(t *testing.T) {
	h := NewHandler(nil)
	spy := &limitsSpy{}
	h.SetLimitsService(spy)
	for _, query := range []string{"", "?refresh=1", "?refresh=true"} {
		rr := httptest.NewRecorder()
		h.GetLimits(rr, httptest.NewRequest(http.MethodGet, "/api/usage/limits"+query, nil))
		if rr.Code != http.StatusOK || spy.refresh != (query == "?refresh=1") {
			t.Fatalf("query %q: code %d, refresh %v", query, rr.Code, spy.refresh)
		}
	}
	rr := httptest.NewRecorder()
	h.GetLimits(rr, httptest.NewRequest(http.MethodPost, "/api/usage/limits", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d", rr.Code)
	}
}
