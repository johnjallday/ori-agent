package usagelimits

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/authdiscovery"
)

func testClaudeReader(t *testing.T, handler http.HandlerFunc) *claudeReader {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	r := newClaudeReader()
	r.mirror = nil
	r.endpoint = server.URL
	r.discover = func(context.Context) (authdiscovery.ClaudeOauth, error) {
		return authdiscovery.ClaudeOauth{AccessToken: "invented", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}, nil
	}
	r.apiKeyPresent = func() bool { return false }
	r.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	return r
}

func TestClaudeUsageShapes(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		count      int
		state      State
		wantErr    bool
	}{
		{"flat", `{"five_hour":{"utilization":23.5,"resets_at":"2026-10-13T20:00:00Z"},"seven_day":{"utilization":85,"resets_at":"2026-10-20T20:00:00Z"},"seven_day_sonnet":{"utilization":4,"resets_at":null},"seven_day_opus":null}`, 3, StateOK, false},
		{"list", `{"limits":[{"kind":"seven_day","percent":16,"resets_at":"2026-10-20T20:00:00Z"},{"kind":"five_hour","percent":3,"resets_at":"2026-10-13T20:00:00Z"},{"kind":"weekly_opus","percent":6}]}`, 3, StateOK, false},
		{"flat with extra limits object", `{"five_hour":{"utilization":3},"limits":{"private":"invented"}}`, 1, StateOK, false},
		{"flat with future limits array", `{"five_hour":{"utilization":3},"limits":[{"type":"new-schema","value":99}]}`, 1, StateOK, false},
		{"flat with null limits", `{"five_hour":{"utilization":3},"limits":null}`, 1, StateOK, false},
		{"flat with empty limits", `{"five_hour":{"utilization":3},"limits":[]}`, 1, StateOK, false},
		{"flat takes precedence", `{"five_hour":{"utilization":3},"limits":[{"kind":"five_hour","percent":99}]}`, 1, StateOK, false},
		{"null windows with limits metadata", `{"five_hour":null,"seven_day":null,"limits":{}}`, 0, StateNoLimits, false},
		{"invalid flat cannot hide behind list", `{"five_hour":{},"limits":[{"kind":"five_hour","percent":3}]}`, 0, "", true},
		{"null windows", `{"five_hour":null,"seven_day":null}`, 0, StateNoLimits, false},
		{"empty list", `{"limits":[]}`, 0, StateNoLimits, false},
		{"null list", `{"limits":null}`, 0, StateNoLimits, false},
		{"unknown", `{"error":"invented-secret"}`, 0, "", true},
		{"empty object", `{}`, 0, "", true},
		{"wrong shape", `{"limits":{}}`, 0, "", true},
		{"missing percent", `{"five_hour":{}}`, 0, "", true},
		{"negative percent", `{"five_hour":{"utilization":-1}}`, 0, "", true},
		{"bad timestamp", `{"five_hour":{"utilization":10,"resets_at":"no"}}`, 0, "", true},
		{"unknown kind", `{"limits":[{"kind":"mystery","percent":10}]}`, 0, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := parseClaudeUsage([]byte(tt.body), time.Now())
			if (err != nil) != tt.wantErr {
				t.Fatalf("got %v", err)
			}
			if tt.wantErr {
				return
			}
			if len(p.Windows) != tt.count || p.State != tt.state {
				t.Fatalf("got %+v", p)
			}
			if strings.HasPrefix(tt.name, "flat ") && p.Windows[0].UsedPercent != 3 {
				t.Fatal("extra limits field displaced the canonical flat reading")
			}
			if tt.count > 0 && p.Windows[0].ID != "five_hour" {
				t.Fatal("windows must be shortest first")
			}
			if tt.count == 3 && !p.Windows[2].ModelSpecific {
				t.Fatal("model window must be flagged")
			}
			if tt.name == "flat" && p.Level != LevelWarn {
				t.Fatal("aggregate warning missing")
			}
		})
	}
}

func TestClaudeUsageHTTPAndPrivacy(t *testing.T) {
	r := testClaudeReader(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" || req.Header.Get("Authorization") != "Bearer invented" || req.Header.Get("anthropic-beta") != "oauth-2025-04-20" || req.Header.Get("Accept") != "application/json" {
			t.Error("incorrect request")
		}
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":20,"resets_at":"2026-10-13T20:00:00Z"},"account_id":"private","email":"private@example.test"}`))
	})
	p, err := r.fetch(context.Background())
	if err != nil || p.Source != SourceClaudeOAuthUsage {
		t.Fatalf("got %+v,%v", p, err)
	}
	data, _ := json.Marshal(p)
	for _, secret := range []string{"invented", "private", "account_id", "email", "accessToken"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
}

func TestClaudeUsageNoCallWithoutValidLogin(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "expired", "api key", "env key", "unreadable", "not installed"} {
		t.Run(mode, func(t *testing.T) {
			r := testClaudeReader(t, func(http.ResponseWriter, *http.Request) { t.Error("must not call usage endpoint") })
			r.discover = func(context.Context) (authdiscovery.ClaudeOauth, error) {
				switch mode {
				case "missing", "env key":
					return authdiscovery.ClaudeOauth{}, authdiscovery.ErrNoClaudeLogin
				case "empty":
					return authdiscovery.ClaudeOauth{}, nil
				case "unreadable":
					return authdiscovery.ClaudeOauth{}, errors.New("private")
				case "api key":
					return authdiscovery.ClaudeOauth{AccessToken: "sk-ant-api-invented"}, nil
				default:
					return authdiscovery.ClaudeOauth{AccessToken: "invented", ExpiresAt: time.Now().Add(-time.Second).UnixMilli()}, nil
				}
			}
			if mode == "env key" {
				r.apiKeyPresent = func() bool { return true }
			}
			if mode == "not installed" {
				r.lookPath = func(string) (string, error) { return "", errors.New("absent") }
			}
			p := r.read(context.Background(), true)
			if mode == "not installed" {
				if p != nil {
					t.Fatal("CLI absent must omit provider")
				}
				return
			}
			want := StateNotSignedIn
			if mode == "api key" || mode == "env key" {
				want = StateNoLimits
			}
			if mode == "unreadable" {
				want = StateUnavailable
			}
			if p.State != want {
				t.Fatalf("got %+v", p)
			}
			if mode == "expired" && p.Detail != "Open Claude Code once to refresh its login" {
				t.Fatal(p.Detail)
			}
		})
	}
}

type failedClaudeTransport struct{}

func (failedClaudeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("private transport error with invented-secret")
}

func TestClaudeUsageFailureDetailsAreActionableAndSanitized(t *testing.T) {
	for _, tt := range []struct {
		name, body, wantDetail string
		code                   int
	}{
		{"credentials", "", "Ori could not read the Claude Code login in this environment", 200},
		{"network", "", "Ori could not connect to the Claude usage endpoint", 200},
		{"rate limited", `{"error":"invented-secret"}`, "Claude usage endpoint is rate-limiting requests (HTTP 429); try again later", 429},
		{"server error", "private response body", "Claude usage endpoint returned HTTP 503; try again later", 503},
		{"shape", `{"private":"invented-secret"}`, "Claude usage endpoint returned an unrecognized response", 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := testClaudeReader(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.code)
				_, _ = w.Write([]byte(tt.body))
			})
			if tt.name == "credentials" {
				r.discover = func(context.Context) (authdiscovery.ClaudeOauth, error) {
					return authdiscovery.ClaudeOauth{}, errors.New("private credential path")
				}
			}
			if tt.name == "network" {
				r.client.Transport = failedClaudeTransport{}
			}
			p := r.read(context.Background(), true)
			if p.State != StateUnavailable || p.Detail != tt.wantDetail || len(p.Windows) != 0 {
				t.Fatalf("unexpected failure: %+v", p)
			}
			data, _ := json.Marshal(p)
			for _, private := range []string{"private", "invented-secret", "accessToken", "Authorization"} {
				if strings.Contains(string(data), private) {
					t.Fatal("failure exposed a raw error or response")
				}
			}
		})
	}
}

func TestClaudeUsageHTTPFailures(t *testing.T) {
	for _, code := range []int{401, 403, 429, 500} {
		r := testClaudeReader(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
		p, err := r.fetch(context.Background())
		if code == 401 || code == 403 {
			if err != nil || p.State != StateNotSignedIn {
				t.Fatalf("%d: %+v %v", code, p, err)
			}
		} else if err == nil {
			t.Fatalf("%d accepted", code)
		}
	}
}

func TestClaudeUsageRejectsRedirectAndForeignOverride(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	r := testClaudeReader(t, func(w http.ResponseWriter, req *http.Request) { http.Redirect(w, req, target.URL, http.StatusFound) })
	if _, err := r.fetch(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
	if forwarded.Load() != 0 {
		t.Fatal("redirect forwarded credentials")
	}
	r.endpoint = "https://example.com/usage"
	if _, err := r.fetch(context.Background()); err == nil {
		t.Fatal("foreign override accepted")
	}
}

func TestClaudeUsageTimeout(t *testing.T) {
	r := testClaudeReader(t, func(_ http.ResponseWriter, req *http.Request) { <-req.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := r.fetch(ctx)
	if err == nil || claudeFailureDetail(err) != "Claude usage request timed out; try Refresh again" {
		t.Fatalf("expected a sanitized timeout explanation, got %v", err)
	}
}
