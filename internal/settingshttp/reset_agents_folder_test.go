package settingshttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

func TestAnAgentsResetNeedsItsFolderConfirmedOverHTTP(t *testing.T) {
	f, server, _ := admissionHTTPFixture(t)
	response, err := server.Do(t.Context(), http.MethodGet, "/api/reset/preview?intent=selected_data&category=agents", nil)
	if err != nil {
		t.Fatal(err)
	}
	var preview settingsreset.Preview
	readAdmissionResponse(t, response, &preview)
	if response.StatusCode != http.StatusOK || len(preview.Blockers) != 0 {
		t.Fatalf("agents preview = %d %+v", response.StatusCode, preview.Blockers)
	}
	review := preview.AgentsFolder
	want, err := filepath.EvalSymlinks(filepath.Join(f.Paths().Workspaces, "Agents"))
	if err != nil {
		t.Fatal(err)
	}
	if review == nil || review.Path != want || !review.ConfirmationRequired || review.Notice != settingsreset.AgentsFolderNotice {
		t.Fatalf("preview agents folder = %+v, want %s", review, want)
	}

	post := func(req settingsreset.ExecuteRequest) (int, ResetResponse) {
		t.Helper()
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Do(t.Context(), http.MethodPost, "/api/reset", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var result ResetResponse
		readAdmissionResponse(t, response, &result)
		return response.StatusCode, result
	}
	status, result := post(settingsreset.ExecuteRequest{PreviewID: preview.ID, RequestID: "agents-unconfirmed", Confirmation: "RESET"})
	if status != http.StatusConflict || result.Code != "agents_folder_confirmation_required" || result.Operation != nil {
		t.Fatalf("unconfirmed agents reset = %d %+v", status, result)
	}
	if _, err := os.Stat(filepath.Join(f.Paths().Workspaces, "Agents", resetfixture.AgentName, "agent_settings.json")); err != nil {
		t.Fatalf("a refused reset touched the agent: %v", err)
	}
	status, result = post(settingsreset.ExecuteRequest{PreviewID: preview.ID, RequestID: "agents-confirmed", Confirmation: "RESET", ConfirmAgentsFolder: review.Path})
	if status != http.StatusAccepted || result.Operation == nil || result.Operation.State != settingsreset.StateAwaitingRestart {
		t.Fatalf("confirmed agents reset = %d %+v", status, result)
	}
}
