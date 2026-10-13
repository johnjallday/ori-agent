package usagehttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/usagelimits"
)

type stubMirrorInstaller struct {
	installed bool
	calls     int
}

func (m *stubMirrorInstaller) Status() usagelimits.MirrorStatus {
	return usagelimits.MirrorStatus{Installed: m.installed, PreviousCommandPresent: true}
}
func (m *stubMirrorInstaller) Install() error { m.calls++; m.installed = true; return nil }
func (m *stubMirrorInstaller) Remove() error  { m.calls++; m.installed = false; return nil }

func TestClaudeMirrorRequiresExplicitSameOriginJSONAction(t *testing.T) {
	for _, tt := range []struct {
		name, method, host, origin, contentType, body, fetchSite string
		wantCode                                                 int
	}{
		{"install", "POST", "localhost:8943", "http://localhost:8943", "application/json", `{"action":"install"}`, "same-origin", 200},
		{"remove from local CLI", "POST", "127.0.0.1:8943", "", "application/json", `{"action":"remove"}`, "", 200},
		{"get", "GET", "localhost:8943", "", "application/json", `{"action":"install"}`, "", 405},
		{"foreign origin", "POST", "localhost:8943", "https://evil.test", "application/json", `{"action":"install"}`, "", 403},
		{"other local port", "POST", "localhost:8943", "http://localhost:9000", "application/json", `{"action":"install"}`, "", 403},
		{"rebinding", "POST", "evil.test", "", "application/json", `{"action":"install"}`, "", 403},
		{"cross-site", "POST", "localhost:8943", "", "application/json", `{"action":"install"}`, "cross-site", 403},
		{"form", "POST", "localhost:8943", "", "text/plain", `{"action":"install"}`, "", 415},
		{"unknown action", "POST", "localhost:8943", "", "application/json", `{"action":"execute"}`, "", 400},
		{"command injection", "POST", "localhost:8943", "", "application/json", `{"action":"install","command":"unsafe"}`, "", 400},
		{"trailing JSON", "POST", "localhost:8943", "", "application/json", `{"action":"install"}{}`, "", 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			installer := &stubMirrorInstaller{}
			h := NewHandler(nil)
			h.SetMirrorInstaller(installer)
			r := httptest.NewRequest(tt.method, "http://"+tt.host+"/api/usage/limits/claude-mirror", strings.NewReader(tt.body))
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("Content-Type", tt.contentType)
			r.Header.Set("Sec-Fetch-Site", tt.fetchSite)
			w := httptest.NewRecorder()
			h.ClaudeMirror(w, r)
			if w.Code != tt.wantCode {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if (installer.calls == 1) != (tt.wantCode == http.StatusOK) {
				t.Fatal("rejected request reached the installer")
			}
			if w.Code == 200 && (strings.Contains(w.Body.String(), "command\"") || strings.Contains(w.Body.String(), "settings")) {
				t.Fatal("response must not expose the previous statusLine value")
			}
		})
	}
}
