package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestProfileSetToolInAnotherWorkspaceCannotReintroduceForgottenInterviewPreference(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	ctx := context.Background()
	post := func(path string, body []byte, want int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != want {
			t.Fatalf("%s: %d %s", path, result.Code, result.Body.String())
		}
	}
	post("/api/settings/workspace-root", []byte(`{"workspace_root":""}`), http.StatusOK)
	post("/api/personal-assistant/hire", []byte(`{"request_id":"guard-hire","if_version":0,"display_name":"Atlas","mandate":"Help plan.","focus_areas":["plan_my_day"]}`), http.StatusCreated)
	state, err := builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	hq, err := json.Marshal(map[string]any{"request_id": "guard-hq", "if_version": state.StateVersion, "name": "My HQ", "timezone": "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	post("/api/personal-assistant/hq", hq, http.StatusCreated)
	state, err = builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := builder.userStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	interview, err := json.Marshal(personalassistant.KnowledgeInterviewSaveRequest{
		StateVersion: state.StateVersion, RequestID: "guarded-preference",
		Rows: []personalassistant.KnowledgeInterviewReviewedRow{{
			RowID: "communication", Category: "how_you_work", Destination: "profile", Text: "concise",
			Preference: "response_style", ExpectedProfileValue: profile.Preferences["response_style"],
			ExpectedProfileUpdatedAt: profile.UpdatedAt,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	post("/api/personal-assistant/knowledge/interview/save", interview, http.StatusOK)
	profile, err = builder.userStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.userStore.(*userprofile.SQLiteStore).UpdateFieldCAS(ctx, "local", "preferences.response_style", profile.UpdatedAt, "concise", ""); err != nil {
		t.Fatal(err)
	}
	provider := chathttp.NewWorkspaceToolProvider(nil, nil, "unrelated-workspace", builder.hqVisibilityDeps())
	provider.SetUserProfileDeps(builder.userStore, userprofile.LocalUserProvider{})
	var profileSet interface {
		Call(context.Context, string) (string, error)
	}
	for _, tool := range provider.Tools() {
		if tool.Definition().Name == "profile_set" {
			profileSet = tool
		}
	}
	if profileSet == nil {
		t.Fatal("profile_set not registered")
	}
	if output, err := profileSet.Call(ctx, `{"fields":{"preferences.response_style":"concise","about":"bundled tool write"}}`); !errors.Is(err, workspace.ErrMemoryManaged) || output != "" {
		t.Fatalf("tool in another workspace restored a forgotten reviewed preference: %q %v", output, err)
	}
	profile, err = builder.userStore.Get(ctx, "local")
	if err != nil || profile.Preferences["response_style"] != "" || profile.About != "" {
		t.Fatalf("tool changed any canonical profile field: %+v %v", profile, err)
	}
	if output, err := profileSet.Call(ctx, `{"field":"preferences.language","value":"Spanish"}`); err != nil || output == "" {
		t.Fatalf("unrelated global profile_set should remain usable: %v", err)
	}
}
