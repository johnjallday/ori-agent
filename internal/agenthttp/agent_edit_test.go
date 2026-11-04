package agenthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// patchAgent sends PATCH /api/agents/{name} with body and returns the recorder.
func patchAgent(t *testing.T, h *Handler, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/"+name, strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestRejectedPatchLeavesLiveRecordUntouched pins that a request refused for
// any reason changes nothing a later read sees — not only nothing on disk.
// Before the guarded mutation boundary, fields assigned before a later
// validation step stayed on the store's live record.
func TestRejectedPatchLeavesLiveRecordUntouched(t *testing.T) {
	cases := []struct {
		name string
		body string
		code int
	}{
		{"invalid reasoning after model", `{"model":"gpt-5-changed","reasoning_effort":"bogus"}`, http.StatusBadRequest},
		{"invalid appearance after prompt", `{"system_prompt":"changed","appearance":{"mode":"nonsense"}}`, http.StatusBadRequest},
		{"invalid rename after prompt", `{"system_prompt":"changed","name":"bad/name"}`, http.StatusBadRequest},
		{"colliding rename after prompt", `{"system_prompt":"changed","name":"Other"}`, http.StatusConflict},
		{"stale version", `{"system_prompt":"changed","expected_version":"deadbeefdeadbeef"}`, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, dash := versionTestHandlers(t, "Solo")
			if err := h.State.CreateAgent("Other", nil); err != nil {
				t.Fatalf("CreateAgent Other: %v", err)
			}
			before := fetchAgentVersion(t, dash, "Solo")
			ag, _ := h.State.GetAgent("Solo")
			prompt, model := ag.Settings.SystemPrompt, ag.Settings.Model

			rr := patchAgent(t, h, "Solo", tc.body)
			if rr.Code != tc.code {
				t.Fatalf("expected %d, got %d body=%s", tc.code, rr.Code, rr.Body.String())
			}
			ag, _ = h.State.GetAgent("Solo")
			if ag.Settings.SystemPrompt != prompt || ag.Settings.Model != model {
				t.Fatalf("rejected request changed the live record: prompt %q→%q model %q→%q",
					prompt, ag.Settings.SystemPrompt, model, ag.Settings.Model)
			}
			if after := fetchAgentVersion(t, dash, "Solo"); after != before {
				t.Fatalf("rejected request moved the version %s → %s", before, after)
			}
		})
	}
}

// TestOverlappingPatchesWithOneBaselineCommitOnce sends many concurrent edits
// that all carry the same loaded version. Exactly one may win; every other one
// must be refused as stale rather than silently overwriting the winner.
func TestOverlappingPatchesWithOneBaselineCommitOnce(t *testing.T) {
	h, dash := versionTestHandlers(t, "Solo")
	version := fetchAgentVersion(t, dash, "Solo")

	const n = 16
	var wg sync.WaitGroup
	codes := make([]int, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			body, _ := json.Marshal(map[string]any{
				"system_prompt":    "edit " + string(rune('a'+i)),
				"expected_version": version,
			})
			codes[i] = patchAgent(t, h, "Solo", string(body)).Code
		}(i)
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			wins++
		case http.StatusConflict:
		default:
			t.Fatalf("unexpected status %d in %v", code, codes)
		}
	}
	if wins != 1 {
		t.Fatalf("expected exactly one edit to commit from one baseline, got %d (%v)", wins, codes)
	}
}

type editReceipt struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Code    string `json:"code"`
	Error   string `json:"error"`
	Current string `json:"current_version"`
	Saved   bool   `json:"definition_saved"`
	Agent   struct {
		Name         string `json:"name"`
		SystemPrompt string `json:"system_prompt"`
		Version      string `json:"version"`
		Metadata     struct {
			Description string `json:"description"`
		} `json:"metadata"`
	} `json:"agent"`
}

func decodeReceipt(t *testing.T, rr *httptest.ResponseRecorder) editReceipt {
	t.Helper()
	var r editReceipt
	if err := json.Unmarshal(rr.Body.Bytes(), &r); err != nil {
		t.Fatalf("decode %s: %v", rr.Body.String(), err)
	}
	return r
}

// TestPatchReturnsTheCommittedDefinitionAsItsReceipt pins the additive
// response fields: the version this request committed and the canonical values
// it wrote, which equal what the next read reports.
func TestPatchReturnsTheCommittedDefinitionAsItsReceipt(t *testing.T) {
	h, dash := versionTestHandlers(t, "Solo")
	version := fetchAgentVersion(t, dash, "Solo")

	rr := patchAgent(t, h, "Solo", `{"system_prompt":"receipt","description":"d","expected_version":"`+version+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", rr.Code, rr.Body.String())
	}
	got := decodeReceipt(t, rr)
	if got.Version == "" || got.Version == version {
		t.Fatalf("receipt version %q should be the new committed token (was %q)", got.Version, version)
	}
	if got.Agent.SystemPrompt != "receipt" || got.Agent.Metadata.Description != "d" || got.Agent.Version != got.Version {
		t.Fatalf("receipt does not carry the committed values: %+v", got.Agent)
	}
	if got.Current != "" {
		t.Fatalf("no follow-up write happened, but current_version = %q", got.Current)
	}
	if after := fetchAgentVersion(t, dash, "Solo"); after != got.Version {
		t.Fatalf("next read reports %q, receipt said %q", after, got.Version)
	}
}

// TestRuntimeActivityDoesNotMakeAnEditStale: a chat turn counting statistics
// between load and save must not turn the user's save into a conflict.
func TestRuntimeActivityDoesNotMakeAnEditStale(t *testing.T) {
	h, dash := versionTestHandlers(t, "Solo")
	version := fetchAgentVersion(t, dash, "Solo")

	ag, _ := h.State.GetAgent("Solo")
	ag.Statistics.RecordMessage(120, 0.02)
	if err := h.State.SetAgent("Solo", ag); err != nil {
		t.Fatalf("record activity: %v", err)
	}

	rr := patchAgent(t, h, "Solo", `{"system_prompt":"after activity","expected_version":"`+version+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("activity made the edit stale: %d %s", rr.Code, rr.Body.String())
	}
	ag, _ = h.State.GetAgent("Solo")
	if ag.Statistics.MessageCount != 1 {
		t.Fatalf("the edit lost runtime statistics: %+v", ag.Statistics)
	}
}

// TestPatchAfterAnEditOnDiskIsRefusedWithItsOwnCode: an outside edit is told
// apart from a second browser's save, and the file on disk wins.
func TestPatchAfterAnEditOnDiskIsRefusedWithItsOwnCode(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewFileStore(filepath.Join(dir, "agents_index.json"), types.Settings{Model: "gpt-4o-mini", Temperature: 1.0})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := st.CreateAgent("Solo", &store.CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	h, dash := New(st), NewDashboardHandler(st)
	version := fetchAgentVersion(t, dash, "Solo")

	path := filepath.Join(dir, "agents", "Solo", "agent_settings.json")
	data, _ := os.ReadFile(path)
	edited := strings.Replace(string(data), `"system_prompt": "original"`, `"system_prompt": "from an editor"`, 1)
	if edited == string(data) {
		t.Fatalf("fixture did not contain the prompt: %s", data)
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatalf("edit on disk: %v", err)
	}

	rr := patchAgent(t, h, "Solo", `{"description":"browser","expected_version":"`+version+`"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d %s", rr.Code, rr.Body.String())
	}
	if got := decodeReceipt(t, rr); got.Code != agentChangedOnDiskCode {
		t.Fatalf("code = %q, want %q", got.Code, agentChangedOnDiskCode)
	}
	after, _ := os.ReadFile(path)
	if string(after) != edited {
		t.Fatal("the outside edit was overwritten")
	}
	ag, _ := st.GetAgent("Solo")
	if ag.Settings.SystemPrompt != "from an editor" || (ag.Metadata != nil && ag.Metadata.Description == "browser") {
		t.Fatalf("memory does not match disk: %+v %+v", ag.Settings, ag.Metadata)
	}
}

// TestSharedEditConfirmationKeepsTheReviewedVersion: declining writes nothing,
// and a confirmed retry is still refused when someone else saved meanwhile.
func TestSharedEditConfirmationKeepsTheReviewedVersion(t *testing.T) {
	h := guardTestHandler(t, []string{"Shared"}, map[string][]string{"ws-a": {"Shared"}, "ws-b": {"Shared"}})
	dash := NewDashboardHandler(h.State)
	version := fetchAgentVersion(t, dash, "Shared")

	rr := patchAgent(t, h, "Shared", `{"system_prompt":"mine","expected_version":"`+version+`"}`)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "shared_agent_edit_requires_confirmation") {
		t.Fatalf("expected confirmation request, got %d %s", rr.Code, rr.Body.String())
	}
	if after := fetchAgentVersion(t, dash, "Shared"); after != version {
		t.Fatal("asking for confirmation changed the definition")
	}

	// Someone else saves while the dialog is open.
	if rr := patchAgent(t, h, "Shared", `{"system_prompt":"theirs","confirm_shared_edit":true}`); rr.Code != http.StatusOK {
		t.Fatalf("other save: %d %s", rr.Code, rr.Body.String())
	}
	rr = patchAgent(t, h, "Shared", `{"system_prompt":"mine","confirm_shared_edit":true,"expected_version":"`+version+`"}`)
	if rr.Code != http.StatusConflict || decodeReceipt(t, rr).Code != staleAgentEditCode {
		t.Fatalf("confirmed stale retry must conflict, got %d %s", rr.Code, rr.Body.String())
	}
	ag, _ := h.State.GetAgent("Shared")
	if ag.Settings.SystemPrompt != "theirs" {
		t.Fatalf("confirmed stale retry overwrote the other save: %q", ag.Settings.SystemPrompt)
	}
}

// TestRenameSavesFieldsAndMovesTheAgent covers the successful rename receipt.
func TestRenameSavesFieldsAndMovesTheAgent(t *testing.T) {
	h, dash := versionTestHandlers(t, "Solo")
	version := fetchAgentVersion(t, dash, "Solo")
	rr := patchAgent(t, h, "Solo", `{"name":"Renamed","description":"moved","expected_version":"`+version+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rr.Code, rr.Body.String())
	}
	got := decodeReceipt(t, rr)
	if got.Name != "Renamed" || got.Agent.Name != "Renamed" || got.Agent.Metadata.Description != "moved" {
		t.Fatalf("rename receipt = %+v", got)
	}
	if _, ok := h.State.GetAgent("Solo"); ok {
		t.Fatal("old name still resolves")
	}
	if after := fetchAgentVersion(t, dash, "Renamed"); after != got.Version {
		t.Fatalf("renamed agent reads %q, receipt said %q", after, got.Version)
	}
}

// TestRenameFailureAfterSaveReportsWhatWasSaved: when the move itself fails
// after the fields were written, the answer says the fields were saved and the
// name was not, instead of looking like a refusal of the whole request.
func TestRenameFailureAfterSaveReportsWhatWasSaved(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewFileStore(filepath.Join(dir, "agents_index.json"), types.Settings{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := st.CreateAgent("Solo", &store.CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	// A stray folder under the target name: not an agent the store holds, so
	// the up-front collision check passes, but the move refuses to merge.
	if err := os.MkdirAll(filepath.Join(dir, "agents", "Taken"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	h, dash := New(st), NewDashboardHandler(st)
	version := fetchAgentVersion(t, dash, "Solo")

	rr := patchAgent(t, h, "Solo", `{"name":"Taken","description":"kept","expected_version":"`+version+`"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d %s", rr.Code, rr.Body.String())
	}
	got := decodeReceipt(t, rr)
	if got.Code != agentRenameFailedErr || !got.Saved || got.Name != "Solo" || got.Agent.Metadata.Description != "kept" {
		t.Fatalf("partial rename answer = %+v", got)
	}
	if after := fetchAgentVersion(t, dash, "Solo"); after != got.Version {
		t.Fatalf("saved definition reads %q, answer said %q", after, got.Version)
	}
}

// TestRefusedRenameSavesNothingElse: an attached agent's rename is refused
// before the request's other fields are written.
func TestRefusedRenameSavesNothingElse(t *testing.T) {
	h := guardTestHandler(t, []string{"Attached"}, map[string][]string{"ws-a": {"Attached"}})
	rr := patchAgent(t, h, "Attached", `{"name":"Elsewhere","system_prompt":"should not save"}`)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "attached_agent_rename_blocked") {
		t.Fatalf("expected rename block, got %d %s", rr.Code, rr.Body.String())
	}
	ag, _ := h.State.GetAgent("Attached")
	if ag.Settings.SystemPrompt == "should not save" {
		t.Fatal("refused rename still saved the other fields")
	}
}
