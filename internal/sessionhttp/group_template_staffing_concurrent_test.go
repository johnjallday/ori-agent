package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestGroupHomeConcurrentReviewedRoleDeliveryCreatesOnlyOneExactProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h, workspaces, id, revision, cleanup := newGroupTemplateHomeHandler(t)
	defer cleanup()
	profiles, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	h.SetAgentStore(profiles)
	newBuildFixtureOn(t, h, false)
	wireReviewedGroupStaffing(t, h, workspaces)
	before := len(profiles.ListAgents())
	request := map[string]any{"group_template_id": id, "revision": revision, "name": "Concurrent collection",
		"role_staffing": []groupHomeRoleChoice{{RoleID: "home-lead", Mode: "create", Name: "Exact Guide", Provider: "scripted", Model: "scripted-1"}}}
	code, body := postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, request))
	if code != http.StatusOK {
		t.Fatalf("review %d", code)
	}
	request["group_review_token"], request["idempotency_key"] = body["group_template_review"].(map[string]any)["review_token"], "concurrent-role-delivery"
	raw := groupTemplateHomeBody(t, request)
	inner := h.assistantWorkspaceRoleStaffer
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h.assistantWorkspaceRoleStaffer = func(ctx context.Context, homeID string, fills []RoleStaffingFill) error {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return inner(ctx, homeID, fills)
	}
	serve := func() *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		h.CommitGroupTemplateHome(r, httptest.NewRequest(http.MethodPost, "/api/workspaces/group-templates/commit", strings.NewReader(raw)))
		return r
	}
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- serve() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("first delivery did not reach canonical role boundary")
	}
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() { second <- serve() }()
	var other *httptest.ResponseRecorder
	select {
	case other = <-second:
	case <-time.After(5 * time.Second):
		close(release)
		<-first
		<-second
		t.Fatal("neighbouring delivery held the operation lease across staffing")
	}
	close(release)
	original := <-first
	for _, response := range []*httptest.ResponseRecorder{original, other} {
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		result, _ := body["group_template"].(map[string]any)
		outcome, _ := result["home_staffing"].(map[string]any)
		if response.Code != http.StatusOK || outcome["state"] != "staffed" {
			t.Fatalf("concurrent exact delivery %d state=%v", response.Code, outcome["state"])
		}
	}
	ids, err := workspaces.List()
	if err != nil || len(ids) != 1 || len(profiles.ListAgents()) != before+1 {
		t.Fatal("concurrent delivery duplicated Home or profile", err)
	}
	home, err := workspaces.Get(ids[0])
	if err != nil || len(home.AgentInstances) != 1 || home.AgentInstances[0].Name != "Exact Guide" {
		t.Fatal("concurrent delivery changed exact role", err)
	}
	// Observe again: a complete canonical role is replayed, not re-created.
	response := serve()
	if response.Code != http.StatusOK || len(profiles.ListAgents()) != before+1 {
		t.Fatal("completed delivery did not replay")
	}
}
