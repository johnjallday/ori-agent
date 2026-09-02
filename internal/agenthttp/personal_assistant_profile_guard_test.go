package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type guardStateReader struct {
	projection *personalassistant.Projection
	err        error
}

func (r guardStateReader) Get(context.Context, string) (*personalassistant.Projection, error) {
	return r.projection, r.err
}

type guardUserProvider struct {
	userID string
	err    error
}

func (p guardUserProvider) CurrentUserID(context.Context) (string, error) {
	return p.userID, p.err
}

var _ userprofile.UserProvider = guardUserProvider{}

// hiredProfileHandler builds a handler whose relationship is a hired assistant
// named "Atlas" that has not built Personal HQ yet, alongside an unrelated
// unattached agent.
func hiredProfileHandler(t *testing.T, state personalassistant.APIState) *Handler {
	t.Helper()
	h := guardTestHandler(t, []string{"Atlas", "Unrelated"}, nil)
	h.SetPersonalAssistantSupport(guardStateReader{projection: &personalassistant.Projection{
		State: state, AssistantID: "assistant-1", DisplayName: "Atlas",
		GlobalAgentProfile: "Atlas", StateVersion: 2,
	}}, guardUserProvider{userID: userprofile.LocalUserID})
	return h
}

func deleteAgent(t *testing.T, h *Handler, name string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	h.handleDelete(recorder, httptest.NewRequest(http.MethodDelete, "/api/agents?name="+name, nil))
	return recorder
}

func renameAgent(t *testing.T, h *Handler, from, to string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	body := `{"name":"` + to + `"}`
	request := httptest.NewRequest(http.MethodPatch, "/api/agents?name="+from, strings.NewReader(body))
	h.ServeHTTP(recorder, request)
	return recorder
}

func TestHiredProfileGuard_BlocksOrdinaryDeleteBeforeHQExists(t *testing.T) {
	for _, state := range []personalassistant.APIState{
		personalassistant.APIStateNeedsHQ, personalassistant.APIStateProvisioningHQ,
	} {
		t.Run(string(state), func(t *testing.T) {
			h := hiredProfileHandler(t, state)
			recorder := deleteAgent(t, h, "Atlas")
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body["error"] != hiredProfileGuardCode || body["next_action"] != "build_hq" {
				t.Fatalf("guard response = %v", body)
			}
			if _, ok := h.State.GetAgent("Atlas"); !ok {
				t.Fatal("the hired assistant's profile was deleted")
			}
		})
	}
}

func TestHiredProfileGuard_BlocksOrdinaryRenameBeforeHQExists(t *testing.T) {
	h := hiredProfileHandler(t, personalassistant.APIStateNeedsHQ)
	recorder := renameAgent(t, h, "Atlas", "Atlas Two")
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] != hiredProfileGuardCode {
		t.Fatalf("guard response = %v", body)
	}
	if _, ok := h.State.GetAgent("Atlas Two"); ok {
		t.Fatal("rename forked the hired assistant's identity")
	}
	if _, ok := h.State.GetAgent("Atlas"); !ok {
		t.Fatal("the hired assistant's profile disappeared")
	}
}

func TestHiredProfileGuard_BulkDeleteCannotBypassTheSingleAgentGuard(t *testing.T) {
	h := hiredProfileHandler(t, personalassistant.APIStateNeedsHQ)
	resp := postBulk(t, h, `{"agent_names":["Atlas","Unrelated"],"operation":"delete"}`)

	if result, _ := resultFor(resp, "Atlas"); result.Status != bulkStatusSkipped ||
		result.ReasonCode != reasonProtectedAgent {
		t.Fatalf("Atlas result = %#v", result)
	}
	if result, _ := resultFor(resp, "Unrelated"); result.Status != bulkStatusSucceeded {
		t.Fatalf("an unrelated unattached agent lost its ordinary delete: %#v", result)
	}
	if _, ok := h.State.GetAgent("Atlas"); !ok {
		t.Fatal("bulk delete removed the hired assistant's profile")
	}
	if _, ok := h.State.GetAgent("Unrelated"); ok {
		t.Fatal("unrelated agent was not deleted")
	}
}

func TestHiredProfileGuard_LeavesUnrelatedAndPostHQAgentsAlone(t *testing.T) {
	t.Run("unrelated unattached agent", func(t *testing.T) {
		h := hiredProfileHandler(t, personalassistant.APIStateNeedsHQ)
		if recorder := deleteAgent(t, h, "Unrelated"); recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
		}
		if _, ok := h.State.GetAgent("Unrelated"); ok {
			t.Fatal("unrelated agent survived an ordinary delete")
		}
	})

	t.Run("same-named agent while no relationship exists", func(t *testing.T) {
		h := guardTestHandler(t, []string{"Atlas"}, nil)
		h.SetPersonalAssistantSupport(guardStateReader{projection: &personalassistant.Projection{
			State: personalassistant.APIStateNeedsHire,
		}}, guardUserProvider{userID: userprofile.LocalUserID})
		if recorder := deleteAgent(t, h, "Atlas"); recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("active relationship falls back to the attachment guard", func(t *testing.T) {
		// Once HQ exists the profile is attached to it, so the ordinary
		// attached-agent guard is what blocks the delete — this guard steps aside.
		h := guardTestHandler(t, []string{"Atlas"}, map[string][]string{"ws-hq": {"Atlas"}})
		h.SetPersonalAssistantSupport(guardStateReader{projection: &personalassistant.Projection{
			State: personalassistant.APIStateActive, AssistantID: "assistant-1",
			GlobalAgentProfile: "Atlas", HQWorkspaceID: "ws-hq",
		}}, guardUserProvider{userID: userprofile.LocalUserID})
		recorder := deleteAgent(t, h, "Atlas")
		if recorder.Code != http.StatusConflict {
			t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["error"] != "attached_agent_delete_blocked" {
			t.Fatalf("post-HQ delete used the wrong guard: %v", body)
		}
	})
}

func TestHiredProfileGuard_FailsClosedWhenTheRelationshipCannotBeRead(t *testing.T) {
	for _, test := range []struct {
		name     string
		reader   guardStateReader
		provider guardUserProvider
	}{
		{"relationship read failed", guardStateReader{err: errors.New("database offline")},
			guardUserProvider{userID: userprofile.LocalUserID}},
		{"user could not be resolved", guardStateReader{projection: &personalassistant.Projection{
			State: personalassistant.APIStateNeedsHQ, GlobalAgentProfile: "Atlas",
		}}, guardUserProvider{err: errors.New("no current user")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := guardTestHandler(t, []string{"Atlas"}, nil)
			h.SetPersonalAssistantSupport(test.reader, test.provider)
			if recorder := deleteAgent(t, h, "Atlas"); recorder.Code != http.StatusConflict {
				t.Fatalf("an unreadable relationship allowed a destructive delete: %d", recorder.Code)
			}
			if _, ok := h.State.GetAgent("Atlas"); !ok {
				t.Fatal("agent deleted while the relationship was unreadable")
			}
		})
	}
}

func TestHiredProfileGuard_IsInertWithoutAConfiguredReader(t *testing.T) {
	// A build that never wires the personal-assistant reader must keep ordinary
	// agent management working rather than blocking everything.
	h := guardTestHandler(t, []string{"Atlas"}, nil)
	if h.support.protectsHiredProfile(context.Background(), "Atlas") {
		t.Fatal("guard blocked with no relationship reader configured")
	}
}
