package settingsreset

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

const cancelTestHost = "127.0.0.1:8765"

type cancelCall struct {
	peer, host, origin, fetchSite, token, contentType, requestedWith string
	body                                                             string
	id                                                               string
	method                                                           string
	localPort                                                        string
}

func goodCall(b *blockedIncident, token string) cancelCall {
	return cancelCall{
		peer: "127.0.0.1:50000", host: cancelTestHost, origin: "http://" + cancelTestHost, token: token,
		contentType: "application/json", requestedWith: "XMLHttpRequest",
		body: `{"revision":3}`, id: b.OperationID, method: http.MethodPost,
	}
}

func (c cancelCall) request() *http.Request {
	r := httptest.NewRequest(c.method, "http://"+c.host+"/api/reset/operations/"+c.id+"/cancel", strings.NewReader(c.body))
	r.RemoteAddr = c.peer
	if c.origin != "" {
		r.Header.Set("Origin", c.origin)
	}
	if c.fetchSite != "" {
		r.Header.Set("Sec-Fetch-Site", c.fetchSite)
	}
	if c.token != "" {
		r.Header.Set(CancelTokenHeader, c.token)
	}
	if c.contentType != "" {
		r.Header.Set("Content-Type", c.contentType)
	}
	if c.requestedWith != "" {
		r.Header.Set("X-Requested-With", c.requestedWith)
	}
	r.SetPathValue("id", c.id)
	if c.localPort != "" {
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: mustPort(c.localPort)}))
	}
	return r
}

func mustPort(p string) int {
	var n int
	for _, ch := range p {
		n = n*10 + int(ch-'0')
	}
	return n
}

func do(h *cancelHost, call cancelCall) (int, cancelResponse) {
	w := httptest.NewRecorder()
	h.handle(w, call.request())
	var response cancelResponse
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	return w.Code, response
}

func TestCancelEndpointRefusesEveryGuardFailureWithoutWriting(t *testing.T) {
	b := newBlockedIncident(t, true)
	h := newCancelHost(b.lease, nil)
	receipt := b.bytes(t, resetstate.OperationRecord)
	cases := []struct {
		name   string
		mutate func(*cancelCall)
		status int
		code   string
	}{
		{"a remote peer", func(c *cancelCall) { c.peer = "203.0.113.9:4000" }, 403, "forbidden_origin"},
		{"a rebound DNS name", func(c *cancelCall) {
			c.host = "ori.attacker.example:8765"
			c.origin = "http://ori.attacker.example:8765"
		}, 403, "forbidden_origin"},
		{"a LAN address as the host", func(c *cancelCall) { c.host = "192.168.1.20:8765"; c.origin = "http://192.168.1.20:8765" }, 403, "forbidden_origin"},
		{"a host without a port", func(c *cancelCall) { c.host = "localhost"; c.origin = "http://localhost" }, 403, "forbidden_origin"},
		{"a host naming another port than the listener", func(c *cancelCall) { c.localPort = "9999" }, 403, "forbidden_origin"},
		{"no Origin", func(c *cancelCall) { c.origin = "" }, 403, "forbidden_origin"},
		{"a foreign Origin", func(c *cancelCall) { c.origin = "https://evil.example" }, 403, "forbidden_origin"},
		{"a https Origin", func(c *cancelCall) { c.origin = "https://" + cancelTestHost }, 403, "forbidden_origin"},
		{"a cross-site fetch", func(c *cancelCall) { c.fetchSite = "cross-site" }, 403, "forbidden_origin"},
		{"a same-site but not same-origin fetch", func(c *cancelCall) { c.fetchSite = "same-site" }, 403, "forbidden_origin"},
		{"no XMLHttpRequest header", func(c *cancelCall) { c.requestedWith = "" }, 400, "invalid_request"},
		{"a form content type", func(c *cancelCall) { c.contentType = "application/x-www-form-urlencoded" }, 400, "invalid_request"},
		{"no token", func(c *cancelCall) { c.token = "" }, 403, "forbidden_token"},
		{"a wrong token", func(c *cancelCall) { c.token = strings.Repeat("a", len(h.token)) }, 403, "forbidden_token"},
		{"a short token", func(c *cancelCall) { c.token = h.token[:4] }, 403, "forbidden_token"},
		{"an extra body field", func(c *cancelCall) { c.body = `{"revision":3,"categories":["settings"]}` }, 400, "invalid_request"},
		{"a client-supplied path", func(c *cancelCall) { c.body = `{"revision":3,"path":"/etc"}` }, 400, "invalid_request"},
		{"no revision", func(c *cancelCall) { c.body = `{}` }, 400, "invalid_request"},
		{"revision zero", func(c *cancelCall) { c.body = `{"revision":0}` }, 400, "invalid_request"},
		{"a string revision", func(c *cancelCall) { c.body = `{"revision":"3"}` }, 400, "invalid_request"},
		{"trailing data", func(c *cancelCall) { c.body = `{"revision":3}{"revision":3}` }, 400, "invalid_request"},
		{"an empty body", func(c *cancelCall) { c.body = `` }, 400, "invalid_request"},
		{"a malformed operation id", func(c *cancelCall) { c.id = "../escape" }, 400, "invalid_request"},
		{"a stale revision", func(c *cancelCall) { c.body = `{"revision":2}` }, 409, "stale"},
		{"a revision from the future", func(c *cancelCall) { c.body = `{"revision":4}` }, 409, "stale"},
		{"another operation's id", func(c *cancelCall) { c.id = "another-operation-id" }, 409, "stale"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := goodCall(b, h.token)
			tc.mutate(&call)
			status, response := do(h, call)
			if status != tc.status || response.Code != tc.code || response.Canceled || response.Operation != nil {
				t.Fatalf("status %d, response %+v; want %d %s", status, response, tc.status, tc.code)
			}
			if !bytes.Equal(receipt, b.bytes(t, resetstate.OperationRecord)) {
				t.Fatal("a refused request wrote the receipt")
			}
		})
	}
	// Control: the unmodified call is accepted, so every refusal above is the
	// mutation's doing and not an unrelated defect of the fixture.
	if status, response := do(h, goodCall(b, h.token)); status != 200 || !response.Canceled {
		t.Fatalf("control request = %d %+v", status, response)
	}
}

func TestCancelEndpointOnlyAcceptsPOSTAndIsAbsentFromTheNormalApp(t *testing.T) {
	b := newBlockedIncident(t, true)
	handler := NewRecoveryHandlerWith(b.lease, nil)
	receipt := b.bytes(t, resetstate.OperationRecord)
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/api/reset/operations/"+b.OperationID+"/cancel", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405", method, w.Code)
		}
	}
	if !bytes.Equal(receipt, b.bytes(t, resetstate.OperationRecord)) {
		t.Fatal("a non-POST request wrote the receipt")
	}
	// The normal application mounts only the status routes.
	normal := http.NewServeMux()
	RegisterResetStatusRoutes(normal, b.lease)
	w := httptest.NewRecorder()
	normal.ServeHTTP(w, goodCall(b, "x").request())
	if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("the normal app routed the cancel POST: %d", w.Code)
	}
	if !bytes.Equal(receipt, b.bytes(t, resetstate.OperationRecord)) {
		t.Fatal("the normal app's status routes wrote the receipt")
	}
}

func TestCancelEndpointCancelsOnceAndHandsOffOnlyAfterTheDurableReply(t *testing.T) {
	b := newBlockedIncident(t, true)
	var enqueued []string
	handoff := NewRestartHandoff(func() bool { return true }, func(id string) { enqueued = append(enqueued, id) })
	h := newCancelHost(b.lease, handoff)

	status, first := do(h, goodCall(b, h.token))
	if status != 200 || !first.Canceled || first.Operation == nil || first.Operation.State != StateCanceled || first.Operation.Revision != 4 ||
		!first.Operation.Restart.Managed {
		t.Fatalf("first = %d %+v", status, first)
	}
	if j := b.receipt(t, b.lease); j.Operation.State != StateCanceled {
		t.Fatalf("durable receipt = %s", j.Operation.State)
	}
	receipt := b.bytes(t, resetstate.OperationRecord)
	// Duplicate clicks, even from a second tab, are harmless and enqueue no
	// second restart.
	for range 3 {
		status, again := do(h, goodCall(b, h.token))
		if status != 200 || !again.Canceled || again.Operation.Revision != 4 || !again.Operation.UpdatedAt.Equal(first.Operation.UpdatedAt) {
			t.Fatalf("duplicate = %d %+v", status, again)
		}
	}
	if !bytes.Equal(receipt, b.bytes(t, resetstate.OperationRecord)) {
		t.Fatal("a duplicate click rewrote the receipt")
	}
	if len(enqueued) != 1 || enqueued[0] != b.OperationID {
		t.Fatalf("restart handoffs = %v, want exactly one for %s", enqueued, b.OperationID)
	}
}

func TestCancelEndpointNeverHandsOffARefusedOrUnconfirmedCancel(t *testing.T) {
	var enqueued atomic.Int32
	handoff := NewRestartHandoff(func() bool { return true }, func(string) { enqueued.Add(1) })

	t.Run("refused", func(t *testing.T) {
		b := newBlockedIncident(t, true)
		h := newCancelHost(b.lease, handoff)
		call := goodCall(b, h.token)
		call.body = `{"revision":9}`
		if status, _ := do(h, call); status != 409 || enqueued.Load() != 0 {
			t.Fatalf("status %d, handoffs %d", status, enqueued.Load())
		}
	})
	t.Run("ineligible: the evidence no longer proves a pre-apply block", func(t *testing.T) {
		b := newBlockedIncident(t, false)
		mustPreview(t, writeStartupPolicy(b.lease, b.OperationID, []CategoryID{CategoryAgents, CategoryAppRecords}, time.Now()))
		h := newCancelHost(b.lease, handoff)
		status, response := do(h, goodCall(b, h.token))
		if status != 409 || response.Code != "not_cancelable" || response.Canceled || enqueued.Load() != 0 {
			t.Fatalf("status %d %+v, handoffs %d", status, response, enqueued.Load())
		}
		if strings.Contains(response.Message, string(CancelPolicyThisOperation)) {
			t.Fatal("a refusal leaked an internal reason code")
		}
	})
	t.Run("a write that cannot be confirmed", func(t *testing.T) {
		b := newBlockedIncident(t, true)
		h := newCancelHost(b.lease, handoff)
		path := filepath.Join(b.DataDir, resetstate.Directory, string(resetstate.OperationRecord))
		// Make the write itself fail: after the host decision, replace the
		// receipt by a directory. The deterministic fault needs no privileges.
		h.coordinator = func() *Coordinator {
			c := NewCoordinator(b.lease, nil, nil)
			c.now = func() time.Time {
				mustPreview(t, os.Remove(path))
				mustPreview(t, os.Mkdir(path, 0o700))
				return time.Now()
			}
			return c
		}
		status, response := do(h, goodCall(b, h.token))
		if status != 503 || response.Code != "not_confirmed" || response.Canceled || response.Operation != nil || enqueued.Load() != 0 {
			t.Fatalf("status %d %+v, handoffs %d", status, response, enqueued.Load())
		}
		if !b.lease.Uncertain() {
			t.Fatal("an unconfirmed write did not latch uncertainty")
		}
	})
}

// A token minted by one recovery process is worthless to another: a tab left
// open across a relaunch cannot act on the new process.
func TestCancelTokenDoesNotSurviveARestart(t *testing.T) {
	b := newBlockedIncident(t, true)
	old := newCancelHost(b.lease, nil)
	fresh := newCancelHost(b.lease, nil)
	if old.token == fresh.token || len(old.token) < 16 {
		t.Fatalf("tokens must be long and per-process: %q %q", old.token, fresh.token)
	}
	if status, response := do(fresh, goodCall(b, old.token)); status != 403 || response.Code != "forbidden_token" {
		t.Fatalf("a previous process's token was accepted: %d %+v", status, response)
	}
}

// ---- the page and the read-only evidence -------------------------------------

func recoveryGET(handler http.Handler, path string) (int, string) {
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w.Code, w.Body.String()
}

func TestRecoveryPageOffersCancelOnlyWhenTheServerFoundItSafe(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T) *blockedIncident
		offer bool
		text  string
	}{
		{"the incident: workspace emptied", func(t *testing.T) *blockedIncident { return newBlockedIncident(t, true) }, true, "is now empty"},
		{"the folder is missing", func(t *testing.T) *blockedIncident {
			s := stageStartFresh(t)
			s.Fixture.EmptyRetainedFolder(t, "projects", false)
			return &blockedIncident{stagedStartFresh: s, lease: s.blockOnWorkspaceChange(t)}
		}, true, "Ori can&#39;t find a folder"},
		{"the policy names this operation", func(t *testing.T) *blockedIncident {
			b := newBlockedIncident(t, false)
			mustPreview(t, writeStartupPolicy(b.lease, b.OperationID, []CategoryID{CategoryAgents, CategoryAppRecords}, time.Now()))
			return b
		}, false, ""},
		{"an interrupted apply, blocked at revision 4", func(t *testing.T) *blockedIncident {
			s := stageStartFresh(t)
			s.crashDuringFirstCategory(t, true)
			s.Fixture.EmptyRetainedFolder(t, "projects", true)
			return &blockedIncident{stagedStartFresh: s, lease: s.blockOnWorkspaceChange(t)}
		}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.setup(t)
			receipt := b.bytes(t, resetstate.OperationRecord)
			handler := NewRecoveryHandlerWith(b.lease, nil)
			code, page := recoveryGET(handler, ResetStatusPath+"?operation="+b.OperationID)
			if code != http.StatusOK {
				t.Fatalf("page = %d", code)
			}
			hasSection := !strings.Contains(page, `id="reset-cancel" class="cancel" aria-labelledby="cancel-heading" hidden`)
			if hasSection != tc.offer {
				t.Fatalf("cancel section visible = %t, want %t\n%s", hasSection, tc.offer, page)
			}
			code, api := recoveryGET(handler, "/api/reset/operations/"+b.OperationID)
			var evidence recoveryEvidence
			mustPreview(t, json.Unmarshal([]byte(api), &evidence))
			if code != http.StatusOK || (evidence.Cancel != nil) != tc.offer {
				t.Fatalf("API offer = %+v, want offer=%t", evidence.Cancel, tc.offer)
			}
			if tc.offer {
				for _, want := range []string{tc.text, "No part of this reset was applied", "does not restore anything that was removed outside Ori", "Canceled is not completed", "Cancel reset"} {
					if !strings.Contains(page, want) {
						t.Errorf("page omitted %q", want)
					}
				}
				if strings.Contains(strings.ToLower(page), "deleted") {
					t.Error("the page claims a deletion Ori cannot prove")
				}
				if evidence.Cancel.Revision != 3 || evidence.Cancel.Token == "" {
					t.Fatalf("offer = %+v", evidence.Cancel)
				}
			}
			// Reading and offering never writes.
			if !bytes.Equal(receipt, b.bytes(t, resetstate.OperationRecord)) {
				t.Fatal("rendering the recovery page wrote the receipt")
			}
			// An ineligible receipt keeps the existing recovery guidance.
			if !tc.offer && !strings.Contains(page, "How to restart Ori") {
				t.Fatal("recovery guidance disappeared")
			}
		})
	}
}

func TestRecoveryPageAfterCancellationSaysCanceledNotCompleted(t *testing.T) {
	b := newBlockedIncident(t, true)
	_, err := b.coordinator().Cancel(t.Context(), b.request())
	mustPreview(t, err)

	t.Run("recovery host: still needs one full restart", func(t *testing.T) {
		code, page := recoveryGET(NewRecoveryHandlerWith(b.lease, nil), ResetStatusPath+"?operation="+b.OperationID)
		if code != http.StatusOK {
			t.Fatalf("page = %d", code)
		}
		for _, want := range []string{"Reset canceled", "Nothing was reset, removed or restored", "one full restart", "How to restart Ori", "Control+C", "Quit"} {
			if !strings.Contains(page, want) {
				t.Errorf("page omitted %q", want)
			}
		}
		for _, banned := range []string{"Reset completed", "Reset receipt found"} {
			if strings.Contains(page, banned) {
				t.Errorf("a canceled receipt reads as %q", banned)
			}
		}
		if !strings.Contains(page, `id="reset-cancel" class="cancel" aria-labelledby="cancel-heading" hidden`) {
			t.Error("a canceled receipt still offers cancellation")
		}
		if !strings.Contains(page, `id="reset-results" hidden`) {
			t.Error("a canceled receipt lists pending categories as if they were results")
		}
		_, api := recoveryGET(NewRecoveryHandlerWith(b.lease, nil), "/api/reset/operations/"+b.OperationID)
		var evidence recoveryEvidence
		mustPreview(t, json.Unmarshal([]byte(api), &evidence))
		if evidence.Success || evidence.Cancel != nil || evidence.Operation.State != StateCanceled || evidence.Operation.Restart.Managed {
			t.Fatalf("evidence = %+v", evidence)
		}
	})
	t.Run("recovery host with a working restart handoff says managed", func(t *testing.T) {
		handoff := NewRestartHandoff(func() bool { return true }, func(string) {})
		_, api := recoveryGET(NewRecoveryHandlerWith(b.lease, handoff), "/api/reset/operations/"+b.OperationID)
		var evidence recoveryEvidence
		mustPreview(t, json.Unmarshal([]byte(api), &evidence))
		if !evidence.Operation.Restart.Managed {
			t.Fatal("a host that can reopen Ori did not say so")
		}
	})
	t.Run("normal app: open, with no restart steps", func(t *testing.T) {
		mux := http.NewServeMux()
		RegisterResetStatusRoutes(mux, b.lease)
		code, page := recoveryGET(mux, ResetStatusPath+"?operation="+b.OperationID)
		if code != http.StatusOK || !strings.Contains(page, "Reset canceled") || !strings.Contains(page, "Ori is open with your data as it was") {
			t.Fatalf("normal app page = %d\n%s", code, page)
		}
		if !strings.Contains(page, `id="reset-restart-instructions" aria-labelledby="restart-heading" hidden`) {
			t.Error("the normal app still shows restart instructions for a canceled reset")
		}
	})
}
