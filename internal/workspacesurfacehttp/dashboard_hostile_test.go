package workspacesurfacehttp

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// The adversarial matrix (PRD success metric 4). A deliberately hostile
// dashboard tries, in turn, to reach the network, read the parent document,
// name another workspace, and call an operation it was never granted. Each
// attempt is asserted separately so a regression names exactly which barrier
// fell.
//
// Two of the four barriers are browser-enforced and cannot be exercised from Go:
// `connect-src 'none'` blocking fetch(), and the sandbox blocking
// parent.document. What Go can assert is that the host still SENDS the headers
// and attributes those depend on — done here — while the browser demo asserts
// the resulting behavior (both were observed failing with TypeError and
// SecurityError respectively).

func hostileFixture(t *testing.T) (*prototypeHTTPFixture, string) {
	t.Helper()
	fixture := newPrototypeHTTPFixture(t)
	dashboardRoot := t.TempDir()
	writeDashboardEntry(t, dashboardRoot, "<!doctype html><title>Hostile</title>")
	binding := dashboardBinding(t, dashboardRoot, fixture.runtime)
	// One declared operation, so "undeclared" is a real distinction rather than
	// an artifact of the surface declaring nothing at all.
	binding.Operations = map[string]workspacesurface.Operation{
		"workspace.summary": {
			ID:             "workspace.summary",
			InputSchema:    json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
			OutputSchema:   json.RawMessage(`{"type":"object","properties":{"message":{"type":"string","maxLength":160}},"required":[],"additionalProperties":false}`),
			MaxOutputBytes: 4096,
			Timeout:        workspacesurface.TimeoutFast,
			Policy:         workspacesurface.PolicyReadOnly,
		},
	}
	source := stubDashboardSource{
		byWorkspace: map[string]workspacesurface.Binding{fixture.workspaceID: binding},
	}
	fixture.handler.SetDashboardSource(hostileSource{stubDashboardSource: source})

	path := "/api/workspaces/" + fixture.workspaceID + "/surfaces/" + url.PathEscape(stubDashboardKey) + "/sessions"
	recorder := fixture.serve(http.MethodPost, path, "")
	if recorder.Code != http.StatusCreated {
		t.Fatalf("open = %d %s", recorder.Code, recorder.Body.String())
	}
	var session openSessionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	return fixture, session.Session
}

// hostileSource declares the one operation the surface is granted, so the
// eligibility check has a real declared set to compare against.
type hostileSource struct{ stubDashboardSource }

func (s hostileSource) Resolve(workspaceID string) (workspacesurface.RegisteredSurface, workspacesurface.Binding, bool, error) {
	surface, binding, ok, err := s.stubDashboardSource.Resolve(workspaceID)
	if ok && err == nil {
		surface.Surface.OperationIDs = []string{"workspace.summary"}
		surface.Capability.Surfaces[0].OperationIDs = []string{"workspace.summary"}
	}
	return surface, binding, ok, err
}

// Barrier 1: the frame cannot issue a network request of its own. Go asserts
// the CSP that enforces it is actually sent.
func TestHostileDashboardCannotReachTheNetwork(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	dashboardRoot := t.TempDir()
	writeDashboardEntry(t, dashboardRoot, "<!doctype html><title>Hostile</title>")
	fixture.handler.SetDashboardSource(stubDashboardSource{
		byWorkspace: map[string]workspacesurface.Binding{
			fixture.workspaceID: dashboardBinding(t, dashboardRoot, fixture.runtime),
		},
	})
	path := "/api/workspaces/" + fixture.workspaceID + "/surfaces/" + url.PathEscape(stubDashboardKey) + "/sessions"
	recorder := fixture.serve(http.MethodPost, path, "")
	var session openSessionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	frame := fixture.serve(http.MethodGet, session.FrameURL, "")

	csp := frame.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("CSP does not forbid connections: %q", csp)
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Fatalf("CSP has been relaxed: %q", csp)
	}
	// Barrier 2's enforcement rides on these: no allow-same-origin in the frame
	// sandbox, plus credentialless. The host attribute set is asserted in
	// workspace-surface-host.test.js; here we assert the server does not send a
	// header that would let the frame be embedded or read cross-origin freely.
	if frame.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" ||
		frame.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("isolation headers = %v", frame.Header())
	}
}

// Barrier 3: naming another workspace does not reach it. The dashboard's key is
// identical in every workspace, so presenting it against a workspace that has
// no dashboard must resolve nothing.
func TestHostileDashboardCannotReachAnotherWorkspace(t *testing.T) {
	fixture, session := hostileFixture(t)

	// Directly: open a session against a foreign workspace using the same key.
	foreign := "/api/workspaces/workspace-unattached/surfaces/" + url.PathEscape(stubDashboardKey) + "/sessions"
	if recorder := fixture.serve(http.MethodPost, foreign, ""); recorder.Code != http.StatusNotFound {
		t.Fatalf("opened a dashboard against a foreign workspace: %d", recorder.Code)
	}

	// Through a live session: the invocation carries no workspace of its own,
	// and the runtime is handed the session's workspace, never the frame's.
	body, _ := json.Marshal(operationRequest{
		Session: session, OperationID: "workspace.summary",
		Input: json.RawMessage(`{}`),
	})
	recorder := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations", string(body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("declared operation = %d %s", recorder.Code, recorder.Body.String())
	}
	if got := fixture.runtime.lastInvocation().Workspace.WorkspaceID; got != fixture.workspaceID {
		t.Fatalf("runtime received workspace %q, want %q", got, fixture.workspaceID)
	}
}

// Barrier 3b: a forged workspace id inside the operation input is rejected
// before the runtime sees it, because the input schema closes
// additionalProperties.
func TestHostileDashboardCannotForgeAWorkspaceIDInInput(t *testing.T) {
	fixture, session := hostileFixture(t)
	before := fixture.runtime.invokeCount()

	body, _ := json.Marshal(operationRequest{
		Session: session, OperationID: "workspace.summary",
		Input: json.RawMessage(`{"workspace_id":"workspace-foreign"}`),
	})
	recorder := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations", string(body))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("forged input = %d %s, want 400", recorder.Code, recorder.Body.String())
	}
	if fixture.runtime.invokeCount() != before {
		t.Fatal("the runtime was invoked with forged input")
	}
}

// Barrier 4: an operation the surface never declared is refused, with no
// fallthrough to a declared one and no partial data.
func TestHostileDashboardCannotCallAnUndeclaredOperation(t *testing.T) {
	fixture, session := hostileFixture(t)
	before := fixture.runtime.invokeCount()

	for _, operationID := range []string{
		"vault.read",
		"greeting.create", // declared by the PLUGIN surface, not by this one
		"workspace.summary.extra",
		"",
	} {
		body, _ := json.Marshal(operationRequest{
			Session: session, OperationID: operationID, Input: json.RawMessage(`{}`),
		})
		recorder := fixture.serve(http.MethodPost, "/api/workspace-surfaces/operations", string(body))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("operation %q = %d %s, want 404", operationID, recorder.Code, recorder.Body.String())
		}
		var response errorResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Code != "operation_unknown" {
			t.Fatalf("operation %q error code = %q", operationID, response.Code)
		}
	}
	if fixture.runtime.invokeCount() != before {
		t.Fatal("an undeclared operation reached the runtime")
	}
}

// A dashboard must not inherit the modal-surface host intents, nor plugin
// state storage, by asking for them over its own session.
func TestHostileDashboardCannotUseHostIntentsOrState(t *testing.T) {
	fixture, session := hostileFixture(t)

	for _, intent := range []string{"ask_ori", "open_setup", "create_task"} {
		body, _ := json.Marshal(intentRequest{Session: session, Type: intent})
		recorder := fixture.serve(http.MethodPost, "/api/workspace-surfaces/intents", string(body))
		if recorder.Code == http.StatusOK {
			t.Fatalf("intent %q was granted to a dashboard: %s", intent, recorder.Body.String())
		}
	}

	body, _ := json.Marshal(stateRequest{Session: session, Action: "set", Key: "k", Value: json.RawMessage(`{"a":1}`)})
	recorder := fixture.serve(http.MethodPost, "/api/workspace-surfaces/state", string(body))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("state write = %d %s, want 403", recorder.Code, recorder.Body.String())
	}
}

// Assets outside the dashboard folder are unreachable through the frame token,
// including by traversal.
func TestHostileDashboardCannotEscapeItsAssetRoot(t *testing.T) {
	fixture := newPrototypeHTTPFixture(t)
	dashboardRoot := t.TempDir()
	writeDashboardEntry(t, dashboardRoot, "<!doctype html><title>Hostile</title>")
	fixture.handler.SetDashboardSource(stubDashboardSource{
		byWorkspace: map[string]workspacesurface.Binding{
			fixture.workspaceID: dashboardBinding(t, dashboardRoot, fixture.runtime),
		},
	})
	path := "/api/workspaces/" + fixture.workspaceID + "/surfaces/" + url.PathEscape(stubDashboardKey) + "/sessions"
	recorder := fixture.serve(http.MethodPost, path, "")
	var session openSessionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	frameBase := strings.TrimSuffix(session.FrameURL, "index.html")

	for _, target := range []string{
		"../../../etc/passwd",
		"..%2f..%2fsettings.json",
		"secrets.env",
		"index.html.bak",
	} {
		recorder := fixture.serve(http.MethodGet, frameBase+target, "")
		if recorder.Code == http.StatusOK {
			t.Fatalf("asset %q was served: %s", target, recorder.Body.String())
		}
	}
}
